package runtime

import (
	"context"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

// TestEventPersistFailureIsObservable 锁住 P1-3:事件落库失败必须可观测。
//
// 此前是 `_ = repo.SaveEvent(ev)` —— 静默丢弃。DB 故障时审计流凭空缺
// 记录,事后无法区分"没发生"与"没记下来",这正是审计流最不可接受的情形。
// 现在:计数 + ERROR 日志 + 一条 runtime.error 事件(活的信号:SSE/UI 能收到)。
func TestEventPersistFailureIsObservable(t *testing.T) {
	eng, repo, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))

	// 关闭 DB 制造真实的落库故障
	if err := repo.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	bus.Emit(event.New("test.event", "exec-persist-fail", "", map[string]any{"n": 1}))

	if got := eng.EventPersistFailures(); got == 0 {
		t.Error("事件落库失败必须可观测(计数),实际计数为 0")
	}
	found := 0
	for _, ev := range bus.History() {
		if ev.Type != "runtime.error" {
			continue
		}
		if reason, _ := ev.Data["reason"].(string); reason == "event_persist_failed" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("应有且仅有 1 条 event_persist_failed 事件(递归护栏防止级联),实际 %d 条", found)
	}
}

// TestEventPersistFailureDoesNotFailExecution 锁住 P1-3 的另一半语义:
// 事件持久化失败**不得**把执行打成 FAILED。
//
// 事件落库失败是"审计流断了",不是"工作流挂了"。把它变成执行失败会制造
// 新的状态混乱(用户看到失败,实际业务逻辑跑完了),比丢一条审计记录更糟。
//
// 故障注入方式:删掉 events 表 —— 审计流断,执行表仍可用(状态可观测)。
func TestEventPersistFailureDoesNotFailExecution(t *testing.T) {
	eng, repo, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	if _, err := repo.SQL().Exec(`DROP TABLE events`); err != nil {
		t.Fatalf("drop events: %v", err)
	}

	wf := parseYAML(t, eng, simpleAgentSkillYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	if eng.EventPersistFailures() == 0 {
		t.Error("预期事件落库失败(计数为 0)")
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCompleted {
		t.Fatalf("事件落库失败把执行改成了 %s(want COMPLETED)", after.State)
	}
	for _, ev := range bus.History() {
		if ev.Type == event.WorkflowFailed {
			t.Errorf("事件落库失败不应产生 workflow.failed 事件: %v", ev.Data)
		}
	}
}

// TestEventPersistedSynchronously 钉住 P2-6 的契约:事件**同步**落库。
//
// 即"Emit 返回时事件已经在 SQLite 里"。这是刻意契约,不是巧合:
//   - 事件顺序即状态顺序(总线层的说明),异步化会让"先落库再推送"的
//     顺序假设失效;
//   - 崩溃场景下"已发出的事件必须已经落库" —— 异步化会让进程被强杀时
//     丢掉最后一段审计记录,恰好是最需要审计的时刻。
//
// 代价(DB 慢会拖慢执行协程)可接受:单连接 WAL 下一次 INSERT 是亚毫秒级,
// 相对一次 Agent 调用可忽略。
func TestEventPersistedSynchronously(t *testing.T) {
	_, repo, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))

	bus.Emit(event.New("test.event", "exec-sync", "", map[string]any{"n": 1}))

	// 不 sleep、不轮询:Emit 返回即应可查
	events, err := repo.ListEvents("exec-sync", 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Emit 返回时事件应已落库,实际查到 %d 条", len(events))
	}
}

// TestEventOrderMatchesEmissionOrder 钉住顺序契约:落库顺序 = 发布顺序。
func TestEventOrderMatchesEmissionOrder(t *testing.T) {
	_, repo, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))

	const n = 5
	for i := 0; i < n; i++ {
		bus.Emit(event.New("test.event", "exec-order", "", map[string]any{"i": i}))
	}
	events, err := repo.ListEvents("exec-order", 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != n {
		t.Fatalf("落库事件数 = %d,want %d", len(events), n)
	}
	for i, ev := range events {
		data, _ := ev["data"].(map[string]any)
		got, _ := data["i"].(float64)
		if int(got) != i {
			t.Errorf("第 %d 条事件的顺序不对: %v", i, ev)
		}
	}
}
