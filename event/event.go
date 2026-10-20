// Package event 提供进程内事件总线与统一事件 DTO。
// 所有重要操作(执行生命周期、节点状态、Agent 输出、人工交互)
// 都产生事件,用于 UI 实时刷新、日志、调试、恢复与审计。
package event

import (
	"fmt"
	"sync"
	"time"

	"agentworkflow/logx"
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
	//
	// 约定 #9:生产代码**不得**置位 —— 回放走 SQLite 持久化事件,
	// 总线保留历史只会让所有 UIEvent 永久累积(内存型 UI 回放已废弃)。
	// 仅测试使用,且必须在总线投入使用前置好:Emit 读它时持锁,
	// 运行期直接赋值不受保护(数据竞争)。
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
//
// 出口统一脱敏:节点失败的 error / agent 输出常含 CLI stderr,而 stderr 里
// 经常带 "ANTHROPIC_API_KEY=sk-..." 这类凭据。日志侧已由 logx.Mask 覆盖
// (见 logx.maskArgs),但事件有三个出口 —— SQLite 落库(审计/回放)、
// SSE 推送、Wails IPC —— 都在这里汇聚。在分发点收敛一次,三条出口同时
// 生效,新增订阅者也不会漏(与 AGENTS.md 硬性约定 #8 一致)。
//
// 同步分发(返回时事件已投递)是刻意契约:事件顺序即状态顺序,异步化会让
// "先落库再推送"的顺序假设失效。代价是订阅者不得阻塞 —— SSE 订阅者用
// 有界队列+丢弃策略实现(见 api/server.go handleSSE)。
func (b *Bus) Emit(ev UIEvent) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	if ev.Data != nil {
		if masked, ok := logx.MaskValue(ev.Data).(map[string]any); ok {
			ev.Data = masked
		}
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
		dispatch(h, ev)
	}
}

// dispatch 调用订阅者并兜住它的 panic。
//
// 发布方往往就是工作流执行协程(桌面模式下更是整个应用的主循环):
// 一个订阅者的 bug 冒泡成 panic,会把正在跑的工作流带走(桌面模式直接
// 终止应用)。约定 #12 要求执行协程有兜底,但不该由订阅者触发兜底 ——
// 兜底会把执行标成 FAILED,掩盖真正的根因(订阅者抛异常)。
// 这里就地兜住并记日志:坏订阅者被隔离,其余订阅者照常收到事件。
func dispatch(h Handler, ev UIEvent) {
	defer func() {
		if r := recover(); r != nil {
			logx.Error("事件订阅者 panic(已隔离)", "type", ev.Type,
				"execution", ev.ExecutionID, "panic", fmt.Sprint(r))
		}
	}()
	h(ev)
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
