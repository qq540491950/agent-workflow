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
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

const loopWorkflowYAML = `
version: "1"
workflow:
  id: coding-task
  name: Coding Task
  settings:
    max_iterations: 5
    on_loop_limit: wait_user
nodes:
  - id: plan
    type: agent
    agent: mock-claude
    mode: plan
  - id: execute
    type: agent
    agent: mock-pi
    mode: execute
  - id: review
    type: agent
    agent: mock-claude
    mode: review
  - id: fix
    type: agent
    agent: mock-pi
    mode: fix
  - id: submit
    type: skill
    skill: submit
    args:
      message: "feat: done"
edges:
  - from: plan
    to: execute
  - from: execute
    to: review
  - from: review
    condition: decision == "APPROVED"
    to: submit
  - from: review
    condition: decision == "REJECTED"
    to: fix
  - from: fix
    to: review
`

type fixture struct {
	engine    *Engine
	repo      *persistence.DB
	bus       *event.Bus
	review    *mock.Agent
	planAgt   *mock.Agent
	pagt      *mock.Agent
	execNodes map[string]*model.Execution
}

func newFixture(t *testing.T, reviewDecisions []coreagent.AgentDecision) *fixture {
	t.Helper()
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	bus := event.NewBus()
	bus.KeepHistory = true

	planAgt := mock.New(mock.Options{
		ID:   "mock-claude",
		Name: "Mock Claude",
		Scripts: map[string]*mock.Script{
			"plan":   {SummaryTemplate: "计划:实现 {task}"},
			"review": {Decisions: reviewDecisions, SummaryTemplate: "review call={call}"},
		},
	})
	pagt := mock.New(mock.Options{
		ID:   "mock-pi",
		Name: "Mock Pi",
		Scripts: map[string]*mock.Script{
			"execute": {SummaryTemplate: "执行完成 {task}"},
			"fix":     {SummaryTemplate: "修复问题 call={call}"},
		},
	})

	agents := coreagent.NewRegistry()
	for _, a := range []coreagent.Agent{planAgt, pagt} {
		if err := agents.Register(a); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatalf("register skill: %v", err)
		}
	}

	eng := NewEngine(agents, skills, permission.NewManager(), bus,
		git.New(t.TempDir()), repo)
	// 为测试 Agent 配置权限策略(生产环境由 AgentService 配置)
	eng.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true, GitRead: true})
	eng.Perms.Set("mock-pi", permission.Policy{
		FilesystemRead: true, FilesystemWrite: true,
		GitRead: true, GitCommit: true,
	})
	eng.Perms.Set("workflow", permission.Policy{FilesystemRead: true, GitRead: true, GitCommit: true})

	return &fixture{
		engine:  eng,
		repo:    repo,
		bus:     bus,
		review:  planAgt,
		planAgt: planAgt,
		pagt:    pagt,
	}
}

