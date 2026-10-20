package runtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentworkflow/api"
	"agentworkflow/app/application"
	"agentworkflow/workflow/model"
)

// 本文件是 masking_test.go 的**读出口**那一半:那边查的是"落库前"有没有脱敏
// (DB 直查),这边查的是"API 响应体"里有没有明文。
//
// 为什么放在外部测试包(package runtime_test):api / app.application 都 import
// workflow/runtime,同包测试文件 import 它们会形成循环(Go 禁止)。
// 外部测试包可以 import 依赖被测包的包 —— 这是 Go 明确支持的用法。
//
// 覆盖缺口背景(迭代 14 复审):此前 masking_test.go 只断言
// execution_nodes 的三个字段,**从不检查 executions.state_json**,
// 于是"API 返回的状态快照里带凭据明文"这件事没人管。

// apiSecret 是测试用的"凭据形态"串(Mask 的规则:key=value 形态)。
const apiSecret = "sk-super-secret-value"

// TestExecutionAPIResponseMasksStateJSON 执行详情的 API 响应体不得含
// 凭据明文 —— 状态快照(task / 节点输出)同样要脱敏。
//
// 断言分三步,缺一不可:
//  1. 响应里**确实带着**状态快照(否则"没有明文"是因为根本没返回状态,
//     是一条恒真断言);
//  2. 响应体不含凭据明文;
//  3. 响应的状态字段是脱敏后的形态(键名保留、值替换为 ***)。
func TestExecutionAPIResponseMasksStateJSON(t *testing.T) {
	a, err := application.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })

	wf := &model.Workflow{
		ID:      "wf-state-mask",
		Name:    "State Mask",
		Enabled: true,
		Nodes: []model.Node{
			{ID: "plan", Type: model.NodeTypeAgent, Config: map[string]any{
				"agent": "mock-claude", "mode": "plan",
			}},
		},
	}
	if _, err := a.Workflows.Save(wf); err != nil {
		t.Fatalf("保存工作流: %v", err)
	}
	// 任务文本会进会话状态 → 状态快照里带凭据。
	exec, err := a.Executions.Run(context.Background(), wf.ID,
		"部署时带上 ANTHROPIC_API_KEY="+apiSecret, nil)
	if err != nil {
		t.Fatalf("运行: %v", err)
	}
	waitExecutionCompleted(t, a, exec.ID)

	srv := api.NewServer(a)
	req := httptest.NewRequest(http.MethodGet, "/api/executions/"+exec.ID, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/executions/%s → %d, want 200: %s", exec.ID, w.Code, w.Body.String())
	}
	body := w.Body.String()

	// (1) 响应里必须真的有状态快照,否则下面的"不含明文"是恒真的。
	if !strings.Contains(body, `"state_data"`) {
		t.Fatalf("响应体里没有 state_data 字段,本用例覆盖不到状态快照: %s", body)
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY") {
		t.Fatalf("响应体里连任务文本都没有,本用例没有覆盖到真实内容: %s", body)
	}
	// (2) 不得含凭据明文
	if strings.Contains(body, apiSecret) {
		t.Errorf("API 响应体含凭据明文: %s", body)
	}
	// (3) 脱敏后的形态:键名保留、值替换为 ***
	if !strings.Contains(body, "ANTHROPIC_API_KEY=***") {
		t.Errorf("API 响应体应把凭据值替换为 ***: %s", body)
	}
}

// waitExecutionCompleted 轮询等待执行到达终态(成功)。
func waitExecutionCompleted(t *testing.T, a *application.App, execID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		e, err := a.Executions.Get(execID)
		if err == nil && e.State == model.ExecutionCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e, _ := a.Executions.Get(execID)
	t.Fatalf("执行 %s 未到达 COMPLETED,当前: %+v", execID, e)
}
