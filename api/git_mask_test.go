package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是"git 出口不得返回未脱敏文件内容"的端到端回归锁(迭代 30,P0-1)。
//
// 越界发现(第二十九轮扫描):`GET /api/git/diff` 返回**未脱敏**文件内容 ——
// 工作目录里的 .env / 含密钥的配置文件,凭据明文直接进 API 响应体,
// 再由前端 GitPanel 渲染到屏幕(违反 AGENTS.md 约定 8:"API 返回掩码";
// 备份导出含明文是**唯一**例外,git 出口此前没有登记为例外)。
//
// 实测证据(修复前):响应体里能直接读到
//
//	ANTHROPIC_API_KEY=sk-ant-supersecret1234567890abcdef
const apiGitSecret = "sk-ant-supersecret1234567890abcdef"

// newSecretRepo 建一个含"带凭据文件"的临时 git 仓库,返回目录。
//
// **在隔离目录里做**(t.TempDir):绝不在真实仓库里造带密钥的文件,
// 也不在真实仓库里执行 git init/add/commit。
func newSecretRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git,跳过")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "initial")
	// 带凭据的文件(暂存 → 出现在 git diff --cached 里;
	// 未跟踪的新文件不出现在 git diff 里,那会让本用例变成假通过)
	if err := os.WriteFile(filepath.Join(dir, "app.env"),
		[]byte("ANTHROPIC_API_KEY="+apiGitSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	return dir
}

// TestGitDiffEndpointDoesNotLeakCredentials:git diff 出口不得含凭据明文。
func TestGitDiffEndpointDoesNotLeakCredentials(t *testing.T) {
	s := newTestServer(t)
	dir := newSecretRepo(t)
	s.app.GitSvc.SetWorkingDir(dir)

	code, body, raw := doReq(t, s, http.MethodGet, "/api/git/diff?staged=true", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/git/diff → %d,期望 200(%s)", code, raw)
	}
	diff, _ := body["diff"].(string)
	if diff == "" {
		t.Fatalf("响应体缺 diff 字段: %s", raw)
	}
	if strings.Contains(diff, apiGitSecret) {
		t.Errorf("git diff 出口含凭据明文:\n%s", diff)
	}
	if !strings.Contains(diff, "ANTHROPIC_API_KEY=***") {
		t.Errorf("git diff 出口应保留键名并把值替换为 ***:\n%s", diff)
	}
}

// TestGitLogEndpointDoesNotLeakCredentials:提交历史里的 commit message
// 同样可能含凭据 → 同样要过。
func TestGitLogEndpointDoesNotLeakCredentials(t *testing.T) {
	s := newTestServer(t)
	dir := newSecretRepo(t)
	s.app.GitSvc.SetWorkingDir(dir)

	// 造一条含凭据的提交信息(在**隔离仓库**里提交)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("add", "-A")
	run("commit", "-m", "chore: rotate "+apiGitSecret)

	code, _, raw := doReq(t, s, http.MethodGet, "/api/git/log", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/git/log → %d,期望 200(%s)", code, raw)
	}
	if strings.Contains(string(raw), apiGitSecret) {
		t.Errorf("git log 出口含凭据明文:\n%s", raw)
	}
}
