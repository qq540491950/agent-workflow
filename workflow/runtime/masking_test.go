package runtime

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestExecutionStateJSONMaskedBeforePersist 锁住 **executions.state_json**
// 的落库脱敏(约定 8:敏感信息不进落库数据)。
//
// 此前本文件只锁了 execution_nodes 的三个字段(output/error/result_json),
// **从不检查 executions.state_json** —— 而状态快照里装着任务原文
// (state["task"])、节点输出原文(state["plan"] 等)与错误文本(state["_error"])。
// 凭据就在里面:节点明细干净了,执行记录却是明文 —— 这正是 P1-1 漏网的
// 原因("只测了看得见的那一格")。
//
// 依赖:本用例要求**运行期落库时**对 state_json 脱敏(迭代 13 的
// persist.go 改动)。在那一侧落地之前,本用例是红的 —— 这是预期的:
// 它是回归锁,不是"为了让门禁变绿"而写的断言。
func TestExecutionStateJSONMaskedBeforePersist(t *testing.T) {
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
  id: mask-state
  name: Mask State
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	// 任务文本会进会话状态(state["task"])—— 凭据因此落到 state_json。
	exec, err := eng.Start(context.Background(), wf, "部署时带上 ANTHROPIC_API_KEY="+leakSecret, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	// DB 直查:库里那份 state_json 不得含凭据明文。
	var raw string
	if err := repo.SQL().QueryRow(
		`SELECT state_json FROM executions WHERE id=?`, exec.ID).Scan(&raw); err != nil {
		t.Fatalf("读取 executions.state_json: %v", err)
	}
	if raw == "" {
		t.Fatal("state_json 为空,本用例没有真正覆盖到状态快照")
	}
	if strings.Contains(raw, leakSecret) {
		t.Errorf("executions.state_json 落库仍是明文: %s", raw)
	}
	// 脱敏不能把内容整体抹掉:键名保留、值替换为 ***(可读性不退化)
	if !strings.Contains(raw, "ANTHROPIC_API_KEY=***") {
		t.Errorf("脱敏后的 state_json 应保留键名并把值替换为 ***: %s", raw)
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

// ---- state_json(会话状态快照)的落库脱敏 ----
//
// 节点明细(execution_nodes)落库前脱敏只是**一半**:executions.state_json
// 存的是整个 ADK 会话状态,里面有每个节点的 outcome 原文
// (node:<id>:result)、最近一次输出(output)、错误文本(_error)——
// 同样来自 Agent 的 stdout/stderr。此前这条链路完全没有脱敏:
// 落库是原样写,API 读出口也原样返回。

// readStateJSON 读取执行记录里落库的会话状态快照(原始 JSON 文本)。
func readStateJSON(t *testing.T, repo *persistence.DB, execID string) string {
	t.Helper()
	var raw string
	if err := repo.SQL().QueryRow(
		`SELECT state_json FROM executions WHERE id=?`, execID).Scan(&raw); err != nil {
		t.Fatalf("读取 state_json: %v", err)
	}
	return raw
}

// decodeStateJSON 解析落库的会话状态快照。
func decodeStateJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("解析 state_json: %v (%s)", err, raw)
	}
	return snap
}

// TestStateSnapshotMaskedBeforePersist 锁住 P1-1:会话状态快照在**落库前**脱敏。
//
// 此前只修了节点明细与 API 读出口,DB 里的 state_json 仍是明文 ——
// 审计文件、备份 DB 直接拿到凭据。
func TestStateSnapshotMaskedBeforePersist(t *testing.T) {
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
  id: mask-state
  name: Mask State
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

	raw := readStateJSON(t, repo, exec.ID)
	if strings.Contains(raw, leakSecret) {
		t.Errorf("state_json 落库仍是明文: %s", raw)
	}
	snap := decodeStateJSON(t, raw)
	// 脱敏不能把内容整体抹掉:键名保留、值替换为 ***(可读性不退化)
	out := fmt.Sprint(snap["output"])
	if !strings.Contains(out, "ANTHROPIC_API_KEY=***") {
		t.Errorf("output 脱敏后应保留键名并把值替换为 ***: %q", out)
	}
	// 结构化键必须原样保留(脱敏它们 = 改写执行语义)
	if got := fmt.Sprint(snap["node:work:status"]); got != string(model.NodeSuccess) {
		t.Errorf("结构化键 node:work:status 被改动: %q", got)
	}
	if got := fmt.Sprint(snap["_current_node"]); got != "work" {
		t.Errorf("结构化键 _current_node 被改动: %q", got)
	}
}

// TestStateSnapshotKeepsDecisionRouting 锁住"脱敏不改执行语义":
// 决策路由必须照旧走对分支,且 decision 在快照里原样保留。
func TestStateSnapshotKeepsDecisionRouting(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"review": {
				Decisions:      []coreagent.AgentDecision{coreagent.DecisionApproved},
				OutputTemplate: "cli stderr: ANTHROPIC_API_KEY=" + leakSecret,
			},
		},
	})
	eng, repo, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: mask-route
  name: Mask Route
nodes:
  - id: review
    type: agent
    agent: mock-a
    mode: review
  - id: ok
    type: skill
    skill: log
    args:
      message: ok
  - id: ng
    type: skill
    skill: log
    args:
      message: ng
