package runtime

// 本文件锁住"状态迁移失败(CAS 冲突)时的可观测性与事件流一致性"(约定 #14)。
//
// 背景:状态收敛到 CAS 之后,终态迁移不再是"必然成功"的整行写 ——
// 库里的状态可能已被别的写者裁决(用户取消/显式重试/崩溃恢复)。
// 此前三处 `_ = e.transitionAndSave(...)` 把错误整个吞掉:
//
//   - 错误不可观测(连日志都没有);
//   - workflow.completed 照发 → 事件流说"已完成"、库里还是"运行中"。
//
// **判断语义**(明确,不是二选一):
//
//	CAS 冲突 = 放弃本次迁移(绝不覆盖别人的裁决);
//	状态没落库 = 不发终态事件(事件流与 DB 必须一致);
//	终态路径上的冲突 = 真异常 → ERROR + runtime.error;
//	取消路径上的冲突 = 预期竞态 → WARN,不制造 ERROR 噪音。

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

// waitEvent 等待事件流里出现指定类型的事件。
func waitEvent(t *testing.T, bus *event.Bus, typ string, timeout time.Duration) event.UIEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, ev := range bus.History() {
			if ev.Type == typ {
				return ev
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("事件流里没出现 %s 事件(实际: %v)", typ, eventTypes(bus))
	return event.UIEvent{}
}

// eventTypes 汇总事件流里的事件类型(报错信息用)。
func eventTypes(bus *event.Bus) []string {
	var out []string
	for _, ev := range bus.History() {
		out = append(out, ev.Type)
	}
	return out
}

// hasEvent 判断事件流里是否出现过指定类型。
func hasEvent(bus *event.Bus, typ string) bool {
	for _, ev := range bus.History() {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// TestTerminalCommitConflictDoesNotEmitCompletedEvent 终态提交冲突时:
//
//   - 不得发 workflow.completed(状态没落库,事件流不能替别人宣布终态);
//   - 必须发 runtime.error(冲突可观测);
//   - 必须被计数(可观测指标);
//   - 库里的状态不被过期写覆盖。
//
// 注入方式:执行跑着的时候,另一个写者把状态裁决成 CANCELLED;
// 执行收尾时的终态提交(RUNNING → COMPLETED)必然与之冲突。
func TestTerminalCommitConflictDoesNotEmitCompletedEvent(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			// 留出时间窗:让"另一个写者"在执行收尾之前完成裁决
			"plan": {DelayMS: 300, SummaryTemplate: "ok"},
		},
	})
	eng, repo, bus := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: cas-conflict
  name: CAS Conflict
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionRunning)

	// 等节点**真的开始跑**再抢裁决。
	//
	// 执行协程启动时会核对库里的状态(engine.go 的"启动即核对状态"):
	// 若此刻库里已经是 CANCELLED,协程会直接收尾返回 —— 事件流里只有
	// workflow.started,不会有 runtime.error,本用例会等满 10s 超时。
	// 原写法在 Start 之后**立刻**抢裁决,于是"抢在协程核对之前"与
	// "抢在之后"是**竞态**:抢早了 → 静默收尾(超时失败);抢晚了 →
	// 终态提交冲突 → runtime.error(本用例的预期)。
	// 实测:HEAD 上用 GOMAXPROCS=1 跑,3/3 稳定复现超时失败(与本轮改动无关)。
	//
	// 显式等 node.started 事件:把竞态变成确定时序 —— 断言一个字都不改。
	waitEvent(t, bus, event.NodeStarted, 10*time.Second)

	// 另一个写者抢先裁决(模拟用户同时点了取消)
	ok, err := repo.CasExecutionState(exec.ID,
		[]model.ExecutionState{model.ExecutionRunning}, model.ExecutionCancelled)
	if err != nil {
		t.Fatalf("抢占裁决: %v", err)
	}
	if !ok {
		t.Fatal("抢占裁决失败:执行状态已不是 RUNNING")
	}

	// 执行收尾时的终态提交会与库里的状态冲突 → 必须发 runtime.error
	waitEvent(t, bus, "runtime.error", 10*time.Second)

	if hasEvent(bus, event.WorkflowCompleted) {
		t.Errorf("状态未落库却发了 workflow.completed(事件流与 DB 不符): %v", eventTypes(bus))
	}
	if eng.StateCommitFailures() == 0 {
		t.Error("CAS 冲突未被计数(冲突不可观测)")
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Errorf("过期写覆盖了别人的裁决: 状态 = %s(want CANCELLED)", after.State)
	}
}

