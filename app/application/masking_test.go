package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentworkflow/workflow/model"
)

// 本文件锁住"执行详情的 API 读出口必须脱敏"(约定 #8:API 返回掩码)。
//
// 背景:节点明细(output/error/result_json)已在读出口脱敏(maskNode),
// 但 **执行记录本身** 的两个自由文本出口没有:
//
//   - exec.Error:执行失败原因(常带 CLI stderr,而 stderr 里常有
//     "ANTHROPIC_API_KEY=sk-..." 这类凭据);
//   - exec.StateData:会话状态快照,里面有每个节点的 outcome 原文
//     (node:<id>:result)、最近一次输出(output)、错误文本(_error)。
//
// 运行期落库时对 exec.Error 做了脱敏(persist.go 的 logx.Mask),
// 但 StateData 的落库副本是**原样**写入的(state_json)——
// 于是 API 读出口把带凭据的状态快照原样返回给了前端。

// secretValue 是测试用的"凭据形态"串(Mask 的规则:key=value 形态)。
const secretValue = "sk-super-secret-value"

// TestExecutionReadOutMasksSecrets 执行读出口(Get / List / Export)不得返回凭据明文。
func TestExecutionReadOutMasksSecrets(t *testing.T) {
	a := newTestApp(t)
	wf := &model.Workflow{
		ID:      "wf-secret",
		Name:    "Secret",
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
	// mock-claude 的 plan 模板会把 task 回显进 summary/output
	// → 凭据会进入节点的 outcome,进而进入会话状态快照。
	exec, err := a.Executions.Run(context.Background(), wf.ID,
		"部署时带上 ANTHROPIC_API_KEY="+secretValue, nil)
	if err != nil {
		t.Fatalf("运行: %v", err)
	}
	waitExecutionState(t, a, exec.ID, model.ExecutionCompleted)

	// Get
	got, err := a.Executions.Get(exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if containsSecret(got.Error) {
		t.Errorf("exec.Error 含凭据明文: %q", got.Error)
	}
	if containsSecret(got.StateData) {
		t.Errorf("exec.StateData 含凭据明文: %s", mustJSON(got.StateData))
	}

	// List
	list, err := a.Executions.List(wf.ID, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range list {
		if containsSecret(e.Error) || containsSecret(e.StateData) {
			t.Errorf("列表项含凭据明文: error=%q state=%s", e.Error, mustJSON(e.StateData))
		}
	}

	// Export
	data, err := a.Executions.Export(exec.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if containsSecret(data) {
		t.Errorf("导出内容含凭据明文: %s", mustJSON(data))
	}
}

// TestExecutionReadOutDoesNotCorruptRuntimeState 读出口脱敏**只**作用于
// 返回给调用方的副本,不得改坏运行期仍在用的状态。
//
// 这一点很关键:StateData 是会话状态快照,运行期会在恢复时把它回放进
// 一次执行。若读出口把**库里的**那份原地改了,恢复后的执行就会拿到
// 被遮蔽过的状态(语义被改写)。
func TestExecutionReadOutDoesNotCorruptRuntimeState(t *testing.T) {
	a := newTestApp(t)
	wf := &model.Workflow{
		ID:      "wf-secret2",
		Name:    "Secret2",
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
	exec, err := a.Executions.Run(context.Background(), wf.ID,
		"带上 ANTHROPIC_API_KEY="+secretValue, nil)
	if err != nil {
		t.Fatalf("运行: %v", err)
	}
	waitExecutionState(t, a, exec.ID, model.ExecutionCompleted)

	// 读一次(触发脱敏)
	if _, err := a.Executions.Get(exec.ID); err != nil {
		t.Fatalf("get: %v", err)
	}
	// 直接查库:库里那份必须仍然是原样的(脱敏只发生在返回副本上)。
	// 若读出口改动了库里的状态,恢复路径就会拿到被遮蔽的状态(语义被改写)。
	var rawState string
	if err := a.Repo.SQL().QueryRow(
		`SELECT state_json FROM executions WHERE id=?`, exec.ID).Scan(&rawState); err != nil {
		t.Fatalf("读取库里的状态快照: %v", err)
	}
	if !strings.Contains(rawState, secretValue) {
		t.Errorf("读出口脱敏改动了库里的状态快照: %s", rawState)
	}
}

// waitExecutionState 轮询等待执行到达指定状态。
func waitExecutionState(t *testing.T, a *App, execID string, want model.ExecutionState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e, err := a.Executions.Get(execID)
		if err == nil && e.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e, _ := a.Executions.Get(execID)
	t.Fatalf("执行 %s 未到达 %s,当前: %+v", execID, want, e)
}

// containsSecret 判断任意结构里是否含凭据明文。
func containsSecret(v any) bool {
	return strings.Contains(mustJSON(v), secretValue)
}

// mustJSON 把任意值序列化成 JSON 字符串(失败时返回占位串)。
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "<marshal error>"
	}
	return string(raw)
}
