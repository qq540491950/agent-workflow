package persistence

// inputs(声明式运行入参)的持久化往返:保存 → 读回,声明不丢、
// 数值默认值的形态与 variables 同一套归一(int 保持 int)。

import (
	"testing"

	"agentworkflow/workflow/model"
)

func TestWorkflowInputsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	wf := &model.Workflow{
		ID:   "wf-inputs",
		Name: "wf-inputs",
		Inputs: []model.InputDecl{
			{Name: "repo", Type: model.InputTypeString, Required: true, Description: "目标仓库"},
			{Name: "limit", Type: model.InputTypeNumber, Default: 3},
			{Name: "dry", Type: model.InputTypeBoolean, Default: false},
		},
		Nodes: []model.Node{{
			ID:   "a",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent": "mock-a",
				"mode":  "plan",
			},
		}},
		Edges: []model.Edge{},
	}
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := db.GetWorkflow("wf-inputs")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Inputs) != 3 {
		t.Fatalf("读回应保留 3 条声明,实际 %d", len(got.Inputs))
	}
	if got.Inputs[0].Name != "repo" || !got.Inputs[0].Required || got.Inputs[0].Description == "" {
		t.Errorf("repo 声明不符: %+v", got.Inputs[0])
	}
	if n, ok := got.Inputs[1].Default.(int); !ok || n != 3 {
		t.Errorf("limit 默认值应为 int 3(JSON 往返后按约定 #2 归一),实际 %T(%v)",
			got.Inputs[1].Default, got.Inputs[1].Default)
	}
	if b, ok := got.Inputs[2].Default.(bool); !ok || b {
		t.Errorf("dry 默认值应为 false,实际 %T(%v)", got.Inputs[2].Default, got.Inputs[2].Default)
	}

	// 更新为空声明后读回 → 空表(不是旧值)
	wf.Inputs = nil
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got2, err := db.GetWorkflow("wf-inputs")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if len(got2.Inputs) != 0 {
		t.Fatalf("清空声明后读回应为空,实际 %d 条", len(got2.Inputs))
	}
}