edges:
  - from: review
    condition: decision == "APPROVED"
    to: ok
  - from: review
    condition: decision == "REJECTED"
    to: ng
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	// 路由:走的是 APPROVED 分支,拒绝分支没跑
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.NodeStates["ok"] != string(model.NodeSuccess) {
		t.Errorf("APPROVED 分支未执行: %v", after.NodeStates)
	}
	if _, ran := after.NodeStates["ng"]; ran {
		t.Errorf("REJECTED 分支不该执行: %v", after.NodeStates)
	}
	// 快照里的 decision 必须原样保留(路由判据)
	snap := decodeStateJSON(t, readStateJSON(t, repo, exec.ID))
	if got := fmt.Sprint(snap["decision"]); got != "APPROVED" {
		t.Errorf("结构化键 decision 被改动: %q", got)
	}
}

// TestMaskedStateDataStillResumesAfterHITL 锁住"落库脱敏不影响 HITL 恢复"。
//
// node:<id>:answered 存的是 WAIT_USER 那次的 outcome JSON 快照,恢复时
// 要被 json.Unmarshal 解析(executor 的 answeredOutcome):脱敏后的快照
// 若不是合法 JSON,恢复路径会静默按"未答复"处理 → 节点重跑,用户白等。
func TestMaskedStateDataStillResumesAfterHITL(t *testing.T) {
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
  id: mask-hitl
  name: Mask HITL
nodes:
  - id: plan
    type: agent
    agent: mock-a
    mode: plan
  - id: gate
    type: human
    prompt: "请审批"
    responses: ["approve", "reject"]
  - id: done
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: plan
    to: gate
  - from: gate
    to: done
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)

	// 落库的快照必须已脱敏
	raw := readStateJSON(t, repo, exec.ID)
	if strings.Contains(raw, leakSecret) {
		t.Errorf("state_json 落库仍是明文: %s", raw)
	}
	// 快照里的 outcome JSON 必须仍是合法 JSON(恢复路径要解析它:
	// 解析失败就按"未答复"处理 → 节点重跑,用户白等)
	snap := decodeStateJSON(t, raw)
	result := fmt.Sprint(snap["node:plan:result"])
	var outcome map[string]any
	if err := json.Unmarshal([]byte(result), &outcome); err != nil {
		t.Errorf("落库的 outcome JSON 不是合法 JSON: %v (%s)", err, result)
	}
	if outcome["State"] != string(model.NodeSuccess) {
		t.Errorf("outcome 的结构化字段被改动: %v", outcome["State"])
	}

	// 恢复:人工答复后工作流必须跑完(恢复路径读的是脱敏后的快照)
	if _, err := eng.Resume(context.Background(), exec.ID,
		map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	after := waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if after.State != model.ExecutionCompleted {
		t.Fatalf("恢复后状态 = %s, want COMPLETED: %s", after.State, after.Error)
	}
	if after.NodeStates["done"] != string(model.NodeSuccess) {
		t.Errorf("闸门之后的节点未执行: %v", after.NodeStates)
	}
}

// TestMaskedStateDataStillAllowsRetry 锁住"落库脱敏不影响恢复语义"。
//
// state_json 是会话状态快照,恢复执行时会被回放进下一次执行:
// 落库脱敏意味着恢复后拿到的是脱敏版快照。这里验证两件事:
//   - RetryNode 仍能从失败处**重试**(失败节点真的重跑了,不是被跳过);
//   - 跳过失败节点后工作流仍能跑完(恢复路径能读懂脱敏后的快照)。
func TestMaskedStateDataStillAllowsRetry(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan":    {OutputTemplate: "cli stderr: ANTHROPIC_API_KEY=" + leakSecret},
			"execute": {FailWith: "exit status 1: ANTHROPIC_API_KEY=" + leakSecret},
			"review":  {Decisions: []coreagent.AgentDecision{coreagent.DecisionApproved}},
		},
	})
	eng, repo, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: mask-retry
  name: Mask Retry
nodes:
  - id: plan
    type: agent
    agent: mock-a
    mode: plan
  - id: execute
    type: agent
    agent: mock-a
    mode: execute
  - id: review
    type: agent
    agent: mock-a
    mode: review
edges:
  - from: plan
    to: execute
  - from: execute
    to: review
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionFailed)

	// 落库的快照必须已脱敏(重试读的就是它)
	if raw := readStateJSON(t, repo, exec.ID); strings.Contains(raw, leakSecret) {
		t.Errorf("state_json 落库仍是明文: %s", raw)
	}

	// 重试:失败节点必须真的重跑(mock 仍然失败 → 再次 FAILED)
	if _, err := eng.RetryNode(context.Background(), exec.ID, false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionFailed)
	if got := agt.CallsFor(exec.ID, "execute"); got != 2 {
		t.Errorf("失败节点应被重跑一次(execute 调用数 = %d,期望 2)", got)
	}

	// 跳过失败节点:工作流应跑完(恢复路径读的是脱敏后的快照)
	if _, err := eng.RetryNode(context.Background(), exec.ID, true); err != nil {
		t.Fatalf("skip: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.NodeStates["execute"] != string(model.NodeSkipped) {
		t.Errorf("execute 应被跳过: %v", after.NodeStates)
	}
	if after.NodeStates["review"] != string(model.NodeSuccess) {
		t.Errorf("review 应在跳过之后继续跑: %v", after.NodeStates)
	}
}
