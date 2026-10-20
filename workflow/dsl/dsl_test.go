package dsl

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
	"agentworkflow/workflow/validator"
)

// nodeTypeFixtures 覆盖 model 注册的全部节点类型,给出最小可校验配置。
//
// 这张表是"节点类型清单"的测试侧对照物:真源是 model.AllNodeTypes(),
// 本表只提供各类型的最小配置样例。
var nodeTypeFixtures = []struct {
	Type   model.NodeType
	Config map[string]any
}{
	{model.NodeTypeAgent, map[string]any{"agent": "claude-code", "mode": "plan"}},
	{model.NodeTypeSkill, map[string]any{"skill": "submit"}},
	{model.NodeTypeCondition, map[string]any{"expression": `decision == "APPROVED"`}},
	{model.NodeTypeParallel, map[string]any{}},
	{model.NodeTypeMerge, map[string]any{}},
	{model.NodeTypeHuman, map[string]any{}},
	{model.NodeTypeScript, map[string]any{"command": "echo hi"}},
	{model.NodeTypeGit, map[string]any{"operation": "status"}},
	{model.NodeTypeSubWorkflow, map[string]any{"workflow_id": "child-wf"}},
}

// fixtureYAML 生成一个只含单个节点的 DSL 文本。
func fixtureYAML(t model.NodeType, cfg map[string]any) string {
	var sb strings.Builder
	sb.WriteString("version: \"1\"\nworkflow:\n  id: wf-demo\n  name: Demo\nnodes:\n")
	sb.WriteString("  - id: n1\n    name: N1\n    type: " + string(t) + "\n")
	for k, v := range cfg {
		sb.WriteString(fmt.Sprintf("    %s: %v\n", k, v))
	}
	sb.WriteString("edges: []\n")
	return sb.String()
}

