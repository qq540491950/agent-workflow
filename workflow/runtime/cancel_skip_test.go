package runtime

import (
	"context"
	"testing"
	"time"

	"agentworkflow/workflow/model"
)

// 本文件锁住"取消一个等待人工的执行时,未终结的节点必须落成 SKIPPED"。
//
// 缺口:取消 WAITING_USER 的执行走的是"没有协程会再来收尾"的那条路
// (Cancel 的 CAS 分支)—— 没有任何 goroutine 会再跑,于是节点状态停在
// WAITING / PENDING:执行状态是"已取消",节点表里却还挂着"等待中/未开始",
// 两处事实对不上(约定 14 的同族:状态与事实不符)。用户点一次取消,
// 监控页上那几个节点永远停在"等待中"。
//
// 修复:取消落定后,把这次执行里**非终态**的节点落成 SKIPPED,两处同步:
//   - exec.NodeStates(node_states_json):节点级视图;
//   - execution_nodes 明细行:仍在跑/等待的行改成 SKIPPED。

// cancelWaitingExecution 起一个会停在人工闸门上的执行并取消它。
func cancelWaitingExecution(t *testing.T) (*Engine, *model.Execution) {
	t.Helper()
	f := newFixture(t, nil)
	wf := loadWf(t, f.engine, humanGateWorkflowYAML)

	exec, err := f.engine.Start(context.Background(), wf, "需要审批的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)
	if err := f.engine.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionCancelled)
	return f.engine, exec
}

// TestCancelMarksNonTerminalNodesSkipped 取消后,非终态节点必须落成 SKIPPED。
func TestCancelMarksNonTerminalNodesSkipped(t *testing.T) {
	eng, exec := cancelWaitingExecution(t)

	// 节点状态是在状态 CAS **之后**写的(状态先落定,再收尾节点),
	// 所以这里轮询等它落定,而不是假设它已经写好了。
	final := waitForNodeState(t, eng, exec.ID, "approve", string(model.NodeSkipped))
	for _, id := range []string{"approve", "execute", "submit"} {
		if got := final.NodeStates[id]; got != string(model.NodeSkipped) {
			t.Errorf("取消后节点 %q 的状态 = %q, want SKIPPED(执行已取消,这些节点不会跑了)",
				id, got)
		}
	}
	// 已完成的节点保持原状(不能把跑成功的节点也标成 SKIPPED)。
	if got := final.NodeStates["plan"]; got != string(model.NodeSuccess) {
		t.Errorf("已完成的 plan 节点状态 = %q, want SUCCESS(取消不应改写已终结的节点)", got)
	}
}

// waitForNodeState 轮询等某个节点的状态落成期望值(节点收尾是异步写的)。
func waitForNodeState(t *testing.T, eng *Engine, execID, nodeID, want string) *model.Execution {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last *model.Execution
	for time.Now().Before(deadline) {
		exec, err := eng.Repo.GetExecution(execID)
		if err == nil {
			last = exec
			if exec.NodeStates[nodeID] == want {
				return exec
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("节点 %q 的状态在 3s 内没有落成 %q,实际: %v", nodeID, want, last.NodeStates)
	return nil
}

// TestCancelSyncsExecutionNodeRows execution_nodes 明细行必须同步:
// 仍在"运行中/等待中"的行要落成 SKIPPED,否则节点明细表与节点状态视图
// 对不上(同一份事实的两个出口)。
func TestCancelSyncsExecutionNodeRows(t *testing.T) {
	eng, exec := cancelWaitingExecution(t)

	// 明细行同样是异步写的,轮询等它落定。
	deadline := time.Now().Add(3 * time.Second)
	var nodes []*model.ExecutionNode
	for time.Now().Before(deadline) {
		var err error
		nodes, err = eng.Repo.ListExecutionNodes(exec.ID)
		if err != nil {
			t.Fatalf("list nodes: %v", err)
		}
		pending := false
		for _, n := range nodes {
			if n.State == model.NodeRunning || n.State == model.NodeWaiting || n.State == model.NodePending {
				pending = true
			}
		}
		if !pending && len(nodes) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(nodes) == 0 {
		t.Fatal("没有节点明细行(用例前提不成立)")
	}
	for _, n := range nodes {
		if n.State == model.NodeRunning || n.State == model.NodeWaiting || n.State == model.NodePending {
			t.Errorf("节点明细 %q 仍是 %s,取消后应为 SKIPPED", n.NodeID, n.State)
		}
	}
}
