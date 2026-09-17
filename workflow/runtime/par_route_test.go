package runtime

import (
	"context"
	"path/filepath"
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

// 并行分支内嵌条件路由:planner 的 branch chain 允许分支内出现
// route(决策节点+条件边),lower 为分支生成 routeAgent —— 组合路径
// 此前无覆盖;REJECTED 分支在 APPROVED 决策下不得被执行。
func TestRouteInsideParallelBranch(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()
	agents := coreagent.NewRegistry()
	agt := mock.New(mock.Options{ID: "mock-x", Scripts: map[string]*mock.Script{
		"plan":   {Decisions: []coreagent.AgentDecision{coreagent.AgentDecision("APPROVED")}},
		"review": {Decisions: []coreagent.AgentDecision{coreagent.AgentDecision("APPROVED")}},
	}})
	if err := agents.Register(agt); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("mock-x", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	yml := `
version: "1"
workflow:
  id: par-route
  name: Par Route
nodes:
  - id: fanout
    type: parallel
  - id: simple
    type: agent
    agent: mock-x
    mode: plan
  - id: decide
    type: agent
    agent: mock-x
    mode: review
  - id: patch
    type: agent
    agent: mock-x
    mode: execute
  - id: merge
    type: merge
  - id: done
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: fanout
    to: simple
  - from: fanout
    to: decide
  - from: decide
    condition: decision == "APPROVED"
    to: merge
  - from: decide
    condition: decision == "REJECTED"
    to: patch
  - from: patch
    to: merge
  - from: simple
    to: merge
  - from: merge
    to: done
`
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if exec.State != model.ExecutionCompleted {
		t.Fatalf("并行分支内路由未完成: %s (%s)", exec.State, exec.Error)
	}
	for _, id := range []string{"simple", "decide", "merge", "done"} {
		if got := exec.NodeStates[id]; got != string(model.NodeSuccess) {
			t.Errorf("node %s = %s, want success", id, got)
		}
	}
	// APPROVED 决策下 REJECTED 分支(patch)不得执行
	if state := exec.NodeStates["patch"]; state != "" {
		t.Errorf("patch 节点不应执行, got %s", state)
	}
}
