package validator

// inputs 段(声明式运行入参)的校验期用例。
//
// 这些问题必须在保存时拦住而不是等到启动:
//   - 类型拼错 → 运行期任何传入值都不匹配,必填入参永远过不了启动;
//   - required + default → 语义矛盾;
//   - default 类型与声明不符 → 默认值自己都过不了类型检查;
//   - 与 variables 键 / 条件顶层变量同名 → 条件里读到的永远不是这个入参
//     (静默走错分支,约定 14 同族)。
//
// 错误码复用 INVALID_NODE(前端 CODE_HINTS 已有该码,约定 10)。

import (
	"testing"

	"agentworkflow/workflow/model"
)

func inputsWorkflow(inputs []model.InputDecl, variables map[string]any) *model.Workflow {
	return &model.Workflow{
		ID:        "wf-inputs",
		Name:      "wf-inputs",
		Inputs:    inputs,
		Variables: variables,
		Nodes: []model.Node{{
			ID:   "a",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent": "mock-a",
				"mode":  "plan",
			},
		}},
	}
}

func TestInputsValidFormsPass(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []model.InputDecl
	}{
		{"必填字符串", []model.InputDecl{{Name: "repo", Type: model.InputTypeString, Required: true}}},
		{"带默认值", []model.InputDecl{{Name: "limit", Type: model.InputTypeNumber, Default: 3}}},
		{"布尔默认 false", []model.InputDecl{{Name: "dry_run", Type: model.InputTypeBoolean, Default: false}}},
		{"多入参", []model.InputDecl{
			{Name: "repo", Type: model.InputTypeString, Required: true},
			{Name: "limit", Type: model.InputTypeNumber, Default: 5},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(inputsWorkflow(tc.inputs, nil), nil, nil)
			if !res.Valid {
				t.Fatalf("校验必须通过,实际: %+v", res.Errors)
			}
		})
	}
}

func TestInputsInvalidFormsRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []model.InputDecl
	}{
		{"缺少 name", []model.InputDecl{{Type: model.InputTypeString}}},
		{"重复声明", []model.InputDecl{
			{Name: "repo", Type: model.InputTypeString},
			{Name: "repo", Type: model.InputTypeString},
		}},
		{"类型拼错", []model.InputDecl{{Name: "repo", Type: "str"}}},
		{"类型为空", []model.InputDecl{{Name: "repo"}}},
		{"required 与 default 并存", []model.InputDecl{
			{Name: "repo", Type: model.InputTypeString, Required: true, Default: "x"},
		}},
		{"default 类型不符(number 声明)", []model.InputDecl{
			{Name: "limit", Type: model.InputTypeNumber, Default: "3"},
		}},
		{"default 类型不符(boolean 声明)", []model.InputDecl{
			{Name: "dry", Type: model.InputTypeBoolean, Default: "true"},
		}},
		{"default 类型不符(string 声明)", []model.InputDecl{
			{Name: "repo", Type: model.InputTypeString, Default: 3},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(inputsWorkflow(tc.inputs, nil), nil, nil)
			if res.Valid {
				t.Fatalf("%s: 校验必须不通过", tc.name)
			}
			if !errHas(res, "INVALID_NODE") {
				t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
			}
		})
	}
}

// TestInputsNameConflictsRejected 与 variables / 条件顶层变量的同名冲突。
func TestInputsNameConflictsRejected(t *testing.T) {
	t.Run("与 variables 键同名", func(t *testing.T) {
		res := Validate(inputsWorkflow(
			[]model.InputDecl{{Name: "env", Type: model.InputTypeString}},
			map[string]any{"env": "prod"},
		), nil, nil)
		if res.Valid || !errHas(res, "INVALID_NODE") {
			t.Fatalf("同名必须报 INVALID_NODE,实际: %+v", res.Errors)
		}
	})
	for _, condName := range []string{"decision", "status", "output", "iteration", "variables"} {
		t.Run("与条件变量 "+condName+" 同名", func(t *testing.T) {
			res := Validate(inputsWorkflow(
				[]model.InputDecl{{Name: condName, Type: model.InputTypeString}},
				nil,
			), nil, nil)
			if res.Valid || !errHas(res, "INVALID_NODE") {
				t.Fatalf("条件顶层变量同名必须报 INVALID_NODE,实际: %+v", res.Errors)
			}
		})
	}
}

// TestInputsNoVariablesConflictAcrossTypes 类型与 variables 无关(数值默认值
// int/float64/uint64 形态都算 number —— 与 NormalizeStateJSON 的归一口径一致)。
func TestInputsNumberDefaultsAllIntForms(t *testing.T) {
	for _, d := range []any{3, int64(3), uint64(3), float64(3)} {
		res := Validate(inputsWorkflow(
			[]model.InputDecl{{Name: "limit", Type: model.InputTypeNumber, Default: d}},
			nil,
		), nil, nil)
		if !res.Valid {
			t.Errorf("default %#v 应放行,实际: %+v", d, res.Errors)
		}
	}
}
