package runtime

import (
	"context"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

// humanGateWorkflowYAML 线性人工闸门:plan → approve(human) → execute → submit。
// 对应内置示例 approval-demo 的拓扑(闸门后仍有副作用节点)。
const humanGateWorkflowYAML = `
version: "1"
workflow:
  id: human-gate
  name: Human Gate
nodes:
  - id: plan
    type: agent
    agent: mock-claude
    mode: plan
  - id: approve
    type: human
    prompt: "方案已生成,请审批"
    responses: ["approve", "reject", "continue"]
  - id: execute
    type: agent
    agent: mock-pi
    mode: execute
  - id: submit
    type: skill
    skill: submit
    args:
      message: "feat: done"
edges:
  - from: plan
    to: approve
  - from: approve
    to: execute
  - from: execute
    to: submit
`

// 回归:人工闸门点 reject 必须中止工作流。此前 runHumanNode 对任何响应
// (含 reject)一律返回 NodeSuccess,拒绝后 execute 照常执行、执行最终标
// COMPLETED,与路由决策路径的 reject→FAILED 语义不一致。
func TestHumanRejectAbortsExecution(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, humanGateWorkflowYAML)

	exec, err := f.engine.Start(context.Background(), wf, "需要审批的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	resumed, err := f.engine.Resume(context.Background(), exec.ID, map[string]any{"response": "reject"})
	if err != nil {
		t.Fatalf("resume reject: %v", err)
	}
	final := waitForState(t, f.engine, resumed.ID, model.ExecutionFailed, model.ExecutionCompleted)

	if final.State != model.ExecutionFailed {
		t.Fatalf("reject 后状态 = %s, want FAILED(此前 reject 被当作 continue,最终 COMPLETED)", final.State)
	}
	if final.NodeStates["approve"] != string(model.NodeFailed) {
		t.Errorf("approve 节点状态 = %s, want FAILED", final.NodeStates["approve"])
	}
	// 闸门之后的副作用节点不得执行
	if got := f.pagt.Calls("execute"); got != 0 {
		t.Errorf("reject 后 execute 仍被调用 %d 次,应为 0", got)
	}
	types := map[string]bool{}
	for _, ev := range f.bus.History() {
		types[ev.Type] = true
	}
	if !types[event.NodeFailed] {
		t.Errorf("缺少 node.failed 事件: %v", types)
	}
}
