package event

import (
	"sync/atomic"
	"testing"
)

// TestEmitSurvivesPanickingSubscriber 锁定"一个坏订阅者不能带走发布方"。
//
// 背景:Emit 同步调用订阅者,订阅者 panic 会一路冒泡到发布方 ——
// 桌面模式下发布方就是工作流执行协程,panic 直接终止整个应用(约定 #12:
// 执行协程必须有兜底,但兜底不该由订阅者的 bug 触发)。
func TestEmitSurvivesPanickingSubscriber(t *testing.T) {
	b := NewBus()
	var got int32
	b.Subscribe(func(ev UIEvent) { panic("订阅者炸了") })
	b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&got, 1) })

	// 不得 panic 冒泡出去。
	b.Emit(UIEvent{Type: NodeCompleted, ExecutionID: "e1"})

	if n := atomic.LoadInt32(&got); n != 1 {
		t.Errorf("后续订阅者未收到事件: %d(want 1)", n)
	}
}

// TestEmitKeepsDeliveringAfterPanic 锁定"某个订阅者 panic 后总线仍可用"。
func TestEmitKeepsDeliveringAfterPanic(t *testing.T) {
	b := NewBus()
	b.Subscribe(func(ev UIEvent) { panic("订阅者炸了") })
	var got int32
	b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&got, 1) })

	for i := 0; i < 3; i++ {
		b.Emit(UIEvent{Type: NodeCompleted})
	}
	if n := atomic.LoadInt32(&got); n != 3 {
		t.Errorf("收到 %d 次,want 3", n)
	}
}

// TestBusKeepsNoHistoryByDefault 锁定约定 #9:生产总线不保留历史
// (回放走 SQLite 持久化事件)。历史上 KeepHistory=true 会让所有 UIEvent
// 永久累积 —— 内存型 UI 回放早已被 API 回放取代。
func TestBusKeepsNoHistoryByDefault(t *testing.T) {
	b := NewBus()
	for i := 0; i < 100; i++ {
		b.Emit(UIEvent{Type: NodeCompleted, ExecutionID: "e1"})
	}
	if h := b.History(); len(h) != 0 {
		t.Fatalf("生产总线保留了 %d 条历史(约定 #9 要求为 0)", len(h))
	}
}

// TestEmitIsSynchronous 锁定分发语义:Emit 返回时事件已投递。
// 订阅者不得改成异步投递 —— 现有契约测试与"事件先于状态落库"的顺序
// 假设都依赖同步语义。
func TestEmitIsSynchronous(t *testing.T) {
	b := NewBus()
	var got int32
	b.Subscribe(func(ev UIEvent) { atomic.AddInt32(&got, 1) })
	b.Emit(UIEvent{Type: NodeCompleted})
	if n := atomic.LoadInt32(&got); n != 1 {
		t.Errorf("Emit 返回后事件未投递: %d", n)
	}
}
