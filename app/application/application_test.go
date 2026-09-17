package application

import (
	"encoding/json"
	"strings"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/workflow/model"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	return a
}

// saveSampleWorkflow 保存一个最小可校验的工作流(测试用)。
func saveSampleWorkflow(t *testing.T, a *App, id string) *model.Workflow {
	t.Helper()
	wf, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: ` + id + `
  name: Sample
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
  - id: b
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: a
    to: b
`)
	if err != nil {
		t.Fatalf("save sample workflow: %v", err)
	}
	return wf
}

// 回归:环境变量更新时 "***" 表示保留原值、空串表示删除,
// 这是掩码展示(GetConfig 返回 ***)与安全保存的核心约定。
func TestUpdateConfigMaskMerge(t *testing.T) {
	a := newTestApp(t)
	svc := a.AgentSvc

	// 初次配置
	err := svc.UpdateConfig("claude-code", agent.AgentConfig{
		Model: "m1",
		Env:   map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-live-123", "KEEP": "v1"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// GET 返回掩码值
	got, err := svc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Env["ANTHROPIC_AUTH_TOKEN"] != agent.MaskedValue || got.Env["KEEP"] != agent.MaskedValue {
		t.Fatalf("GetConfig 必须返回掩码值: %v", got.Env)
	}

	// 前端回传掩码值:*** 保留原值,新增项生效
	err = svc.UpdateConfig("claude-code", agent.AgentConfig{
		Model: "m2",
		Env:   map[string]string{"ANTHROPIC_AUTH_TOKEN": agent.MaskedValue, "KEEP": agent.MaskedValue, "NEW": "v2"},
	})
	if err != nil {
		t.Fatalf("update 2: %v", err)
	}
	raw, err := a.Repo.GetAgentConfig("claude-code")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	var stored agent.AgentConfig
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Env["ANTHROPIC_AUTH_TOKEN"] != "sk-live-123" {
		t.Errorf("*** 未保留原值: %q", stored.Env["ANTHROPIC_AUTH_TOKEN"])
	}
	if stored.Env["KEEP"] != "v1" {
		t.Errorf("KEEP 被篡改: %q", stored.Env["KEEP"])
	}
	if stored.Env["NEW"] != "v2" {
		t.Errorf("新增项丢失: %v", stored.Env)
	}
	if stored.Model != "m2" {
		t.Errorf("model = %q, want m2", stored.Model)
	}

	// 空串表示删除
	err = svc.UpdateConfig("claude-code", agent.AgentConfig{
		Model: "m2",
		Env:   map[string]string{"ANTHROPIC_AUTH_TOKEN": "", "KEEP": agent.MaskedValue, "NEW": agent.MaskedValue},
	})
	if err != nil {
		t.Fatalf("update 3: %v", err)
	}
	// 注意:json.Unmarshal 到已有 struct 会合并非 nil map,必须用新变量
	var storedAfter agent.AgentConfig
	raw, _ = a.Repo.GetAgentConfig("claude-code")
	if err := json.Unmarshal(raw, &storedAfter); err != nil {
		t.Fatal(err)
	}
	if _, exists := storedAfter.Env["ANTHROPIC_AUTH_TOKEN"]; exists {
		t.Errorf("空串未删除项: %v", stored.Env)
	}
	if storedAfter.Env["NEW"] != "v2" {
		t.Errorf("*** 未保留新增项: %v", storedAfter.Env)
	}
}

// 掩码值在无已存值时(例如直接 POST 一个 ***)应被丢弃而非存入字面 "***"。
func TestUpdateConfigMaskWithoutStoredValue(t *testing.T) {
	a := newTestApp(t)
	err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Env: map[string]string{"GHOST": agent.MaskedValue},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, _ := a.Repo.GetAgentConfig("claude-code")
	var stored agent.AgentConfig
	_ = json.Unmarshal(raw, &stored)
	if _, exists := stored.Env["GHOST"]; exists {
		t.Errorf("无原值的 *** 不应落库: %v", stored.Env)
	}
}

func TestResetConfig(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{Model: "m", Env: map[string]string{"K": "v"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := a.AgentSvc.ResetConfig("mock-claude"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	cfg, err := a.AgentSvc.GetConfig("mock-claude")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.Model != "" || cfg.Env != nil {
		t.Errorf("reset 后应为零值: %+v", cfg)
	}
}

func TestExportRestoreBackup(t *testing.T) {
	a1 := newTestApp(t)
	saveSampleWorkflow(t, a1, "backup-sample")
	if err := a1.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Model: "m-backup",
		Env:   map[string]string{"TOK": "secret"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	backup, err := a1.Settings.ExportBackup()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	srcList, err := a1.Workflows.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(srcList) == 0 {
		t.Fatal("导出前应存在工作流")
	}

	// 恢复到全新实例
	a2 := newTestApp(t)
	result, err := a2.Settings.RestoreBackup(backup)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result["workflows_restored"].(int) == 0 {
		t.Error("未恢复任何工作流")
	}
	if result["agent_configs_restored"].(int) != 1 {
		t.Errorf("agent_configs_restored = %v, want 1", result["agent_configs_restored"])
	}

	// 恢复后配置语义正确(明文在备份中,GetConfig 掩码)
	raw, _ := a2.Repo.GetAgentConfig("claude-code")
	var cfg agent.AgentConfig
	_ = json.Unmarshal(raw, &cfg)
	if cfg.Env["TOK"] != "secret" || cfg.Model != "m-backup" {
		t.Errorf("恢复的配置不完整: %+v", cfg)
	}
	masked, _ := a2.AgentSvc.GetConfig("claude-code")
	if masked.Env["TOK"] != agent.MaskedValue {
		t.Errorf("恢复后 GetConfig 未掩码: %v", masked.Env)
	}

	// 重复恢复:同 ID 工作流跳过
	again, err := a2.Settings.RestoreBackup(backup)
	if err != nil {
		t.Fatalf("restore 2: %v", err)
	}
	if again["workflows_restored"].(int) != 0 || again["workflows_skipped"].(int) != result["workflows_restored"].(int) {
		t.Errorf("重复恢复应全部跳过: %+v (首次 %+v)", again, result)
	}
}

func TestWorkflowDuplicateAndSetEnabled(t *testing.T) {
	a := newTestApp(t)
	src := saveSampleWorkflow(t, a, "dup-sample")

	copyWf, err := a.Workflows.Duplicate(src.ID)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if copyWf.ID == src.ID {
		t.Fatal("副本 ID 不应相同")
	}
	if !strings.HasSuffix(copyWf.Name, "(副本)") {
		t.Errorf("副本名称 = %q", copyWf.Name)
	}

	got, err := a.Workflows.SetEnabled(src.ID, false)
	if err != nil {
		t.Fatalf("setEnabled: %v", err)
	}
	if got.Enabled {
		t.Error("SetEnabled(false) 未生效")
	}
	again, _ := a.Workflows.Get(src.ID)
	if again.Enabled {
		t.Error("持久化的 Enabled 未更新")
	}
}

func TestImportYAML(t *testing.T) {
	a := newTestApp(t)
	yml := `
version: "1"
workflow:
  id: import-test
  name: Imported
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
  - id: b
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: a
    to: b
`
	wf, err := a.Workflows.ImportYAML(yml)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if wf.ID != "import-test" {
		t.Errorf("id = %q", wf.ID)
	}

	// 再导入一次:ID 冲突自动分配新 ID
	wf2, err := a.Workflows.ImportYAML(yml)
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if wf2.ID == "import-test" {
		t.Error("冲突 ID 未重新分配")
	}

	// 非法 YAML 报错且不落库
	if _, err := a.Workflows.ImportYAML("::: not yaml"); err == nil {
		t.Error("非法 YAML 应报错")
	}
}

func TestSkillEnableDisable(t *testing.T) {
	a := newTestApp(t)
	if err := a.SkillSvc.SetEnabled("log", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !a.Engine.IsSkillDisabled("log") {
		t.Error("禁用未生效")
	}
	if err := a.SkillSvc.SetEnabled("log", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if a.Engine.IsSkillDisabled("log") {
		t.Error("启用未生效")
	}
}
