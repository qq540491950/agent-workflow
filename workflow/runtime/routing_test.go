package runtime

import (
	"context"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件覆盖"静默成功"类缺陷:工作流在语义上没走完,执行却被标
// COMPLETED,UI 与用户都以为成功。两条:
//   - 决策节点没有任何出口匹配且无默认分支(#R2)
//   - 决策节点存在多条回边时,第二条回边子图被丢弃(#R3)

// noFallbackRouteYAML:review 的唯一出口带条件,没有无条件兜底,
// 也没有任何回边 —— 决策不匹配时没有任何合法路径可走。
const noFallbackRouteYAML = `
workflow:
  id: no-fallback
  name: 无默认出口
nodes:
  - id: review
    name: Review
    type: agent
    agent: mock-claude
    mode: review
  - id: submit
    name: Submit
    type: skill
    skill: log
edges:
  - from: review
    to: submit
    condition: decision == "APPROVED"
settings:
  max_iterations: 3
`

// TestNoMatchingBranchFailsExecution 验证:决策节点的条件出口不匹配
// 且无默认分支时,执行必须落 FAILED(此前直接收尾 → COMPLETED,
// 而 submit 从未执行)。
func TestNoMatchingBranchFailsExecution(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, noFallbackRouteYAML)

	exec, err := f.engine.Start(context.Background(), wf, "会被拒绝的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Fatalf("条件出口不匹配时应 FAILED,实际 %s (error=%q)", final.State, final.Error)
	}
	if !strings.Contains(final.Error, "没有匹配的出口分支") {
		t.Errorf("失败原因应说明无可用出口, 实际 %q", final.Error)
	}
	// 错误信息要带决策值与已尝试条件,便于用户定位
	if !strings.Contains(final.Error, "REJECTED") || !strings.Contains(final.Error, "APPROVED") {
		t.Errorf("错误信息应含 decision 与已尝试条件, 实际 %q", final.Error)
	}
	// submit 不得被当成已完成
	if st := final.NodeStates["submit"]; st == string(model.NodeSuccess) {
		t.Errorf("submit 未匹配分支不应成功, 实际 %q", st)
	}
	// 必须发出节点失败事件,否则 UI 只看到静默收尾
	failed := false
	for _, ev := range f.bus.History() {
		if ev.Type == event.NodeFailed && ev.NodeID == "review" {
			failed = true
		}
	}
	if !failed {
		t.Error("应发出 node.failed 事件(节点 review)")
	}
}

// multiBackEdgeYAML:plan → review,review 有两条回边(REJECTED→fix、
// REFACTOR→refactor 都回到 review)与一条出口(APPROVED→submit)。
// 修复前只有第一条回边进入 IR,refactor 子图永不执行,但校验器判定它
// "可达"因而通过 —— 用户既看不到错误也查不到原因。
const multiBackEdgeYAML = `
workflow:
  id: multi-back-edge
  name: 多条回边
nodes:
  - id: plan
    name: Plan
    type: agent
    agent: mock-claude
    mode: plan
  - id: review
    name: Review
    type: agent
    agent: mock-claude
    mode: review
  - id: fix
    name: Fix
    type: skill
    skill: log
  - id: refactor
    name: Refactor
    type: skill
    skill: log
  - id: submit
    name: Submit
    type: skill
    skill: log
edges:
  - from: plan
    to: review
  - from: review
    to: submit
    condition: decision == "APPROVED"
  - from: review
    to: fix
    condition: decision == "REJECTED"
  - from: fix
    to: review
  - from: review
    to: refactor
    condition: decision == "REFACTOR"
  - from: refactor
    to: review
settings:
  max_iterations: 3
`

// TestSecondBackEdgeBranchIsCompiled 验证多条回边都被编译进 IR:
// 走 REFACTOR 回边时 refactor 节点必须真实执行(修复前该子图被丢弃,
// 条件恒不成立,直接走出口)。
func TestSecondBackEdgeBranchIsCompiled(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, multiBackEdgeYAML)

	// 编译期断言:两条回边的目标都在 Plan 内(不依赖运行时决策)
	plan := compiler.BuildPlan(wf)
	inPlan := map[string]bool{}
	for _, id := range plan.Flatten() {
		inPlan[id] = true
	}
	for _, id := range []string{"fix", "refactor", "submit"} {
		if !inPlan[id] {
			t.Errorf("节点 %s 未进入执行计划, plan=%v", id, plan.Flatten())
		}
	}

	// 运行时:decision 走 REFACTOR 回边 → refactor 执行 → 再次 APPROVED 收尾
	exec, err := f.engine.Start(context.Background(), wf, "多回边任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED, 实际 %s (%s)", final.State, final.Error)
	}
	if final.NodeStates["submit"] != string(model.NodeSuccess) {
		t.Errorf("最终应走 APPROVED 出口到 submit, 实际 %v", final.NodeStates)
	}
}