// TestAllNodeTypesRoundTrip 覆盖全部节点类型的最小实例:
// DSL 文本 → 文档 → 模型 → 文档 → DSL 文本 → 文档 → 模型,配置不丢。
func TestAllNodeTypesRoundTrip(t *testing.T) {
	for _, fx := range nodeTypeFixtures {
		t.Run(string(fx.Type), func(t *testing.T) {
			doc, err := Parse([]byte(fixtureYAML(fx.Type, fx.Config)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			wf := doc.ToModel()
			if len(wf.Nodes) != 1 {
				t.Fatalf("nodes = %d, want 1", len(wf.Nodes))
			}
			if wf.Nodes[0].Type != fx.Type {
				t.Errorf("node type = %q, want %q", wf.Nodes[0].Type, fx.Type)
			}
			if !reflect.DeepEqual(wf.Nodes[0].Config, fx.Config) {
				t.Errorf("config = %#v, want %#v", wf.Nodes[0].Config, fx.Config)
			}

			// 模型 → 文档 → YAML → 文档 → 模型
			out, err := FromModel(wf).EncodeYAML()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			doc2, err := Parse(out)
			if err != nil {
				t.Fatalf("reparse: %v\n%s", err, out)
			}
			wf2 := doc2.ToModel()
			if len(wf2.Nodes) != 1 {
				t.Fatalf("往返后 nodes = %d, want 1", len(wf2.Nodes))
			}
			if wf2.Nodes[0].Type != fx.Type {
				t.Errorf("往返后 type = %q, want %q", wf2.Nodes[0].Type, fx.Type)
			}
			if !reflect.DeepEqual(wf2.Nodes[0].Config, fx.Config) {
				t.Errorf("往返后 config = %#v, want %#v", wf2.Nodes[0].Config, fx.Config)
			}
		})
	}
}

// TestParseJSONKeepsNodeConfig 回归:JSON 格式的 DSL 曾丢掉节点的全部配置
// (Node.Rest 标了 json:"-",JSON 解析不认 YAML 的 inline 语义)。
// 结果:导入 JSON 工作流后 agent 节点报"缺少 agent 配置",
// human / parallel 节点的配置则静默消失。
func TestParseJSONKeepsNodeConfig(t *testing.T) {
	const src = `{
  "version": "1",
  "workflow": {"id": "wf-json", "name": "JSON 工作流"},
  "nodes": [
    {"id": "a", "type": "agent", "agent": "claude-code", "mode": "plan", "timeout_seconds": 600},
    {"id": "b", "type": "human", "prompt": "确认?"},
    {"id": "c", "type": "script", "command": "echo hi"}
  ],
  "edges": [{"from": "a", "to": "b"}]
}`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if len(wf.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(wf.Nodes))
	}
	if got := wf.Nodes[0].Config["agent"]; got != "claude-code" {
		t.Errorf("agent 配置丢失: %#v", wf.Nodes[0].Config)
	}
	if got := wf.Nodes[0].Config["mode"]; got != "plan" {
		t.Errorf("mode 配置丢失: %#v", wf.Nodes[0].Config)
	}
	// 数值在 DSL→模型边界已归一成 int(YAML 解出 uint64、JSON 解出 float64)
	if got, ok := wf.Nodes[0].Config["timeout_seconds"].(int); !ok || got != 600 {
		t.Errorf("timeout_seconds = %v(%T), want int 600", wf.Nodes[0].Config["timeout_seconds"], wf.Nodes[0].Config["timeout_seconds"])
	}
	if got := wf.Nodes[1].Config["prompt"]; got != "确认?" {
		t.Errorf("human 节点配置丢失: %#v", wf.Nodes[1].Config)
	}
	if got := wf.Nodes[2].Config["command"]; got != "echo hi" {
		t.Errorf("script 节点配置丢失: %#v", wf.Nodes[2].Config)
	}
	// 结构化字段同样要解出来
	if wf.Nodes[0].ID != "a" || wf.Nodes[0].Type != model.NodeTypeAgent {
		t.Errorf("结构化字段错误: %+v", wf.Nodes[0])
	}
	if len(wf.Edges) != 1 || wf.Edges[0].From != "a" || wf.Edges[0].To != "b" {
		t.Errorf("edges = %+v", wf.Edges)
	}
}

// TestJSONAndYAMLProduceSameModel 验证两条解析路径(YAML / JSON)语义同源:
// 同一份工作流换种格式导入,得到的模型必须一致。
func TestJSONAndYAMLProduceSameModel(t *testing.T) {
	const src = `{
  "version": "1",
  "workflow": {"id": "wf-same", "name": "同名", "description": "描述"},
  "variables": {"max_review": 5},
  "settings": {"max_iterations": 10, "on_loop_limit": "wait_user", "timeout_seconds": 300},
  "nodes": [
    {"id": "a", "name": "A", "type": "agent", "agent": "claude-code", "mode": "plan", "x": 10, "y": 20}
  ],
  "edges": []
}`
	const yml = `version: "1"
workflow:
  id: wf-same
  name: 同名
  description: 描述
variables:
  max_review: 5
settings:
  max_iterations: 10
  on_loop_limit: wait_user
  timeout_seconds: 300
nodes:
  - id: a
    name: A
    type: agent
    agent: claude-code
    mode: plan
    x: 10
    y: 20
edges: []
`
	jsonDoc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	yamlDoc, err := Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	jm := jsonDoc.ToModel()
	ym := yamlDoc.ToModel()

	if jm.ID != ym.ID || jm.Name != ym.Name || jm.Description != ym.Description {
		t.Errorf("元信息不一致:\njson %+v\nyaml %+v", jm, ym)
	}
	if jm.Settings != ym.Settings {
		t.Errorf("settings 不一致: json %+v / yaml %+v", jm.Settings, ym.Settings)
	}
	if len(jm.Nodes) != len(ym.Nodes) {
		t.Fatalf("节点数不一致: %d / %d", len(jm.Nodes), len(ym.Nodes))
	}
	if jm.Nodes[0].Position != ym.Nodes[0].Position {
		t.Errorf("坐标不一致: json %+v / yaml %+v", jm.Nodes[0].Position, ym.Nodes[0].Position)
	}
	// 配置内容一致(两条解析路径的数值类型已在 DSL→模型边界归一为 int)
	if !reflect.DeepEqual(jm.Nodes[0].Config, ym.Nodes[0].Config) {
		t.Errorf("节点配置不一致:\njson %#v\nyaml %#v", jm.Nodes[0].Config, ym.Nodes[0].Config)
	}
	if !reflect.DeepEqual(jm.Variables, ym.Variables) {
		t.Errorf("变量不一致:\njson %#v\nyaml %#v", jm.Variables, ym.Variables)
	}
}

// TestNumericConfigNormalizedToInt 回归:YAML inline map 里的整数被
// goccy/go-yaml 解成 uint64,而运行期的读取点(cfgInt / retryPolicyOf)
// 只认 int 与 float64 —— uint64 会被当成 0,节点级 timeout_seconds 与
// retry.max_attempts 直接静默失效(只在不经数据库往返的内存路径上暴露)。
//
// 归一点在 DSL→模型边界:两条解析路径(YAML / JSON)都产出 int。
func TestNumericConfigNormalizedToInt(t *testing.T) {
	const src = `version: "1"
workflow:
  id: wf-num
  name: 数值归一
variables:
  max_review: 5
nodes:
  - id: a
    type: agent
    agent: claude-code
    mode: plan
    timeout_seconds: 600
    retry:
      max_attempts: 3
      backoff: exponential
edges: []
`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()

	if got, ok := wf.Nodes[0].Config["timeout_seconds"].(int); !ok || got != 600 {
		t.Errorf("timeout_seconds = %v(%T), want int 600", wf.Nodes[0].Config["timeout_seconds"], wf.Nodes[0].Config["timeout_seconds"])
	}
	retry, ok := wf.Nodes[0].Config["retry"].(map[string]any)
	if !ok {
		t.Fatalf("retry = %v(%T)", wf.Nodes[0].Config["retry"], wf.Nodes[0].Config["retry"])
	}
	if got, ok := retry["max_attempts"].(int); !ok || got != 3 {
		t.Errorf("retry.max_attempts = %v(%T), want int 3", retry["max_attempts"], retry["max_attempts"])
	}
	if got, ok := wf.Variables["max_review"].(int); !ok || got != 5 {
		t.Errorf("variables.max_review = %v(%T), want int 5", wf.Variables["max_review"], wf.Variables["max_review"])
	}
	// 归一不得改掉源文档(ToModel 是纯函数)
	if _, ok := doc.Nodes[0].Rest["timeout_seconds"].(uint64); !ok {
		t.Errorf("源文档的数值类型被改动了: %v(%T)", doc.Nodes[0].Rest["timeout_seconds"], doc.Nodes[0].Rest["timeout_seconds"])
	}
}

// TestToModelDoesNotMutateDocument 验证 ToModel 是纯函数:
// 不修改源文档,模型与文档也不共享 map。
func TestToModelDoesNotMutateDocument(t *testing.T) {
	doc, err := Parse([]byte(fixtureYAML(model.NodeTypeAgent, map[string]any{"agent": "claude-code", "mode": "plan"})))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	before := len(doc.Nodes[0].Rest)
	wf := doc.ToModel()

	// 文档里的 inline map 不该被 ToModel 删字段
	if got := len(doc.Nodes[0].Rest); got != before {
		t.Errorf("ToModel 修改了源文档的 Rest: %d → %d", before, got)
	}
	if _, ok := doc.Nodes[0].Rest["id"]; !ok {
		t.Error("ToModel 删掉了源文档 Rest 里的 id 键")
	}
	// 模型配置里不应再有结构化字段
	for _, k := range []string{"id", "name", "type", "x", "y"} {
		if _, ok := wf.Nodes[0].Config[k]; ok {
			t.Errorf("模型配置里残留结构化字段 %q: %#v", k, wf.Nodes[0].Config)
		}
	}
	// 模型与文档不共享 map:改模型不影响文档
	second := doc.ToModel()
	if !reflect.DeepEqual(wf, second) {
		t.Errorf("ToModel 不可重复:\n%+v\n%+v", wf, second)
	}
	wf.Nodes[0].Config["agent"] = "mutated"
	if doc.Nodes[0].Rest["agent"] == "mutated" {
		t.Error("模型与文档共享同一张 map")
	}
}

// TestParseRejectsUnknownTopLevelKey 锁住顶层键白名单(拼写错误要报错)。
func TestParseRejectsUnknownTopLevelKey(t *testing.T) {
	_, err := Parse([]byte("version: \"1\"\nworkflow:\n  id: x\n  name: X\nnodez: []\n"))
	if err == nil {
		t.Fatal("未知顶层键应报错")
	}
	var se *model.Error
	if !model.IsKind(err, model.KindValidationError) {
		t.Errorf("错误类型 = %v, want ValidationError", err)
	}
	if e, ok := err.(*model.Error); ok {
		if e.Code != "DSL_UNKNOWN_KEY" {
			t.Errorf("错误码 = %q, want DSL_UNKNOWN_KEY", e.Code)
		}
	}
	_ = se
}

// TestParseInvalidJSON 锁住 JSON 解析失败的错误码。
func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{"version": "1", "nodes": [`))
	if err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	e, ok := err.(*model.Error)
	if !ok {
		t.Fatalf("错误类型 = %T, want *model.Error", err)
	}
	if e.Code != "DSL_PARSE_ERROR" {
		t.Errorf("错误码 = %q, want DSL_PARSE_ERROR", e.Code)
	}
	if e.Kind != model.KindValidationError {
		t.Errorf("kind = %q, want ValidationError", e.Kind)
	}
}

// TestParseEmptyDocument 验证空文档不报错(后续由 validator 报"至少一个节点")。
func TestParseEmptyDocument(t *testing.T) {
	doc, err := Parse([]byte(""))
	if err != nil {
		t.Fatalf("空文档不应报错: %v", err)
	}
	if len(doc.Nodes) != 0 || len(doc.Edges) != 0 {
		t.Errorf("空文档应无节点无边: %+v", doc)
	}
	wf := doc.ToModel()
	if wf.Variables == nil {
		t.Error("ToModel 应给出非 nil 的 Variables")
	}
	if len(wf.Nodes) != 0 {
		t.Errorf("nodes = %d, want 0", len(wf.Nodes))
	}
}

// ---- 约定 13:校验期与运行期必须同源 ----

// TestValidatorAcceptsAllModelNodeTypes 钉死"节点类型清单只有一份":
// model 注册的每个类型,validator 都必须认识(不得报 UNKNOWN_NODE_TYPE)。
//
// 历史上若 dsl / validator / compiler 各维护一份清单,新增类型漏改一处
// 就会出现"用户被自己的校验器挡住"或"运行期支持但校验期判非法"。
func TestValidatorAcceptsAllModelNodeTypes(t *testing.T) {
	for _, nt := range model.AllNodeTypes() {
		t.Run(string(nt), func(t *testing.T) {
			wf := &model.Workflow{
				ID:   "wf-1",
				Name: "Demo",
				Nodes: []model.Node{
					{ID: "n1", Type: nt, Config: map[string]any{}},
				},
			}
			res := validator.Validate(wf, nil, nil)
			for _, e := range res.Errors {
				if e.Code == "UNKNOWN_NODE_TYPE" {
					t.Errorf("validator 不认识 model 已注册的节点类型 %q", nt)
				}
			}
		})
	}
}

// TestValidatorRejectsUnknownNodeType 验证未注册的类型被明确拒绝
// (而不是悄悄放行 —— "该失败却成功"等同 P0)。
func TestValidatorRejectsUnknownNodeType(t *testing.T) {
	wf := &model.Workflow{
		ID:   "wf-1",
		Name: "Demo",
		Nodes: []model.Node{
			{ID: "n1", Type: model.NodeType("custom-node"), Config: map[string]any{}},
		},
	}
	res := validator.Validate(wf, nil, nil)
	found := false
	for _, e := range res.Errors {
		if e.Code == "UNKNOWN_NODE_TYPE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("未注册节点类型应报 UNKNOWN_NODE_TYPE,实际 %+v", res.Errors)
	}
}

// requiredKeyByCode 把 validator 的"缺少必填配置"错误码映射到配置键。
// 探针式断言用:先问 validator 要哪些键,再检查 dsl 的配置结构体是否覆盖。
var requiredKeyByCode = map[string]string{
	"MISSING_AGENT":         "agent",
	"MISSING_MODE":          "mode",
	"MISSING_SKILL":         "skill",
	"MISSING_EXPRESSION":    "expression",
	"MISSING_COMMAND":       "command",
	"INVALID_GIT_OPERATION": "operation",
	"MISSING_WORKFLOW_ID":   "workflow_id",
}

// dslConfigStructs 把节点类型映射到 dsl 包里的配置结构体。
//
// 这里存在**两份实现**:validator 内联着"各类型必填哪些配置",
// dsl 里另有一份结构体描述同一批字段。本测试锁住两者一致:
// validator 新增必填字段却没同步 dsl 结构体时,这里会失败。
//
// 真正的合并(让 validator 直接消费 dsl 的定义)需要改 validator ——
// 不在本轮可改范围内,方案见交付报告。
var dslConfigStructs = map[model.NodeType]any{
	model.NodeTypeAgent:       AgentNodeConfig{},
	model.NodeTypeSkill:       SkillNodeConfig{},
	model.NodeTypeCondition:   ConditionNodeConfig{},
	model.NodeTypeHuman:       HumanNodeConfig{},
	model.NodeTypeScript:      ScriptNodeConfig{},
	model.NodeTypeGit:         GitNodeConfig{},
	model.NodeTypeSubWorkflow: SubWorkflowNodeConfig{},
	// parallel / merge 无必填配置,dsl 里也没有对应结构体
}

// TestDSLConfigStructsCoverValidatorRequirements 用"探针"方式断言同源:
// 对每个节点类型,先用空配置问 validator 要哪些必填键,
// 再检查 dsl 里对应的配置结构体是否覆盖这些键。
func TestDSLConfigStructsCoverValidatorRequirements(t *testing.T) {
	for _, nt := range model.AllNodeTypes() {
		t.Run(string(nt), func(t *testing.T) {
			wf := &model.Workflow{
				ID:   "wf-1",
				Name: "Demo",
				Nodes: []model.Node{
					{ID: "n1", Type: nt, Config: map[string]any{}},
				},
			}
			res := validator.Validate(wf, nil, nil)
			required := map[string]bool{}
			for _, e := range res.Errors {
				if key, ok := requiredKeyByCode[e.Code]; ok {
					required[key] = true
				}
			}
			structVal, hasStruct := dslConfigStructs[nt]
			if len(required) == 0 {
				// 无必填配置的节点类型:validator 不要求任何配置键,
				// dsl 里可以没有对应结构体(human 有结构体但字段都是可选的,
				// parallel / merge 则没有结构体)—— 两边不冲突,无需断言。
				_ = hasStruct
				return
			}
			if !hasStruct {
				t.Fatalf("%s 有必填配置 %v,dsl 里却没有对应配置结构体", nt, required)
			}
			fields := configFieldKeys(t, structVal)
			for key := range required {
				if !fields[key] {
					t.Errorf("%s 的必填配置 %q 在 dsl 配置结构体 %T 里缺失", nt, key, structVal)
				}
			}
		})
	}
}

// configFieldKeys 取出配置结构体的字段名(yaml tag 优先,退化到 json tag)。
func configFieldKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" {
			name = strings.Split(f.Tag.Get("json"), ",")[0]
		}
		if name == "" || name == "-" {
			continue
		}
		keys[name] = true
	}
	return keys
}

// TestDocumentJSONRoundTrip 验证文档级 JSON 往返不丢节点配置。
func TestDocumentJSONRoundTrip(t *testing.T) {
	doc, err := Parse([]byte(fixtureYAML(model.NodeTypeAgent, map[string]any{"agent": "claude-code", "mode": "plan"})))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := &Document{}
	if err := json.Unmarshal(raw, got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	if got.Nodes[0].ID != "n1" || got.Nodes[0].Type != "agent" {
		t.Errorf("结构化字段丢失: %+v", got.Nodes[0])
	}
	if got.Nodes[0].Rest["agent"] != "claude-code" || got.Nodes[0].Rest["mode"] != "plan" {
		t.Errorf("节点配置丢失: %#v", got.Nodes[0].Rest)
	}
}
