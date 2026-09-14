// Package contextx 实现 Context Manager:在不同 Node 之间传递最小化上下文。
// 例如 Review 节点只拿到 Task / Original Plan / Git Diff / Test Result /
// Previous Review / Iteration / Relevant Files,而不是整个项目。
package contextx

import (
	"fmt"

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
	// MaxDiffChars 限制注入 Prompt 的 Git Diff 长度,防止上下文爆炸。
	MaxDiffChars int
}

// NewManager 创建 Context Manager(默认 diff 上限 8000 字符)。
func NewManager() *Manager {
	return &Manager{MaxDiffChars: 8000}
}

// Build 为指定节点模式构造模板渲染变量。state 是 ADK session state 的
// 领域视图(由 runtime 维护)。
func (m *Manager) Build(mode string, ec *ExecutionContext, nodeID string) map[string]any {
	state := ec.State
	if state == nil {
		state = map[string]any{}
	}
	data := map[string]any{
		"task":       state["task"],
		"plan":       state["plan"],
		"git_diff":   m.truncate(str(state["git_diff"])),
		"review":     state["review"],
		"test_result": state["test_result"],
		"iteration":  state["iteration"],
		"variables":  ec.Variables,
		"node_id":    nodeID,
		"workflow_id": ec.WorkflowID,
		"execution_id": ec.ExecutionID,
	}

	switch mode {
	case "review":
		// Review 只需要:任务、方案、diff、测试结果、上一次 Review、迭代数
		data["relevant_files"] = state["relevant_files"]
	case "execute", "fix":
		// 执行类节点:任务、方案、上一次 Review 的问题
		data["issues"] = state["issues"]
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

// iterationOf 读取当前迭代数。
func IterationOf(state map[string]any, nodeID string) int {
	if v, ok := state["iteration"].(int); ok {
		return v
	}
	return 0
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

func (m *Manager) truncate(s string) string {
	if m.MaxDiffChars <= 0 || len(s) <= m.MaxDiffChars {
		return s
	}
	return s[:m.MaxDiffChars] + fmt.Sprintf("\n... (diff 截断,共 %d 字符)", len(s))
}
