// Package model 定义了 Agent Workflow Orchestrator 的核心领域模型。
// 这些模型是平台无关的:既不依赖 UI,也不依赖具体的 Workflow Runtime(ADK)。
package model

import (
	"encoding/json"
	"fmt"
	"math"
)

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

// AllNodeTypes 返回当前注册的全部节点类型。
//
// 节点类型清单只有这一份(model 是常量定义的所在处);validator 与
// compiler 都引用这些常量。新增节点类型时把常量加进来即可,枚举函数
// 与测试(validator 接受的类型集合 == 本集合)随之更新,不会出现
// "运行期支持但校验期判非法"的第二份清单(AGENTS.md 硬性约定 #13)。
func AllNodeTypes() []NodeType {
	return []NodeType{
		NodeTypeAgent,
		NodeTypeSkill,
		NodeTypeCondition,
		NodeTypeParallel,
		NodeTypeMerge,
		NodeTypeHuman,
		NodeTypeScript,
		NodeTypeGit,
		NodeTypeSubWorkflow,
	}
}

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
	// TimeoutSeconds 单轮执行超时(秒);0 = 不限。
	TimeoutSeconds int `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

// Workflow 是用户可配置的编排定义。
type Workflow struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Version     int            `json:"version"`
	Enabled     bool           `json:"enabled"`
	Variables   map[string]any `json:"variables,omitempty"`
	// Permission 工作流级权限覆盖(需求:DSL permissions 段)。
	Permission map[string]any   `json:"permission,omitempty"`
	Nodes      []Node           `json:"nodes"`
	Edges      []Edge           `json:"edges"`
	Settings   WorkflowSettings `json:"settings"`
	CreatedAt  string           `json:"created_at,omitempty"`
	UpdatedAt  string           `json:"updated_at,omitempty"`
}

// UnmarshalJSON 解析工作流定义,兼容两种权限段写法。
//
//   - "permission" :模型自身的 json tag(历史版本快照 snapshot_json 里
//     就是这个键);
//   - "permissions":DSL 根级段的正式写法(需求.md 第十八章,DSL 导出文本
//     里也是它)。
//
// 此前只认单数:"permissions" 被 json.Unmarshal 当成未知字段**静默丢弃** ——
// 用户在工作流里写了权限收紧,导入后运行期拿到的仍是默认宽松策略,没有任何
// 提示(约定 14:该失败却成功 / 静默丢弃)。
//
// 输出侧不变(仍写 "permission"):既有 API 响应与历史快照的键名不动,
// 读侧多认一个别名即可,两边都不丢数据。
//
// 两个键同时出现时报错而不是静默挑一个:挑错的那个正是"用户以为生效了"
// 的假象来源。
func (w *Workflow) UnmarshalJSON(data []byte) error {
	type alias Workflow
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*w = Workflow(a)
	var probe struct {
		Permissions json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if len(probe.Permissions) == 0 || string(probe.Permissions) == "null" {
		return nil
	}
	if w.Permission != nil {
		return fmt.Errorf("model: 工作流同时出现 permission 与 permissions 两个权限段,只能保留一个")
	}
	var perm map[string]any
	if err := json.Unmarshal(probe.Permissions, &perm); err != nil {
		return fmt.Errorf("model: permissions 段结构非法: %w", err)
	}
	w.Permission = perm
	return nil
}

// ExecutionState 是 Execution 的状态机状态。
type ExecutionState string

const (
	ExecutionCreated     ExecutionState = "CREATED"
	ExecutionRunning     ExecutionState = "RUNNING"
	ExecutionPaused      ExecutionState = "PAUSED"
	ExecutionWaitingUser ExecutionState = "WAITING_USER"
	ExecutionCompleted   ExecutionState = "COMPLETED"
	ExecutionFailed      ExecutionState = "FAILED"
	ExecutionCancelled   ExecutionState = "CANCELLED"
)

// NodeState 是单个 Node 执行的状态。
type NodeState string

const (
	NodePending NodeState = "PENDING"
	NodeRunning NodeState = "RUNNING"
	NodeSuccess NodeState = "SUCCESS"
	NodeFailed  NodeState = "FAILED"
	NodeSkipped NodeState = "SKIPPED"
	NodeWaiting NodeState = "WAITING"
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
	ID              string            `json:"id"`
	WorkflowID      string            `json:"workflow_id"`
	WorkflowVersion int               `json:"workflow_version"`
	WorkflowName    string            `json:"workflow_name"`
	Snapshot        *Workflow         `json:"-"`
	State           ExecutionState    `json:"state"`
	Task            string            `json:"task"`
	Variables       map[string]any    `json:"variables,omitempty"`
	CurrentNodeID   string            `json:"current_node_id,omitempty"`
	Iterations      map[string]int    `json:"iterations,omitempty"`
	CreatedAt       string            `json:"created_at"`
	StartedAt       string            `json:"started_at,omitempty"`
	FinishedAt      string            `json:"finished_at,omitempty"`
	Error           string            `json:"error,omitempty"`
	NodeStates      map[string]string `json:"node_states,omitempty"`
	// StateData 是运行时的会话状态快照(用于恢复与 UI 查询)。
	StateData map[string]any `json:"state_data,omitempty"`
}

// ExecutionNode 记录某个节点在某次 Execution 中的执行明细。
type ExecutionNode struct {
	ID          string    `json:"id"`
	ExecutionID string    `json:"execution_id"`
	NodeID      string    `json:"node_id"`
	NodeType    NodeType  `json:"node_type"`
	NodeName    string    `json:"node_name"`
	State       NodeState `json:"state"`
	Attempt     int       `json:"attempt"`
	Output      string    `json:"output,omitempty"`
	ResultJSON  string    `json:"result_json,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   string    `json:"started_at,omitempty"`
	FinishedAt  string    `json:"finished_at,omitempty"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
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

// NormalizeStateJSON 在**读取边界**把无类型数值统一成 int。
//
// 背景(轮次 29):`StateData` 来自 ADK 会话状态快照,执行期内存里是 int,
// 经 SQLite `state_json` 持久化往返后 encoding/json 把它解成 float64。
// 也就是说**同一个字段的类型随生命周期变化** —— 执行期 int、恢复期
// float64。结果是每个读取点都得写双类型兼容(compiler.IntOfOk 就是
// 这么来的),而漏掉一处就是一个静默 bug:轮次 10/24/25/27 都踩过
// "循环计数回落导致过早触发循环保护"这一类。
//
// 与其在每个读取点防守,不如在唯一的解码入口把类型钉死一次:
//   - 之后所有消费者拿到的都是 int,IntOfOk 的双类型分支成为纯冗余;
//   - 数值语义(计数、attempt、下标)在整个生命周期内稳定;
//   - 未来新增读取点不会再踩同一个坑。
//
// 只转换"整数语义"的值 —— 带小数的确实是浮点语义
// (如有),保持 float64 不动。无法识别的值原样保留。
//
// 除 JSON 往返(float64)外,YAML inline map 里的整数(goccy/go-yaml
// 解成 uint64)也一并归一,避免同一字段出现第三种数值类型。
func NormalizeStateJSON(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	for k, v := range data {
		data[k] = normalizeJSONNumbers(v)
	}
	return data
}

// normalizeJSONNumbers 递归地把无类型数值统一成 int。
//
// 两类来源:
//   - float64:JSON 往返后所有数字都是 float64(约定 2);
//   - uint64 :YAML inline map 里的整数(goccy/go-yaml 的默认行为)。
//
// 两者不统一就会出现"同一字段三种类型"(int / float64 / uint64):
// 运行期的读取点(cfgInt / retryPolicyOf)只认 int 与 float64,
// uint64 会被静默当成 0 —— 节点级 timeout_seconds / retry.max_attempts
// 直接失效。
//
// 递归覆盖数组里的 map / 数组:此前只处理"数组里的裸 float64",
// 形如 []any{map[string]any{"n": 1.0}} 的嵌套结构会漏掉 ——
// 解码边界就该一次性把类型钉死,漏一处就是一个静默的类型不一致。
func normalizeJSONNumbers(v any) any {
	switch n := v.(type) {
	case float64:
		// JSON 无 int/float 之分,所有数字都解成 float64。
		// 整数值的转成 int,让类型在往返前后一致。
		if isWholeJSONNumber(n) {
			return int(n)
		}
		return n
	case uint64:
		if n <= math.MaxInt64 {
			return int(n)
		}
		return n
	case []any:
		for i, item := range n {
			n[i] = normalizeJSONNumbers(item)
		}
		return n
	case map[string]any:
		NormalizeStateJSON(n)
		return n
	}
	return v
}

// isWholeJSONNumber 判断一个 float64 是否是 int64 范围内的整数。
// 超出 int64 范围(如 1e20)、NaN、Inf 一律视为浮点语义,保持 float64。
func isWholeJSONNumber(n float64) bool {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return false
	}
	if n != math.Trunc(n) {
		return false
	}
	// math.MaxInt64 转 float64 后是 2^63(不可表示为 int64),
	// 因此用 >= 排除该边界值。
	return n >= math.MinInt64 && n < math.MaxInt64
}
