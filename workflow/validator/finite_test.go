package validator

import (
	"math"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"**落库前的数值必须有限**"这条校验期规则(第二轮 P1-1)。
//
// 实测缺陷(隔离副本,基线 73c02eb):YAML 里的一个 `.nan` 让整张 variables
// map 与版本快照**静默变空串** —— 旁边合法的 `limit: 10` 一起丢;运行期
// variables 为空 → 模板变量渲染空串(有 node.warning),**条件变量则零
// 告警** → 条件恒假 → 静默走错分支,执行仍 COMPLETED。
//
// 校验期是用户能看见的那一层:导入时就报明确的错,而不是"导入成功但变量
// 全没了"。持久层是兜底(序列化失败即报错)。
//
// 错误码复用 INVALID_NODE(前端 CODE_HINTS 已有该码,不必同步前端)。

// finiteWorkflow 造一个最小可用的工作流(单节点、无环、有出口)。
func finiteWorkflow() *model.Workflow {
	return &model.Workflow{
		ID:   "wf-finite",
		Name: "有限性校验",
		Nodes: []model.Node{{
			ID:   "n1",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent": "claude",
				"mode":  "plan",
			},
		}},
	}
}

// TestValidateRejectsNonFiniteVariables 含 NaN/Inf 的 variables 必须校验失败。
func TestValidateRejectsNonFiniteVariables(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wf := finiteWorkflow()
			wf.Variables = map[string]any{"env": c.value, "limit": 10}
			res := Validate(wf, nil, nil)
			if res.Valid {
				t.Fatalf("含 %s 的 variables 必须校验失败(否则落库时整张 map 会被清空)", c.name)
			}
			if !hasCode(res.Errors, "INVALID_NODE") {
				t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
			}
			// 错误信息要能定位到具体的变量名。
			if !mentions(res.Errors, "variables.env") {
				t.Errorf("错误信息必须指出出问题的变量(variables.env),实际: %+v", res.Errors)
			}
		})
	}
}

// TestValidateRejectsNonFiniteNestedVariable 嵌套在 map / 数组里的非有限值
// 同样必须被抓住(只查顶层等于没查)。
func TestValidateRejectsNonFiniteNestedVariable(t *testing.T) {
	wf := finiteWorkflow()
	wf.Variables = map[string]any{
		"cfg": map[string]any{"deep": []any{1, 2, math.Inf(1)}},
	}
	res := Validate(wf, nil, nil)
	if res.Valid {
		t.Fatal("嵌套在数组里的 Inf 必须校验失败")
	}
	if !mentions(res.Errors, "variables.cfg.deep[2]") {
		t.Errorf("错误信息必须指出出问题的位置,实际: %+v", res.Errors)
	}
}

// TestValidateRejectsNonFiniteNodeConfig 节点配置里的非有限值同样必须失败
// (nodes_json 整列会被清空 = 跑的是另一个工作流,约定 14)。
func TestValidateRejectsNonFiniteNodeConfig(t *testing.T) {
	wf := finiteWorkflow()
	wf.Nodes[0].Config["timeout_seconds"] = math.NaN()
	res := Validate(wf, nil, nil)
	if res.Valid {
		t.Fatal("节点配置含 NaN 必须校验失败")
	}
	if !hasCode(res.Errors, "INVALID_NODE") {
		t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
	}
}

// TestValidateRejectsNonFinitePermission permissions 段同样要查。
func TestValidateRejectsNonFinitePermission(t *testing.T) {
	wf := finiteWorkflow()
	wf.Permission = map[string]any{"filesystem.write": math.Inf(-1)}
	res := Validate(wf, nil, nil)
	if res.Valid {
		t.Fatal("permissions 含 -Inf 必须校验失败")
	}
}

// TestValidateAcceptsFiniteValues 正常值必须**不被误报**(判据不能过严)。
func TestValidateAcceptsFiniteValues(t *testing.T) {
	wf := finiteWorkflow()
	wf.Variables = map[string]any{
		"env":    "prod",
		"limit":  10,
		"ratio":  1.5,
		"big":    int64(1) << 40,
		"nested": map[string]any{"a": []any{1, "two", false}},
	}
	res := Validate(wf, nil, nil)
	if !res.Valid {
		t.Fatalf("正常值不得被判非法: %+v", res.Errors)
	}
}

// TestValidateAcceptsInfinityLookalikes 长得像非有限值但其实是普通字符串的
// 值不得被误报。
func TestValidateAcceptsInfinityLookalikes(t *testing.T) {
	wf := finiteWorkflow()
	wf.Variables = map[string]any{"a": "NaN", "b": "Inf", "c": ".nan"}
	res := Validate(wf, nil, nil)
	if !res.Valid {
		t.Fatalf("字符串形态的 NaN/Inf 不得被判非法: %+v", res.Errors)
	}
}

// hasCode 判断错误列表里有没有指定错误码。
func hasCode(errs []ValidationError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

// mentions 判断错误列表里有没有提到指定片段。
func mentions(errs []ValidationError, sub string) bool {
	for _, e := range errs {
		if contains(e.Message, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
