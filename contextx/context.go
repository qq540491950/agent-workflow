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
		"task":         state["task"],
		"plan":         state["plan"],
		"git_diff":     m.truncate(str(state["git_diff"])),
		"review":       state["review"],
		"test_result":  state["test_result"],
		"iteration":    state["iteration"],
		"variables":    copyVariables(ec.Variables),
		"node_id":      nodeID,
		"workflow_id":  ec.WorkflowID,
		"execution_id": ec.ExecutionID,
	}

	switch mode {
	case "review":
		// Review 只需要:任务、方案、diff、测试结果、上一次 Review、迭代数
		data["relevant_files"] = state["relevant_files"]
	case "execute", "fix":
		// 执行类节点:任务、方案、上一次 Review 的问题、人工补充指令
		data["issues"] = state["issues"]
		data["user_instruction"] = state["user_instruction"]
	case "plan":
		// Plan 只需要任务本身与变量
		delete(data, "git_diff")
		delete(data, "review")
	}
	return data
}

// SaveNodeOutput 把节点输出写入执行状态,供后续节点按需引用。
// 只保留关键工件,不复制整棵输出树。
func (m *Manager) SaveNodeOutput(mode string, nodeID string, result map[string]any, state map[string]any) {
	if state == nil {
		return
	}
	switch mode {
	case "plan":
		state["plan"] = str(result["summary"])
		if p, ok := result["output"]; ok {
			state["plan"] = str(p)
		}
	case "review":
		state["review"] = str(result["summary"])
		if issues, ok := result["issues"]; ok {
			state["issues"] = issues
		}
	case "execute", "fix":
		state["last_output"] = str(result["summary"])
	case "test":
		state["test_result"] = str(result["output"])
	}
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
