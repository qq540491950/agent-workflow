package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// leakSecret 是假装泄漏的凭据。形态取自真实事故:Agent 的 stderr 里
// 常带 "ANTHROPIC_API_KEY=sk-..." 这类环境变量回显。
const leakSecret = "sk-live-should-not-leak"

// newEngineWithAgent 造一个只注册单个 Agent 的引擎(便于脚本化输出)。
func newEngineWithAgent(t *testing.T, agt coreagent.Agent) (*Engine, *persistence.DB, *event.Bus) {
	t.Helper()
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	bus := event.NewBus()
	bus.KeepHistory = true
	agents := coreagent.NewRegistry()
	if err := agents.Register(agt); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatalf("register skill: %v", err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set(agt.ID(), permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	return eng, repo, bus
}

func parseYAML(t *testing.T, eng *Engine, yml string) *model.Workflow {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("workflow invalid: %+v", res.Errors)
	}
	return wf
}

// TestNodeOutputMaskedBeforePersist 锁住 P0-1:节点明细在**落库前**脱敏。
//
// 此前只修了 API 读出口(app/application 的 maskNode),DB 里仍是明文 ——
// 审计文件、备份 DB 直接泄漏凭据。脱敏必须在写入 SQLite 之前发生。
func TestNodeOutputMaskedBeforePersist(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {OutputTemplate: "cli stderr: ANTHROPIC_API_KEY=" + leakSecret},
		},
	})
	eng, repo, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: mask-out
  name: Mask Output
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	nodes, err := repo.ListExecutionNodes(exec.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("没有节点明细落库")
	}
	for _, n := range nodes {
		if strings.Contains(n.Output, leakSecret) {
			t.Errorf("节点 %s 的 output 落库仍是明文: %q", n.NodeID, n.Output)
		}
		if strings.Contains(n.ResultJSON, leakSecret) {
			t.Errorf("节点 %s 的 result_json 落库仍是明文: %q", n.NodeID, n.ResultJSON)
		}
	}
	// 脱敏不能把内容整体抹掉:键名保留、值替换为 ***(可读性不退化)
	var out string
	for _, n := range nodes {
		if n.NodeID == "work" {
			out = n.Output
		}
	}
	if !strings.Contains(out, "ANTHROPIC_API_KEY=***") {
		t.Errorf("脱敏后的 output 应保留键名并把值替换为 ***: %q", out)
	}
}

// TestNodeErrorMaskedBeforePersist 锁住 P0-1 的失败路径:节点失败原因
// 常带 CLI stderr,落库前同样要脱敏。
func TestNodeErrorMaskedBeforePersist(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {FailWith: "exit status 1: ANTHROPIC_API_KEY=" + leakSecret},
		},
	})
	eng, repo, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: mask-err
  name: Mask Error
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionFailed)

	nodes, err := repo.ListExecutionNodes(exec.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("没有节点明细落库")
	}
	for _, n := range nodes {
		if strings.Contains(n.Error, leakSecret) {
			t.Errorf("节点 %s 的 error 落库仍是明文: %q", n.NodeID, n.Error)
		}
	}
}

// TestGitDiffArtifactKeepsRawContent 锁住 P0-1 的边界:节点明细脱敏,
// 但 **artifact(git diff)必须保持原样** —— diff 产物是要给人看的,
// 抹掉关键行等于毁掉审计证据。
func TestGitDiffArtifactKeepsRawContent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git,跳过")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	writeFile(t, filepath.Join(dir, "app.txt"), "line1\n")
	run("add", ".")
	run("commit", "-m", "init")
	// 未提交的改动里带"凭据"形态的行:节点明细会脱敏,artifact 不脱敏
	writeFile(t, filepath.Join(dir, "app.txt"), "line1\nANTHROPIC_API_KEY="+leakSecret+"\n")

	repo, err := persistence.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	bus := event.NewBus()
	agents := coreagent.NewRegistry()
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatalf("register skill: %v", err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(dir), repo)
	eng.Perms.Set("workflow", permission.Policy{GitRead: true})

	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: git-diff
  name: Git Diff
nodes:
  - id: diff
    type: git
    operation: diff
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	arts, err := repo.ListArtifacts(exec.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(arts) == 0 {
		t.Fatal("git diff 节点应产出 artifact")
	}
	var content string
	for _, a := range arts {
		content += a.Content
	}
	if !strings.Contains(content, leakSecret) {
		t.Errorf("artifact(git diff)必须保持原样(不脱敏): %q", content)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