// TestCancelCommitConflictIsExpectedRace 取消路径的 CAS 冲突是预期竞态:
//
//   - 不得发 workflow.cancelled(状态没落库);
//   - 也不发 runtime.error(预期竞态不是异常,不该报 ERROR 噪音);
//   - 但必须被计数(可观测)。
func TestCancelCommitConflictIsExpectedRace(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {DelayMS: 300, SummaryTemplate: "ok"},
		},
	})
	eng, repo, bus := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: cas-cancel-race
  name: CAS Cancel Race
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionRunning)

	// 另一个写者抢先裁决成 CANCELLED
	ok, err := repo.CasExecutionState(exec.ID,
		[]model.ExecutionState{model.ExecutionRunning}, model.ExecutionCancelled)
	if err != nil {
		t.Fatalf("抢占裁决: %v", err)
	}
	if !ok {
		t.Fatal("抢占裁决失败:执行状态已不是 RUNNING")
	}
	// 用户点取消:ctx 被取消 → 执行走取消分支
	//
	// 注意(迭代 19 / P1-1):此时库里的状态已被"另一个写者"裁决成
	// CANCELLED —— 那是**终态**,按新语义"取消一个已终态的执行"必须
	// 返回明确错误(INVALID_TRANSITION),不能返回 200 假成功。
	// 本用例要验的是**取消分支的 CAS 冲突处理**,不是 Cancel 的返回值;
	// ctx 仍会被取消(引擎在终态分支里也会停掉还在跑的协程),执行照样
	// 走取消分支,冲突照样被计数。
	if err := eng.Cancel(context.Background(), exec.ID); err == nil {
		t.Fatal("取消一个已终态(CANCELLED)的执行返回了 nil(200 假成功)")
	} else if code := errCode(err); code != "INVALID_TRANSITION" {
		t.Fatalf("cancel 错误码 = %q, want INVALID_TRANSITION(err=%v)", code, err)
	}

	// 等执行收尾(取消分支的 CAS 必然冲突)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && eng.StateCommitFailures() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if eng.StateCommitFailures() == 0 {
		t.Fatalf("取消分支的 CAS 冲突未被计数(事件: %v)", eventTypes(bus))
	}
	if hasEvent(bus, event.WorkflowCancelled) {
		t.Errorf("状态未落库却发了 workflow.cancelled: %v", eventTypes(bus))
	}
	if hasEvent(bus, "runtime.error") {
		t.Errorf("预期竞态不该发 runtime.error(ERROR 噪音): %v", eventTypes(bus))
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Errorf("过期写覆盖了别人的裁决: 状态 = %s(want CANCELLED)", after.State)
	}
}

// TestFailExecutionConflictDoesNotEmitFailedEvent 失败态提交冲突时:
//
//   - 不得发 workflow.failed(状态没落库);
//   - 改发 runtime.error(冲突可观测);
//   - 失败原因仍要落库(否则用户只看到别的终态却不知道为何失败)。
func TestFailExecutionConflictDoesNotEmitFailedEvent(t *testing.T) {
	eng, repo, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	exec := &model.Execution{
		ID: "exec-fail-conflict", WorkflowID: "wf", State: model.ExecutionRunning, Task: "t",
	}
	if err := repo.SaveExecution(exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	// 另一个写者抢先裁决成 CANCELLED
	ok, err := repo.CasExecutionState(exec.ID,
		[]model.ExecutionState{model.ExecutionRunning}, model.ExecutionCancelled)
	if err != nil {
		t.Fatalf("抢占裁决: %v", err)
	}
	if !ok {
		t.Fatal("抢占裁决失败:执行状态已不是 RUNNING")
	}

	// 引擎的过期副本(RUNNING)尝试落 FAILED
	eng.failExecution(exec, "节点失败: ANTHROPIC_API_KEY="+leakSecret)

	if hasEvent(bus, event.WorkflowFailed) {
		t.Errorf("状态未落库却发了 workflow.failed(事件流与 DB 不符): %v", eventTypes(bus))
	}
	if !hasEvent(bus, "runtime.error") {
		t.Errorf("CAS 冲突必须可观测(runtime.error 事件): %v", eventTypes(bus))
	}
	if eng.StateCommitFailures() == 0 {
		t.Error("CAS 冲突未被计数(冲突不可观测)")
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Errorf("过期写覆盖了别人的裁决: 状态 = %s(want CANCELLED)", after.State)
	}
	// 失败原因仍要留下(脱敏后落库)
	if after.Error == "" {
		t.Error("失败原因没有落库:用户只看到终态却不知道为何失败")
	}
	if strings.Contains(after.Error, leakSecret) {
		t.Errorf("落库的失败原因含凭据明文: %q", after.Error)
	}
}

// TestNormalTerminalCommitStillEmitsEvent 回归:迁移成功时终态事件照发。
// 上面几条锁的是"冲突时不发",这条锁的是"成功时必须发" ——
// 否则修复会退化成"永远不发终态事件"(静默失败,约定 #14)。
func TestNormalTerminalCommitStillEmitsEvent(t *testing.T) {
	eng, _, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: cas-normal
  name: CAS Normal
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)
	waitEvent(t, bus, event.WorkflowCompleted, 5*time.Second)
	if eng.StateCommitFailures() != 0 {
		t.Errorf("正常路径不该有状态提交失败: %d", eng.StateCommitFailures())
	}
}
