package runtime

// 失败分支(on_failure)的端到端语义:
//   - 失败节点重试耗尽后走失败边,失败节点仍为 FAILED;
//   - 执行可以 COMPLETED,但必须带 via_failure_branch;
//   - 成功路径不执行失败分支。

import (
	"context"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

const onFailureWorkflowYAML = `
version: "1"
workflow:
  id: on-failure-demo
  name: On Failure Demo
nodes:
  - id: fail
    type: agent
    agent: mock-fail
    mode: plan
  - id: recover
    type: agent
    agent: mock-claude
    mode: plan
  - id: done
    type: agent
    agent: mock-claude
    mode: plan
edges:
  - from: fail
    to: recover
    on_failure: true
  - from: fail
    to: done
`

func registerFailingAgent(t *testing.T, eng *Engine, failWith string) {
	t.Helper()
	a := mock.New(mock.Options{
		ID:   "mock-fail",
		Name: "Mock Fail",
		Scripts: map[string]*mock.Script{
			"plan": {FailWith: failWith},
		},
	})
	if err := eng.Agents.Replace(a); err != nil {
		t.Fatalf("register failing agent: %v", err)
	}
	eng.Perms.Set("mock-fail", permission.Policy{FilesystemRead: true, GitRead: true})
}

func TestOnFailureRoutesToFailureBranch(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	registerFailingAgent(t, f.engine, "boom")
	wf := loadWf(t, f.engine, onFailureWorkflowYAML)
	if len(wf.Edges) != 2 || !wf.Edges[0].OnFailure {
		t.Fatalf("DSL 未保留失败边: %+v", wf.Edges)
	}

	exec, err := f.engine.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if exec.State != model.ExecutionCompleted {
		t.Fatalf("失败被失败分支接管后应 COMPLETED,实际 %s(%s)", exec.State, exec.Error)
	}
	if len(exec.ViaFailureBranch) != 1 || exec.ViaFailureBranch[0] != "fail" {
		t.Fatalf("via_failure_branch = %v, want [fail]", exec.ViaFailureBranch)
	}
	if got := exec.NodeStates["fail"]; got != string(model.NodeFailed) {
		t.Errorf("失败节点状态 = %q, want FAILED", got)
	}
	if got := exec.NodeStates["recover"]; got != string(model.NodeSuccess) {
		t.Errorf("失败分支节点状态 = %q, want SUCCESS", got)
	}
	if got := exec.NodeStates["done"]; got != "" {
		t.Errorf("成功分支不应执行,实际 done=%q", got)
	}

	// 事件流必须带 via_failure_branch(审计/UI 依据)。
	found := false
	for _, ev := range f.bus.History() {
		if ev.Type != event.WorkflowCompleted {
			continue
		}
		if got, ok := ev.Data["via_failure_branch"]; ok {
			found = true
			if list, ok := got.([]string); !ok || len(list) != 1 || list[0] != "fail" {
				t.Errorf("workflow.completed.via_failure_branch = %#v", got)
			}
		}
	}
	if !found {
		t.Fatal("workflow.completed 事件缺少 via_failure_branch")
	}
}

func TestOnFailureNotUsedOnSuccess(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	// 用同一个 mock-claude 作为 fail 节点:成功路径。
	registerFailingAgent(t, f.engine, "")
	wf := loadWf(t, f.engine, `
version: "1"
workflow:
  id: on-failure-success
  name: On Failure Success
nodes:
  - id: fail
    type: agent
    agent: mock-fail
    mode: plan
  - id: recover
    type: agent
    agent: mock-claude
    mode: plan
  - id: done
    type: agent
    agent: mock-claude
    mode: plan
edges:
  - from: fail
    to: recover
    on_failure: true
  - from: fail
    to: done
`)
	// mock-fail 的 plan 没有 FailWith 时默认成功。
	exec, err := f.engine.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if exec.State != model.ExecutionCompleted {
		t.Fatalf("成功路径应 COMPLETED,实际 %s(%s)", exec.State, exec.Error)
	}
	if len(exec.ViaFailureBranch) != 0 {
		t.Fatalf("成功路径不应记录失败分支,实际 %v", exec.ViaFailureBranch)
	}
	if got := exec.NodeStates["done"]; got != string(model.NodeSuccess) {
		t.Errorf("正常出口节点状态 = %q, want SUCCESS", got)
	}
	if got := exec.NodeStates["recover"]; got != "" {
		t.Errorf("失败分支不应执行,实际 recover=%q", got)
	}
}
