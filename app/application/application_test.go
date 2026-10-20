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
	// ImportYAML 现在返回 *WorkflowResult(携带被忽略的键与校验警告);
	// 这里只要工作流本体 —— 断言未改动。
	return wf.Workflow
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
	before := a.GitSvc.WorkingDir()
	// 走访问器而非直读字段:logLevel 现在不导出(并发保护),直读会绕过锁
	levelBefore := a.LogLevelValue()
	_ = a.Repo.Close() // 之后所有写入都会失败

	if _, err := a.Settings.Update("/tmp/after", "", nil); err == nil {
		t.Error("git_working_dir 持久化失败应上抛")
	}
	if a.GitSvc.WorkingDir() != before {
		t.Errorf("内存态不应在落库失败后被改动: %q → %q", before, a.GitSvc.WorkingDir())
	}
	if _, err := a.Settings.Update("", "debug", nil); err == nil {
		t.Error("log_level 持久化失败应上抛")
	}
	if a.LogLevelValue() != levelBefore {
		t.Errorf("日志级别不应在落库失败后被改动: %q → %q", levelBefore, a.LogLevelValue())
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
//
// **读写窗口必须确定**(#208):旧写法是"写者跑固定次数 + time.Sleep(150ms)
// 建窗口"—— 写者(每次落库)可能几十毫秒就跑完,读者与写者**零重叠**,
// -race 什么也报不出(恒真的假保护)。
// 现在用**屏障**:读者先就位并持续自旋,写者才开始写 —— 读写必然重叠。
func TestSkillDisabledConcurrentAccess(t *testing.T) {
	a := newTestApp(t)
	var wg sync.WaitGroup
	var ready sync.WaitGroup
	stop := make(chan struct{})

	// 读者:模拟执行协程持续查询禁用状态
	for i := 0; i < 4; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done() // 就位信号:屏障 —— 写者开始写时读者已在自旋
			for {
				select {
				case <-stop:
					return
				default:
					_ = a.Engine.IsSkillDisabled("log")
					_ = a.SkillDisabled("submit")
				}
			}
		}()
	}
	ready.Wait()

	// 写者:模拟 REST 开关反复切换
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop) // 写者结束(含提前返回)→ 读者退出
		for i := 0; i < 50; i++ {
			if err := a.SkillSvc.SetEnabled("log", i%2 == 0); err != nil {
				t.Errorf("SetEnabled: %v", err)
				return
			}
		}
	}()

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

// logLevelRaceIterations 是 TestLogLevelConcurrentAccess 的写次数。
//
// 依据(实测:-race 下**去掉** logLevelMu 的读写锁,同一份代码跑 5 次统计
// "DATA RACE" 命中数):
//
//	走 Settings.Update(含一次 SQLite 写)20 次  → 0/5 命中(假绿!)
//	走 Settings.Update 200 次                 → 5/5 命中
//	走 setLogLevel(真正的争用点)20 次         → 5/5 命中
//
// 迭代数太小 = 恒真的假保护(去掉锁仍全绿,比没有测试更危险);太大又拖慢
// 门禁。这里取 3000:比"能稳定命中的最小值"高一个数量级以上的余量,
// 而写循环本身只占 ~1ms(混合写 0.15s)。
//
// **窗口形态同样重要**(#208):迭代数只保证"命中概率",**屏障**才保证
// "读写必然重叠" —— 两者缺一,探针都可能恒真。
const logLevelRaceIterations = 3000

// TestLogLevelConcurrentAccess 验证 Settings.Info 与 Settings.Update
// 并发时 LogLevel 无数据竞争(此前是无锁裸 string 字段)。
//
// 写侧分两路:
//
//  1. setLogLevel:Settings.Update 真正写 a.logLevel 的地方(真正的争用
//     点),迭代 logLevelRaceIterations 次;
//  2. Settings.Update:公开路径(落库 + 应用日志级别 + 写内存态),每 100
//     次迭代来一次 —— 端到端语义仍然被覆盖。
//
// **读写窗口必须确定**(#208):旧写法靠 time.Sleep(150ms) 建窗口 ——
// 写者早已跑完时读写**零重叠**,-race 什么也报不出(恒真的假保护)。
// 现在用**屏障**:读者先就位并持续自旋,写者才开始写 —— 读写必然重叠,
// 窗口 = 写者的循环时长(不再由 sleep 撞运气)。
//
// 读侧 4 个 goroutine 持续读(Settings.Info + LogLevelValue)。
func TestLogLevelConcurrentAccess(t *testing.T) {
	a := newTestApp(t)
	var wg sync.WaitGroup
	var ready sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done() // 就位信号:屏障 —— 写者开始写时读者已在自旋
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
	ready.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop) // 写者结束 → 读者退出
		levels := []string{"debug", "info", "warn", "error"}
		for i := 0; i < logLevelRaceIterations; i++ {
			level := levels[i%len(levels)]
			a.setLogLevel(level)
			if i%100 == 0 {
				_, _ = a.Settings.Update("", level, nil)
			}
		}
	}()
	wg.Wait()
}

