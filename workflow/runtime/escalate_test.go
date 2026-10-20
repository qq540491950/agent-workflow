package runtime

import (
	"context"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// TestClassifyEscalate 锁住 Escalate 的分类规则(纯函数,白盒)。
//
// 依据全部是 compiler 写入的结构化状态键(约定 #13 同源),不猜字符串。
func TestClassifyEscalate(t *testing.T) {
	cases := []struct {
		name     string
		delta    map[string]any
		wantKind escalateKind
	}{
		{"循环出口条件成立", map[string]any{compiler.KeyLoopExited: true}, escalateExpected},
		{"HITL 暂停", map[string]any{compiler.KeyAbort: "WAIT_USER"}, escalateExpected},
		{"节点失败中断", map[string]any{compiler.KeyAbort: "FAILED:plan"}, escalateUnexpected},
		{"循环上限", map[string]any{compiler.KeyAbort: "LOOP_LIMIT:review"}, escalateUnexpected},
		{"循环停滞", map[string]any{compiler.KeyLoopStuck: true}, escalateUnexpected},
		{"循环未退出(false 不算出口)", map[string]any{compiler.KeyLoopExited: false}, escalateUnexpected},
		{"无线索", nil, escalateUnexpected},
		{"空字符串 abort 视为无线索", map[string]any{compiler.KeyAbort: ""}, escalateUnexpected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, reason := classifyEscalate(tc.delta)
			if kind != tc.wantKind {
				t.Errorf("分类 = %s,want %s(reason=%q)", kind, tc.wantKind, reason)
			}
			if reason == "" {
				t.Error("必须给出可读原因(否则日志/审计里无法判断发生了什么)")
			}
		})
	}
}

// TestNormalLoopExitIsNotWarned 锁住 P2-5:正常跑通的循环不得产生
// "异常 Escalate"。
//
// 此前引擎把**所有** Escalate 一律记 WARN,于是任何含循环的工作流
// 正常跑通都会产出 WARN 告警 —— 噪声掩盖真正的异常,用户与运维都会
// 对告警脱敏。
func TestNormalLoopExitIsNotWarned(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected, coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, err := f.engine.Start(context.Background(), wf, "正常收敛的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionCompleted)

	expected, unexpected := f.engine.EscalateStats()
	if expected == 0 {
		t.Error("正常循环退出应至少产生一次预期内 Escalate(实际 0)")
	}
	if unexpected != 0 {
		t.Errorf("正常跑通不应有异常 Escalate: %d 次", unexpected)
	}
	// 事件流里同样必须能区分(供审计/UI 使用)
	found := 0
	for _, ev := range f.bus.History() {
		if ev.Type != "runtime.escalate" {
			continue
		}
		found++
		kind, _ := ev.Data["kind"].(string)
		if kind != string(escalateExpected) {
			t.Errorf("正常跑通出现异常 Escalate 事件: %v", ev.Data)
		}
	}
	if found == 0 {
		t.Error("应发出 runtime.escalate 事件(审计:为什么循环在这里结束了)")
	}
}

// TestLoopLimitEscalateIsUnexpected 锁住:on_loop_limit=fail(循环没有自行
// 收敛 → 执行失败)属于异常 Escalate,不能被"预期内"吞掉。
//
// 注意与 on_loop_limit=wait_user 区分:后者是**设计好的**人工介入暂停
// (HITL),属预期内;前者是"工作流没能自己收敛",属异常。
func TestLoopLimitEscalateIsUnexpected(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, `
version: "1"
settings:
  max_iterations: 2
  on_loop_limit: fail
workflow:
  id: loop-limit-fail
  name: Loop Limit Fail
nodes:
  - id: plan
    type: agent
    agent: mock-claude
    mode: plan
  - id: execute
    type: agent
    agent: mock-pi
    mode: execute
  - id: review
    type: agent
    agent: mock-claude
    mode: review
  - id: fix
    type: agent
    agent: mock-pi
    mode: fix
  - id: submit
    type: skill
    skill: submit
    args:
      message: "feat: done"
edges:
  - from: plan
    to: execute
  - from: execute
    to: review
  - from: review
    condition: decision == "APPROVED"
    to: submit
  - from: review
    condition: decision == "REJECTED"
    to: fix
  - from: fix
    to: review
`)

	exec, err := f.engine.Start(context.Background(), wf, "永不通过的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionFailed)

	_, unexpected := f.engine.EscalateStats()
	if unexpected == 0 {
		t.Error("循环上限(fail 策略)应产生异常 Escalate")
	}
}

// TestLoopLimitWaitUserIsExpectedEscalate 锁住:on_loop_limit=wait_user 是
// 设计好的人工介入暂停,属预期内 Escalate(不产生 WARN 噪声)。
func TestLoopLimitWaitUserIsExpectedEscalate(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, err := f.engine.Start(context.Background(), wf, "永不通过的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	expected, unexpected := f.engine.EscalateStats()
	if expected == 0 {
		t.Error("HITL 暂停应被分类为预期内 Escalate")
	}
	if unexpected != 0 {
		t.Errorf("wait_user 策略下不应有异常 Escalate: %d 次", unexpected)
	}
}

// TestNodeFailureEscalateIsUnexpected 锁住:节点失败中断属于异常 Escalate。
func TestNodeFailureEscalateIsUnexpected(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {FailWith: "boom"},
		},
	})
	eng, _, bus := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: fail-escalate
  name: Fail Escalate
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "会失败的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionFailed)

	_, unexpected := eng.EscalateStats()
	if unexpected == 0 {
		t.Error("节点失败中断应产生异常 Escalate")
	}
	var sawFailed bool
	for _, ev := range bus.History() {
		if ev.Type == event.WorkflowFailed {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Error("应发出 workflow.failed 事件")
	}
}
