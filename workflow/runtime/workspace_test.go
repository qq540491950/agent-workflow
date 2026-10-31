package runtime

// 执行级工作区租约:同目录串行化、等待人工释放、恢复重新获取。

import (
	"context"
	"testing"
	"time"

	"agentworkflow/agent/mock"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

const workspaceAgentYAML = `
version: "1"
workflow:
  id: ws-agent
  name: WS Agent
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: execute
edges: []
`

const workspaceHumanYAML = `
version: "1"
workflow:
  id: ws-human
  name: WS Human
nodes:
  - id: gate
    type: human
    prompt: 需要审批
  - id: work
    type: agent
    agent: mock-a
    mode: execute
edges:
  - from: gate
    to: work
`

func newWorkspaceEngine(t *testing.T, delayMS int) *Engine {
	t.Helper()
	eng := newMinimalEngine(t)
	eng.Perms.Set("mock-a", permission.Policy{
		FilesystemRead: true, FilesystemWrite: true,
	})
	// 用延时 mock 让第一个执行在第二个 Start 时仍然持有租约。
	_ = eng.Agents.Replace(mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"execute": {DelayMS: delayMS},
		},
	}))
	return eng
}

func TestWorkspaceLeaseRejectsConcurrentSameDir(t *testing.T) {
	eng := newWorkspaceEngine(t, 800)
	wf := loadWf(t, eng, workspaceAgentYAML)

	first, err := eng.Start(context.Background(), wf, "first", nil)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if len(first.WorkspaceDirs) != 1 || first.WorkspaceDirs[0] != eng.Git.WorkingDir() {
		t.Fatalf("workspace_dirs = %v, want [%s]", first.WorkspaceDirs, eng.Git.WorkingDir())
	}

	_, err = eng.Start(context.Background(), wf, "second", nil)
	if err == nil {
		t.Fatal("同目录第二个执行必须被 WORKSPACE_BUSY 拒绝")
	}
	assertModelErrorCode(t, err, "WORKSPACE_BUSY")

	waitForState(t, eng, first.ID, model.ExecutionCompleted, model.ExecutionFailed)

	third, err := eng.Start(context.Background(), wf, "third", nil)
	if err != nil {
		t.Fatalf("第一个结束后应能再次启动: %v", err)
	}
	waitForState(t, eng, third.ID, model.ExecutionCompleted, model.ExecutionFailed)
}

func TestWorkspaceLeaseReleasedWhileWaitingUser(t *testing.T) {
	eng := newWorkspaceEngine(t, 0)
	wf := loadWf(t, eng, workspaceHumanYAML)

	first, err := eng.Start(context.Background(), wf, "first", nil)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	first = waitForState(t, eng, first.ID, model.ExecutionWaitingUser)
	// 等 execute 协程的 defer 完成释放(commitTerminal 之后)。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		second, startErr := eng.Start(context.Background(), wf, "second", nil)
		if startErr == nil {
			waitForState(t, eng, second.ID, model.ExecutionWaitingUser)
			// 恢复第一个:两个都在等待,租约都已释放,应能重新获取。
			if _, err := eng.Resume(context.Background(), first.ID, map[string]any{"response": "approve"}); err != nil {
				t.Fatalf("resume after waiting should reacquire lease: %v", err)
			}
			waitForState(t, eng, first.ID, model.ExecutionCompleted, model.ExecutionFailed)
			if err := eng.Cancel(context.Background(), second.ID); err != nil {
				t.Fatalf("cancel second: %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待人工后工作区租约未释放:同目录执行一直 WORKSPACE_BUSY")
}

func TestWorkspaceDirsOnlyForSideEffectNodes(t *testing.T) {
	eng := newWorkspaceEngine(t, 0)
	wf := loadWf(t, eng, `
version: "1"
workflow:
  id: ws-readonly
  name: WS Readonly
nodes:
  - id: plan
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "readonly", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(exec.WorkspaceDirs) != 0 {
		t.Fatalf("只读工作流不应占工作区租约,实际 %v", exec.WorkspaceDirs)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
}
