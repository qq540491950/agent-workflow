// Package contextx 实现 Context Manager:在不同 Node 之间传递最小化上下文。
// 例如 Review 节点只拿到 Task / Original Plan / Git Diff / Test Result /
// Previous Review / Iteration / Relevant Files,而不是整个项目。
package contextx

import (
	"fmt"
	"sync"
	"unicode/utf8"

	"agentworkflow/workflow/model"
)

// 会话状态里由上下文链使用的键名(**唯一定义处**)。
//
// 为什么要有常量:Build(读)与 SaveNodeOutput(写)必须用**同一份**键名 ——
// 两处各写一份字面量,拼写漂移("plan" vs "plans")会让整条链静默失效:
// 下游读到 nil → 模板渲染成空串 → Agent 收到没有方案的提示词照常执行
// (第 19 轮实测:整条跨节点上下文链是死的)。
//
// 这些键**不是**条件变量(条件变量的唯一定义见
// workflow/compiler/conditionenv.go:decision / status / output /
// iteration / variables)—— 写进会话状态不影响路由(约定 #13 的同源精神)。
const (
	// StateKeyTask 任务文本(引擎种进初始状态)。
	StateKeyTask = "task"
	// StateKeyPlan 方案:plan 节点产出。
	StateKeyPlan = "plan"
	// StateKeyReview 评审结论:review 节点产出。
	StateKeyReview = "review"
	// StateKeyIssues 评审问题清单:review 节点产出。
	StateKeyIssues = "issues"
	// StateKeyTestResult 测试结果:test 节点产出。
	StateKeyTestResult = "test_result"
	// StateKeyLastOutput 最近一次节点输出:execute / fix / skill 节点产出。
	StateKeyLastOutput = "last_output"
	// StateKeyGitDiff Git diff(git 节点产出)。
	StateKeyGitDiff = "git_diff"
	// StateKeyIteration 当前循环轮数(编译器维护)。
	StateKeyIteration = "iteration"
	// StateKeyRelevantFiles 相关文件清单。
	StateKeyRelevantFiles = "relevant_files"
	// StateKeyUserInstruction 人工补充指令。
	StateKeyUserInstruction = "user_instruction"
)

// StateWriter 是 SaveNodeOutput 写回会话状态所需的**最小**能力。
//
// 为什么不用 compiler.StateAccess:contextx 是"上下文构造"层,不该反向
// 依赖编排层(contextx → compiler 会把两个层绑死;compiler 将来一旦需要
// contextx 就成环)。这里只声明需要的那一个方法 —— compiler.StateAccess
// 天然满足它(结构化子集),调用方零适配。
type StateWriter interface {
	Set(key string, val any) error
}

// ExecutionContext 是一次执行的全局上下文。
type ExecutionContext struct {
	WorkflowID  string
	ExecutionID string
	Variables   map[string]any
	Artifacts   map[string]model.Artifact
	State       map[string]any
}

// Manager 根据节点职责构造最小化上下文。
type Manager struct {
	// mu 保护 MaxDiffChars(运行期可调,跨 goroutine 读)。
	mu sync.RWMutex
	// MaxDiffChars 限制注入 Prompt 的 Git Diff 长度,防止上下文爆炸。
	//
	// 构造期字段:启动后不再修改。运行期如需修改请用 SetMaxDiffChars
	// (带锁);直接写字段与并发 Build 存在数据竞争(约定 15)。
	MaxDiffChars int
}

// NewManager 创建 Context Manager(默认 diff 上限 8000 字符)。
func NewManager() *Manager {
	return &Manager{MaxDiffChars: 8000}
}

// maxDiffChars 读取 diff 上限(并发安全)。
func (m *Manager) maxDiffChars() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.MaxDiffChars
}

// SetMaxDiffChars 更新 diff 上限(并发安全)。
func (m *Manager) SetMaxDiffChars(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MaxDiffChars = n
}

