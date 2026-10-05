package application

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"agentworkflow/agent"
	"agentworkflow/permission"
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

// script 节点默认不放行,只有显式开启才允许执行(任意本地命令执行的门禁)。
func TestScriptNodesGateDefaultsOff(t *testing.T) {
	a := newTestApp(t)
	if a.ScriptNodesAllowed() {
		t.Error("新应用默认应关闭 script 节点")
	}
	if a.Engine.AllowScriptNodes == nil {
		t.Fatal("引擎未注入 AllowScriptNodes 钩子")
	}
	if a.Engine.AllowScriptNodes() {
		t.Error("引擎门禁应报告未放行")
	}

	// 显式放行:即时生效 + 落库
	allow := true
	info, err := a.Settings.Update("", "", &allow)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !info.AllowScriptNodes || !a.ScriptNodesAllowed() || !a.Engine.AllowScriptNodes() {
		t.Errorf("放行未生效: info=%+v allowed=%v", info.AllowScriptNodes, a.ScriptNodesAllowed())
	}

	// 缺省(nil)不修改既有值
	if _, err := a.Settings.Update("", "", nil); err != nil {
		t.Fatalf("update nil: %v", err)
	}
	if !a.ScriptNodesAllowed() {
		t.Error("nil 应表示不修改,放行状态被意外重置")
	}

	// 关闭后重新打开应用应保持关闭
	deny := false
	if _, err := a.Settings.Update("", "", &deny); err != nil {
		t.Fatalf("update deny: %v", err)
	}
	reopened, err := NewApp(a.DataDir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Repo.Close() })
	if reopened.ScriptNodesAllowed() {
		t.Error("重启后应恢复为关闭")
	}
}

