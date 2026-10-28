package validator

// settings.max_concurrent 的取值范围校验(唯一来源:compiler.MaxConcurrentRange)。

import (
	"testing"

	"agentworkflow/workflow/model"
)

func TestMaxConcurrentRangeCheck(t *testing.T) {
	base := func(mc int) *model.Workflow {
		return &model.Workflow{
			ID:       "wf-mc",
			Name:     "wf-mc",
			Settings: model.WorkflowSettings{MaxConcurrent: mc},
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
	for _, tc := range []struct {
		name string
		mc   int
		ok   bool
	}{
		{"0 = 不限", 0, true},
		{"1", 1, true},
		{"上界 100", 100, true},
		{"负数(手滑)", -1, false},
		{"超上界", 101, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(base(tc.mc), nil, nil)
			if res.Valid != tc.ok {
				t.Fatalf("max_concurrent=%d: 期望 valid=%v,实际 %v(错误: %+v)",
					tc.mc, tc.ok, res.Valid, res.Errors)
			}
			if !tc.ok && !errHas(res, "INVALID_MAX_CONCURRENT") {
				t.Errorf("错误码必须是 INVALID_MAX_CONCURRENT,实际: %+v", res.Errors)
			}
		})
	}
}
