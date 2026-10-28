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

// ---- agent 节点的 mode 取值域(唯一定义处) ----
//
// mode 是 agent 节点上"这个节点要干什么"的声明。它的取值域此前散落在
// **三份互不相同的清单**里:
//
//   - agent.ModeRequiresWrite:execute|fix|submit —— 决定要不要写权限;
//   - contextx.SaveNodeOutput/BuildWithSlots:plan|review|execute|fix|skill|test
//     —— 决定产出进哪个上下文槽位;
//   - 前端 Designer 的下拉:plan|execute|review|fix|test|submit —— 用户能选的。
//
// 三份清单的成员不一样,于是同一个 mode 在不同层的含义不同:
//   - 此前 `mode: submit` 在上下文层没有槽位(产出不进上下文链)——
//     已于迭代 28 收口:submit 的产出现在写进 last_output;
//   - `mode: skill` 是**节点类型**不是 agent 的 mode,却出现在上下文层;
//   - 任何**拼错**的 mode(exectue / banana)在校验期与运行期都被放行,
//     且因为"不在只读清单里"而被跳过写权限检查 —— 用户写错一个词,
//     节点照常执行,权限收紧对它无效(约定 14)。
//
// 现在取值域只有这一份:校验期(validator)、运行期(runtime/executor)、
// 写权限判定(agent.ModeRequiresWrite)与上下文槽位映射(contextx)全部
// 引用它(约定 13:校验期与运行期必须同源)。
//
// 为什么放在 model:model 是最底层的领域模型,只依赖标准库 ——
// agent / contextx / compiler / validator / runtime 都能引它而不成环
// (compiler 依赖 agent,agent 不能再依赖 compiler)。

// agent 节点的合法 mode 取值。
const (
	// ModePlan 只出方案。
	ModePlan = "plan"
	// ModeReview 只做评审(产出评审结论与问题清单)。
	ModeReview = "review"
	// ModeExecute 执行任务(改文件)。
	ModeExecute = "execute"
	// ModeFix 修复问题(改文件)。
	ModeFix = "fix"
	// ModeTest 只跑测试。
	ModeTest = "test"
	// ModeSubmit 提交代码(git commit,伴随写文件)。
	//
	// 产物(提交摘要 / commit message)进 last_output 槽位:提交也是
	// "一次节点产出",下游引用 {{last_output}} 时应当拿到**它**,而不是
	// 上一个 execute / fix 节点的旧产物(迭代 28 的 P2-1)。
	ModeSubmit = "submit"
)

// 上下文槽位键(唯一定义处;与 contextx.StateKey* 取值一致)。
//
// 这些键是"会话状态"的键名:引擎种入 task,节点产出写入 plan / review /
// test_result / last_output。contextx 侧的定义(contextx.StateKeyPlan 等)
// 必须与本表一致 —— 由 workflow/runtime 的守卫测试
// (mode_contract_test.go)锁住,任何一边单独改都会立刻失败。
const (
	// ContextSlotTask 任务文本(引擎种进初始状态)。
	ContextSlotTask = "task"
	// ContextSlotPlan 方案:plan 节点产出。
	ContextSlotPlan = "plan"
	// ContextSlotReview 评审结论:review 节点产出。
	ContextSlotReview = "review"
	// ContextSlotIssues 评审问题清单:review 节点产出。
	ContextSlotIssues = "issues"
	// ContextSlotTestResult 测试结果:test 节点产出。
	ContextSlotTestResult = "test_result"
	// ContextSlotLastOutput 最近一次节点输出:execute / fix / skill /
	// submit 节点产出(这几个 mode 的产物都归"最近一次输出"这个槽位)。
	ContextSlotLastOutput = "last_output"
	// ContextSlotGitDiff Git diff(git 节点产出)。
	ContextSlotGitDiff = "git_diff"
	// ContextSlotIteration 当前循环轮数(编译器维护)。
	ContextSlotIteration = "iteration"
	// ContextSlotRelevantFiles 相关文件清单。
	ContextSlotRelevantFiles = "relevant_files"
	// ContextSlotUserInstruction 人工补充指令。
	ContextSlotUserInstruction = "user_instruction"
)