// TestRestoreBackupRejectsInvalidWorkflow 验证备份恢复也过校验。
// 备份是不可信输入(可来自他人分享的 JSON),此前直接 Repo.SaveWorkflow
// 绕过 WorkflowService.Save 与 Engine.Validate,悬空边/空图这类非法
// 定义会落库、在列表里正常可见,直到运行时才炸 —— 与 ImportYAML 的
// 严格度不一致。恢复时必须跳过并如实计数。
func TestRestoreBackupRejectsInvalidWorkflow(t *testing.T) {
	a := newTestApp(t)
	res, err := a.Settings.RestoreBackup(map[string]any{
		"workflows": []any{
			// 合法
			map[string]any{
				"id": "ok-wf", "name": "OK", "enabled": true,
				"nodes": []any{
					map[string]any{"id": "a", "name": "A", "type": "agent",
						"config": map[string]any{"agent": "mock-claude", "mode": "plan"}},
					map[string]any{"id": "b", "name": "B", "type": "agent",
						"config": map[string]any{"agent": "mock-claude", "mode": "review"}},
				},
				"edges": []any{map[string]any{"from": "a", "to": "b"}},
			},
			// 非法:边指向不存在的节点
			map[string]any{
				"id": "dangling-wf", "name": "Bad", "enabled": true,
				"nodes": []any{map[string]any{"id": "a", "name": "A", "type": "agent",
					"config": map[string]any{"agent": "mock-claude", "mode": "plan"}}},
				"edges": []any{map[string]any{"from": "a", "to": "ghost"}},
			},
			// 非法:无节点
			map[string]any{"id": "empty-wf", "name": "Empty", "nodes": []any{}, "edges": []any{}},
		},
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := res["workflows_restored"].(int); got != 1 {
		t.Errorf("合法工作流应恢复 1 个,实际 %d (%v)", got, res)
	}
	if got := res["workflows_invalid"].(int); got != 2 {
		t.Errorf("非法工作流应被拒 2 个,实际 %d (%v)", got, res)
	}
	// 非法定义不得进库
	if _, err := a.Repo.GetWorkflow("dangling-wf"); err == nil {
		t.Error("悬空边的工作流不应落库")
	}
	if _, err := a.Repo.GetWorkflow("empty-wf"); err == nil {
		t.Error("空工作流不应落库")
	}
	ids, _ := res["workflows_invalid_ids"].([]string)
	if len(ids) != 2 {
		t.Errorf("应回报非法工作流 ID 便于用户定位,实际 %v", res["workflows_invalid_ids"])
	}
}

// TestRestoreBackupRejectsBrokenAgentConfig 验证解不出结构的 Agent 配置
// 不落库(此前无条件写入,却跳过应用,形成"库里有、运行不生效"的分裂态)。
func TestRestoreBackupRejectsBrokenAgentConfig(t *testing.T) {
	a := newTestApp(t)
	res, err := a.Settings.RestoreBackup(map[string]any{
		"workflows": []any{},
		"agent_configs": map[string]any{
			// timeout_seconds 是 int,给字符串 → 结构非法
			"claude-code": map[string]any{"timeout_seconds": "三百秒"},
		},
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := res["agent_configs_invalid"].(int); got != 1 {
		t.Errorf("结构非法的配置应计 1,实际 %d (%v)", got, res)
	}
	if got := res["agent_configs_restored"].(int); got != 0 {
		t.Errorf("非法配置不应计入已恢复,实际 %d", got)
	}
	raw, err := a.Repo.GetAgentConfig("claude-code")
	if err == nil && raw != nil {
		t.Error("非法配置不应落库")
	}
}

// 回归:Git 工作目录必须先落库再改内存。
// GitService.SetWorkingDir 此前反过来(先改内存再落库):落库失败时
// 接口返回错误,但内存态已经被改掉 —— 界面提示"失败",Git 面板却已经
// 指向新目录;重启后又回到旧目录。与 SettingsService.Update 的
// "先落库"约定不一致。
func TestGitSetWorkingDirPersistsBeforeMemory(t *testing.T) {
	a := newTestApp(t)
	before := a.GitSvc.WorkingDir()
	_ = a.Repo.Close() // 之后所有写入都会失败

	if err := a.GitAPI.SetWorkingDir("/tmp/should-not-apply"); err == nil {
		t.Fatal("持久化失败应上抛")
	}
	if a.GitSvc.WorkingDir() != before {
		t.Errorf("落库失败时内存态不应改变: %q → %q", before, a.GitSvc.WorkingDir())
	}
}

// 回归:SetPermission 是"读整表 → 改一项 → 写整表"的读-改-写。
// 并发调用(多 Agent 分别授权)会互相覆盖:后写者拿着旧快照写回,
// 先写者的策略被整条抹掉 —— 用户看到"某个 Agent 的权限莫名恢复默认"。
//
// 检测方式:每次写入后回读全表,若某个"曾经确认存在"的键消失,
// 即为丢更新(只看最终状态会漏检:丢更新是瞬态的,后续写入会补回)。
func TestSetPermissionConcurrentNoLostUpdate(t *testing.T) {
	a := newTestApp(t)
	ids := []string{"claude-code", "pi-agent", "mock-claude", "mock-pi"}

	var mu sync.Mutex
	written := map[string]bool{} // 已完成写入的键
	present := map[string]bool{} // 回读时确认存在过的键
	lost := []string{}           // 曾经存在、随后消失的键

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if err := a.AgentSvc.SetPermission(id, permission.Policy{
					FilesystemRead: true,
					GitRead:        i%2 == 0,
				}); err != nil {
					t.Errorf("SetPermission(%s): %v", id, err)
					return
				}
				var policies map[string]map[string]any
				if err := a.Repo.GetSetting("agent_policies", &policies); err != nil {
					t.Errorf("读取权限表失败: %v", err)
					return
				}
				mu.Lock()
				written[id] = true
				for k := range written {
					if _, ok := policies[k]; ok {
						present[k] = true
					} else if present[k] {
						lost = append(lost, k)
					}
				}
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	if len(lost) > 0 {
		t.Errorf("并发写入丢失了权限策略: %v", lost)
	}
	var policies map[string]map[string]any
	if err := a.Repo.GetSetting("agent_policies", &policies); err != nil {
		t.Fatalf("读取权限表失败: %v", err)
	}
	for _, id := range ids {
		if _, ok := policies[id]; !ok {
			t.Errorf("最终权限表缺少 %s: %v", id, policies)
		}
	}
}

// 回归:备份里出现本机不存在的 Agent 时,配置既无法生效(applyAgentConfig
// 对未知 ID 静默跳过)也不该被计入"已恢复" —— 此前计数为已恢复,
// 用户以为凭据恢复了,实际库里躺着一行永远不会被读取的配置。
func TestRestoreBackupSkipsUnknownAgent(t *testing.T) {
	a := newTestApp(t)

	backup := map[string]any{
		"version":   1,
		"workflows": []any{},
		"agent_configs": map[string]any{
			"claude-code": map[string]any{"model": "m", "env": map[string]any{"TOK": "sk"}},
			"ghost-agent": map[string]any{"model": "m2"},
		},
	}
	res, err := a.Settings.RestoreBackup(backup)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n, _ := res["agent_configs_restored"].(int); n != 1 {
		t.Errorf("agent_configs_restored = %v,期望 1", res["agent_configs_restored"])
	}
	if n, _ := res["agent_configs_invalid"].(int); n != 1 {
		t.Errorf("agent_configs_invalid = %v,期望 1(未知 Agent 不该算已恢复)", res["agent_configs_invalid"])
	}
	raw, err := a.Repo.GetAgentConfig("ghost-agent")
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if raw != nil {
		t.Errorf("未知 Agent 的配置不应落库: %s", raw)
	}
	// 已知 Agent 的配置仍然落库且生效
	cfg, err := a.AgentSvc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if cfg.Env["TOK"] != agent.MaskedValue {
		t.Errorf("已知 Agent 的配置未恢复: %v", cfg.Env)
	}
}

// 回归:节点明细的 output/error 直接来自 Agent 的 stdout/stderr,
// 常含 ANTHROPIC_API_KEY=sk-... 这类凭据。事件流在总线出口已脱敏,
// 但节点明细是直写 SQLite、原样返回 —— API 读出口必须再脱敏一次。
func TestExecutionNodesOutputIsMasked(t *testing.T) {
	a := newTestApp(t)

	const secret = "ANTHROPIC_API_KEY=sk-live-should-not-leak"
	exec := &model.Execution{ID: "exec-mask", WorkflowID: "wf-x", State: model.ExecutionCompleted}
	if err := a.Repo.SaveExecution(exec); err != nil {
		t.Fatalf("save execution: %v", err)
	}
	if err := a.Repo.SaveExecutionNode(&model.ExecutionNode{
		ID:          "exec-mask:n1:1",
		ExecutionID: exec.ID,
		NodeID:      "n1",
		NodeType:    model.NodeTypeAgent,
		State:       model.NodeSuccess,
		Output:      "cli stderr: " + secret,
		Error:       "failed: " + secret,
	}); err != nil {
		t.Fatalf("save node: %v", err)
	}

	nodes, err := a.Executions.Nodes(exec.ID)
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d 条,期望 1", len(nodes))
	}
	if strings.Contains(nodes[0].Output, "sk-live-should-not-leak") {
		t.Errorf("节点输出泄漏明文凭据: %q", nodes[0].Output)
	}
	if strings.Contains(nodes[0].Error, "sk-live-should-not-leak") {
		t.Errorf("节点错误泄漏明文凭据: %q", nodes[0].Error)
	}
	// 非敏感的正常输出不受影响
	if !strings.Contains(nodes[0].Output, "cli stderr:") {
		t.Errorf("正常输出被误伤: %q", nodes[0].Output)
	}
}
