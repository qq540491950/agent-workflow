package runtime

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"运行期对非法 mode fail closed,且绝不因为 mode 非法而
// 跳过写权限检查"。
//
// 缺口:运行期此前**不校验 mode 取值**(校验期也不校验 —— 见
// workflow/validator/mode_domain_test.go)。于是 mode 拼错("exectue")
// 的节点照常执行:写权限检查被跳过(ModeRequiresWrite 对未知 mode 返回
// false)、上下文链没有对应槽位、用户以为节点按 execute 跑了,实际它
// 跑的是"一个没人认识的 mode"(约定 14)。
//
// 两条要求:
//  1. 非法 mode → 节点**失败**(fail closed),不执行任何副作用;
//  2. 未知 mode 的写权限检查**不得被跳过** —— ModeRequiresWrite 对
//     未知/空 mode 恒为 true,所以写权限这道门始终在非法 mode 判定之前。
//
// 为什么直接调 runAgentNode:引擎的 Start 会先过校验期(非法 mode 在
// 那里就被挡住,根本跑不起来)。而历史快照、备份恢复出来的执行未必
// 经过当次校验 —— 运行期必须自己 fail closed,所以这里绕过校验直接
// 调节点执行路径。

// runAgentNodeWithMode 以指定 mode 跑一个 agent 节点,返回节点结果。
func runAgentNodeWithMode(t *testing.T, eng *Engine, mode string) compiler.NodeOutcome {
	t.Helper()
	node := &model.Node{
		ID:   "work",
		Name: "work",
		Type: model.NodeTypeAgent,
		Config: map[string]any{
			"agent": "mock-a",
			"mode":  mode,
		},
	}
	exec := &model.Execution{ID: "exec-mode", WorkflowID: "wf-mode", Task: "t"}
	env := &compiler.RunEnv{
		Exec: exec,
		WF:   &model.Workflow{ID: "wf-mode", Name: "wf-mode"},
		Bus:  eng.Bus,
		Vars: map[string]any{},
	}
	st := &fakeState{m: map[string]any{}}
	return eng.runAgentNode(context.Background(), env, node, st)
}

// mockCallsOf 返回 mock Agent 在某 mode 上的调用次数(-1 = 取不到)。
func mockCallsOf(t *testing.T, eng *Engine, mode string) int {
	t.Helper()
	a, err := eng.Agents.Get("mock-a")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	m, ok := a.(*mock.Agent)
	if !ok {
		t.Fatalf("mock-a 不是 mock.Agent(%T)", a)
	}
	return m.Calls(mode)
}

// TestIllegalModeFailsNode 非法 mode → 节点失败,原因可读,且**不调用 Agent**
// (fail closed:一个不认识的 mode 绝不能"照常执行" —— 它到底该做什么
// 没人知道,包括它要不要写文件)。
func TestIllegalModeFailsNode(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	out := runAgentNodeWithMode(t, eng, "exectue")
	if out.State != model.NodeFailed {
		t.Fatalf("非法 mode 的节点必须失败,实际状态: %s(错误: %q)", out.State, out.Error)
	}
	if !strings.Contains(out.Error, "exectue") {
		t.Errorf("失败原因应指出非法 mode %q,实际: %q", "exectue", out.Error)
	}
	if !strings.Contains(out.Error, "mode") {
		t.Errorf("失败原因应说明是 mode 的问题,实际: %q", out.Error)
	}
	if n := mockCallsOf(t, eng, "exectue"); n != 0 {
		t.Errorf("非法 mode 的节点不得调用 Agent,实际调用 %d 次", n)
	}
}

// TestUnknownModeStillChecksWritePermission 未知 mode 的写权限检查
// **不得被跳过**:写权限被收紧时,mode 拼错的节点必须因为"没有写权限"
// 而失败(而不是因为"mode 非法"或干脆跑过去)。
//
// 这条锁住的是执行顺序:写权限检查在前,非法 mode 判定在后 ——
// ModeRequiresWrite 对未知 mode 恒返回 true,两道门都不会被绕过。
func TestUnknownModeStillChecksWritePermission(t *testing.T) {
	eng := newMinimalEngine(t)
	// 全局策略:可读、**不可写**。
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: false})

	out := runAgentNodeWithMode(t, eng, "exectue")
	if out.State != model.NodeFailed {
		t.Fatalf("未知 mode 必须先过写权限检查,实际状态: %s(错误: %q)", out.State, out.Error)
	}
	if !strings.Contains(out.Error, "fs:write") {
		t.Errorf("未知 mode 必须先过写权限检查(失败原因应含 fs:write),实际: %q", out.Error)
	}
}

// TestLegalModeStillRuns 对照组:取值域内的 mode 必须照常执行
// (fail closed 不能变成"一律拒绝")。
func TestLegalModeStillRuns(t *testing.T) {
	for _, mode := range model.AllModes() {
		eng := newMinimalEngine(t)
		eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
		out := runAgentNodeWithMode(t, eng, mode)
		if out.State != model.NodeSuccess {
			t.Errorf("合法 mode=%q 必须正常执行,实际: %s(%q)", mode, out.State, out.Error)
		}
	}
}
