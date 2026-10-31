package dsl

import (
	"encoding/json"
	"fmt"

	"github.com/goccy/go-yaml"

	"agentworkflow/workflow/model"
)

func parse(data []byte) (*Document, error) {
	doc := &Document{}
	trimmed := trimSpaceStart(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, doc); err != nil {
			return nil, model.NewError(model.KindValidationError, "DSL_PARSE_ERROR",
				fmt.Sprintf("JSON 解析失败: %v", err))
		}
		// JSON 路径此前**完全没有**顶层键检查 —— YAML 会报错,JSON 静默接受,
		// 同一份 DSL 换格式导入行为就不同。两条路径现在共用同一套校验。
		raw, err := decodeRawJSON(trimmed)
		if err != nil {
			return nil, model.NewError(model.KindValidationError, "DSL_PARSE_ERROR",
				fmt.Sprintf("JSON 解析失败: %v", err))
		}
		if err := checkDocumentKeys(doc, raw, jsonKeys); err != nil {
			return nil, err
		}
	} else {
		// 不使用 Strict 模式:节点 config 为开放结构(可扩展),仅对
		// 闭合结构(顶层 / workflow 段 / settings 段 / 单条边)做键校验。
		if err := yaml.Unmarshal(trimmed, doc); err != nil {
			return nil, model.NewError(model.KindValidationError, "DSL_PARSE_ERROR",
				fmt.Sprintf("YAML 解析失败: %v", err))
		}
		raw, err := decodeRawYAML(trimmed)
		if err != nil {
			return nil, model.NewError(model.KindValidationError, "DSL_PARSE_ERROR",
				fmt.Sprintf("YAML 解析失败: %v", err))
		}
		if err := checkDocumentKeys(doc, raw, yamlKeys); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

func trimSpaceStart(b []byte) []byte {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return b[i:]
}

// ToModel 将 DSL 文档转换为领域模型 Workflow(不含持久化元数据)。
// 转换过程只做结构映射,不做语义校验(校验由 validator 负责)。
//
// 转换是**纯函数**:不修改文档本身,也不与文档共享 map。
// 此前直接复用 n.Rest 并从中 delete 结构化字段,等于把调用方的
// 文档改掉了(且模型与文档共享同一张 map,改一边另一边跟着变)。
func (d *Document) ToModel() *model.Workflow {
	wf := &model.Workflow{
		ID:          d.Workflow.ID,
		Name:        d.Workflow.Name,
		Description: d.Workflow.Description,
		Enabled:     d.Workflow.Enabled == nil || *d.Workflow.Enabled,
		Variables:   copyMap(d.Variables),
		Inputs:      copyInputs(d.Inputs),
		Permission:  copyMap(d.Permission),
		Settings: model.WorkflowSettings{
			MaxIterations:  d.Settings.MaxIterations,
			OnLoopLimit:    d.Settings.OnLoopLimit,
			TimeoutSeconds: d.Settings.TimeoutSeconds,
			MaxConcurrent:  d.Settings.MaxConcurrent,
		},
		Nodes: make([]model.Node, 0, len(d.Nodes)),
		Edges: make([]model.Edge, 0, len(d.Edges)),
	}
	if wf.Variables == nil {
		wf.Variables = map[string]any{}
	}
	// 数值类型在这里钉死一次:YAML 的整数是 uint64、JSON 的整数是 float64,
	// 归一成 int 后模型里的数值语义稳定 —— 运行期的读取点(cfgInt /
	// retryPolicyOf)只认 int 与 float64,uint64 会被当成 0
	// (节点级 timeout_seconds / retry.max_attempts 静默失效)。
	model.NormalizeStateJSON(wf.Variables)
	model.NormalizeStateJSON(wf.Permission)
	for _, n := range d.Nodes {
		cfg := copyMap(n.Rest)
		if cfg == nil {
			cfg = map[string]any{}
		}
		// id/name/type/x/y 由结构化字段承载,避免与 inline map 重复
		for _, k := range []string{"x", "y", "id", "name", "type"} {
			delete(cfg, k)
		}
		model.NormalizeStateJSON(cfg)
		mn := model.Node{
			ID:     n.ID,
			Name:   n.Name,
			Type:   model.NodeType(n.Type),
			Config: cfg,
		}
		if mn.Name == "" {
			mn.Name = n.ID
		}
		if n.X != nil {
			mn.Position.X = *n.X
		}
		if n.Y != nil {
			mn.Position.Y = *n.Y
		}
		wf.Nodes = append(wf.Nodes, mn)
	}
	for _, e := range d.Edges {
		wf.Edges = append(wf.Edges, model.Edge{From: e.From, To: e.To, Condition: e.Condition, OnFailure: e.OnFailure})
	}
	return wf
}

// copyMap 深复制一张 map(nil → nil),避免文档与模型共享可变状态。
func copyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = copyValue(v)
	}
	return out
}

// copyValue 深复制 map / slice 里的值(其余类型按值语义复制)。
func copyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return copyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = copyValue(item)
		}
		return out
	default:
		return v
	}
}

// copyInputs 复制入参声明,并把默认值的数值类型钉死一次(YAML 整数是
// uint64、JSON 整数是 float64 —— 与 variables 的归一口径一致,运行期的
// 数值判定只认 int/float64)。
func copyInputs(src []model.InputDecl) []model.InputDecl {
	if src == nil {
		return nil
	}
	out := make([]model.InputDecl, len(src))
	for i, in := range src {
		if in.Default != nil {
			box := map[string]any{"d": in.Default}
			model.NormalizeStateJSON(box)
			in.Default = box["d"]
		}
		out[i] = in
	}
	return out
}

// FromModel 将领域模型 Workflow 序列化回 DSL Document(用于导出 YAML)。
func FromModel(wf *model.Workflow) *Document {
	enabled := wf.Enabled
	doc := &Document{
		Version:    "1",
		Workflow:   WorkflowMeta{ID: wf.ID, Name: wf.Name, Description: wf.Description, Enabled: &enabled},
		Variables:  copyMap(wf.Variables),
		Inputs:     append([]model.InputDecl(nil), wf.Inputs...),
		Permission: copyMap(wf.Permission),
		Settings: Settings{
			MaxIterations:  wf.Settings.MaxIterations,
			OnLoopLimit:    wf.Settings.OnLoopLimit,
			TimeoutSeconds: wf.Settings.TimeoutSeconds,
			MaxConcurrent:  wf.Settings.MaxConcurrent,
		},
	}
	for _, n := range wf.Nodes {
		x, y := n.Position.X, n.Position.Y
		cfg := map[string]any{}
		for k, v := range n.Config {
			if k == "x" || k == "y" {
				continue
			}
			cfg[k] = v
		}
		dn := Node{ID: n.ID, Name: n.Name, Type: string(n.Type), X: &x, Y: &y, Rest: cfg}
		doc.Nodes = append(doc.Nodes, dn)
	}
	for _, e := range wf.Edges {
		doc.Edges = append(doc.Edges, Edge{From: e.From, To: e.To, Condition: e.Condition, OnFailure: e.OnFailure})
	}
	return doc
}

// EncodeYAML 输出 Document 的 YAML 文本。
func (d *Document) EncodeYAML() ([]byte, error) {
	out, err := yaml.Marshal(d)
	if err != nil {
		return nil, model.NewError(model.KindValidationError, "DSL_ENCODE_ERROR", err.Error())
	}
	return out, nil
}