func loadWf(t *testing.T, eng *Engine, yml string) *model.Workflow {
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

func waitForState(t *testing.T, eng *Engine, execID string, states ...model.ExecutionState) *model.Execution {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		exec, err := eng.Repo.GetExecution(execID)
		if err == nil {
			for _, s := range states {
				if exec.State == s {
					return exec
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	exec, _ := eng.Repo.GetExecution(execID)
	t.Fatalf("execution %s did not reach %v, current: %+v", execID, states, exec)
	return nil
}

// TestFullLoop 验证 MVP 闭环:
// Plan → Execute → Review(REJECTED) → Fix → Review(APPROVED) → Submit → COMPLETED
func TestFullLoop(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected, coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, err := f.engine.Start(context.Background(), wf, "实现登录功能", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionCompleted)

	// 断言调用序列
	if got := f.planAgt.Calls("plan"); got != 1 {
		t.Errorf("plan calls = %d, want 1", got)
	}
	if got := f.pagt.Calls("execute"); got != 1 {
		t.Errorf("execute calls = %d, want 1", got)
	}
	if got := f.planAgt.Calls("review"); got != 2 {
		t.Errorf("review calls = %d, want 2 (REJECTED→APPROVED)", got)
	}
	if got := f.pagt.Calls("fix"); got != 1 {
		t.Errorf("fix calls = %d, want 1", got)
	}

	// 断言节点状态
	wantStates := map[string]model.NodeState{
		"plan": model.NodeSuccess, "execute": model.NodeSuccess,
		"review": model.NodeSuccess, "fix": model.NodeSuccess, "submit": model.NodeSuccess,
	}
	for id, want := range wantStates {
		if got := exec.NodeStates[id]; got != string(want) {
			t.Errorf("node %s state = %s, want %s", id, got, want)
		}
	}

	// 断言事件流
	types := map[string]bool{}
	for _, ev := range f.bus.History() {
		types[ev.Type] = true
	}
	for _, want := range []string{
		event.WorkflowStarted, event.NodeStarted, event.NodeCompleted,
		event.ReviewRejected, event.ReviewApproved, event.WorkflowCompleted,
		event.SkillExecuted,
	} {
		if !types[want] {
			t.Errorf("missing event %s in %v", want, types)
		}
	}

	// 断言持久化:节点明细与事件可查(review 因循环执行 2 次,attempt=2)
	nodes, _ := f.repo.ListExecutionNodes(exec.ID)
	if len(nodes) != 5 {
		t.Errorf("execution nodes = %d, want 5", len(nodes))
	}
	for _, n := range nodes {
		if n.NodeID == "review" && n.Attempt != 2 {
			t.Errorf("review attempt = %d, want 2", n.Attempt)
		}
		if n.DurationMS < 0 {
			t.Errorf("node %s negative duration", n.NodeID)
		}
	}
	events, _ := f.repo.ListEvents(exec.ID, 0)
	if len(events) == 0 {
		t.Error("events not persisted")
	}
}

// TestLoopProtectionAlwaysRejected 验证循环保护:
// Review 永远 REJECTED → 达到 max_iterations → WAITING_USER(而非无限循环)。
func TestLoopProtectionAlwaysRejected(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, err := f.engine.Start(context.Background(), wf, "永不通过的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	// review 被调用的次数应受 max_iterations 限制(5 + 首次 = 至多 6)
	if got := f.planAgt.Calls("review"); got > 7 {
		t.Errorf("review calls = %d, expected loop protection at ~6", got)
	}
	if got := f.pagt.Calls("fix"); got > 6 {
		t.Errorf("fix calls = %d, expected loop protection", got)
	}
}

// TestResumeAfterWaitUser 验证 HITL:循环达到上限暂停后,
// 用户提供 approve 输入可恢复并完成。
func TestResumeAfterWaitUser(t *testing.T) {
	// review 永远拒绝 → 触发循环上限 → WAITING_USER
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, _ := f.engine.Start(context.Background(), wf, "需要人工裁决的任务", nil)
	waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	resumed, err := f.engine.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumed = waitForState(t, f.engine, resumed.ID, model.ExecutionCompleted)
	if resumed.NodeStates["submit"] != string(model.NodeSuccess) {
		t.Errorf("submit should run after approve resume: %+v", resumed.NodeStates)
	}
}

// TestStateMachine 验证非法状态转换被拒绝。
func TestStateMachine(t *testing.T) {
	if model.CanTransitionExecution(model.ExecutionCompleted, model.ExecutionRunning) {
		t.Error("COMPLETED → RUNNING should be invalid")
	}
	if model.CanTransitionExecution(model.ExecutionFailed, model.ExecutionCompleted) {
		t.Error("FAILED → COMPLETED should be invalid")
	}
	if !model.CanTransitionExecution(model.ExecutionRunning, model.ExecutionWaitingUser) {
		t.Error("RUNNING → WAITING_USER should be valid")
	}
	if !model.CanTransitionNode(model.NodeWaiting, model.NodeRunning) {
		t.Error("WAITING → RUNNING should be valid")
	}
	if model.CanTransitionNode(model.NodePending, model.NodeSuccess) {
		t.Error("PENDING → SUCCESS should be invalid")
	}
}

// TestCancelRejectsAndValidates 验证:未运行执行不可取消 + 校验失败的工作流拒绝运行。
func TestStartValidation(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	badYAML := `
workflow:
  id: bad
  name: Bad
nodes:
  - id: a
    type: agent
    agent: non-existent
    mode: plan
`
	doc, _ := dsl.Parse([]byte(badYAML))
	wf := doc.ToModel()
	_, err := f.engine.Start(context.Background(), wf, "t", nil)
	if err == nil {
		t.Fatal("expected validation error for missing agent")
	}
}
