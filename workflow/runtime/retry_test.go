package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
	"agentworkflow/permission"
)

// TestRetryFailedNodeSkip 验证失败执行的两种恢复:
// 重试(仍失败)与跳过(继续走完)。
func TestRetryFailedNodeSkip(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()

	failing := mock.New(mock.Options{
		ID:      "mock-flaky",
		Name:    "Flaky",
		Scripts: map[string]*mock.Script{"execute": {FailWith: "injected failure"}},
	})
	okAgt := mock.New(mock.Options{
		ID:   "mock-ok",
		Name: "OK",
		Scripts: map[string]*mock.Script{
			"plan":   {SummaryTemplate: "plan ok"},
			"review": {Decisions: []coreagent.AgentDecision{coreagent.DecisionApproved}, SummaryTemplate: "review ok"},
		},
	})
	agents := coreagent.NewRegistry()
	_ = agents.Register(failing)
	_ = agents.Register(okAgt)
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		_ = skills.Register(s)
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("mock-flaky", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	eng.Perms.Set("mock-ok", permission.Policy{FilesystemRead: true})
	eng.Perms.Set("workflow", permission.Policy{FilesystemRead: true, GitRead: true, GitCommit: true})

	yml := `
version: "1"
workflow:
  id: flaky
  name: Flaky
nodes:
  - id: plan
    type: agent
    agent: mock-ok
    mode: plan
  - id: execute
    type: agent
    agent: mock-flaky
    mode: execute
  - id: review
    type: agent
    agent: mock-ok
    mode: review
edges:
  - from: plan
    to: execute
  - from: execute
    to: review
  - from: review
    to: end
  - from: review
    to: ghost
`
	_ = yml
	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: flaky
  name: Flaky
nodes:
  - id: plan
    type: agent
    agent: mock-ok
    mode: plan
  - id: execute
    type: agent
    agent: mock-flaky
    mode: execute
  - id: review
    type: agent
    agent: mock-ok
    mode: review
  - id: end
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: plan
    to: execute
  - from: execute
    to: review
  - from: review
    to: end
`))
	if err != nil {
		t.Fatal(err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}

	exec, err := eng.Start(context.Background(), wf, "flaky task", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e, _ := repo.GetExecution(exec.ID)
		if e != nil && e.State == model.ExecutionFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cur, _ := repo.GetExecution(exec.ID)
	if cur.State != model.ExecutionFailed || cur.CurrentNodeID != "execute" {
		t.Fatalf("expected FAILED at execute, got %s @%s", cur.State, cur.CurrentNodeID)
	}

	// 重试:仍然失败(mock 永远失败)
	if _, err := eng.RetryNode(context.Background(), exec.ID, false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e, _ := repo.GetExecution(exec.ID)
		if e != nil && e.State == model.ExecutionFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cur, _ = repo.GetExecution(exec.ID)
	if cur.State != model.ExecutionFailed {
		t.Fatalf("retry should fail again, got %s", cur.State)
	}

	// 跳过:执行完成
	if _, err := eng.RetryNode(context.Background(), exec.ID, true); err != nil {
		t.Fatalf("skip: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e, _ := repo.GetExecution(exec.ID)
		if e != nil && (e.State == model.ExecutionCompleted || e.State == model.ExecutionFailed) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cur, _ = repo.GetExecution(exec.ID)
	if cur.State != model.ExecutionCompleted {
		t.Fatalf("skip should complete, got %s (%s)", cur.State, cur.Error)
	}
	if cur.NodeStates["execute"] != string(model.NodeSkipped) {
		t.Errorf("execute should be SKIPPED, got %s", cur.NodeStates["execute"])
	}
	if cur.NodeStates["review"] != string(model.NodeSuccess) {
		t.Errorf("review should run after skip: %+v", cur.NodeStates)
	}

	// 非法:对已完成执行重试
	if _, err := eng.RetryNode(context.Background(), exec.ID, false); err == nil {
		t.Fatal("retry on COMPLETED should be rejected")
	}
}
