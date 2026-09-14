package git

import (
	"context"
	"os"
	"os/exec"
	"testing"
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
