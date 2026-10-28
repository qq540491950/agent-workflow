package dsl

// inputs 段(声明式运行入参,迭代 36)的解析与往返测试。
//
// 入参声明的语义:
//   - 解析层只做结构映射:条目键集合封闭(name/type/required/default/
//     description),未知键记 IgnoredKeys(WARN)不阻断 —— 与 edges 同口径;
//   - `inputs` 成为合法顶层键(此前会报 DSL_UNKNOWN_KEY 拒绝导入);
//   - 默认值的数值形态在 ToModel 时钉死一次(YAML uint64 → int)。
//
// 语义校验(重名/类型/必填冲突)在 validator;启动时校验在 runtime。

import (
	"testing"

	"agentworkflow/workflow/model"
)

const inputsYAML = `
version: "1"
workflow:
  id: inputs-demo
  name: Inputs Demo
inputs:
  - name: repo
    type: string
    required: true
    description: 目标仓库路径
  - name: limit
    type: number
    default: 3
  - name: dry_run
    type: boolean
    default: false
nodes:
  - id: a
    type: agent
    agent: mock-a
    mode: plan
edges: []
`

func TestInputsParseYAML(t *testing.T) {
	doc, err := Parse([]byte(inputsYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Inputs) != 3 {
		t.Fatalf("应有 3 个入参声明,实际 %d", len(doc.Inputs))
	}
	repo := doc.Inputs[0]
	if repo.Name != "repo" || repo.Type != model.InputTypeString || !repo.Required || repo.Description == "" {
		t.Fatalf("repo 声明不符: %+v", repo)
	}
	// YAML 整数在文档层是 uint64(goccy 解析口径);ToModel 时钉成 int
	// (见 copyInputs,运行期数值判定只认 int/float64)
	if lim := doc.Inputs[1]; lim.Default != uint64(3) {
		t.Fatalf("文档层 limit 默认值应为 uint64 3,实际 %T(%v)", lim.Default, lim.Default)
	}
	if mLimit := doc.ToModel().Inputs[1].Default; mLimit != 3 {
		t.Fatalf("模型层 limit 默认值应钉成 int 3,实际 %T(%v)", mLimit, mLimit)
	}
	if dr := doc.Inputs[2]; dr.Default != false {
		t.Fatalf("dry_run 默认值应为 false,实际 %v", dr.Default)
	}
}

func TestInputsParseJSON(t *testing.T) {
	const js = `{"version":"1","workflow":{"id":"inputs-demo","name":"Inputs Demo"},
		"inputs":[{"name":"repo","type":"string","required":true},{"name":"limit","type":"number","default":3}],
		"nodes":[{"id":"a","type":"agent","agent":"mock-a","mode":"plan"}],"edges":[]}`
	doc, err := Parse([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Inputs) != 2 || doc.Inputs[0].Name != "repo" || doc.Inputs[1].Default != float64(3) {
		t.Fatalf("JSON 解析结果不符: %+v", doc.Inputs)
	}
}

// TestInputsUnknownItemKeyIgnored 条目内未知键 → 忽略但可观测(与 edges 同口径)。
func TestInputsUnknownItemKeyIgnored(t *testing.T) {
	doc, err := Parse([]byte(`
version: "1"
workflow: {id: i, name: i}
inputs:
  - name: repo
    type: string
    reqired: true
nodes:
  - {id: a, type: agent, agent: mock-a, mode: plan}
`))
	if err != nil {
		t.Fatalf("未知条目键不应阻断导入: %v", err)
	}
	found := false
	for _, k := range doc.IgnoredKeys {
		if k == "inputs.0.reqired" {
			found = true
		}
	}
	if !found {
		t.Fatalf("IgnoredKeys 应含 inputs.0.reqired,实际 %v", doc.IgnoredKeys)
	}
	if doc.Inputs[0].Required {
		t.Fatal("拼错的键不应生效(required 应保持缺省 false)")
	}
}

// TestInputsToModelRoundtrip ToModel/FromModel 往返不丢声明。
func TestInputsToModelRoundtrip(t *testing.T) {
	doc, err := Parse([]byte(inputsYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if len(wf.Inputs) != 3 {
		t.Fatalf("模型应带 3 个入参声明,实际 %d", len(wf.Inputs))
	}
	back := FromModel(wf)
	if len(back.Inputs) != 3 || back.Inputs[0].Name != "repo" || back.Inputs[1].Default != 3 {
		t.Fatalf("导出文本应还原入参声明: %+v", back.Inputs)
	}
	// 导出的 YAML 再导回,语义不变
	doc2, err := Parse(mustYAML(t, back))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(doc2.Inputs) != 3 || doc2.Inputs[2].Type != model.InputTypeBoolean {
		t.Fatalf("往返后声明不符: %+v", doc2.Inputs)
	}
}

func mustYAML(t *testing.T, d *Document) []byte {
	t.Helper()
	b, err := d.EncodeYAML()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}
