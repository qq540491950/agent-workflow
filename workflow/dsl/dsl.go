// Package dsl 定义 Workflow 的配置化描述语言(YAML/JSON)及其解析。
// UI 保存的 JSON 与用户手写的 YAML 都通过这里进入系统,随后由
// validator 校验、compiler 编译为 ADK 运行时。
package dsl

// Document 是 DSL 文档的顶层结构。
type Document struct {
	Version    string         `yaml:"version" json:"version"`
	Workflow   WorkflowMeta   `yaml:"workflow" json:"workflow"`
	Variables  map[string]any `yaml:"variables" json:"variables,omitempty"`
	Nodes      []Node         `yaml:"nodes" json:"nodes"`
	Edges      []Edge         `yaml:"edges" json:"edges,omitempty"`
	Settings   Settings       `yaml:"settings" json:"settings,omitempty"`
	Permission map[string]any `yaml:"permissions" json:"permissions,omitempty"`
}

// WorkflowMeta 对应 workflow: 段。
type WorkflowMeta struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Enabled     *bool  `yaml:"enabled" json:"enabled,omitempty"`
}

// Settings 对应 workflow 级别执行策略。
type Settings struct {
	MaxIterations int    `yaml:"max_iterations" json:"max_iterations,omitempty"`
	OnLoopLimit   string `yaml:"on_loop_limit" json:"on_loop_limit,omitempty"`
}

// Node 是 DSL 中的节点定义。
type Node struct {
	ID   string   `yaml:"id" json:"id"`
	Name string   `yaml:"name" json:"name"`
	Type string   `yaml:"type" json:"type"`
	X    *float64 `yaml:"x" json:"x,omitempty"`
	Y    *float64 `yaml:"y" json:"y,omitempty"`
	// Rest 保留节点类型的全部配置字段,由 compiler 按 Node 类型消费。
	Rest map[string]any `yaml:",inline" json:"-"`
}

// Edge 是 DSL 中的边定义。
type Edge struct {
	From      string `yaml:"from" json:"from"`
	To        string `yaml:"to" json:"to"`
	Condition string `yaml:"condition" json:"condition,omitempty"`
}

// PromptDef 支持 prompt 为纯字符串或 {template: ...} 两种写法。
type PromptDef struct {
	Template string
}

// AgentNodeConfig 是 agent 节点的配置结构。
type AgentNodeConfig struct {
	Agent          string            `yaml:"agent" json:"agent"`
	Mode           string            `yaml:"mode" json:"mode"`
	Prompt         *PromptDef        `yaml:"prompt" json:"prompt,omitempty"`
	WorkingDir     string            `yaml:"working_dir" json:"working_dir,omitempty"`
	TimeoutSeconds int               `yaml:"timeout_seconds" json:"timeout_seconds,omitempty"`
	Retry          *RetryPolicy      `yaml:"retry" json:"retry,omitempty"`
	Instructions   string            `yaml:"instructions" json:"instructions,omitempty"`
	Env            map[string]string `yaml:"env" json:"env,omitempty"`
}

// RetryPolicy 是节点级重试策略。
type RetryPolicy struct {
	MaxAttempts int    `yaml:"max_attempts" json:"max_attempts,omitempty"`
	Backoff     string `yaml:"backoff" json:"backoff,omitempty"` // fixed | exponential
}

// SkillNodeConfig 是 skill 节点的配置结构。
type SkillNodeConfig struct {
	Skill string         `yaml:"skill" json:"skill"`
	Agent string         `yaml:"agent" json:"agent,omitempty"`
	Args  map[string]any `yaml:"args" json:"args,omitempty"`
}

// ConditionNodeConfig 是 condition 节点的配置结构。
type ConditionNodeConfig struct {
	Expression string `yaml:"expression" json:"expression"`
}

// HumanNodeConfig 是 human 节点的配置结构。
type HumanNodeConfig struct {
	Prompt    string   `yaml:"prompt" json:"prompt,omitempty"`
	Responses []string `yaml:"responses" json:"responses,omitempty"` // approve/reject/continue/instruction
}

// ScriptNodeConfig 是 script 节点的配置结构。
type ScriptNodeConfig struct {
	Command string `yaml:"command" json:"command"`
	Shell   string `yaml:"shell" json:"shell,omitempty"`
}

// GitNodeConfig 是 git 节点的配置结构。
type GitNodeConfig struct {
	Operation string         `yaml:"operation" json:"operation"` // status|diff|log|branch|checkout|commit
	Args      map[string]any `yaml:"args" json:"args,omitempty"`
}

// SubWorkflowNodeConfig 是 subworkflow 节点的配置结构。
type SubWorkflowNodeConfig struct {
	WorkflowID string         `yaml:"workflow_id" json:"workflow_id"`
	Input      map[string]any `yaml:"input" json:"input,omitempty"`
}

// Parse 将 YAML/JSON 文本解析为 Document。
// 输入格式由内容自动识别:以 '{' 开头按 JSON,否则按 YAML。
func Parse(data []byte) (*Document, error) {
	return parse(data)
}