// ModeSpec 描述一个 mode 的语义。
type ModeSpec struct {
	// Name mode 取值。
	Name string
	// RequiresWrite 该 mode 是否隐含写操作(需要 fs:write 权限)。
	RequiresWrite bool
	// ContextSlot 该 mode 的产出写进哪个上下文槽位。
	//
	// **每个合法 mode 都必须有一个非空槽位**(迭代 28 的 P2-1):
	// 空串意味着"该 mode 的产出不进上下文链",于是槽位里留着**上一个
	// 节点的旧值** —— 下游 {{last_output}} 拿到的是**陈旧上下文**,
	// 而"空槽位告警"只报"空"、不报"陈旧" → 全程无告警(约定 14 同族:
	// 用户以为拿到的是最新产出,实际是旧的)。
	//
	// 因此 mode → 槽位的映射是**全覆盖**的:mode 要么刷新自己的专属
	// 槽位(plan/review/test),要么刷新 last_output(execute/fix/skill/
	// submit —— 它们都是"最近一次节点输出"的候选)。
	ContextSlot string
}

// modeSpecs 是 mode → 语义 的唯一定义(顺序即前端下拉的展示顺序)。
//
// 新增 mode 时只改这里:校验期取值域、运行期 fail closed、写权限判定、
// 上下文槽位映射全部随之更新,不会出现"一半认一半不认"(约定 13)。
var modeSpecs = []ModeSpec{
	{Name: ModePlan, ContextSlot: ContextSlotPlan},
	{Name: ModeExecute, RequiresWrite: true, ContextSlot: ContextSlotLastOutput},
	{Name: ModeReview, ContextSlot: ContextSlotReview},
	{Name: ModeFix, RequiresWrite: true, ContextSlot: ContextSlotLastOutput},
	{Name: ModeTest, ContextSlot: ContextSlotTestResult},
	{Name: ModeSubmit, RequiresWrite: true, ContextSlot: ContextSlotLastOutput},
}

// AllModes 返回全部合法 mode(副本,顺序与 modeSpecs 一致)。
func AllModes() []string {
	out := make([]string, 0, len(modeSpecs))
	for _, s := range modeSpecs {
		out = append(out, s.Name)
	}
	return out
}

// ModeSpecFor 返回指定 mode 的语义。第二个返回值为 false 表示该 mode
// **不在取值域内**(拼错的、别的节点类型专用的、空的)。
func ModeSpecFor(mode string) (ModeSpec, bool) {
	for _, s := range modeSpecs {
		if s.Name == mode {
			return s, true
		}
	}
	return ModeSpec{}, false
}

// IsValidMode 判断 mode 是否在取值域内。
//
// 校验期用它判非法值(INVALID_NODE),运行期用它 fail closed(节点失败)。
func IsValidMode(mode string) bool {
	_, ok := ModeSpecFor(mode)
	return ok
}

// ModeRequiresWrite 判断某个 mode 是否隐含**写操作**(需要 fs:write)。
//
// **未知/空 mode 一律返回 true(fail closed)**:mode 拼错("exectue")时,
// 若按"不在只读清单里"处理,写权限检查会被跳过 —— 一个可能要改文件的
// 节点在权限收紧的工作流里畅通无阻,而用户以为自己写的是 execute
// (约定 14:用户以为生效了实际没有)。
//
// 因此"不认识"一律按"最危险"处理:先问写权限,再判 mode 非法。
//
// 唯一来源在 model(校验期/运行期/适配器共用,约定 13)。
func ModeRequiresWrite(mode string) bool {
	spec, ok := ModeSpecFor(mode)
	if !ok {
		return true
	}
	return spec.RequiresWrite
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
	// Inputs 声明工作流的运行入参(DSL inputs 段,迭代 36)。
	//
	// 与 Variables 的分工:Variables 是**静态**变量(DSL 里写死的值),
	// Inputs 声明的是**每次运行可由调用方提供**的入参(类型/必填/默认值)。
	// 入参在 Engine.Start 时落到 variables map(合并顺序:wf.Variables
	// 为底 → input default → 请求 variables 覆盖),模板 {{variables.xxx}}
	// 与条件 `variables.xxx == ...` 无需改动即可使用 —— 变量宇宙不变,
	// 条件变量的可用集合仍只有 conditionenv 一份定义(约定 13)。
	Inputs []InputDecl `json:"inputs,omitempty"`
	// Permission 工作流级权限覆盖(需求:DSL permissions 段)。
	Permission map[string]any   `json:"permission,omitempty"`
	Nodes      []Node           `json:"nodes"`
	Edges      []Edge           `json:"edges"`
	Settings   WorkflowSettings `json:"settings"`
	CreatedAt  string           `json:"created_at,omitempty"`
	UpdatedAt  string           `json:"updated_at,omitempty"`
}

// InputType 声明工作流入参的取值类型。
type InputType string

const (
	InputTypeString  InputType = "string"
	InputTypeNumber  InputType = "number"
	InputTypeBoolean InputType = "boolean"
)

