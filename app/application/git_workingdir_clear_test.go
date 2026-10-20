package application

import (
	"testing"
)

// 本文件锁住"设置页把 git_working_dir 清空 = 清除并走默认目录"。
//
// 背景:此前 Settings.Update 里 `if gitWorkingDir != ""` 把空串当作
// "不修改" —— 用户在设置页清空输入框点保存,后端什么都不做,但界面提示
// "已保存";清空后显示与实际不一致(约定 14 的同类问题:"该失败却成功"
// 的镜像 —— "该生效却没生效")。
//
// 现在:空串 = 清除(落库空串 + Git 服务回到启动默认目录)。
// 注意前端每次保存都回传当前值(Settings.tsx 的 save),所以空串只在
// 用户主动清空时出现。
//
// **冲突提示**:另一子代理正在并发修改 app/application/application.go
// 的同一函数(Settings.Update 的校验顺序)。本用例是本项的验收标准,
// 合并冲突时请保留"空串=清除"分支。

// TestSettingsUpdateClearsGitWorkingDir 空串必须清除已存目录。
func TestSettingsUpdateClearsGitWorkingDir(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if _, err := a.Settings.Update(dir, "", nil); err != nil {
		t.Fatalf("设置目录: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got != dir {
		t.Fatalf("工作目录 = %q, want %q", got, dir)
	}

	// 用户清空输入框
	if _, err := a.Settings.Update("", "", nil); err != nil {
		t.Fatalf("清空目录: %v", err)
	}
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err != nil {
		t.Fatalf("读取落库目录: %v", err)
	}
	if stored != "" {
		t.Errorf("落库目录应被清空: %q", stored)
	}
	// 内存态回到启动默认目录(而不是置空:cmd.Dir="" 会让子进程落在
	// 不确定的当前目录里)
	if got := a.GitSvc.WorkingDir(); got != a.defaultGitDir {
		t.Errorf("清空后应回到默认目录: %q, want %q", got, a.defaultGitDir)
	}
	// 设置页回显:走默认目录(与实际一致)
	if got := a.Settings.Info().GitWorkingDir; got != a.defaultGitDir {
		t.Errorf("设置页回显应与实际一致: %q, want %q", got, a.defaultGitDir)
	}
}

// TestSettingsUpdateKeepsGitWorkingDir 非空值照常生效(不能被"清空"
// 的改动堵死正常路径)。
func TestSettingsUpdateKeepsGitWorkingDir(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if _, err := a.Settings.Update(dir, "", nil); err != nil {
		t.Fatalf("设置目录: %v", err)
	}
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err != nil {
		t.Fatalf("读取落库目录: %v", err)
	}
	if stored != dir {
		t.Errorf("落库目录 = %q, want %q", stored, dir)
	}
	if got := a.GitSvc.WorkingDir(); got != dir {
		t.Errorf("工作目录 = %q, want %q", got, dir)
	}
}
