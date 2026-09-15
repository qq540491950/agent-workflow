// Package model 定义了 Agent Workflow Orchestrator 的核心领域模型。
// 这些模型是平台无关的:既不依赖 UI,也不依赖具体的 Workflow Runtime(ADK)。
package model

import "fmt"

// NodeType 标识 Node 的类型。类型是可注册扩展的,不写死在 UI 或 Engine 中。
type NodeType string

const (
	NodeTypeAgent       NodeType = "agent"
	NodeTypeSkill       NodeType = "skill"
	NodeTypeCondition   NodeType = "condition"
	NodeTypeParallel    NodeType = "parallel"
	NodeTypeMerge       NodeType = "merge"
	NodeTypeHuman       NodeType = "human"
	NodeTypeScript      NodeType = "script"
	NodeTypeGit         NodeType = "git"
	NodeTypeSubWorkflow NodeType = "subworkflow"
)

// Position 是节点在画布上的坐标。
type Position struct {
	X float64 `json:"x" yaml:"x"`
	Y float64 `json:"y" yaml:"y"`
}

// Node 是 Workflow 图中的一个节点。
type Node struct {
	ID       string         `json:"id" yaml:"id"`
	Name     string         `json:"name" yaml:"name"`
	Type     NodeType       `json:"type" yaml:"type"`
	Config   map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
	Position Position       `json:"position" yaml:"position"`
}

// Edge 连接两个节点。Condition 为空表示无条件跳转。
type Edge struct {
	From      string `json:"from" yaml:"from"`
	To        string `json:"to" yaml:"to"`
	Condition string `json:"condition,omitempty" yaml:"condition,omitempty"`
}

// WorkflowSettings 控制 Workflow 级别的执行策略。
type WorkflowSettings struct {
	// MaxIterations 限制循环(如 Review→Fix)的最大轮数,防止无限循环。
	MaxIterations int `json:"max_iterations,omitempty" yaml:"max_iterations,omitempty"`
	// OnLoopLimit 达到循环上限后的处理策略:wait_user(默认)或 fail。
	OnLoopLimit string `json:"on_loop_limit,omitempty" yaml:"on_loop_limit,omitempty"`
}

// Workflow 是用户可配置的编排定义。
type Workflow struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Version     int              `json:"version"`
	Enabled     bool             `json:"enabled"`
	Variables   map[string]any   `json:"variables,omitempty"`
	// Permission 工作流级权限覆盖(需求:DSL permissions 段)。
	Permission map[string]any   `json:"permission,omitempty"`
	Nodes      []Node           `json:"nodes"`
	Edges      []Edge           `json:"edges"`
	Settings   WorkflowSettings `json:"settings"`
	CreatedAt   string           `json:"created_at,omitempty"`
	UpdatedAt   string           `json:"updated_at,omitempty"`
}

// ExecutionState 是 Execution 的状态机状态。
type ExecutionState string

const (
	ExecutionCreated      ExecutionState = "CREATED"
	ExecutionRunning      ExecutionState = "RUNNING"
	ExecutionPaused       ExecutionState = "PAUSED"
	ExecutionWaitingUser  ExecutionState = "WAITING_USER"
	ExecutionCompleted    ExecutionState = "COMPLETED"
	ExecutionFailed       ExecutionState = "FAILED"
	ExecutionCancelled    ExecutionState = "CANCELLED"
)

// NodeState 是单个 Node 执行的状态。
type NodeState string

const (
	NodePending   NodeState = "PENDING"
	NodeRunning   NodeState = "RUNNING"
	NodeSuccess   NodeState = "SUCCESS"
	NodeFailed    NodeState = "FAILED"
	NodeSkipped   NodeState = "SKIPPED"
	NodeWaiting   NodeState = "WAITING"
)

