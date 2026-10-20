package main

import (
	"testing"

	app "agentworkflow/app/application"
)

// 本文件锁住启动参数 --git-dir 的语义。
//
// 背景:此前 `_ = application.GitAPI.SetWorkingDir(opts.GitDir)` 把错误
// 整个丢掉。落库失败时启动参数静默失效,日志里没有任何痕迹,用户以为
// 参数被接受了 —— 而设置页回显的是另一个目录(显示与实际不一致)。
//
// 现在的语义:**持久化设置**。--git-dir 与设置页写的是同一个键
// (git_working_dir):
//   - 成功 → 落库 + 生效,设置页回显的就是实际在用的目录;
//   - 失败 → 回退到默认目录(保持已持久化的值),错误上抛并告警。

// TestApplyGitDirArgPersistsAndShowsInSettings 启动参数必须落库、生效,
// 且设置页回显的就是同一个目录(显示 == 实际)。
func TestApplyGitDirArgPersistsAndShowsInSettings(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })

	dir := t.TempDir()
	if err := applyGitDirArg(a, dir); err != nil {
		t.Fatalf("applyGitDirArg: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got != dir {
		t.Errorf("实际工作目录 = %q, want %q", got, dir)
	}
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err != nil {
		t.Fatalf("读取落库目录: %v", err)
	}
	if stored != dir {
		t.Errorf("落库目录 = %q, want %q", stored, dir)
	}
	if info := a.Settings.Info(); info.GitWorkingDir != dir {
		t.Errorf("设置页回显目录 = %q, want %q", info.GitWorkingDir, dir)
	}
}

// TestApplyGitDirArgFallsBackWhenPersistFails 落库失败必须报错并回退到
// 默认目录(不得出现"参数被接受但没生效"或"内存态被改了一半")。
func TestApplyGitDirArgFallsBackWhenPersistFails(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	before := a.GitSvc.WorkingDir()
	_ = a.Repo.Close() // 之后所有写入都会失败

	if err := applyGitDirArg(a, t.TempDir()); err == nil {
		t.Fatal("落库失败应返回错误(不得静默忽略)")
	}
	if got := a.GitSvc.WorkingDir(); got != before {
		t.Errorf("落库失败时应回退到默认目录: %q → %q", before, got)
	}
}

// TestApplyGitDirArgEmptyIsNoop 空目录参数不改动任何状态。
func TestApplyGitDirArgEmptyIsNoop(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })

	before := a.GitSvc.WorkingDir()
	if err := applyGitDirArg(a, ""); err != nil {
		t.Fatalf("空目录不应报错: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got != before {
		t.Errorf("空目录不应改动工作目录: %q → %q", before, got)
	}
}