// TestRefactorBackEdgeActuallyRuns 验证 REFACTOR 回边真实可走通:
// 第一轮 REFACTOR → refactor 节点执行 → 第二轮 APPROVED → submit。
// 修复前 REFACTOR 条件没有任何分支承载,refactor 永不执行。
func TestRefactorBackEdgeActuallyRuns(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{"REFACTOR", coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, multiBackEdgeYAML)

	exec, err := f.engine.Start(context.Background(), wf, "需要重构的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED, 实际 %s (%s)", final.State, final.Error)
	}
	if final.NodeStates["refactor"] != string(model.NodeSuccess) {
		t.Errorf("REFACTOR 回边应执行 refactor 节点, 实际 %v", final.NodeStates)
	}
	if final.NodeStates["fix"] != "" {
		t.Errorf("未走 REJECTED 回边, fix 不应执行, 实际 %v", final.NodeStates)
	}
	if final.NodeStates["submit"] != string(model.NodeSuccess) {
		t.Errorf("第二轮 APPROVED 后应到 submit, 实际 %v", final.NodeStates)
	}
}

// TestSingleUnconditionalExitStillWorks 回归:补了默认分支的正常拓扑
// 仍走默认出口,不被新增的"无匹配即失败"逻辑误伤。
func TestSingleUnconditionalExitStillWorks(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, `
workflow:
  id: fallback-ok
  name: 有默认出口
nodes:
  - id: review
    name: Review
    type: agent
    agent: mock-claude
    mode: review
  - id: notify
    name: Notify
    type: skill
    skill: log
edges:
  - from: review
    to: notify
    condition: decision == "APPROVED"
  - from: review
    to: notify
`)
	exec, err := f.engine.Start(context.Background(), wf, "有兜底的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("有默认分支时应 COMPLETED, 实际 %s (%s)", final.State, final.Error)
	}
	if final.NodeStates["notify"] != string(model.NodeSuccess) {
		t.Errorf("默认分支应被走到, notify=%q", final.NodeStates["notify"])
	}
}

// variablesYAML:工作流在 DSL variables 段声明 env/tier,prompt 与条件
// 都引用它。修复前 Engine.Start 只取运行请求里的 variables,DSL 段完全
// 不生效:{{variable.env}} 渲染空串、条件恒假。
const variablesYAML = `
workflow:
  id: wf-vars
  name: 变量工作流
variables:
  env: prod
  tier: gold
nodes:
  - id: plan
    name: Plan
    type: agent
    agent: mock-claude
    mode: plan
    prompt: "环境={{variable.env}} 等级={{variable.tier}} 任务={{task}}"
  - id: review
    name: Review
    type: agent
    agent: mock-claude
    mode: review
  - id: prod_only
    name: ProdOnly
    type: skill
    skill: log
  - id: dev_only
    name: DevOnly
    type: skill
    skill: log
edges:
  - from: plan
    to: review
  - from: review
    to: prod_only
    condition: variables.env == "prod"
  - from: review
    to: dev_only
    condition: variables.env == "dev"
`

// TestWorkflowVariablesReachRuntime 验证 DSL variables 段在运行时生效:
// 变量参与条件路由(prod_only 被选中,dev_only 不被选中)。
func TestWorkflowVariablesReachRuntime(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, variablesYAML)
	if wf.Variables["env"] != "prod" {
		t.Fatalf("DSL 变量应被解析进 workflow: %v", wf.Variables)
	}

	exec, err := f.engine.Start(context.Background(), wf, "变量任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED, 实际 %s (%s)", final.State, final.Error)
	}
	if final.NodeStates["prod_only"] != string(model.NodeSuccess) {
		t.Errorf("DSL 变量未生效:variables.env==\"prod\" 分支应被选中, 实际 %v", final.NodeStates)
	}
	if _, ran := final.NodeStates["dev_only"]; ran {
		t.Errorf("dev_only 不应被执行, 实际 %v", final.NodeStates)
	}
	// 变量应落到执行记录,便于 UI 排查
	if final.Variables["env"] != "prod" || final.Variables["tier"] != "gold" {
		t.Errorf("执行记录的 variables 应含 DSL 变量, 实际 %v", final.Variables)
	}
}

// TestRunVariablesOverrideWorkflowVariables 验证优先级:运行请求传入的
// 变量覆盖 DSL 默认值(单次运行临时改环境)。
func TestRunVariablesOverrideWorkflowVariables(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, variablesYAML)

	exec, err := f.engine.Start(context.Background(), wf, "覆盖任务",
		map[string]any{"env": "dev"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED, 实际 %s (%s)", final.State, final.Error)
	}
	if final.NodeStates["dev_only"] != string(model.NodeSuccess) {
		t.Errorf("请求变量应覆盖 DSL 变量: 实际 %v", final.NodeStates)
	}
	// 未被覆盖的键仍取 DSL 默认值
	if final.Variables["tier"] != "gold" {
		t.Errorf("未覆盖的键应保留 DSL 默认值, 实际 %v", final.Variables)
	}
}