// CanTransitionExecution 校验 Execution 状态机的合法转换,禁止非法状态跳转。
// FAILED→RUNNING 仅允许用户显式重试(Engine.RetryNode)。
func CanTransitionExecution(from, to ExecutionState) bool {
	allowed := map[ExecutionState][]ExecutionState{
		ExecutionCreated:     {ExecutionRunning, ExecutionCancelled, ExecutionFailed},
		ExecutionRunning:     {ExecutionPaused, ExecutionWaitingUser, ExecutionCompleted, ExecutionFailed, ExecutionCancelled},
		ExecutionPaused:      {ExecutionRunning, ExecutionCancelled, ExecutionFailed},
		ExecutionWaitingUser: {ExecutionRunning, ExecutionCancelled, ExecutionFailed},
		ExecutionCompleted:   {},
		ExecutionFailed:      {ExecutionRunning}, // 用户显式重试
		ExecutionCancelled:   {},
	}
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}

// CanTransitionNode 校验 Node 状态机的合法转换。
func CanTransitionNode(from, to NodeState) bool {
	allowed := map[NodeState][]NodeState{
		NodePending: {NodeRunning, NodeSkipped},
		NodeRunning: {NodeSuccess, NodeFailed, NodeWaiting},
		NodeWaiting: {NodeRunning, NodeSkipped, NodeSuccess},
		NodeSuccess: {},
		NodeFailed:  {},
		NodeSkipped: {},
	}
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Execution 是一次 Workflow 运行实例。它绑定具体的 Workflow 版本,
// 保证运行中的 Execution 不受后续修改影响。
type Execution struct {
	ID                 string            `json:"id"`
	WorkflowID         string            `json:"workflow_id"`
	WorkflowVersion    int               `json:"workflow_version"`
	WorkflowName       string            `json:"workflow_name"`
	Snapshot           *Workflow         `json:"-"`
	State              ExecutionState    `json:"state"`
	Task               string            `json:"task"`
	Variables          map[string]any    `json:"variables,omitempty"`
	CurrentNodeID      string            `json:"current_node_id,omitempty"`
	Iterations         map[string]int    `json:"iterations,omitempty"`
	CreatedAt          string            `json:"created_at"`
	StartedAt          string            `json:"started_at,omitempty"`
	FinishedAt         string            `json:"finished_at,omitempty"`
	Error              string            `json:"error,omitempty"`
	NodeStates         map[string]string `json:"node_states,omitempty"`
	// StateData 是运行时的会话状态快照(用于恢复与 UI 查询)。
	StateData map[string]any `json:"state_data,omitempty"`
}

// ExecutionNode 记录某个节点在某次 Execution 中的执行明细。
type ExecutionNode struct {
	ID          string         `json:"id"`
	ExecutionID string         `json:"execution_id"`
	NodeID      string         `json:"node_id"`
	NodeType    NodeType       `json:"node_type"`
	NodeName    string         `json:"node_name"`
	State       NodeState      `json:"state"`
	Attempt     int            `json:"attempt"`
	Output      string         `json:"output,omitempty"`
	ResultJSON  string         `json:"result_json,omitempty"`
	Error       string         `json:"error,omitempty"`
	StartedAt   string         `json:"started_at,omitempty"`
	FinishedAt  string         `json:"finished_at,omitempty"`
	DurationMS  int64          `json:"duration_ms,omitempty"`
}

// Artifact 是执行过程中产生的制品(例如 plan、diff、review 结果)。
type Artifact struct {
	ID          string `json:"id"`
	ExecutionID string `json:"execution_id"`
	NodeID      string `json:"node_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	CreatedAt   string `json:"created_at"`
}

// FindNode 按 ID 查找节点。
func (w *Workflow) FindNode(id string) (*Node, error) {
	for i := range w.Nodes {
		if w.Nodes[i].ID == id {
			return &w.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("node %q not found in workflow %q", id, w.ID)
}

// OutgoingEdges 返回从指定节点出发的所有边。
func (w *Workflow) OutgoingEdges(nodeID string) []Edge {
	var out []Edge
	for _, e := range w.Edges {
		if e.From == nodeID {
			out = append(out, e)
		}
	}
	return out
}

// IncomingEdges 返回进入指定节点的所有边。
func (w *Workflow) IncomingEdges(nodeID string) []Edge {
	var out []Edge
	for _, e := range w.Edges {
		if e.To == nodeID {
			out = append(out, e)
		}
	}
	return out
}
