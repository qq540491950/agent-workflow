package event

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmitSetsTimestamp(t *testing.T) {
	b := NewBus()
	var got UIEvent
	b.Subscribe(func(ev UIEvent) { got = ev })
	b.Emit(UIEvent{Type: WorkflowStarted, ExecutionID: "e1"})
	if got.Type != WorkflowStarted || got.ExecutionID != "e1" {
		t.Fatalf("event = %+v", got)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp not set")
	}
	if time.Since(got.Timestamp) > time.Second {
		t.Errorf("timestamp too old: %v", got.Timestamp)
	}
}

func TestUnsubscribe(t *testing.T) {
	b := NewBus()
	var count int32
	unsub := b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&count, 1) })
	b.Emit(UIEvent{Type: NodeStarted})
	unsub()
	b.Emit(UIEvent{Type: NodeStarted})
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("handler called %d times after unsubscribe, want 1", count)
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := NewBus()
	var a, bCount int32
	b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&a, 1) })
	b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&bCount, 1) })
	b.Emit(UIEvent{Type: AgentOutput})
	if a != 1 || bCount != 1 {
		t.Errorf("a=%d b=%d, want 1/1", a, bCount)
	}
}

func TestHistoryOnlyWhenEnabled(t *testing.T) {
	off := NewBus()
	off.Emit(UIEvent{Type: WorkflowCompleted})
	if len(off.History()) != 0 {
		t.Error("history should be off by default")
	}

	on := NewBus()
	on.KeepHistory = true
	ev := New(NodeCompleted, "e1", "n1", map[string]any{"k": "v"})
	on.Emit(ev)
	on.Emit(UIEvent{Type: NodeFailed})
	h := on.History()
	if len(h) != 2 {
		t.Fatalf("history len = %d, want 2", len(h))
	}
	if h[0].Type != NodeCompleted || h[0].NodeID != "n1" {
		t.Errorf("history[0] = %+v", h[0])
	}
	// 返回副本:修改不影响内部状态
	h[0].Type = "tampered"
	if on.History()[0].Type != NodeCompleted {
		t.Error("History() must return a copy")
	}
}

func TestConcurrentEmit(t *testing.T) {
	b := NewBus()
	var count int32
	for i := 0; i < 8; i++ {
		b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&count, 1) })
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b.Emit(UIEvent{Type: NodeCompleted, ExecutionID: "e", Data: map[string]any{"n": n}})
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&count); got != 800 {
		t.Errorf("handler calls = %d, want 800", got)
	}
}

// TestEmitMasksSensitivePayload 验证事件出口统一脱敏。
// 事件有三个下游:SQLite 落库(审计/回放)、SSE 推送、Wails IPC,三者都在
// Emit 汇聚。在分发点脱敏一次,三条出口同时生效,新增订阅者也不会漏。
// 真实载荷:agent CLI 失败时 stderr 带凭据,被拼进 node.failed 的 error。
func TestEmitMasksSensitivePayload(t *testing.T) {
	b := NewBus()
	b.KeepHistory = true
	var got UIEvent
	b.Subscribe(func(ev UIEvent) { got = ev })

	b.Emit(New(NodeFailed, "exec-1", "claude", map[string]any{
		"error": "claude exited 1: ANTHROPIC_API_KEY=sk-ant-leak123",
		"env":   map[string]any{"token": "Bearer raw.token.value"},
	}))

	errText, _ := got.Data["error"].(string)
	if strings.Contains(errText, "sk-ant-leak123") {
		t.Errorf("订阅者收到的 error 未脱敏: %q", errText)
	}
	if !strings.Contains(errText, "***") {
		t.Errorf("脱敏后应保留 *** 标记: %q", errText)
	}
	env, _ := got.Data["env"].(map[string]any)
	if env["token"] == "Bearer raw.token.value" {
		t.Errorf("嵌套 token 未脱敏: %v", env)
	}
	// 历史副本同样脱敏(测试模式下 KeepHistory 打开)
	hist := b.History()
	if len(hist) != 1 {
		t.Fatalf("history len = %d", len(hist))
	}
	if strings.Contains(fmt.Sprint(hist[0].Data), "sk-ant-leak123") {
		t.Errorf("历史副本未脱敏: %v", hist[0].Data)
	}
}

// TestEmitLeavesNormalPayloadIntact 回归:普通事件 payload 不受影响。
func TestEmitLeavesNormalPayloadIntact(t *testing.T) {
	b := NewBus()
	var got UIEvent
	b.Subscribe(func(ev UIEvent) { got = ev })

	b.Emit(New(NodeCompleted, "exec-2", "review", map[string]any{
		"node": "review", "summary": "review call=2", "attempt": 3,
	}))
	if got.Data["summary"] != "review call=2" || got.Data["node"] != "review" {
		t.Errorf("正常 payload 被改动: %v", got.Data)
	}
	if got.Data["attempt"].(int) != 3 {
		t.Errorf("非字符串值被改动: %v", got.Data["attempt"])
	}
	if got.NodeID != "review" || got.ExecutionID != "exec-2" {
		t.Errorf("事件元数据被改动: %+v", got)
	}
}
