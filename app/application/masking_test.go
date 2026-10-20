package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentworkflow/agent"
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
// 一次执行。若读出口把**传入的那份**原地改了,恢复后的执行就会拿到
// 被遮蔽过的状态(语义被改写)。
//
// **语义变更说明(迭代 13)**:此前本用例断言"库里的 state_json 仍是明文",
// 那条断言与新语义(落库即脱敏,约定 #8)直接冲突 —— 库里留明文正是本轮
// 要修的缺陷(state_json 是节点明细之外的第四条泄漏通道:落库原样写 +
// API 原样返回)。因此断言改为两条更强的不变式:
//  1. 落库的快照已脱敏(库里没有明文);
//  2. 读出口的脱敏是纯函数:不改传入的 map(运行期还在用同一份结构)。
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
	// 库里那份必须是**脱敏后**的:落库即脱敏(约定 #8)。
	// 审计文件/备份 DB 直接取 state_json,明文落在这里就是泄漏。
	var rawState string
	if err := a.Repo.SQL().QueryRow(
		`SELECT state_json FROM executions WHERE id=?`, exec.ID).Scan(&rawState); err != nil {
		t.Fatalf("读取库里的状态快照: %v", err)
	}
	if strings.Contains(rawState, secretValue) {
		t.Errorf("落库的 state_json 含凭据明文: %s", rawState)
	}

	// 读出口的脱敏不得就地改传入的结构(运行期还在用同一份 map)。
	live := &model.Execution{StateData: map[string]any{
		"output": "cli stderr: ANTHROPIC_API_KEY=" + secretValue,
	}}
	original := live.StateData // 保留原 map 引用:脱敏后 exec.StateData 会被换成新副本
	maskExecution(live)
	if !strings.Contains(fmt.Sprint(original["output"]), secretValue) {
		t.Errorf("读出口脱敏就地改了传入的状态(运行期会读到被遮蔽的值): %v",
			original["output"])
	}
	if strings.Contains(fmt.Sprint(live.StateData["output"]), secretValue) {
		t.Errorf("读出口返回的副本仍含凭据明文: %v", live.StateData["output"])
	}
}

