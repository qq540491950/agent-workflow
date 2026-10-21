// Package contextx 实现 Context Manager:在不同 Node 之间传递最小化上下文。
// 例如 Review 节点只拿到 Task / Original Plan / Git Diff / Test Result /
// Previous Review / Iteration / Relevant Files,而不是整个项目。
package contextx

import (
	"fmt"
	"sort"
	"strings"
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
//
// **与 workflow/model.ContextSlot\* 的关系**:model 侧是同一组键的**领域
// 定义**,本包是**上下文层**的定义;两者必须逐项一致(键名是写进
// state_json 的持久化契约,漂移会让存量会话状态读不出来)。
// 一致性由两处守卫锁住:
//   - workflow/runtime/mode_contract_test.go(TestModelSlotKeysMatchContextx):
//     跨包的逐项比对;
//   - contextx/mode_source_test.go:本包内的逐项比对。
//
// **为什么这里不直接写 `= model.ContextSlotPlan`(别名)**:运行期的
// 状态脱敏守卫(workflow/runtime/state_mask_test.go 的
// TestStateWritesAreClassified)会**源码扫描**所有状态写入点,把键名
// 解析成字符串再逐个登记分类;它的常量表只收**字符串字面量**常量
// (constStringValues 要求 *ast.BasicLit),别名(选择器表达式)解析
// 不出来 —— 守卫会报"状态键写法无法解析"并连带判定"登记与实现漂移"。
// 那份守卫在 workflow/** 下(本轮只读),因此本包的常量保持字面量,
// 由上面的守卫保证与 model 逐项一致(值一致 = 契约一致)。
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

// chainSlotKeys 是"**上游节点产出的事实**"类槽位(上下文链的键)。
//
// 为什么单独列一份:空槽位告警(#280)只针对这一类 ——
//   - plan / review / issues / test_result / last_output:上游节点的产物
//     (由 SaveNodeOutput 写进会话状态);
//   - git_diff:git 节点的产物;
//   - relevant_files / user_instruction:上下文链里的另外两个槽位。
//
// 刻意**不含**的两个:
//   - task:引擎种进初始状态,永远有值 —— 没有"上游没产出"这回事;
//   - iteration:编译器维护的循环计数,**不是**"上游产出";而且**非循环
//     工作流里它恒为空** —— 把它算进来,每个引用 {{iteration}} 的 prompt
//     都会刷一条告警(纯噪音),而"上游没产出"这条事实与它无关。
//
// 这份清单是"哪些槽位算上下文链"的**唯一定义处**:Build 产出它,
// runtime 只负责把"被提示词引用到的空槽位"报成事件。
var chainSlotKeys = map[string]bool{
	StateKeyPlan:            true,
	StateKeyReview:          true,
	StateKeyIssues:          true,
	StateKeyTestResult:      true,
	StateKeyLastOutput:      true,
	StateKeyGitDiff:         true,
	StateKeyRelevantFiles:   true,
	StateKeyUserInstruction: true,
}

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
//
// 只返回数据表;需要"哪些链槽位是空的"请用 BuildWithSlots。
func (m *Manager) Build(mode string, ec *ExecutionContext, nodeID string) map[string]any {
	data, _ := m.BuildWithSlots(mode, ec, nodeID)
	return data
}

// BuildWithSlots 与 Build 相同,但**额外**返回"键存在但值为空"的链槽位
// 清单(按槽位名排序)。
//
// 这就是 #280 的修法。模板引擎的口径是"**键存在 = 可解析**"(值为 nil
// 渲染成空串、不进 missing 列表)—— 那是**渲染层**的口径,决定"渲染成
// 什么";而"上游到底有没有产出"是**上下文层**的事实,决定"要不要告警"。
// 两个口径**正交**,所以不必(也不该)改 template 包的通用口径:
//   - 键**不存在**({{revwie}} 拼错)→ template 层报 missing → 既有告警;
//   - 键**存在但值为空**({{plan}} 上游没产出)→ 本函数报"空槽位"。
//
// 空槽位的判定口径是"**渲染出去是空的**"(事实对下游不可见):
// nil 与空串/纯空白算空;数字、布尔、列表、map 都不算 —— 它们渲染出来
// 是有内容的(0 / false / [] / {})。
func (m *Manager) BuildWithSlots(mode string, ec *ExecutionContext, nodeID string) (map[string]any, []string) {
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

	// mode 的**可见范围**:不同职责的节点拿到不同的槽位。
	//
	// mode 的取值域与语义只有一份(workflow/model.ModeSpec):这里用
	// model.Mode* 常量做分派,不再自带一份 mode 字面量清单(约定 13)。
	// 不认识的 mode(如 skill 伪 mode)拿基础表 —— 不多给,也不少给。
	spec, known := model.ModeSpecFor(mode)
	if !known {
		spec = model.ModeSpec{Name: mode}
	}
	switch spec.Name {
	case model.ModeReview:
		// Review 只需要:任务、方案、diff、测试结果、上一次 Review、迭代数
		data[StateKeyRelevantFiles] = state[StateKeyRelevantFiles]
	case model.ModeExecute, model.ModeFix:
		// 执行类节点:任务、方案、上一次 Review 的问题、人工补充指令
		data[StateKeyIssues] = state[StateKeyIssues]
		data[StateKeyUserInstruction] = state[StateKeyUserInstruction]
	case model.ModePlan:
		// Plan 只需要任务本身与变量
		delete(data, StateKeyGitDiff)
		delete(data, StateKeyReview)
	}

	// 空槽位清单:只看**这次真的进了数据表**的链槽位(mode 会剔掉几个),
	// 避免报出"这个 mode 根本拿不到"的槽位。
	var empty []string
	for key, val := range data {
		if !chainSlotKeys[key] {
			continue
		}
		if isEmptySlotValue(val) {
			empty = append(empty, key)
		}
	}
	sort.Strings(empty)
	return data, empty
}

// isEmptySlotValue 判定"键存在但值为空"(渲染出去是空的 → 事实不可见)。
//
// 口径:只有 nil 与空串/纯空白算空;数字、布尔、列表、map **都不算** ——
// 它们渲染出来是有内容的(0 / false / [] / {})。例如 review 节点返回
// issues: [] 时,"没有发现问题"这个事实是**可见的**("[]"),不该报成
// "上游没产出"。
func isEmptySlotValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	}
	return false
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
//
// **写进哪个槽位由 model.ModeSpec 决定**(约定 13:mode 的语义只有一份):
// 本包不再自带 mode → 槽位的映射表 —— 此前是 switch 的四组 case,
// 与 model 的 ModeSpec 各演各的:model 认了某个 mode 而本包没认,
// 产物就被静默丢掉(用户看不出任何异常)。
func (m *Manager) SaveNodeOutput(mode string, nodeID string, result map[string]any, w StateWriter) error {
	if w == nil {
		return nil
	}
	slot, ok := outputSlot(mode)
	if !ok || slot == "" {
		// 该 mode 的产出**不进**上下文链(如 submit:ModeSpec.ContextSlot
		// 为空),或 mode 根本不认识(fail closed:宁可不写,也不写错槽位)。
		return nil
	}
	// 分派按**槽位**走(槽位来自 model.ModeSpecFor):每个槽位一个分支,
	// 键名用本包的常量(运行期状态脱敏守卫按源码扫描这些写入点,
	// 见 StateKey* 的说明)。
	switch slot {
	case model.ContextSlotPlan:
		// 方案正文优先取 output(完整产物),空则退回 summary(结论摘要)。
		if err := w.Set(StateKeyPlan, pickText(result, "output", "summary")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyPlan, err)
		}
	case model.ContextSlotReview:
		// 评审结论:summary 优先(结论摘要),空则退回 output。
		if err := w.Set(StateKeyReview, pickText(result, "summary", "output")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyReview, err)
		}
		// 问题清单原样保留(可能是结构化列表,不是文本)。
		if issues, ok := result["issues"]; ok && issues != nil {
			if err := w.Set(StateKeyIssues, issues); err != nil {
				return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyIssues, err)
			}
		}
	case model.ContextSlotLastOutput:
		// 最近一次节点输出:执行类节点与技能节点的产物。
		if err := w.Set(StateKeyLastOutput, pickText(result, "summary", "output")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyLastOutput, err)
		}
	case model.ContextSlotTestResult:
		if err := w.Set(StateKeyTestResult, pickText(result, "output", "summary")); err != nil {
			return fmt.Errorf("写入会话状态 %q 失败: %w", StateKeyTestResult, err)
		}
	default:
		// 未知槽位(fail closed):model 加了新槽位而本包还没认 → **显式失败**,
		// 绝不静默丢弃产物(约定 14:该失败却成功等同 P0)。此前这里是
		// "匹配不到就什么都不写",模型侧新增一个槽位就会让该 mode 的
		// 产出**无声消失**,下游拿到空上下文照常执行。
		return fmt.Errorf("未知的上下文槽位 %q(mode=%q):model.ContextSlot* 新增了槽位,contextx 需要同步处理",
			slot, mode)
	}
	return nil
}

// outputSlot 返回该 mode 的产出应写进哪个上下文槽位。
//
// 唯一来源是 workflow/model.ModeSpec(约定 13:同一份 mode 语义):
//   - 合法的 agent mode → spec.ContextSlot(空串 = 不进上下文链);
//   - 伪 mode(skill 节点类型)→ last_output;
//   - 不认识的 mode → 不写(fail closed)。
//
// 伪 mode 说明:skill 是**节点类型**(model.NodeTypeSkill)而不是 agent
// 节点的 mode,不在 model.AllModes() 的取值域里 —— 但 skill 节点的产物
// 仍要进上下文链(写进 last_output),所以这里单列,不能因为"不在取值域
// 里"就把它的产物丢掉(第 19 轮修复过这个洞)。
func outputSlot(mode string) (string, bool) {
	if spec, ok := model.ModeSpecFor(mode); ok {
		return spec.ContextSlot, true
	}
	if mode == string(model.NodeTypeSkill) {
		return model.ContextSlotLastOutput, true
	}
	return "", false
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
