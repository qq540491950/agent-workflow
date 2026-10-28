package api

// 声明式入参(inputs)经 REST 启动执行的契约(迭代 36)。
//
// run 端点的请求体形状不变(task + variables),新增的是**校验行为**:
//   - 必填入参缺失 → 400 + code=INPUT_REQUIRED;
//   - 传入值类型不符 → 400 + code=INPUT_TYPE_MISMATCH;
//   - 提供 required + 缺省项兜 default → 200,执行变量里带默认值。

import (
	"net/http"
	"testing"
)

func TestContract_RunWithInputs(t *testing.T) {
	s := newTestServer(t)
	body := `{"id":"wf-inputs-api","name":"Inputs API",
		"inputs":[
			{"name":"repo","type":"string","required":true},
			{"name":"limit","type":"number","default":3}
		],
		"nodes":[{"id":"a","name":"A","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],
		"edges":[],"enabled":true}`
	code, m, raw := doReq(t, s, http.MethodPost, "/api/workflows", body)
	if code != 200 {
		t.Fatalf("seed: %d %v %s", code, m, raw)
	}

	// 必填缺失 → 400 INPUT_REQUIRED,执行不得启动
	code, body2, raw := doReq(t, s, http.MethodPost, "/api/workflows/wf-inputs-api/run", `{"variables":{"limit":1}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("必填缺失 run → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "必填缺失 run", body2, raw, "INPUT_REQUIRED")

	// 类型不符 → 400 INPUT_TYPE_MISMATCH
	code, body2, raw = doReq(t, s, http.MethodPost, "/api/workflows/wf-inputs-api/run", `{"variables":{"repo":"x","limit":"3"}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("类型不符 run → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "类型不符 run", body2, raw, "INPUT_TYPE_MISMATCH")

	// 提供 required → 200;default 兜底(快照里的整数值按约定 #2 是 float64)
	code, body2, raw = doReq(t, s, http.MethodPost, "/api/workflows/wf-inputs-api/run", `{"variables":{"repo":"x"}}`)
	if code != http.StatusOK {
		t.Fatalf("正常 run → %d (%s)", code, raw)
	}
	execID, _ := body2["id"].(string)
	if execID == "" {
		t.Fatalf("run 响应缺 id: %s", raw)
	}
	vars, _ := body2["variables"].(map[string]any)
	if vars == nil || vars["repo"] != "x" {
		t.Fatalf("执行变量应含 repo=x: %s", raw)
	}
	if n, ok := vars["limit"].(float64); !ok || n != 3 {
		t.Fatalf("limit 应兜 default 3,实际 %T(%v)", vars["limit"], vars["limit"])
	}
	waitExecState(t, s, execID, "COMPLETED", "FAILED")

	// 工作流详情带出 inputs 声明(前端据此渲染)
	code, body3, raw := doReq(t, s, http.MethodGet, "/api/workflows/wf-inputs-api", "")
	if code != 200 {
		t.Fatalf("get workflow: %d (%s)", code, raw)
	}
	inputs, ok := body3["inputs"].([]any)
	if !ok || len(inputs) != 2 {
		t.Fatalf("工作流详情应带 inputs 声明: %s", raw)
	}
}
