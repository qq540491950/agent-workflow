package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是"git 命令输出出口必须脱敏"的回归锁(迭代 30,P0-1)。
//
// 背景:`GET /api/git/diff` 此前**原样**返回工作区的 diff 文本 —— 用户
// 的 Git 工作目录里常有 .env / 带密钥的配置文件,凭据明文直接进 API
// 响应体,再由前端 GitPanel 渲染到屏幕(违反 AGENTS.md 约定 8:
// "API 返回掩码",备份导出含明文是唯一例外)。
//
// 实测证据(修复前):响应体里能读到
//
//	ANTHROPIC_API_KEY=sk-ant-supersecret1234567890abcdef
//
// 脱敏口径:**与节点 output 出口同源**(logx.Mask = 键名 ∪ 值形态)。
// 见 logx/credential.go 的分工说明。
const (
	// maskSecret 是"呈凭据形态"的值(已知前缀 sk-ant-)。
	maskSecret = "sk-ant-supersecret1234567890abcdef"
	// lowEntropySecret 是"低熵"的值:只有键名规则能抓住它
	// (值形态规则判它不是凭据 —— .env 里的密码正是这种形态)。
	lowEntropySecret = "hunter2-low-entropy"
)

// rawGit 在临时仓库里跑一条 git 命令,拿未脱敏的原文(可读性对照用)。
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return string(out)
}

// stageAll 把工作区改动全部暂存。
//
// **为什么必须暂存**:`git diff` 只看**已跟踪**文件;新建文件是未跟踪
// 状态,不出现在 diff 里(测试会拿到 "(no diff)" —— 一个恒真的假通过)。
func stageAll(t *testing.T, dir string) {
	t.Helper()
	rawGit(t, dir, "add", "-A")
}

// TestDiffMasksCredentialInFile:diff 里的凭据明文不得出现在出口。
//
// 两种形态都要覆盖:
//   - 呈凭据形态的值(sk-ant-…)→ 值形态规则抓;
//   - 低熵的值(hunter2-low-entropy)→ **只有键名规则**抓得住
//     (值形态规则判它不是凭据)。.env 里的密码正是这种形态 ——
//     只靠值形态规则,秘密照样明文出站。
func TestDiffMasksCredentialInFile(t *testing.T) {
	dir := newTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "app.env"), []byte(
		"ANTHROPIC_API_KEY="+maskSecret+"\nDB_PASSWORD="+lowEntropySecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageAll(t, dir)
	s := New(dir)
	d, err := s.Diff(context.Background(), true)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if strings.Contains(d, maskSecret) {
		t.Errorf("diff 不得含凭据明文(%q):\n%s", maskSecret, d)
	}
	if strings.Contains(d, lowEntropySecret) {
		t.Errorf("diff 不得含低熵凭据明文(%q):\n%s", lowEntropySecret, d)
	}
	// 键名保留、值替换 —— 用户要看得见"是哪个键出的问题"。
	if !strings.Contains(d, "ANTHROPIC_API_KEY=***") {
		t.Errorf("diff 应保留键名并把值替换为 ***:\n%s", d)
	}
}

// TestDiffKeepsOrdinaryContentByteIdentical:可读性回归锁。
//
// 脱敏**只改值**,不改结构:普通代码与文件路径必须逐字不变 ——
// 否则 GitPanel 展示的 diff 与仓库里的真实内容对不上,用户没法审阅。
func TestDiffKeepsOrdinaryContentByteIdentical(t *testing.T) {
	dir := newTempRepo(t)
	body := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stageAll(t, dir)
	s := New(dir)
	got, err := s.Diff(context.Background(), true)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	want := rawGit(t, dir, "diff", "--cached")
	if got != want {
		t.Errorf("普通 diff 内容必须逐字不变\n got: %q\nwant: %q", got, want)
	}
}

// TestDiffMasksLowEntropyButKeepsStructure:低熵键值被替换,但行结构不变。
//
// 这是本轮刻意登记的取舍(与节点 output 出口同源 → 宁可多遮):
// 键名像凭据的行,连低熵值也一并替换;diff 的**行结构**不受影响。
func TestDiffMasksLowEntropyButKeepsStructure(t *testing.T) {
	dir := newTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "app.env"), []byte("DB_PASSWORD="+lowEntropySecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageAll(t, dir)
	s := New(dir)
	got, err := s.Diff(context.Background(), true)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	want := rawGit(t, dir, "diff", "--cached")
	if strings.Count(got, "\n") != strings.Count(want, "\n") {
		t.Errorf("脱敏不得改变行数\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(got, "DB_PASSWORD=***") {
		t.Errorf("低熵凭据应被替换:\n%s", got)
	}
}

// TestLogMasksCredentialInCommitMessage:提交历史里的 commit message
// 同样可能含凭据("fix: rotate ANTHROPIC_API_KEY=sk-…")→ 同样要过。
func TestLogMasksCredentialInCommitMessage(t *testing.T) {
	dir := newTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if _, err := s.Commit(context.Background(), "chore: rotate "+maskSecret); err != nil {
		t.Fatalf("commit: %v", err)
	}
	entries, err := s.Log(context.Background(), 5)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("log 应有记录")
	}
	for _, e := range entries {
		if strings.Contains(e.Subject, maskSecret) {
			t.Errorf("提交信息不得含凭据明文: %q", e.Subject)
		}
	}
	if !strings.Contains(entries[0].Subject, "chore: rotate") {
		t.Errorf("提交信息的前半段应保留: %q", entries[0].Subject)
	}
}

// TestStatusMasksCredentialInPath:状态里的文件路径同样可能带凭据
// (例如 "API_KEY=sk-….txt"),原始 porcelain 文本不得出站。
//
// 注意:文件必须放在**仓库根目录** —— git 会把未跟踪的**目录**折叠成
// "notes/" 一行,放在子目录里根本不会出现在 porcelain 输出里
// (那样这条用例就是个恒真的假通过)。
func TestStatusMasksCredentialInPath(t *testing.T) {
	dir := newTempRepo(t)
	name := "API_KEY=" + maskSecret + ".txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(st.Short, "API_KEY=") {
		t.Fatalf("前置条件:porcelain 原文应含该路径(否则用例是假通过): %q", st.Short)
	}
	if strings.Contains(st.Short, maskSecret) {
		t.Errorf("status 原文不得含凭据明文: %q", st.Short)
	}
	for _, p := range append(append([]string{}, st.Modified...), st.Untracked...) {
		if strings.Contains(p, maskSecret) {
			t.Errorf("status 路径不得含凭据明文: %q", p)
		}
	}
}

// TestCommitOutputMasksCredential:commit 的输出里含文件列表与提交摘要,
// 同样要过脱敏。
func TestCommitOutputMasksCredential(t *testing.T) {
	dir := newTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "app.env"), []byte("ANTHROPIC_API_KEY="+maskSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	out, err := s.Commit(context.Background(), "chore: commit env")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if strings.Contains(out, maskSecret) {
		t.Errorf("commit 输出不得含凭据明文: %q", out)
	}
}