// TestExecutionStateDataMaskedInDatabase 锁住 P1-1 的落库侧:
// executions.state_json 不得含凭据明文(节点明细之外的第四条通道)。
func TestExecutionStateDataMaskedInDatabase(t *testing.T) {
	a := newTestApp(t)
	wf := &model.Workflow{
		ID:      "wf-secret3",
		Name:    "Secret3",
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

	// 落库的会话状态快照
	var rawState string
	if err := a.Repo.SQL().QueryRow(
		`SELECT state_json FROM executions WHERE id=?`, exec.ID).Scan(&rawState); err != nil {
		t.Fatalf("读取库里的状态快照: %v", err)
	}
	if strings.Contains(rawState, secretValue) {
		t.Errorf("state_json 落库含凭据明文: %s", rawState)
	}
	// API 读出口(Get = GET /api/executions/{id} 的数据来源)
	got, err := a.Executions.Get(exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if containsSecret(got.StateData) {
		t.Errorf("执行详情读出口含凭据明文: %s", mustJSON(got.StateData))
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

// ---------------------------------------------------------------------------
// 迭代 10(P0-1):ExtraArgs 的 *** 回写语义
//
// GET 返回的 ExtraArgs 里凭据位是 "***";前端把整份配置回传时会把
// "***" 一起带回来(Agents.tsx 的 openConfig/saveConfig 原样回传整份
// cfg)。若后端不合并,存库的真实凭据就被字面量 "***" 覆盖 —— 之后
// 每次 CLI 调用都带 --api-key *** → 鉴权失败,而用户只看到"已保存"。
// ---------------------------------------------------------------------------

// storedExtraArgs 读库里某 Agent 的 ExtraArgs。
func storedExtraArgs(t *testing.T, a *App, id string) []string {
	t.Helper()
	raw, err := a.Repo.GetAgentConfig(id)
	if err != nil {
		t.Fatalf("读取 Agent 配置: %v", err)
	}
	var stored agent.AgentConfig
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("解析 Agent 配置: %v", err)
	}
	return stored.ExtraArgs
}

// TestUpdateConfigExtraArgsMaskRoundTrip GET→PUT 往返:只改其它字段,
// 库里必须仍是**真实凭据**,且下发 CLI 的参数里没有 ***。
func TestUpdateConfigExtraArgsMaskRoundTrip(t *testing.T) {
	a := newTestApp(t)
	svc := a.AgentSvc

	if err := svc.UpdateConfig("claude-code", agent.AgentConfig{
		Model:     "m1",
		ExtraArgs: []string{"--verbose", "--api-key", "sk-real-secret"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}

	// GET:前端拿到的是掩码值
	got, err := svc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	joined := strings.Join(got.ExtraArgs, " ")
	if strings.Contains(joined, "sk-real-secret") {
		t.Fatalf("GetConfig 泄露了凭据明文: %q", joined)
	}
	if !strings.Contains(joined, agent.MaskedValue) {
		t.Fatalf("GetConfig 未返回掩码: %q", joined)
	}

	// 用户只改了模型,前端把整份 cfg(含 *** )回传
	roundtrip := got
	roundtrip.Model = "m2"
	if err := svc.UpdateConfig("claude-code", roundtrip); err != nil {
		t.Fatalf("往返保存: %v", err)
	}

	stored := storedExtraArgs(t, a, "claude-code")
	joined = strings.Join(stored, " ")
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("库里被写入了字面量 %s(凭据已被毁掉): %q", agent.MaskedValue, joined)
	}
	if !strings.Contains(joined, "sk-real-secret") {
		t.Errorf("库里应仍是真实凭据: %q", joined)
	}
	if !strings.Contains(joined, "--verbose") {
		t.Errorf("普通参数丢失: %q", joined)
	}
}

// TestUpdateConfigExtraArgsMaskWithoutStoredValue 无原值的 *** 应丢弃
// (与 Env 一致,约定 5),且不能留下悬空的 "--api-key"。
func TestUpdateConfigExtraArgsMaskWithoutStoredValue(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		ExtraArgs: []string{"--api-key", agent.MaskedValue},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	stored := storedExtraArgs(t, a, "claude-code")
	joined := strings.Join(stored, " ")
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("无原值的 *** 不应落库: %q", joined)
	}
	if strings.Contains(joined, "--api-key") {
		t.Errorf("无原值的 *** 应丢弃整个选项(不能留悬空选项名): %q", joined)
	}
}

// TestUpdateConfigExtraArgsNoCrossSlotLeak 关键不变式:凭据只能在
// **同名**选项之间流动 —— 绝不能出现"把 A 的凭据写到 B 的参数位"。
func TestUpdateConfigExtraArgsNoCrossSlotLeak(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		ExtraArgs: []string{"--api-key", "sk-secret-A", "--token", "sk-secret-B"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}
	// 用户删掉了 --api-key 那一对,只剩 --token ***(前端回传的掩码)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		ExtraArgs: []string{"--token", agent.MaskedValue},
	}); err != nil {
		t.Fatalf("更新: %v", err)
	}
	stored := storedExtraArgs(t, a, "claude-code")
	joined := strings.Join(stored, " ")
	if strings.Contains(joined, "sk-secret-A") {
		t.Errorf("A 的凭据被写到了别的参数位: %q", joined)
	}
	if !strings.Contains(joined, "sk-secret-B") {
		t.Errorf("B 的凭据应沿用已存同名选项的值: %q", joined)
	}
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("库里出现字面量 %s: %q", agent.MaskedValue, joined)
	}
}

