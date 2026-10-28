package api

// settings.max_concurrent 经 REST 的契约:达到上限的 run → 409 +
// code=WORKFLOW_BUSY(请求合法,只是现在不能跑)。

import (
	"net/http"
	"testing"
)

func TestContract_RunConcurrencyLimit(t *testing.T) {
	s := newTestServer(t)
	body := `{"id":"wf-mc-api","name":"MC API",
		"settings":{"max_concurrent":1},
		"nodes":[{"id":"gate","name":"G","type":"human","config":{"prompt":"等待审批"}}],
		"edges":[],"enabled":true}`
	code, m, raw := doReq(t, s, http.MethodPost, "/api/workflows", body)
	if code != 200 {
		t.Fatalf("seed: %d %v %s", code, m, raw)
	}

	// 第一个执行 → 200(卡在 WAITING_USER,占住额度)
	code, m, raw = doReq(t, s, http.MethodPost, "/api/workflows/wf-mc-api/run", `{"task":"first"}`)
	if code != http.StatusOK {
		t.Fatalf("first run → %d (%s)", code, raw)
	}
	execID, _ := m["id"].(string)
	waitExecState(t, s, execID, "WAITING_USER")

	// 第二个执行 → 409 WORKFLOW_BUSY
	code, m, raw = doReq(t, s, http.MethodPost, "/api/workflows/wf-mc-api/run", `{"task":"second"}`)
	if code != http.StatusConflict {
		t.Fatalf("second run → %d, want 409 (%s)", code, raw)
	}
	assertErrorBody(t, "second run", m, raw, "WORKFLOW_BUSY")
}
