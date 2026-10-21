package application

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/logx"
	"agentworkflow/permission"
)

// 本文件锁住 `SettingsService.Set` 不得成为绕过校验的后门(迭代 19 / P3-4)。
//
// 背景:`Set(key string, value any)` 此前是"任意键写入设置表",而它经
// main.go 的 `wails.NewService(app.Settings)` **暴露在 Wails IPC 上**
// (前端绑定 frontend/bindings/agentworkflow/app/application/settingsservice.ts
// 里的 `Set(key, value)` 就是它),因此任何能触达 IPC 的调用方都能:
//   - 直写 agent_policies → 绕过 AgentService.permMu 的"读整表→改一项→
//     写整表"串行化 → 并发授权丢更新(并发保护失效);
//   - 直写 git_working_dir / log_level → 绕过 Update 里的存在性校验与
//     日志级别校验(迭代 20 刚加的校验被绕开)。
//
// 现在:Set 只接受**白名单键**(设置页那三个),并复用 Update 的同一套校验;
// 白名单之外的键一律拒绝。

// TestSettingsSetRejectsNonWhitelistedKey 白名单外的键必须被拒绝。
//
// agent_policies 是最危险的一个:它由 AgentService 在 permMu 下读改写,
// 绕过去直写会丢并发更新。
func TestSettingsSetRejectsNonWhitelistedKey(t *testing.T) {
	a := newTestApp(t)

	// 先用正规路径写一条策略,再用 Set 试图覆盖 —— 必须被拒绝且原值不变。
	if err := a.AgentSvc.SetPermission("mock-claude", policyWithGitCommit()); err != nil {
		t.Fatalf("准备策略: %v", err)
	}
	before := storedAgentPolicies(t, a)

	err := a.Settings.Set("agent_policies", map[string]any{
		"mock-claude": map[string]any{"git_push": true},
	})
	if err == nil {
		t.Fatal("Set 必须拒绝白名单外的键 agent_policies")
	}
	if after := storedAgentPolicies(t, a); after != before {
		t.Errorf("被拒绝的写入不得落库:之前 %q,之后 %q", before, after)
	}
}

// TestSettingsSetRejectsUnknownKey 未知键必须被拒绝(不得"先写了再说")。
func TestSettingsSetRejectsUnknownKey(t *testing.T) {
	a := newTestApp(t)

	for _, key := range []string{"", "unknown_key", "data_dir", "AGENT_POLICIES"} {
		if err := a.Settings.Set(key, "x"); err == nil {
			t.Errorf("Set 必须拒绝未知键 %q", key)
		}
	}
	// 未知键不得落库。
	var got string
	if err := a.Repo.GetSetting("unknown_key", &got); err == nil {
		t.Errorf("被拒绝的键不得落库,实际读到 %q", got)
	}
}

// TestSettingsSetValidatesGitWorkingDir Set 必须复用 Update 的存在性校验。
//
// 此前 Set 直写库:填一个不存在的路径 → "保存成功" → 此后每个 git /
// script / agent 节点因 cmd.Dir 不存在而失败,报错与设置页毫无关联。
func TestSettingsSetValidatesGitWorkingDir(t *testing.T) {
	a := newTestApp(t)
	before := a.GitSvc.WorkingDir()
	missing := filepath.Join(t.TempDir(), "not-exist")

	if err := a.Settings.Set("git_working_dir", missing); err == nil {
		t.Fatal("Set 必须拒绝不存在的 Git 工作目录")
	}
	if got := a.GitSvc.WorkingDir(); got != before {
		t.Errorf("校验失败时内存态工作目录不应被改动: %q → %q", before, got)
	}
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err == nil && stored != "" {
		t.Errorf("校验失败时不得落库,实际 %q", stored)
	}
}

// TestSettingsSetValidatesLogLevel Set 必须复用 Update 的级别校验。
//
// 此前 Set 能写进一个后端不认的级别:界面回显 bogus、实际跑 info
// (静默失败,约定 14)。
func TestSettingsSetValidatesLogLevel(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(func() { _ = logx.SetLevelChecked("info") })
	before := a.LogLevelValue()

	if err := a.Settings.Set("log_level", "bogus"); err == nil {
		t.Fatal("Set 必须拒绝非法日志级别")
	}
	if got := a.LogLevelValue(); got != before {
		t.Errorf("校验失败时内存级别不应被改动: %q → %q", before, got)
	}
	var stored string
	if err := a.Repo.GetSetting("log_level", &stored); err == nil && stored == "bogus" {
		t.Error("非法级别不得落库")
	}
}

