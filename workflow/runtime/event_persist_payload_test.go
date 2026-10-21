package runtime

import (
	"math"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/event"
)

// TestEventPersistFailureOnUnserializablePayloadDoesNotCascade 载荷不可
// JSON 化(NaN/Inf)的事件落库失败时,连锁反应必须**止于一级**。
//
// 背景(#313):persistence.SaveEvent 收口为"失败即返回错误"后,运行期对
// 落库失败的既定反应链被真正激活:
//
//	坏事件 → 落库失败 → 计数 + ERROR 日志 + 发一条 runtime.error
//	      → runtime.error 自己也要落库 → ？
//
// 两级防护:
//
//  1. runtime.error 的载荷是普通字符串(reason / event_type / persist_error),
//     **能**正常落库 → 链路到此为止;
//  2. 万一它也落库失败(DB 整体坏了),运行期的 persistFailGuard 仍在位
//     (engine.go 的 onEventPersistFailed)→ 不再发第二条 runtime.error,
//     最多一级嵌套,不会级联。
//
// 同时锁住"审计流里留下的是**显式的失败信号**,而不是一条被改了样的正常事件":
// 坏事件本身**不在**表里(持久层失败即报错),runtime.error 在表里。
func TestEventPersistFailureOnUnserializablePayloadDoesNotCascade(t *testing.T) {
	eng, repo, bus := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))

	bus.Emit(event.New(event.NodeStarted, "exec-nan-payload", "n1", map[string]any{
		"ratio": math.NaN(),
	}))

	if got := eng.EventPersistFailures(); got == 0 {
		t.Error("载荷不可 JSON 化的事件落库失败必须被计数,实际计数为 0")
	}
	// 只有一条 runtime.error:没有级联。
	var runtimeErrors int
	for _, ev := range bus.History() {
		if ev.Type == "runtime.error" {
			runtimeErrors++
		}
	}
	if runtimeErrors != 1 {
		t.Errorf("应有且仅有 1 条 runtime.error(递归护栏防止级联),实际 %d 条", runtimeErrors)
	}
	// 审计流:坏事件不在表里,显式的失败信号在表里。
	events, err := repo.ListEvents("exec-nan-payload", 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("审计流里应只有 1 条(runtime.error),实际 %d 条: %v", len(events), events)
	}
	if got, _ := events[0]["type"].(string); got != "runtime.error" {
		t.Errorf("审计流里留下的必须是显式的失败信号(runtime.error),实际: %q", got)
	}
}
