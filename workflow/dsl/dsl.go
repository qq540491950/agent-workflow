// Package dsl 定义 Workflow 的配置化描述语言(YAML/JSON)及其解析。
// UI 保存的 JSON 与用户手写的 YAML 都通过这里进入系统,随后由
// validator 校验、compiler 编译为 ADK 运行时。
package dsl

import (
	"encoding/json"

	"agentworkflow/workflow/model"
)

// Document 是 DSL 文档的顶层结构。
type Document struct {
	Version   string         `yaml:"version" json:"version"`
	Workflow  WorkflowMeta   `yaml:"workflow" json:"workflow"`
	Variables map[string]any `yaml:"variables" json:"variables,omitempty"`
	// Inputs 声明运行入参(类型/必填/默认值):启动时由 Engine 校验与兜底,
	// 声明本身在 ToModel 时原样带入 model.Workflow.Inputs。段内每个条目的
	// 键集合是封闭的(name/type/required/default/description),未知键由
	// schema.go 的条目级检查记入 IgnoredKeys(WARN)。
	Inputs     []model.InputDecl `yaml:"inputs" json:"inputs,omitempty"`
	Nodes      []Node            `yaml:"nodes" json:"nodes"`
	Edges      []Edge            `yaml:"edges" json:"edges,omitempty"`
	Settings   Settings          `yaml:"settings" json:"settings,omitempty"`
	Permission map[string]any    `yaml:"permissions" json:"permissions,omitempty"`

	// IgnoredKeys 记录"出现在文档里但被解析器丢弃"的键(路径形如
	// "workflow.settings" / "settings.max_iteration")。
	//
	// 解析器对**闭合结构**(顶层 / workflow 段 / settings 段 / 单条边)里的
	// 未知键是非严格的:键被丢掉,用户以为设置生效了,实际没有。
	// 除 permissions 外的这类键不再阻断导入(存量 DSL 里确实存在这种写法,
	// 直接报错会让它们导不进来),但必须**可观测**:记在这里 + WARN 日志。
	//
	// 不参与序列化(yaml:"-" / json:"-"):它是解析诊断,不是文档内容 ——
	// 导出文本里出现它会让"导出→再导入"凭空多出一批 WARN。
	IgnoredKeys []string `yaml:"-" json:"-"`
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
	// TimeoutSeconds 单轮执行超时(秒);0 = 不限。
	TimeoutSeconds int `yaml:"timeout_seconds" json:"timeout_seconds,omitempty"`
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

// MarshalJSON 把节点配置平铺进 JSON 对象(与 YAML 的 inline 语义一致)。
//
// 此前 Rest 标了 json:"-",而 JSON 解析路径(json.Unmarshal)不认 YAML 的
// inline 语义 —— 于是 **JSON 格式的 DSL 会丢掉节点的全部配置**
// (agent / mode / prompt / command …):导入后每个节点都是空配置,
// agent 节点报"缺少 agent 配置",human / parallel 节点则是配置静默消失。
func (n Node) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(n.Rest)+5)
	for k, v := range n.Rest {
		out[k] = v
	}
	out["id"] = n.ID
	out["name"] = n.Name
	out["type"] = n.Type
	if n.X != nil {
		out["x"] = *n.X
	}
	if n.Y != nil {
		out["y"] = *n.Y
	}
	return json.Marshal(out)
}

// UnmarshalJSON 解析 JSON 节点:已知字段进结构化字段,全部键留在 Rest。
//
// 与 YAML inline 保持一致:Rest 收下所有键(含 id/name/type/x/y),
// ToModel 再把结构化字段从配置里剔除 —— YAML 与 JSON 两条解析路径
// 产出同构的文档,同一份 DSL 文本换种格式导入不会改变语义。
func (n *Node) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	rest := make(map[string]any, len(raw))
	for k, v := range raw {
		rest[k] = v
		switch k {
		case "id":
			if s, ok := v.(string); ok {
				n.ID = s
			}
		case "name":
			if s, ok := v.(string); ok {
				n.Name = s
			}
		case "type":
			if s, ok := v.(string); ok {
				n.Type = s
			}
		case "x":
			if f, ok := v.(float64); ok {
				x := f
				n.X = &x
			}
		case "y":
			if f, ok := v.(float64); ok {
				y := f
				n.Y = &y
			}
		}
	}
	n.Rest = rest
	return nil
}

// Edge 是 DSL 中的边定义。
type Edge struct {
	From      string `yaml:"from" json:"from"`
	To        string `yaml:"to" json:"to"`
	Condition string `yaml:"condition" json:"condition,omitempty"`
}

// Parse 将 YAML/JSON 文本解析为 Document。
// 输入格式由内容自动识别:以 '{' 开头按 JSON,否则按 YAML。
func Parse(data []byte) (*Document, error) {
	return parse(data)
}
