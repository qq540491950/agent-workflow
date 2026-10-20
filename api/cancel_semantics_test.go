package api

// 本文件钉住迭代 19 / P1-1 在 **API 层**的取消语义:刚跑完的执行
// 被取消时,必须返回明确错误(400 + INVALID_TRANSITION),不能 200 假成功。
//
// 与 server_test.go:TestContract_ExecutionCancel 的区别:那个用例按 50ms
// 的间隔轮询执行状态,几乎碰不到"终态写库 → e.cancels 句柄摘除"那个
// 窗口(窗口 ≈ 几百微秒);本用例**紧密轮询**,终态一出现就立刻取消 ——
// 正好落在窗口里,是那个窗口的端到端复刻。
//
// 修复前:窗口内取消走"运行中"快路径(只 cancel() 就返回 nil)→ 200 假成功。
// 修复后:取消先读库里的状态(唯一裁决来源)→ 终态 → INVALID_TRANSITION。

import (
	"net/http"
	"testing"
	"time"
)

// TestContract_CancelRightAfterCompletion 终态一出现就立刻取消 → 400。
func TestContract_CancelRightAfterCompletion(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-cancel-tight")

	_, body, _ := doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	execID, _ := body["id"].(string)
	if execID == "" {
		t.Fatal("run 未返回执行 ID")
	}

	// 紧密轮询:不等 50ms 的间隔,终态一落库就发起取消。
	deadline := time.Now().Add(10 * time.Second)
	var state string
	for time.Now().Before(deadline) {
		_, b, _ := doReq(t, s, http.MethodGet, "/api/executions/"+execID, "")
		state, _ = b["state"].(string)
		if state == "COMPLETED" || state == "FAILED" {
			break
		}
	}
	if state != "COMPLETED" && state != "FAILED" {
		t.Fatalf("执行未进入终态,最后状态 %q", state)
	}

	code, body, raw := doReq(t, s, http.MethodPost, "/api/executions/"+execID+"/cancel", "")
	if code != http.StatusBadRequest {
		t.Fatalf("cancel 刚跑完的执行 → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "cancel 刚跑完的执行", body, raw, "INVALID_TRANSITION")
}
