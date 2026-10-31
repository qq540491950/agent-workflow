package runtime

// 全局并发与排队:超限 409(SYSTEM_BUSY)、开启排队后 202/QUEUED、
// 槽位释放后自动调度、取消排队、队列满载。

import (
	"context"
	"testing"

	"agentworkflow/workflow/model"
)

func newQueueEngine(t *testing.T, delayMS int, limits RuntimeLimits) *Engine {
	t.Helper()
	eng := newWorkspaceEngine(t, delayMS)
	eng.SetRuntimeLimits(limits)
	return eng
}

func TestGlobalCapFailFastWithoutQueue(t *testing.T) {
	eng := newQueueEngine(t, 600, RuntimeLimits{MaxActiveExecutions: 1})
	wf := loadWf(t, eng, workspaceAgentYAML)

	first, err := eng.Start(context.Background(), wf, "first", nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err = eng.Start(context.Background(), wf, "second", nil)
	if err == nil {
		t.Fatal("全局并发已满且未开排队时应拒绝")
	}
	assertModelErrorCode(t, err, "SYSTEM_BUSY")
	waitForState(t, eng, first.ID, model.ExecutionCompleted, model.ExecutionFailed)
}

func TestQueueAdmitsAndSchedules(t *testing.T) {
	eng := newQueueEngine(t, 400, RuntimeLimits{
		MaxActiveExecutions: 1, QueueEnabled: true,
		MaxQueuedExecutions: 10, QueuePolicy: "fifo",
	})
	wf := loadWf(t, eng, workspaceAgentYAML)

	first, err := eng.Start(context.Background(), wf, "first", nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := eng.Start(context.Background(), wf, "second", nil)
	if err != nil {
		t.Fatalf("second should be queued: %v", err)
	}
	if second.State != model.ExecutionQueued {
		t.Fatalf("第二个执行状态 = %s, want QUEUED", second.State)
	}
	if second.QueuePosition != 1 {
		t.Errorf("queue position = %d, want 1", second.QueuePosition)
	}
	waitForState(t, eng, first.ID, model.ExecutionCompleted, model.ExecutionFailed)
	// 槽位释放后自动调度排队执行。
	waitForState(t, eng, second.ID, model.ExecutionCompleted, model.ExecutionFailed)
}

func TestCancelQueuedExecution(t *testing.T) {
	eng := newQueueEngine(t, 600, RuntimeLimits{
		MaxActiveExecutions: 1, QueueEnabled: true,
		MaxQueuedExecutions: 10, QueuePolicy: "fifo",
	})
	wf := loadWf(t, eng, workspaceAgentYAML)

	first, err := eng.Start(context.Background(), wf, "first", nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := eng.Start(context.Background(), wf, "second", nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if err := eng.Cancel(context.Background(), second.ID); err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	exec, err := eng.Repo.GetExecution(second.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if exec.State != model.ExecutionCancelled {
		t.Fatalf("取消排队后状态 = %s, want CANCELLED", exec.State)
	}
	if depth, _ := eng.QueueStats(); depth != 0 {
		t.Errorf("取消后队列深度 = %d, want 0", depth)
	}
	waitForState(t, eng, first.ID, model.ExecutionCompleted, model.ExecutionFailed)
}

func TestQueueFullRejects(t *testing.T) {
	eng := newQueueEngine(t, 600, RuntimeLimits{
		MaxActiveExecutions: 1, QueueEnabled: true,
		MaxQueuedExecutions: 1, QueuePolicy: "fifo",
	})
	wf := loadWf(t, eng, workspaceAgentYAML)

	first, err := eng.Start(context.Background(), wf, "first", nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := eng.Start(context.Background(), wf, "second", nil); err != nil {
		t.Fatalf("second should fill queue: %v", err)
	}
	_, err = eng.Start(context.Background(), wf, "third", nil)
	if err == nil {
		t.Fatal("队列满时应拒绝")
	}
	assertModelErrorCode(t, err, "QUEUE_FULL")
	waitForState(t, eng, first.ID, model.ExecutionCompleted, model.ExecutionFailed)
}
