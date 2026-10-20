package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentworkflow/workflow/model"
)

// gitRaceIterations 是并发探针的写侧迭代数(#208)。
//
// **为什么要有下限**:迭代数太少 = 恒真的假保护 —— 去掉锁后 -race 仍全绿,
// 比没有测试更危险(约定 14 的测试侧同款)。
//
// 实测(隔离副本,去掉 wdMu 的读写锁后跑 `go test -race`):
//
//	200 次 → 5/5 命中 DATA RACE
//
// 取 200:既能稳定命中,又几乎不拖慢门禁(整个循环 <1ms)。
//
// **更重要的是窗口形态**:旧写法靠 time.Sleep 建窗口,读写可能零重叠;
// 现在用屏障(读者先就位)让读写**必然**重叠 —— 迭代数只是下限保障,
// 屏障才是"重叠是确定的"这件事的保证。
const gitRaceIterations = 200

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

// 回归:工作流 git 节点走 Service.Run("commit", {add_all:true})。
// 此前 add 分支把 gitArgs 覆盖成 ["add","-A"],commit 从未执行,
// 却返回成功(工作区脏、后续 diff/review 读到未提交状态)。
func TestRunCommitWithAddAll(t *testing.T) {
	dir := newTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if _, err := s.Run(context.Background(), "commit", map[string]any{
		"message": "feat: node commit", "add_all": true,
	}); err != nil {
		t.Fatalf("Run commit: %v", err)
	}
	log, err := s.Log(context.Background(), 3)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(log) != 2 || log[0].Subject != "feat: node commit" {
		t.Fatalf("commit 未发生: log = %+v", log)
	}
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(st.Modified) != 0 || len(st.Untracked) != 0 || len(st.Staged) != 0 {
		t.Fatalf("工作区应干净: %+v", st)
	}
}

// 回归:DSL args 里的分支名不得被 git 当作选项解析。
func TestRunRejectsOptionLikeRefName(t *testing.T) {
	dir := newTempRepo(t)
	s := New(dir)
	for _, args := range []map[string]any{
		{"name": "--delete"},
	} {
		if _, err := s.Run(context.Background(), "branch", args); err == nil {
			t.Fatalf("branch %v 应被拒绝", args)
		}
	}
	if _, err := s.Run(context.Background(), "checkout", map[string]any{"branch": "-f"}); err == nil {
		t.Fatal("checkout -f 应被拒绝")
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

// TestWorkingDirConcurrentAccess 验证默认工作目录的并发安全。
//
// WorkingDir 由设置页写、**执行协程读**(git / script / agent 节点在跑时
// 读它决定 cmd.Dir)。Go 字符串是 (ptr, len) 二元组,非原子:此前是裸
// 导出字段,无锁并发读写是 DATA RACE,撕裂后可产生非法指针
// (AGENTS.md 硬性约定 #15)。
//
// **读写窗口必须确定**(#208):旧写法是"写者跑固定次数 + time.Sleep(100ms)
// 建窗口"—— 调度不巧时写者早已跑完,读者与写者**零重叠**,
// -race 什么也报不出(实测:去掉锁仍全绿 = 恒真的假保护)。
// 现在用**屏障**:读者先就位并持续自旋,写者才开始写 ——
// 读写必然重叠,窗口 = 写者的循环时长。
func TestWorkingDirConcurrentAccess(t *testing.T) {
	s := New("/tmp/before")
	var wg sync.WaitGroup
	var ready sync.WaitGroup
	stop := make(chan struct{})

	// 读者:模拟执行协程
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
					_ = s.WorkingDir()
				}
			}
		}()
	}
	ready.Wait()

	// 写者:模拟设置页反复改目录(紧凑循环,迭代数足够让去锁后必红)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < gitRaceIterations; i++ {
			s.SetWorkingDir(fmt.Sprintf("/tmp/dir-%d", i))
		}
		close(stop) // 写者跑完 → 读者退出(窗口 = 写者的循环时长)
	}()
	wg.Wait()

	// 最终值必须是一个完整的字符串(不是撕裂的 ptr/len)
	final := s.WorkingDir()
	if final == "" {
		t.Error("最终工作目录不应为空")
	}
	if !strings.HasPrefix(final, "/tmp/dir-") {
		t.Errorf("最终工作目录 = %q,形状异常(可能发生了字符串撕裂)", final)
	}
}