// InputDecl 声明工作流的一个运行入参(对标 Temporal/n8n/Windmill 的
// declared inputs:类型 + 必填 + 默认值,启动时校验、缺省时兜底)。
type InputDecl struct {
	Name        string    `json:"name" yaml:"name"`
	Type        InputType `json:"type" yaml:"type"`
	Required    bool      `json:"required,omitempty" yaml:"required,omitempty"`
	Default     any       `json:"default,omitempty" yaml:"default,omitempty"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
}

// InputTypes 返回 inputs.type 的合法取值(唯一来源,约定 13:校验期与
// 运行期都从这里取)。
func InputTypes() []InputType {
	return []InputType{InputTypeString, InputTypeNumber, InputTypeBoolean}
}

// IsValidInputType 判断类型声明是否合法。
func IsValidInputType(t InputType) bool {
	for _, v := range InputTypes() {
		if v == t {
			return true
		}
	}
	return false
}

// InputTypeNameOf 返回值在 inputs 类型体系里的形态名;不认识的形态返回
// Go 类型名(错误信息里可读)。JSON 往返的 float64 与 YAML 的 uint64 /
// int 都算 number(与 NormalizeStateJSON 的数值归一口径一致)。
func InputTypeNameOf(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int64, uint64, float64, json.Number:
		return "number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// InputValueMatchesType 校验入参值与声明的类型相符。
//
// 约定 13 的同源点:校验期查 default 的类型、运行期查请求传入值的类型,
// 都必须走这一个函数 —— 两份实现各自演化,就会出现"校验期放行、启动报错"
// 或反过来。
func InputValueMatchesType(decl InputDecl, v any) bool {
	switch decl.Type {
	case InputTypeString:
		_, ok := v.(string)
		return ok
	case InputTypeBoolean:
		_, ok := v.(bool)
		return ok
	case InputTypeNumber:
		switch n := v.(type) {
		case int, int64, uint64, float64:
			return true
		case json.Number:
			_, err := n.Float64()
			return err == nil
		}
		return false
	default:
		// 未知类型声明:任何值都不匹配(校验期会把类型本身报为非法)。
		return false
	}
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

// PermissionSectionKeys 返回权限段在两种序列化格式下的正式拼写集合。
//
// 唯一来源 = 本文件:Permission 字段的 json tag(`permission`,上方
// 结构体定义)与 UnmarshalJSON 接受的 DSL 根级写法(`permissions`,
// 需求.md 第十八章)。校验层(workflow/validator)用它判定"权限段
// 出现在节点 config 里 = 写错层级"(硬失败,错误码 CodeMisplacedSection);
// 这两个拼写不得在 model 之外手抄第二份 —— 权限段改名时只改这里。
func PermissionSectionKeys() map[string]bool {
	return map[string]bool{"permission": true, "permissions": true}
}

// ExecutionState 是 Execution 的状态机状态。
type ExecutionState string

const (
	ExecutionCreated     ExecutionState = "CREATED"
	ExecutionRunning     ExecutionState = "RUNNING"
	ExecutionWaitingUser ExecutionState = "WAITING_USER"
	ExecutionCompleted   ExecutionState = "COMPLETED"
	ExecutionFailed      ExecutionState = "FAILED"
	ExecutionCancelled   ExecutionState = "CANCELLED"
)

// legacyPausedState 是**历史遗留**的执行状态字面量。
//
// PAUSED 曾是执行状态(状态机里有分支、运行期 Resume/Cancel 接受它、
// 崩溃恢复把它当"可恢复"、前端有专属徽章),但**从来不可达**:没有任何
// 代码路径会把执行迁移到它。它已从状态机里移除,老库里仍可能留有
// state='PAUSED' 的行 —— 读出口归一为 CANCELLED
// (见 persistence.NormalizeExecutionState)。
//
// 这里保留字面量(而不是常量):它只用于**识别**历史数据,不是可用的
// 状态 —— 任何"迁移到 PAUSED"的代码都该被评审挡下。
const legacyPausedState = ExecutionState("PAUSED")

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
//
// 注意:PAUSED **不在**状态机里 —— 它从来不可达(没有代码路径会迁移到
// 它),留着转换分支等于留着一个永远走不到的分支(见 legacyPausedState)。
func CanTransitionExecution(from, to ExecutionState) bool {
	allowed := map[ExecutionState][]ExecutionState{
		ExecutionCreated:     {ExecutionRunning, ExecutionCancelled, ExecutionFailed},
		ExecutionRunning:     {ExecutionWaitingUser, ExecutionCompleted, ExecutionFailed, ExecutionCancelled},
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
// 运行期的读取点(cfgFloat / retryPolicyOf)只认 int 与 float64,
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
