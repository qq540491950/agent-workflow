// Package event 提供进程内事件总线与统一事件 DTO。
// 所有重要操作(执行生命周期、节点状态、Agent 输出、人工交互)
// 都产生事件,用于 UI 实时刷新、日志、调试、恢复与审计。
package event

import (
	"sync"
	"time"
)

// 预定义事件类型。
const (
	WorkflowStarted     = "workflow.started"
	WorkflowPaused      = "workflow.paused"
	WorkflowResumed     = "workflow.resumed"
	WorkflowCompleted   = "workflow.completed"
	WorkflowFailed      = "workflow.failed"
	WorkflowCancelled   = "workflow.cancelled"
	WorkflowWaitingUser = "workflow.waiting_user"

	NodeStarted   = "node.started"
	NodeCompleted = "node.completed"
	NodeFailed    = "node.failed"
	NodeWaiting   = "node.waiting"

	AgentStarted   = "agent.started"
	AgentOutput    = "agent.output"
	AgentCompleted = "agent.completed"

	ReviewApproved = "review.approved"
	ReviewRejected = "review.rejected"

	HumanInputRequired = "human.input_required"
	HumanInputReceived = "human.input_received"

	SkillExecuted = "skill.executed"
	GitExecuted   = "git.executed"
)

// UIEvent 是推送给前端的统一事件 DTO。
type UIEvent struct {
	Type        string         `json:"type"`
	ExecutionID string         `json:"execution_id"`
	NodeID      string         `json:"node_id,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
	Data        map[string]any `json:"data,omitempty"`
}

// Handler 订阅事件。
type Handler func(UIEvent)

// Bus 是进程内事件总线,支持多订阅者并发分发。
type Bus struct {
	mu      sync.RWMutex
	nextID  int
	subs    map[int]Handler
	history []UIEvent
	// KeepHistory 控制是否保留历史(内存型 UI 回放/测试用)。
	KeepHistory bool
}

// NewBus 创建事件总线。
func NewBus() *Bus {
	return &Bus{subs: map[int]Handler{}}
}

// Subscribe 注册处理器,返回取消订阅函数。
func (b *Bus) Subscribe(h Handler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	b.subs[id] = h
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

// Emit 同步分发事件给全部订阅者。
func (b *Bus) Emit(ev UIEvent) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	b.mu.Lock()
	if b.KeepHistory {
		b.history = append(b.history, ev)
	}
	handlers := make([]Handler, 0, len(b.subs))
	for _, h := range b.subs {
		handlers = append(handlers, h)
	}
	b.mu.Unlock()
	for _, h := range handlers {
		h(ev)
	}
}

// History 返回历史事件副本。
func (b *Bus) History() []UIEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UIEvent, len(b.history))
	copy(out, b.history)
	return out
}

// New 构造 UIEvent 的便捷函数。
func New(typ, executionID, nodeID string, data map[string]any) UIEvent {
	return UIEvent{Type: typ, ExecutionID: executionID, NodeID: nodeID, Timestamp: time.Now(), Data: data}
}
