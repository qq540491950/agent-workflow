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
	} else {
		// 不使用 Strict 模式:节点 config 为开放结构(可扩展),仅对
		// 顶层键做白名单检查以捕获拼写错误。
		if err := yaml.Unmarshal(trimmed, doc); err != nil {
			return nil, model.NewError(model.KindValidationError, "DSL_PARSE_ERROR",
				fmt.Sprintf("YAML 解析失败: %v", err))
		}
		if err := checkTopLevelKeys(trimmed); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// checkTopLevelKeys 校验 YAML 顶层键拼写(节点 config 内字段保持开放)。
func checkTopLevelKeys(data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil // 已在上面报过错
	}
	allowed := map[string]bool{
		"version": true, "workflow": true, "variables": true,
		"nodes": true, "edges": true, "settings": true, "permissions": true,
	}
	for k := range raw {
		if !allowed[k] {
			return model.NewError(model.KindValidationError, "DSL_UNKNOWN_KEY",
				fmt.Sprintf("未知的顶层字段 %q (允许: version, workflow, variables, nodes, edges, settings, permissions)", k))
		}
	}
	return nil
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
func (d *Document) ToModel() *model.Workflow {
	wf := &model.Workflow{
		ID:          d.Workflow.ID,
		Name:        d.Workflow.Name,
		Description: d.Workflow.Description,
		Enabled:     d.Workflow.Enabled == nil || *d.Workflow.Enabled,
		Variables:   d.Variables,
		Permission:  d.Permission,
		Settings: model.WorkflowSettings{
			MaxIterations:  d.Settings.MaxIterations,
			OnLoopLimit:    d.Settings.OnLoopLimit,
			TimeoutSeconds: d.Settings.TimeoutSeconds,
		},
		Nodes: make([]model.Node, 0, len(d.Nodes)),
		Edges: make([]model.Edge, 0, len(d.Edges)),
	}
	if wf.Variables == nil {
		wf.Variables = map[string]any{}
	}
	for _, n := range d.Nodes {
		cfg := n.Rest
		if cfg == nil {
			cfg = map[string]any{}
		}
		// id/name/type/x/y 由结构化字段承载,避免与 inline map 重复
		for _, k := range []string{"x", "y", "id", "name", "type"} {
			delete(cfg, k)
		}
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
		wf.Edges = append(wf.Edges, model.Edge{From: e.From, To: e.To, Condition: e.Condition})
	}
	return wf
}

// FromModel 将领域模型 Workflow 序列化回 DSL Document(用于导出 YAML)。
func FromModel(wf *model.Workflow) *Document {
	enabled := wf.Enabled
	doc := &Document{
		Version:    "1",
		Workflow:   WorkflowMeta{ID: wf.ID, Name: wf.Name, Description: wf.Description, Enabled: &enabled},
		Variables:  wf.Variables,
		Permission: wf.Permission,
		Settings: Settings{
			MaxIterations:  wf.Settings.MaxIterations,
			OnLoopLimit:    wf.Settings.OnLoopLimit,
			TimeoutSeconds: wf.Settings.TimeoutSeconds,
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
		doc.Edges = append(doc.Edges, Edge{From: e.From, To: e.To, Condition: e.Condition})
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
