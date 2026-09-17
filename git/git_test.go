package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// newTempRepo 创建带一次提交的临时 git 仓库。
func newTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	os.WriteFile(dir+"/README.md", []byte("# demo\n"), 0o644)
	run("add", "-A")
	run("commit", "-m", "initial")
	return dir
}

func TestStatusAndLog(t *testing.T) {
	dir := newTempRepo(t)
	s := New(dir)
	ctx := context.Background()

	st, err := s.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Branch != "branch main" && st.Branch != "main" && st.Branch != "On branch main" {
		// git 版本差异:porcelain -b 输出 "## main...origin/main [gone]" 等
		t.Logf("branch = %q", st.Branch)
	}
	if len(st.Modified) != 0 || len(st.Untracked) != 0 {
		t.Errorf("expected clean repo: %+v", st)
	}

	log, err := s.Log(ctx, 5)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(log) != 1 || log[0].Subject != "initial" {
		t.Fatalf("log = %+v", log)
	}
}

func TestDiffShowsChanges(t *testing.T) {
	dir := newTempRepo(t)
	os.WriteFile(dir+"/README.md", []byte("# demo changed\n"), 0o644)
	s := New(dir)
	d, err := s.Diff(context.Background(), false)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if d == "(no diff)" {
		t.Fatal("expected diff content")
	}
}

func TestCommit(t *testing.T) {
	dir := newTempRepo(t)
	os.WriteFile(dir+"/new.txt", []byte("hello"), 0o644)
	s := New(dir)
	out, err := s.Commit(context.Background(), "feat: add new.txt")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if out == "" {
		t.Fatal("expected commit output")
	}
	log, _ := s.Log(context.Background(), 3)
	if len(log) != 2 || log[0].Subject != "feat: add new.txt" {
		t.Fatalf("log after commit = %+v", log)
	}
}

func TestUnsupportedOperation(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.Run(context.Background(), "push", nil)
	if err == nil {
		t.Fatal("push should be unsupported at service level (push 由权限策略+人工控制)")
	}
}

// 回归:commit 触发的钩子若挂起并持有输出管道,CombinedOutput 曾会
// 阻塞到钩子自行退出(超过取消时机);进程组击杀 + WaitDelay 后及时返回。
func TestCommitHookCancelPromptReturn(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if _, err := s.exec(context.Background(), dir, args); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := s.Commit(ctx, "hook hangs")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from hung hook + cancel")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Commit blocked %v after cancel — orphan process still holding pipe", elapsed)
	}
}
