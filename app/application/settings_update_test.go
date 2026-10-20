package application

import (
	"testing"
)

// 本文件锁住 Settings.Update 的"先全量校验、后统一落库/应用"顺序。
//
// 背景:此前 Update 先把 git_working_dir 落库并生效,之后才校验 log_level。
// 传"非法日志级别 + 合法工作目录"时:接口返回 400,但**目录已经改了**、
// 也已经落库 —— 用户看到"保存失败",实际有一半设置生效了
// (约定 14:"部分成功被当成失败"同族)。
//
// 现在:所有校验都在任何写入之前完成;校验不过则一个字段都不落库、
// 内存态一个都不改。

// TestSettingsUpdateInvalidLogLevelLeavesWorkingDirUntouched 非法日志级别
// 不得让工作目录被改动或落库。
func TestSettingsUpdateInvalidLogLevelLeavesWorkingDirUntouched(t *testing.T) {
	a := newTestApp(t)
	before := a.GitSvc.WorkingDir()
	newDir := t.TempDir()

	if _, err := a.Settings.Update(newDir, "trace", nil); err == nil {
		t.Fatal("非法日志级别应被拒绝")
	}
	if got := a.GitSvc.WorkingDir(); got != before {
		t.Errorf("校验失败时工作目录不应被改动: %q → %q", before, got)
	}
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err == nil && stored != "" {
		t.Errorf("校验失败时工作目录不应落库: %q", stored)
	}
}

// TestSettingsUpdateInvalidLogLevelKeepsAllowScriptNodesUnchanged 非法日志级别
// 不得让放行开关被打开(同样属于"部分生效")。
func TestSettingsUpdateInvalidLogLevelKeepsAllowScriptNodesUnchanged(t *testing.T) {
	a := newTestApp(t)
	if a.ScriptNodesAllowed() {
		t.Fatal("前置条件:放行开关默认应为关闭")
	}
	allow := true
	if _, err := a.Settings.Update("", "trace", &allow); err == nil {
		t.Fatal("非法日志级别应被拒绝")
	}
	if a.ScriptNodesAllowed() {
		t.Error("校验失败时放行开关不应被打开")
	}
}

// TestSettingsUpdateValidValuesAllApplied 合法输入仍要全部落库并生效
// (修顺序不能把正常路径堵死)。
func TestSettingsUpdateValidValuesAllApplied(t *testing.T) {
	a := newTestApp(t)
	newDir := t.TempDir()
	allow := true
	if _, err := a.Settings.Update(newDir, "debug", &allow); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got != newDir {
		t.Errorf("工作目录 = %q, want %q", got, newDir)
	}
	if !a.ScriptNodesAllowed() {
		t.Error("放行开关未生效")
	}
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err != nil {
		t.Fatalf("读取落库目录: %v", err)
	}
	if stored != newDir {
		t.Errorf("落库目录 = %q, want %q", stored, newDir)
	}
}