// Build 为指定节点模式构造模板渲染变量。state 是 ADK session state 的
// 领域视图(由 runtime 维护)。
func (m *Manager) Build(mode string, ec *ExecutionContext, nodeID string) map[string]any {
	state := ec.State
	if state == nil {
		state = map[string]any{}
	}
	data := map[string]any{
		StateKeyTask:       state[StateKeyTask],
		StateKeyPlan:       state[StateKeyPlan],
		StateKeyGitDiff:    m.truncate(str(state[StateKeyGitDiff])),
		StateKeyReview:     state[StateKeyReview],
		StateKeyTestResult: state[StateKeyTestResult],
		StateKeyLastOutput: state[StateKeyLastOutput],
		StateKeyIteration:  state[StateKeyIteration],
		"variables":        copyVariables(ec.Variables),
		"node_id":          nodeID,
		"workflow_id":      ec.WorkflowID,
		"execution_id":     ec.ExecutionID,
	}

	switch mode {
	case "review":
		// Review 只需要:任务、方案、diff、测试结果、上一次 Review、迭代数
		data[StateKeyRelevantFiles] = state[StateKeyRelevantFiles]
	case "execute", "fix":
		// 执行类节点:任务、方案、上一次 Review 的问题、人工补充指令
		data[StateKeyIssues] = state[StateKeyIssues]
		data[StateKeyUserInstruction] = state[StateKeyUserInstruction]
	case "plan":
		// Plan 只需要任务本身与变量
		delete(data, StateKeyGitDiff)
		delete(data, StateKeyReview)
	}
	return data
}

// SaveNodeOutput 把节点输出写进**活的**会话状态,供后续节点按需引用。
// 只保留关键工件,不复制整棵输出树。
//
// 写的是会话状态,不是副本 —— 这是第 19 轮 P0 的根因:此前入参是
// map[string]any,调用方传进来的是 e.stateView(st)(**新建的一次性副本**),
// SaveNodeOutput 把 plan/review/issues/last_output/test_result 写进那个
// 副本,副本随即被丢弃,**从未写回 ADK 会话状态**。于是下游节点渲染时
// Build 读到的 state["plan"] 恒为 nil → {{plan}} / {{review}} /
// {{issues}} / {{test_result}} 永远渲染成空串:Agent 收到**没有方案**的
// 提示词,照常返回 APPROVED/REJECTED,整个执行报告 SUCCESS。
//
// 现在直接写 StateWriter(运行期传进来的就是 ADK 会话状态访问器)。
// 写失败返回 error,调用方必须让节点失败:写不进 = 上下文链断了,下游会
// 拿到空上下文照常执行(约定 #14:该失败却成功等同 P0)。
func (m *Manager) SaveNodeOutput(mode string, nodeID string, result map[string]any, w StateWriter) error {
	if w == nil {
		return nil
	}
	switch mode {
	case "plan":
		// 方案正文优先取 output(完整产物),空则退回 summary(结论摘要)。
		if err := w.Set(StateKeyPlan, pickText(result, "output", "summary")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyPlan, err)
		}
	case "review":
		if err := w.Set(StateKeyReview, pickText(result, "summary", "output")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyReview, err)
		}
		// 问题清单原样保留(可能是结构化列表,不是文本)。
		if issues, ok := result["issues"]; ok && issues != nil {
			if err := w.Set(StateKeyIssues, issues); err != nil {
				return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyIssues, err)
			}
		}
	case "execute", "fix", "skill":
		// 最近一次节点输出:执行类节点与技能节点的产物。
		if err := w.Set(StateKeyLastOutput, pickText(result, "summary", "output")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyLastOutput, err)
		}
	case "test":
		if err := w.Set(StateKeyTestResult, pickText(result, "output", "summary")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyTestResult, err)
		}
	}
	return nil
}

// pickText 按给定顺序取第一个**非空**文本值。
//
// 为什么不是"键存在就用":节点的 output 键**总是存在**(哪怕是空串)——
// 只产出 summary 的 Agent(绝大多数)会让 output 为空串,"键存在就用"
// 于是把空串写进上下文槽位,下游照样拿到空上下文(第 19 轮实测的第二个
// 断点:即使把副本换成活状态,plan 仍然是空串)。
func pickText(result map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(result[k]); s != "" {
			return s
		}
	}
	return ""
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return ""
}

// truncate 按上限截断 diff。
// 截断点回退到 UTF-8 字符边界:按字节切会把一个中文字切成两半,
// Prompt 里出现乱码字符。
func (m *Manager) truncate(s string) string {
	limit := m.maxDiffChars()
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n... (diff 截断,共 %d 字符)", utf8.RuneCountInString(s))
}

// copyVariables 浅拷贝变量表:返回的 map 与执行的变量表解耦,
// 调用方误改返回值不会污染执行状态(约定 15:共享可变状态)。
func copyVariables(vars map[string]any) map[string]any {
	if vars == nil {
		return nil
	}
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}
