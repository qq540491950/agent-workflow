package runtime

// 缺陷 #205:`output` 键的 resume/实时口径差 —— 恢复后走错分支。
//
// 复现场景(与第七轮终审复审的实测一致):
//
//	skill 节点输出 "ANTHROPIC_API_KEY=sk-xyz"(凭据形态),
//	边条件 `output contains "sk-"`。
//
//	首次运行(实时):条件在**原文**上求值 → 走 secret_path;
//	HITL 答复触发 resume:引擎回放**已脱敏**的 state_json
//	(output 变成 "ANTHROPIC_API_KEY=***")→ 路由用脱敏值**重新求值**
//	→ 改走 normal_path。
//
//	在审批流里 = 人批准 A 分支,恢复后走 B 分支(约定 #14 同族:
//	该走 A 却走了 B,比崩溃更危险 —— 用户看不到任何异常)。
//
// 本文件锁住的行为:路由一旦选定出口分支,这个选择就是本次执行的
// 既成事实;恢复执行必须**回放**它,而不是拿脱敏后的 output 重算。

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// maskedOutputRouteYAML:决策节点的输出含凭据形态,边条件查 output。
//
// 拓扑:cred(决策节点)→ 条件 output contains "sk-" → ask(人工闸门)
//
//	→ secret_path;兜底出口 → normal_path。
//
// 闸门 ask 是**条件分支的第一个节点**:暂停时状态快照里的 output
// 仍是 cred 的输出(脱敏副本),于是恢复时重新求值必然得到不同的结果。
const maskedOutputRouteYAML = `
version: "1"
workflow:
  id: masked-output-route
  name: 脱敏输出路由
nodes:
  - id: cred
    name: 产出凭据
    type: skill
    skill: log
    args:
      message: "ANTHROPIC_API_KEY=sk-xyz"
  - id: ask
    name: 审批闸门
    type: human
    prompt: "分支已选定,请审批"
    responses: ["approve", "reject"]
  - id: secret_path
    name: 含凭据分支
    type: skill
    skill: log
    args:
      message: "secret"
  - id: normal_path
    name: 普通分支
    type: skill
    skill: log
    args:
      message: "normal"
edges:
  - from: cred
    condition: output contains "sk-"
    to: ask
  - from: cred
    to: normal_path
  - from: ask
    to: secret_path
`

// TestResumeKeepsBranchChosenBeforePause 恢复执行必须沿用**暂停前已选定**
// 的出口分支。
//
// 修复前:resume 时 output 是脱敏副本("ANTHROPIC_API_KEY=***"),
// 条件重新求值不成立 → 走兜底的 normal_path → 人批准的分支被换掉。
func TestResumeKeepsBranchChosenBeforePause(t *testing.T) {
	f := newFixture(t, nil)
	wf := loadWf(t, f.engine, maskedOutputRouteYAML)

	exec, err := f.engine.Start(context.Background(), wf, "产出凭据的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	// 首次运行(实时原文):条件成立 → 走条件分支;兜底分支不得执行。
	if got := exec.NodeStates["ask"]; got != string(model.NodeWaiting) {
		t.Fatalf("实时路径: ask 状态 = %q, want WAITING(条件成立 → 应走条件分支的闸门)", got)
	}
	if got := exec.NodeStates["normal_path"]; got != "" {
		t.Fatalf("实时路径: normal_path 状态 = %q, want 空(条件成立时不得走兜底分支)", got)
	}

	// 恢复前先确认前提:落库的 state_json 里 output 已是脱敏副本
	// (这正是"重新求值会走错分支"的根源)。
	persisted, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	got, _ := persisted.StateData[compiler.KeyOutput].(string)
	if !strings.Contains(got, "***") {
		t.Fatalf("落库的 output = %q, want 脱敏副本(含 ***);本用例的前提不成立", got)
	}
	if strings.Contains(got, "sk-") {
		t.Fatalf("落库的 output 含凭据明文: %q", got)
	}

	// 人工批准 → resume。
	resumed, err := f.engine.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	final := waitForState(t, f.engine, resumed.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("resume 后状态 = %s, want COMPLETED (error=%q)", final.State, final.Error)
	}

	// 关键断言:恢复后必须仍是**人批准的那条分支**。
	if got := final.NodeStates["normal_path"]; got != "" {
		t.Errorf("resume 后走了 normal_path(状态 %q):人批准的分支被换掉了"+
			"(恢复时用脱敏后的 output 重新求值 → 条件不成立 → 兜底分支被选中)", got)
	}
	if got := final.NodeStates["secret_path"]; got != string(model.NodeSuccess) {
		t.Errorf("resume 后 secret_path 状态 = %q, want SUCCESS(分支应被回放,不被换掉)", got)
	}
	if got := final.NodeStates["ask"]; got != string(model.NodeSuccess) {
		t.Errorf("resume 后 ask 状态 = %q, want SUCCESS(闸门答复应被消费)", got)
	}
}

// TestRouteBranchDecisionIsStructuredState 路由选定的分支必须以
// **结构化状态键**落库(不脱敏),否则恢复时读不到它、回放无从谈起。
//
// 约定 #8:结构化键(路由依据)必须原样保留;自由文本键才脱敏。
func TestRouteBranchDecisionIsStructuredState(t *testing.T) {
	f := newFixture(t, nil)
	wf := loadWf(t, f.engine, maskedOutputRouteYAML)

	exec, err := f.engine.Start(context.Background(), wf, "产出凭据的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	persisted, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	key := compiler.RouteBranchKey("cred")
	raw, ok := persisted.StateData[key]
	if !ok {
		t.Fatalf("状态快照缺少路由分支记录 %q: %v", key, persisted.StateData)
	}
	idx, ok := compiler.IntOfOk(raw)
	if !ok {
		t.Fatalf("路由分支记录 %q 的值不是整数: %v(%T)", key, raw, raw)
	}
	if idx != 0 {
		t.Errorf("路由分支记录 %q = %d, want 0(条件分支 ask 是第一条出口)", key, idx)
	}
}
