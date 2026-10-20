package application

// 本文件锁住"裸值形态凭据"的 **API 读出口**脱敏(迭代 12)。
//
// 第十一轮终审实测到的泄漏:Agent 输出里的裸 `Bearer <JWT>` 落库并经
// 所有 API 出口(SSE / 执行详情 / 节点明细 / 导出)流到前端。
//
// 落库侧的回归锁在 workflow/runtime/credential_shape_leak_test.go;
// 本文件锁读出口 —— 两边都绿才说明"落库即脱敏 + 读出口再脱敏"这条
// 链路完整(读出口那道是兜底:库里可能有本轮修复之前的历史数据)。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// outletJWT 是终审实测泄漏的那个 JWT。
const outletJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ." +
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

// TestBareCredentialMaskedAtAllReadOutlets 裸值形态凭据不得出现在任何
// API 读出口里。
func TestBareCredentialMaskedAtAllReadOutlets(t *testing.T) {
	a := newTestApp(t)
	wf := &model.Workflow{
		ID: "wf-bare-cred", Name: "BareCred", Enabled: true,
		Nodes: []model.Node{
			{ID: "plan", Type: model.NodeTypeAgent, Config: map[string]any{
				"agent": "mock-claude", "mode": "plan",
			}},
		},
	}
	if _, err := a.Workflows.Save(wf); err != nil {
		t.Fatalf("保存工作流: %v", err)
	}
	// 任务文本会进会话状态,也会被 mock 回显进节点输出 —— 凭据因此同时
	// 出现在 state_data 与节点明细两个出口。
	exec, err := a.Executions.Run(context.Background(), wf.ID,
		"带上 Bearer "+outletJWT, nil)
	if err != nil {
		t.Fatalf("运行: %v", err)
	}
	waitExecutionState(t, a, exec.ID, model.ExecutionCompleted)

	// 出口 1:执行详情(Get)
	got, err := a.Executions.Get(exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if containsSecret(got.Error) || containsSecret(got.StateData) || containsSecret(got.Task) {
		t.Errorf("执行详情含凭据明文: %s", mustJSON(got))
	}

	// 出口 2:节点明细(Nodes)
	nodes, err := a.Executions.Nodes(exec.ID)
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("应至少有一条节点明细")
	}
	for _, n := range nodes {
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("序列化节点: %v", err)
		}
		if strings.Contains(string(b), outletJWT) {
			t.Errorf("节点明细含凭据明文: %s", b)
		}
	}

	// 出口 3:导出(Export)
	data, err := a.Executions.Export(exec.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if strings.Contains(mustJSON(data), outletJWT) {
		t.Errorf("导出内容含凭据明文: %s", mustJSON(data))
	}

	// 出口 4:事件流(Events)
	events, err := a.Executions.Events(exec.ID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, ev := range events {
		if strings.Contains(mustJSON(ev), outletJWT) {
			t.Errorf("事件流含凭据明文: %s", mustJSON(ev))
		}
	}
}