// gitErrCode 取出结构化 git 错误的 code。
func gitErrCode(t *testing.T, err error) string {
	t.Helper()
	var ge *model.Error
	if !errors.As(err, &ge) {
		t.Fatalf("应返回 *model.Error, 实际 %T (%v)", err, err)
	}
	return ge.Code
}

// 回归:失败语义必须可区分(约定 14:该失败却成功等同 P0)。
// 此前 GIT_TIMEOUT / GIT_CANCELLED 两个分支没有任何测试覆盖。
func TestExecTimeoutMapsToTimeoutCode(t *testing.T) {
	dir := newTempRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := s.Commit(ctx, "slow hook")
	if err == nil {
		t.Fatal("挂起的钩子应超时失败")
	}
	if code := gitErrCode(t, err); code != "GIT_TIMEOUT" {
		t.Errorf("code = %q, 期望 GIT_TIMEOUT", code)
	}
}

func TestExecCancelMapsToCancelledCode(t *testing.T) {
	dir := newTempRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	_, err := s.Commit(ctx, "cancelled hook")
	if err == nil {
		t.Fatal("被取消的提交应失败")
	}
	if code := gitErrCode(t, err); code != "GIT_CANCELLED" {
		t.Errorf("code = %q, 期望 GIT_CANCELLED", code)
	}
}

// 回归:git 没有输出时错误信息不能是空的。
// API 只回传 Message(model.Error.Detail 不进 error 文本),
// 空原因会让用户只看到"git status 失败:"。
func TestExecFailureMessageHasReason(t *testing.T) {
	s := New(t.TempDir())
	s.SetBin(filepath.Join(t.TempDir(), "no-such-git-binary"))
	_, err := s.Status(context.Background())
	if err == nil {
		t.Fatal("不存在的 git 可执行文件应失败")
	}
	if code := gitErrCode(t, err); code != "GIT_COMMAND_FAILED" {
		t.Errorf("code = %q, 期望 GIT_COMMAND_FAILED", code)
	}
	msg := err.Error()
	if strings.HasSuffix(msg, "失败: ") || strings.HasSuffix(msg, "失败:") {
		t.Errorf("错误信息缺少原因: %q", msg)
	}
	if !strings.Contains(msg, "no-such-git-binary") {
		t.Errorf("错误信息应包含可执行文件路径: %q", msg)
	}
}

// 非仓库目录必须失败,不能静默返回空状态(约定 14)。
func TestStatusOutsideRepoFails(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Status(context.Background()); err == nil {
		t.Fatal("非仓库目录应返回错误")
	}
}

// 缺参数的分支必须显式失败。
func TestRunMissingArguments(t *testing.T) {
	dir := newTempRepo(t)
	s := New(dir)
	if _, err := s.Run(context.Background(), "commit", map[string]any{}); err == nil {
		t.Error("commit 缺 message 应失败")
	}
	if _, err := s.Run(context.Background(), "checkout", map[string]any{}); err == nil {
		t.Error("checkout 缺 branch 应失败")
	}
}

// SetBin / bin() 的并发安全(约定 15:共享可变状态加锁)。
func TestBinConcurrentAccess(t *testing.T) {
	s := New(t.TempDir())
	var wg sync.WaitGroup
	var ready sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done() // 就位信号:屏障(见 TestWorkingDirConcurrentAccess)
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.bin()
				}
			}
		}()
	}
	ready.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < gitRaceIterations; i++ {
			s.SetBin("git")
		}
		close(stop) // 写者跑完 → 读者退出
	}()
	wg.Wait()
}
