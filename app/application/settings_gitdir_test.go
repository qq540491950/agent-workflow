package application

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"Git 工作目录必须存在且是目录"这一契约(迭代 20 / P2-4)。
//
// 缺口:此前完全不校验 —— 用户在设置页填一个不存在的路径,界面提示
// "已保存",此后每个 git / script / agent 节点因 cmd.Dir 不存在而失败,
// 报错来自子进程、与设置页毫无关联(约定 14:该失败却成功)。
//
// 现在:保存前校验;非法目录 → 400,且**一个字段都不落库**
// (与 log_level 同一口径:不能出现"目录没改成、级别改成了"的部分成功)。

// errCodeOf 取出结构化错误码。
func errCodeOf(err error) string {
	var se *model.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// TestSettingsUpdateRejectsMissingGitWorkingDir 不存在的目录必须被拒绝,
// 且不得落库、不得改动其它设置。
func TestSettingsUpdateRejectsMissingGitWorkingDir(t *testing.T) {
	a := newTestApp(t)
	before := a.GitSvc.WorkingDir()
	missing := filepath.Join(t.TempDir(), "no-such-dir")

	_, err := a.Settings.Update(missing, "debug", nil)
	if err == nil {
		t.Fatal("不存在的 Git 工作目录必须被拒绝")
	}
	if code := errCodeOf(err); code != "INVALID_GIT_WORKING_DIR" {
		t.Errorf("错误码 = %q, want INVALID_GIT_WORKING_DIR (%v)", code, err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("错误信息必须写明是哪个路径: %v", err)
	}
	// 不得落库
	var stored string
	if err := a.Repo.GetSetting("git_working_dir", &stored); err == nil && stored != "" {
		t.Errorf("校验失败时工作目录不应落库: %q", stored)
	}
	// 不得改内存态
	if got := a.GitSvc.WorkingDir(); got != before {
		t.Errorf("校验失败时工作目录不应被改动: %q → %q", before, got)
	}
	// 不得部分生效:日志级别也不能改(同一口径:先全量校验)
	if a.LogLevelValue() == "debug" {
		t.Error("校验失败时日志级别不应被应用(部分成功被当成失败)")
	}
}

// TestSettingsUpdateRejectsFileAsGitWorkingDir 指向一个**文件**的路径
// 同样必须被拒绝(cmd.Dir 指向文件时子进程根本起不来)。
func TestSettingsUpdateRejectsFileAsGitWorkingDir(t *testing.T) {
	a := newTestApp(t)
	file := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("写文件: %v", err)
	}

	_, err := a.Settings.Update(file, "", nil)
	if err == nil {
		t.Fatal("指向文件的 Git 工作目录必须被拒绝")
	}
	if code := errCodeOf(err); code != "INVALID_GIT_WORKING_DIR" {
		t.Errorf("错误码 = %q, want INVALID_GIT_WORKING_DIR (%v)", code, err)
	}
}

// TestSettingsUpdateAcceptsExistingDir 合法的目录仍然要能保存
// (校验不能把正常路径堵死)。
func TestSettingsUpdateAcceptsExistingDir(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if _, err := a.Settings.Update(dir, "info", nil); err != nil {
		t.Fatalf("合法目录应被接受: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got != dir {
		t.Errorf("工作目录 = %q, want %q", got, dir)
	}
}

// TestSettingsUpdateEmptyDirStillClears 清空(回到默认目录)的语义不变 ——
// 空串不是"不存在的目录"。
func TestSettingsUpdateEmptyDirStillClears(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if _, err := a.Settings.Update(dir, "", nil); err != nil {
		t.Fatalf("设置目录: %v", err)
	}
	if _, err := a.Settings.Update("", "", nil); err != nil {
		t.Fatalf("清空目录应被接受: %v", err)
	}
	if got := a.GitSvc.WorkingDir(); got == dir {
		t.Errorf("空串必须清除工作目录,但仍是 %q", got)
	}
}