// 嵌套工作流集成测试:父工作流的 subworkflow 节点同步等待子工作流完成。
func TestSubworkflowEndToEnd(t *testing.T) {
	a := newTestApp(t)
	// 子工作流:mock plan → log
	if _, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: child-wf
  name: Child
nodes:
  - id: plan
    type: agent
    agent: mock-claude
    mode: plan
  - id: log
    type: skill
    skill: log
    args:
      message: child done
edges:
  - from: plan
    to: log
`); err != nil {
		t.Fatalf("import child: %v", err)
	}
	// 父工作流:subworkflow → log
	if _, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: parent-wf
  name: Parent
nodes:
  - id: sub
    type: subworkflow
    workflow_id: child-wf
  - id: after
    type: skill
    skill: log
    args:
      message: parent done
edges:
  - from: sub
    to: after
`); err != nil {
		t.Fatalf("import parent: %v", err)
	}

	parent, err := a.Workflows.Get("parent-wf")
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	exec, err := a.Engine.Start(context.Background(), parent, "嵌套测试", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// 等待父执行完成(子工作流为阻塞执行)
	deadline := time.Now().Add(15 * time.Second)
	var final *model.Execution
	for time.Now().Before(deadline) {
		e, err := a.Repo.GetExecution(exec.ID)
		if err == nil && (e.State == model.ExecutionCompleted || e.State == model.ExecutionFailed) {
			final = e
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if final == nil {
		t.Fatal("父执行未在期限内结束")
	}
	if final.State != model.ExecutionCompleted {
		t.Fatalf("父执行状态 = %s (error=%s)", final.State, final.Error)
	}

	// 子执行已落库且完成
	children, err := a.Repo.ListExecutions("child-wf", 10)
	if err != nil || len(children) != 1 {
		t.Fatalf("子执行记录 = %d, %v; want 1", len(children), err)
	}
	if children[0].State != model.ExecutionCompleted {
		t.Errorf("子执行状态 = %s, want COMPLETED", children[0].State)
	}
}

// 引用不存在的子工作流:父执行必须明确失败(而非静默成功)。
func TestSubworkflowMissingChild(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: orphan-parent
  name: Orphan Parent
nodes:
  - id: sub
    type: subworkflow
    workflow_id: no-such-child
edges: []
`); err != nil {
		t.Fatalf("import: %v", err)
	}
	parent, _ := a.Workflows.Get("orphan-parent")
	exec, err := a.Engine.Start(context.Background(), parent, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		e, err := a.Repo.GetExecution(exec.ID)
		if err == nil && e.State == model.ExecutionFailed {
			if !strings.Contains(e.Error, "不存在") {
				t.Errorf("error = %q, want 提示子工作流不存在", e.Error)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("父执行未按预期失败")
}

// 回归:取消父执行时,阻塞在子工作流上的父 goroutine 曾不感知取消
// (轮询最长阻塞 10 分钟)且子执行继续运行;现应级联取消并快速返回。
func TestSubworkflowCancelPropagation(t *testing.T) {
	a := newTestApp(t)
	// 子工作流:以人工节点开头(必然 WAITING_USER 长挂)
	if _, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: slow-child
  name: Slow Child
nodes:
  - id: gate
    type: human
    prompt: "等待中"
    responses: ["approve"]
edges: []
`); err != nil {
		t.Fatalf("import child: %v", err)
	}
	if _, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: cancel-parent
  name: Cancel Parent
nodes:
  - id: sub
    type: subworkflow
    workflow_id: slow-child
edges: []
`); err != nil {
		t.Fatalf("import parent: %v", err)
	}
	parent, _ := a.Workflows.Get("cancel-parent")
	exec, err := a.Engine.Start(context.Background(), parent, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// 等子执行 WAITING_USER(说明父已阻塞在子工作流上)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		kids, _ := a.Repo.ListExecutions("slow-child", 10)
		if len(kids) > 0 && kids[0].State == model.ExecutionWaitingUser {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 取消父执行
	if err := a.Engine.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel parent: %v", err)
	}

	// 子执行应被级联取消
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		kids, _ := a.Repo.ListExecutions("slow-child", 10)
		if len(kids) > 0 && kids[0].State == model.ExecutionCancelled {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("子工作流未被级联取消")
}

// 回归:设置持久化失败此前被忽略 —— 内存已改、界面提示"已保存",重启即丢。
// 现必须上抛,且不改动内存态。
func TestSettingsUpdatePropagatesPersistError(t *testing.T) {
	a := newTestApp(t)
	// 先造一个可观测的旧值
	if err := a.Settings.Set("git_working_dir", "/tmp/before"); err != nil {
		t.Fatal(err)
	}
	before := a.GitSvc.WorkingDir
	levelBefore := a.LogLevel
	_ = a.Repo.Close() // 之后所有写入都会失败

	if _, err := a.Settings.Update("/tmp/after", "", nil); err == nil {
		t.Error("git_working_dir 持久化失败应上抛")
	}
	if a.GitSvc.WorkingDir != before {
		t.Errorf("内存态不应在落库失败后被改动: %q → %q", before, a.GitSvc.WorkingDir)
	}
	if _, err := a.Settings.Update("", "debug", nil); err == nil {
		t.Error("log_level 持久化失败应上抛")
	}
	if a.LogLevel != levelBefore {
		t.Errorf("日志级别不应在落库失败后被改动: %q → %q", levelBefore, a.LogLevel)
	}
	allow := true
	if _, err := a.Settings.Update("", "", &allow); err == nil {
		t.Error("allow_script_nodes 持久化失败应上抛")
	}
	if a.ScriptNodesAllowed() {
		t.Error("放行开关不应在落库失败后被打开")
	}
}

// 回归:SetPermission 读取 agent_policies 失败时,此前吞掉错误后重建空 map,
// 整表写会把其他 Agent 已配置的权限策略全部清空。现读失败必须上抛。
func TestSetPermissionPropagatesReadError(t *testing.T) {
	a := newTestApp(t)
	// 先给两个 Agent 配好权限
	seedPolicies := map[string]permission.Policy{
		"claude-code": {FilesystemRead: true, GitRead: true},
		"pi-agent":    {FilesystemRead: true, FilesystemWrite: true, GitRead: true, GitCommit: true},
	}
	for _, id := range []string{"claude-code", "pi-agent"} {
		if err := a.AgentSvc.SetPermission(id, seedPolicies[id]); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	var policies map[string]map[string]any
	if err := a.Repo.GetSetting("agent_policies", &policies); err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("种子策略 = %v", policies)
	}

	_ = a.Repo.Close() // 读失败
	if err := a.AgentSvc.SetPermission("mock-claude", permission.Policy{FilesystemRead: true}); err == nil {
		t.Error("读失败时 SetPermission 应上抛,否则会清空其他 Agent 的权限策略")
	}
}

// TestSkillDisabledConcurrentAccess 验证 DisabledSkills 的并发安全:
// 执行协程经 IsSkillDisabled 读(工作流 skill 节点路径),Skills 页开关
// 经 SetEnabled 写。此前是裸 map,同进程内并发读写在生产环境是
// "concurrent map read and map write" runtime fatal(recover 无效,
// 整应用退出),-race 下是 DATA RACE。
func TestSkillDisabledConcurrentAccess(t *testing.T) {
	a := newTestApp(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者:模拟执行协程持续查询禁用状态
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = a.Engine.IsSkillDisabled("log")
					_ = a.SkillDisabled("submit")
					_ = a.SkillSvc.List()
				}
			}
		}()
	}
	// 写者:模拟 REST 开关反复切换
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := a.SkillSvc.SetEnabled("log", i%2 == 0); err != nil {
				t.Errorf("SetEnabled: %v", err)
				return
			}
		}
	}()

	// 等写者结束再停读者
	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestSkillSetEnabledPersistsBeforeMemory 验证 SetEnabled 先落库:
// 持久化失败时不留下"内存已禁用但库中未禁用"的分裂状态。
func TestSkillSetEnabledPersistsBeforeMemory(t *testing.T) {
	a := newTestApp(t)
	_ = a.Repo.Close() // 之后所有写入都会失败

	if err := a.SkillSvc.SetEnabled("log", false); err == nil {
		t.Fatal("持久化失败应上抛")
	}
	if a.SkillDisabled("log") {
		t.Error("落库失败时内存态不应改变(重启即失效的假成功)")
	}
}

// TestLogLevelConcurrentAccess 验证 Settings.Info 与 Settings.Update
// 并发时 LogLevel 无数据竞争(此前是无锁裸 string 字段)。
func TestLogLevelConcurrentAccess(t *testing.T) {
	a := newTestApp(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = a.Settings.Info()
					_ = a.LogLevelValue()
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		levels := []string{"debug", "info", "warn", "error"}
		for i := 0; i < 20; i++ {
			_, _ = a.Settings.Update("", levels[i%len(levels)], nil)
		}
	}()
	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}
