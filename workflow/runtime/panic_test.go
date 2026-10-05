package runtime

import (
	"context"
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

// panicAgent 在 Execute 里 panic,模拟第三方 Agent 适配器/ADK 内部崩溃。
type panicAgent struct{}

func (panicAgent) ID() string   { return "panic-agent" }
func (panicAgent) Name() string { return "Panic Agent" }
func (panicAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	panic("boom from agent adapter")
}

const panicWorkflowYAML = `
version: "1"
workflow:
  id: panic-wf
  name: Panic Workflow
nodes:
  - id: work
    type: agent
    agent: panic-agent
    mode: plan
edges: []
`

// 回归:执行在独立 goroutine 里跑,节点适配器 panic 曾直接带走整个进程
// (桌面模式即整应用退出),该执行还会永久停留 RUNNING。现统一兜底为
// FAILED + workflow.failed 事件,且不影响其他测试与后续执行。
func TestNodePanicDoesNotCrashProcess(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	bus := event.NewBus()
	bus.KeepHistory = true
	agents := coreagent.NewRegistry()
	if err := agents.Register(panicAgent{}); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})

	doc, err := dsl.Parse([]byte(panicWorkflowYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	exec, err := eng.Start(context.Background(), wf, "触发 panic 的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	final := waitForState(t, eng, exec.ID, model.ExecutionFailed)
	if !strings.Contains(final.Error, "panic") {
		t.Errorf("执行错误信息未记录 panic: %q", final.Error)
	}
	failed := false
	for _, ev := range bus.History() {
		if ev.Type == event.WorkflowFailed {
			failed = true
		}
	}
	if !failed {
		t.Error("缺少 workflow.failed 事件")
	}
	// 兜底后引擎仍可继续接收新执行
	okAgt := mock.New(mock.Options{ID: "mock-a", Scripts: map[string]*mock.Script{
		"plan": {SummaryTemplate: "ok"},
	}})
	if err := agents.Replace(okAgt); err != nil {
		t.Fatal(err)
	}
	doc2, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: after-panic
  name: After Panic
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	exec2, err := eng.Start(context.Background(), doc2.ToModel(), "panic 之后的正常任务", nil)
	if err != nil {
		t.Fatalf("start after panic: %v", err)
	}
	waitForState(t, eng, exec2.ID, model.ExecutionCompleted)
}

// 回归:snapshot_json 缺失/损坏时 GetExecution 静默留 nil,execute 曾
// 直接解引用崩溃;现应显式失败并给出可操作提示。
func TestMissingSnapshotFailsCleanly(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	bus := event.NewBus()
	agents := coreagent.NewRegistry()
	if err := agents.Register(mock.New(mock.Options{ID: "mock-a"})); err != nil {
		t.Fatal(err)
	}
	if err := agents.Register(panicAgent{}); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})

	doc, err := dsl.Parse([]byte(panicWorkflowYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	exec, err := eng.Start(context.Background(), wf, "任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionFailed)

	// 构造一个快照为 nil 的执行记录并直接走 Resume 路径(崩溃恢复后
	// 状态为 WAITING_USER 的记录若快照损坏,不应 panic)
	broken, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	broken.Snapshot = nil
	eng.execute(context.Background(), broken, "resume", map[string]any{"response": "approve"})
	if broken.State != model.ExecutionFailed {
		t.Errorf("快照缺失时状态 = %s, want FAILED", broken.State)
	}
	if !strings.Contains(broken.Error, "快照") {
		t.Errorf("错误信息未提示快照问题: %q", broken.Error)
	}
}
