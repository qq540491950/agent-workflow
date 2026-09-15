// Package agent 定义统一的 Agent 抽象与注册表。
// Claude Code、Pi Agent、Mock 等都是 Agent 接口的实现,彼此完全解耦;
// Workflow Runtime 只依赖接口与注册表,禁止直接引用具体实现。
package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// AgentStatus 是 Agent 执行状态。
type AgentStatus string

const (
	StatusSuccess  AgentStatus = "SUCCESS"
	StatusFailed   AgentStatus = "FAILED"
	StatusWaitUser AgentStatus = "WAIT_USER"
)

// AgentDecision 是 Agent 的结构化决策。禁止用字符串匹配 Agent 输出来路由。
type AgentDecision string

const (
	DecisionApproved AgentDecision = "APPROVED"
	DecisionRejected AgentDecision = "REJECTED"
	DecisionContinue AgentDecision = "CONTINUE"
	DecisionDone     AgentDecision = "DONE"
	DecisionWaitUser AgentDecision = "WAIT_USER"
	DecisionError    AgentDecision = "ERROR"
)

// AgentRequest 是发给 Agent 的一次执行请求。
type AgentRequest struct {
	Task         string
	Mode         string
	// Model 允许节点级覆盖模型;空串表示使用 Agent 配置的默认模型。
	// 模型仅通过本次调用的 CLI 参数/环境变量传递,不会修改任何本地配置。
	Model        string
	Context      map[string]any
	Instructions string
	WorkingDir   string
	Environment  map[string]string
	Timeout      int // seconds, 0 = default
}

// AgentResponse 是 Agent 返回的结构化结果。
type AgentResponse struct {
	Status   AgentStatus    `json:"status"`
	Decision AgentDecision  `json:"decision,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Output   string         `json:"output,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// Agent 是所有外部 Agent 的统一接口。
type Agent interface {
	ID() string
	Name() string
	Execute(ctx context.Context, req AgentRequest) (*AgentResponse, error)
}

// Registry 管理 Agent 的注册与查找。
type Registry interface {
	Register(a Agent) error
	// Replace 注册或替换 Agent(用于配置变更后重建适配器实例)。
	Replace(a Agent) error
	Get(id string) (Agent, error)
	List() []Agent
}

type registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
}

// NewRegistry 创建空的 Agent 注册表。
func NewRegistry() Registry {
	return &registry{agents: map[string]Agent{}}
}

// Register 注册 Agent,ID 冲突返回错误。
func (r *registry) Register(a Agent) error {
	if a == nil || a.ID() == "" {
		return fmt.Errorf("agent: register requires non-nil agent with id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agents[a.ID()]; exists {
		return fmt.Errorf("agent: %q already registered", a.ID())
	}
	r.agents[a.ID()] = a
	return nil
}

// Replace 注册或覆盖同 ID 的 Agent。
func (r *registry) Replace(a Agent) error {
	if a == nil || a.ID() == "" {
		return fmt.Errorf("agent: register requires non-nil agent with id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[a.ID()] = a
	return nil
}

// Get 按 ID 查找 Agent。
func (r *registry) Get(id string) (Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent: %q not registered", id)
	}
	return a, nil
}

// List 返回全部已注册 Agent(按 ID 排序,保证顺序稳定)。
func (r *registry) List() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