// TestUpdateConfigExtraArgsDropsLegacyLiteralMask 库里存着旧版本往返
// bug 的产物("--api-key ***")时,下发到 CLI 的参数里不能有字面量 ***。
func TestUpdateConfigExtraArgsDropsLegacyLiteralMask(t *testing.T) {
	a := newTestApp(t)
	// 绕过 UpdateConfig 直接写库,模拟历史脏数据
	raw, err := json.Marshal(agent.AgentConfig{
		ExtraArgs: []string{"--verbose", "--api-key", agent.MaskedValue},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Repo.SaveAgentConfig("claude-code", raw); err != nil {
		t.Fatalf("写入脏数据: %v", err)
	}
	// 重新应用配置(启动路径):适配器拿到的参数里不能有字面量 ***
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		ExtraArgs: []string{"--verbose", "--api-key", agent.MaskedValue},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	stored := storedExtraArgs(t, a, "claude-code")
	joined := strings.Join(stored, " ")
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("字面量 %s 不得下发到 CLI: %q", agent.MaskedValue, joined)
	}
	if !strings.Contains(joined, "--verbose") {
		t.Errorf("普通参数应保留: %q", joined)
	}
}

// ---------------------------------------------------------------------------
// 迭代 10(P0-2):制品(artifacts)出口脱敏
//
// 节点明细有 maskNode 脱敏,制品此前**完全没有** —— git diff 原文既
// 落库又经 API 原样返回,同一个内容在两个出口口径不一致(约定 8)。
// ---------------------------------------------------------------------------

// TestArtifactsReadOutMasksCredentials 制品读出口(Get artifacts /
// Export)不得返回凭据明文;代码标识符形态必须原样保留。
func TestArtifactsReadOutMasksCredentials(t *testing.T) {
	a := newTestApp(t)
	wf := &model.Workflow{
		ID: "wf-art", Name: "Art", Enabled: true,
		Nodes: []model.Node{
			{ID: "plan", Type: model.NodeTypeAgent, Config: map[string]any{
				"agent": "mock-claude", "mode": "plan",
			}},
		},
	}
	if _, err := a.Workflows.Save(wf); err != nil {
		t.Fatalf("保存工作流: %v", err)
	}
	exec, err := a.Executions.Run(context.Background(), wf.ID, "t", nil)
	if err != nil {
		t.Fatalf("运行: %v", err)
	}
	waitExecutionState(t, a, exec.ID, model.ExecutionCompleted)

	// 直接写一条含凭据的制品(等价于 git diff 节点落库的产物)
	artifact := &model.Artifact{
		ID: "art-secret", ExecutionID: exec.ID, NodeID: "plan",
		Name: "git-diff", ContentType: "text/x-diff",
		Content: "diff --git a/.env b/.env\n" +
			"+ANTHROPIC_API_KEY=" + secretValue + "\n" +
			"+  const token = getToken();\n",
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}
	if err := a.Repo.SaveArtifact(artifact); err != nil {
		t.Fatalf("保存制品: %v", err)
	}

	// 读出口 1:/artifacts
	arts, err := a.Executions.Artifacts(exec.ID)
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	if len(arts) == 0 {
		t.Fatal("应至少有一条制品")
	}
	for _, art := range arts {
		if strings.Contains(art.Content, secretValue) {
			t.Errorf("制品读出口含凭据明文: %q", art.Content)
		}
		if !strings.Contains(art.Content, "ANTHROPIC_API_KEY=") {
			t.Errorf("键名应保留(可读性): %q", art.Content)
		}
		if !strings.Contains(art.Content, "const token = getToken();") {
			t.Errorf("代码标识符不应被破坏: %q", art.Content)
		}
	}

	// 读出口 2:/export
	data, err := a.Executions.Export(exec.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if strings.Contains(mustJSON(data), secretValue) {
		t.Errorf("导出内容含凭据明文: %s", mustJSON(data))
	}
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