// TestSettingsSetAcceptsWhitelistedKeys 白名单里的键必须都能写(且真的生效)。
//
// 白名单与处理逻辑若是两份,漂移方向是"键在白名单里却写不进去"或
// "键不在白名单里却能写进去" —— 两个方向都要被这条用例挡住。
func TestSettingsSetAcceptsWhitelistedKeys(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(func() { _ = logx.SetLevelChecked("info") })

	dir := t.TempDir()
	if err := a.Settings.Set("git_working_dir", dir); err != nil {
		t.Fatalf("Set git_working_dir: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got != dir {
		t.Errorf("写入后工作目录 = %q, want %q", got, dir)
	}
	var storedDir string
	if err := a.Repo.GetSetting("git_working_dir", &storedDir); err != nil {
		t.Fatalf("读取落库目录: %v", err)
	}
	if storedDir != dir {
		t.Errorf("落库目录 = %q, want %q", storedDir, dir)
	}

	if err := a.Settings.Set("log_level", "WARN"); err != nil {
		t.Fatalf("Set log_level: %v", err)
	}
	var storedLevel string
	if err := a.Repo.GetSetting("log_level", &storedLevel); err != nil {
		t.Fatalf("读取落库级别: %v", err)
	}
	if storedLevel != "warn" {
		t.Errorf("落库级别 = %q, want warn(落库归一为小写)", storedLevel)
	}
	if got := a.LogLevelValue(); got != "warn" {
		t.Errorf("内存级别 = %q, want warn", got)
	}

	if err := a.Settings.Set("allow_script_nodes", true); err != nil {
		t.Fatalf("Set allow_script_nodes: %v", err)
	}
	if !a.ScriptNodesAllowed() {
		t.Error("放行开关未生效")
	}
	var storedAllow bool
	if err := a.Repo.GetSetting("allow_script_nodes", &storedAllow); err != nil {
		t.Fatalf("读取落库开关: %v", err)
	}
	if !storedAllow {
		t.Error("放行开关未落库")
	}
}

// TestSettingsSetRejectsWrongValueType 值的类型必须被校验。
//
// value 是 any(Wails IPC 传进来的是任意 JSON 值):把字符串 "true" 写进
// allow_script_nodes,库里就躺着一行"读不出来"的值(解不进 bool)——
// 同一类"设置行损坏",而且是这次写入自己造的。
func TestSettingsSetRejectsWrongValueType(t *testing.T) {
	a := newTestApp(t)

	if err := a.Settings.Set("allow_script_nodes", "true"); err == nil {
		t.Error("Set 必须拒绝非布尔的 allow_script_nodes")
	}
	if err := a.Settings.Set("git_working_dir", 42); err == nil {
		t.Error("Set 必须拒绝非字符串的 git_working_dir")
	}
	if err := a.Settings.Set("log_level", 3); err == nil {
		t.Error("Set 必须拒绝非字符串的 log_level")
	}
	// 被拒绝的值不得落库。
	var storedAllow bool
	if err := a.Repo.GetSetting("allow_script_nodes", &storedAllow); err == nil {
		t.Errorf("被拒绝的值不得落库,实际读到 %v", storedAllow)
	}
}

// TestSettingsSetErrorMentionsKey 拒绝时的错误必须说清是哪个键。
func TestSettingsSetErrorMentionsKey(t *testing.T) {
	a := newTestApp(t)

	err := a.Settings.Set("agent_policies", map[string]any{})
	if err == nil {
		t.Fatal("Set 必须拒绝白名单外的键")
	}
	if !strings.Contains(err.Error(), "agent_policies") {
		t.Errorf("错误必须指明被拒绝的键,实际: %v", err)
	}
}

// TestWritableSettingKeysAllWritable 白名单里的键必须都真的能写。
//
// 白名单是一份清单,处理逻辑是另一处 switch:两者漂移时,漂移方向是
// "键在白名单里却写不进去"(用户点保存报错,却说不出为什么)。这条用例
// 让漂移立刻显形。
func TestWritableSettingKeysAllWritable(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(func() { _ = logx.SetLevelChecked("info") })

	for _, key := range writableSettingKeys() {
		var value any
		switch key {
		case "git_working_dir":
			value = t.TempDir()
		case "log_level":
			value = "info"
		case "allow_script_nodes":
			value = false
		default:
			t.Fatalf("白名单里出现了没有约定取值的键 %q(请同步更新本用例与实现)", key)
		}
		if err := a.Settings.Set(key, value); err != nil {
			t.Errorf("白名单里的键 %q 必须可写,实际报错: %v", key, err)
		}
	}
}

// policyWithGitCommit 造一条带 git 提交权限的策略(测试用)。
func policyWithGitCommit() permission.Policy {
	return permission.Policy{FilesystemRead: true, GitRead: true, GitCommit: true}
}

// storedAgentPolicies 读出 agent_policies 的原始 JSON(测试用)。
func storedAgentPolicies(t *testing.T, a *App) string {
	t.Helper()
	var policies map[string]map[string]any
	if err := a.Repo.GetSetting("agent_policies", &policies); err != nil {
		t.Fatalf("读取 agent_policies: %v", err)
	}
	raw, err := json.Marshal(policies)
	if err != nil {
		t.Fatalf("序列化 agent_policies: %v", err)
	}
	return string(raw)
}
