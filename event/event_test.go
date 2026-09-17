package event

import (
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
