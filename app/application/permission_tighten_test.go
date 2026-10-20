package application

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentworkflow/agent"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

// 本文件锁住"权限判定单一来源"在应用层的两个用户可见后果:
//
//  1. DSL permissions 段**想放宽**全局已禁用的能力 → **校验期**就报出来
//     (不是运行期才以"无权执行 fs:write"的形式暴露);
//  2. 全局权限在**运行中**被收紧 → 在跑的执行立即受限
//     (此前用执行启动时的快照:执行期间在 Agents 页收紧权限,
//      只有 pi 适配器的全局检查兜底 write 类节点,mock/自定义 Agent 无兜底)。
//
// 第 2 条的测试要点:收紧必须发生在**同一次执行之内**(节点执行期间),
// 而不是跨"暂停→恢复"边界 —— 恢复会重新进入 execute(),重新取快照,
// 那样测不到"运行中改权限"。

// blockingAgent 是一个可控的 Agent:被调用时通知测试并阻塞,直到测试放行。
type blockingAgent struct {
	id      string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingAgent) ID() string   { return b.id }
func (b *blockingAgent) Name() string { return "Blocking Agent" }

func (b *blockingAgent) Execute(ctx context.Context, req agent.AgentRequest) (*agent.AgentResponse, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &agent.AgentResponse{
		Status:   agent.StatusSuccess,
		Decision: agent.DecisionDone,
		Summary:  "ok",
	}, nil
}

// waitExecState 轮询直到执行到达期望状态。
func waitExecState(t *testing.T, a *App, execID string, want model.ExecutionState) *model.Execution {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last *model.Execution
	for time.Now().Before(deadline) {
		e, err := a.Repo.GetExecution(execID)
		if err != nil {
			t.Fatalf("读取执行: %v", err)
		}
		last = e
		if e.State == want {
			return e
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("执行未在超时前到达状态 %s(实际 %s,error=%q)", want, last.State, last.Error)
	return nil
}

// TestSaveWorkflowRejectsPermissionLoosening permissions 段想放宽全局禁用
// → 保存必须失败,并说清是 permissions 的问题。
func TestSaveWorkflowRejectsPermissionLoosening(t *testing.T) {
	a := newTestApp(t)
	// 全局:claude-code 只读(禁写)
	if err := a.AgentSvc.SetPermission("claude-code", permission.Policy{
		FilesystemRead: true, FilesystemWrite: false,
	}); err != nil {
		t.Fatalf("设置全局权限: %v", err)
	}

	_, err := a.Workflows.ImportYAML(`
version: "1"
permissions:
  claude-code:
    filesystem:
      write: true
workflow:
  id: loosen-wf
  name: Loosen
nodes:
  - id: a
    type: agent
    agent: claude-code
    mode: plan
edges: []
`)
	if err == nil {
		t.Fatal("permissions 段放宽全局禁用必须被拒绝(否则用户以为写权限生效了)")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Errorf("错误消息应指向 permissions 段,实际: %q", err.Error())
	}
	// 不得落库
	if _, err := a.Workflows.Get("loosen-wf"); err == nil {
		t.Error("被拒绝的工作流不得落库")
	}
}

// TestSaveWorkflowAcceptsPermissionTightening 收紧必须放行(不能把正常路径堵死)。
func TestSaveWorkflowAcceptsPermissionTightening(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.SetPermission("claude-code", permission.Policy{
		FilesystemRead: true, FilesystemWrite: true,
	}); err != nil {
		t.Fatalf("设置全局权限: %v", err)
	}
	wf, err := a.Workflows.ImportYAML(`
version: "1"
permissions:
  claude-code:
    filesystem:
      write: false
workflow:
  id: tighten-wf
  name: Tighten
nodes:
  - id: a
    type: agent
    agent: claude-code
    mode: plan
edges: []
`)
	if err != nil {
		t.Fatalf("permissions 段的收紧必须放行: %v", err)
	}
	if len(wf.Workflow.Permission) == 0 {
		t.Error("permissions 段应保留在工作流里")
	}
}

// TestRunningExecutionSeesTightenedPermission 运行中被收紧的全局权限必须
// 立即对在跑的执行生效(不再依赖执行启动时的快照)。
func TestRunningExecutionSeesTightenedPermission(t *testing.T) {
	a := newTestApp(t)
	ba := &blockingAgent{id: "slow-a", entered: make(chan struct{}), release: make(chan struct{})}
	if err := a.Agents.Register(ba); err != nil {
		t.Fatalf("注册阻塞 Agent: %v", err)
	}
	// 前置:两个 Agent 默认可读
	a.Perms.Set("slow-a", permission.Policy{FilesystemRead: true})
	a.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true})

	wf, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: live-tighten
  name: Live Tighten
nodes:
  - id: slow
    type: agent
    agent: slow-a
    mode: plan
  - id: work
    type: agent
    agent: mock-claude
    mode: plan
edges:
  - from: slow
    to: work
`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	exec, err := a.Engine.Start(context.Background(), wf.Workflow, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 等第一个节点进入(执行尚未结束)
	select {
	case <-ba.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("第一个节点未进入")
	}
	// **运行中**收紧全局权限:mock-claude 不再可读
	a.Perms.Set("mock-claude", permission.Policy{})
	close(ba.release)

	final := waitExecState(t, a, exec.ID, model.ExecutionFailed)
	if !strings.Contains(final.Error, "fs:read") {
		t.Errorf("失败原因应为权限不足,实际: %q", final.Error)
	}
}

// TestRunningExecutionAllowsBeforeTightening 对照:未收紧时执行必须正常完成
// (证明上一条不是因为别的原因失败)。
func TestRunningExecutionAllowsBeforeTightening(t *testing.T) {
	a := newTestApp(t)
	ba := &blockingAgent{id: "slow-a", entered: make(chan struct{}), release: make(chan struct{})}
	if err := a.Agents.Register(ba); err != nil {
		t.Fatalf("注册阻塞 Agent: %v", err)
	}
	a.Perms.Set("slow-a", permission.Policy{FilesystemRead: true})
	a.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true})

	wf, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: live-ok
  name: Live OK
nodes:
  - id: slow
    type: agent
    agent: slow-a
    mode: plan
  - id: work
    type: agent
    agent: mock-claude
    mode: plan
edges:
  - from: slow
    to: work
`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	exec, err := a.Engine.Start(context.Background(), wf.Workflow, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-ba.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("第一个节点未进入")
	}
	close(ba.release)
	final := waitExecState(t, a, exec.ID, model.ExecutionCompleted)
	if final.Error != "" {
		t.Errorf("未收紧权限时执行应成功,实际错误: %q", final.Error)
	}
}
