package runtime

import (
	"context"
	"testing"
	"time"

	"agentworkflow/agent/mock"
	"agentworkflow/workflow/model"
)

// simpleAgentSkillYAML 是最小可跑工作流:一个 Agent 节点 + 一个 Skill 节点。
const simpleAgentSkillYAML = `
version: "1"
workflow:
  id: simple
  name: Simple
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
  - id: done
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: work
    to: done
`

// runToCompletion 跑一个最小工作流直到结束,返回执行 ID。
func runToCompletion(t *testing.T, eng *Engine) string {
	t.Helper()
	wf := parseYAML(t, eng, simpleAgentSkillYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)
	return exec.ID
}

// TestSaveMetaNeverTouchesStateColumn 锁住 P0-2/P1-4 的核心不变式:
//
// **整行写不覆盖状态列**。状态只能由 CAS 落定。
//
// 此前整行 upsert 会把 state 一起写回去:一个拿着过期副本的写者能把
// 刚由 CAS 落定的状态改回去 —— 用户在 UI 上看到"运行中",实际早已
// "完成"。这条不变式是所有并发场景(取消/重试/崩溃恢复)的共同地基。
func TestSaveMetaNeverTouchesStateColumn(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))

	// 直接造一行"已完成"的执行
	if err := repo.SaveExecution(&model.Execution{
		ID: "exec-state", WorkflowID: "simple", State: model.ExecutionCompleted,
	}); err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	// 引擎侧的整行写:内存副本里的状态是"运行中"(过期副本)
	eng.save(&model.Execution{ID: "exec-state", State: model.ExecutionRunning})

	after, err := repo.GetExecution("exec-state")
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCompleted {
		t.Fatalf("整行写把状态改成了 %s(want COMPLETED):状态列只能由 CAS 落定", after.State)
	}
}

// TestStaleMetaWriteCannotRevertCompletedExecution 端到端版:执行跑完后,
// 一个过期副本的整行写不得把状态改回 RUNNING。
func TestStaleMetaWriteCannotRevertCompletedExecution(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	execID := runToCompletion(t, eng)

	// 过期副本:状态还是 RUNNING(执行完成之前拿到的)
	eng.save(&model.Execution{ID: execID, State: model.ExecutionRunning, CurrentNodeID: "work"})

	after, err := repo.GetExecution(execID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCompleted {
		t.Fatalf("过期写把已完成执行改成了 %s(want COMPLETED)", after.State)
	}
}

// TestStaleTransitionCannotOverwriteNewerState 锁住:状态迁移必须经 CAS。
//
// 场景:执行已经 COMPLETED,一个过期副本(状态 RUNNING)尝试把它标成
// FAILED。CAS 的 from 状态(RUNNING)与库里的状态(COMPLETED)不符 →
// 迁移被拒绝,状态不变。此前整行写会直接把它改成 FAILED。
func TestStaleTransitionCannotOverwriteNewerState(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	execID := runToCompletion(t, eng)

	stale := &model.Execution{ID: execID, State: model.ExecutionRunning, Error: "stale"}
	err := eng.transitionAndSave(stale, model.ExecutionFailed)
	if err == nil {
		t.Error("过期状态迁移应当被拒绝(返回冲突错误)")
	}
	after, err := repo.GetExecution(execID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCompleted {
		t.Fatalf("过期迁移把已完成执行改成了 %s(want COMPLETED)", after.State)
	}
	if after.Error != "" {
		t.Errorf("被拒绝的迁移不应写入错误文本: %q", after.Error)
	}
}

// TestRecoverPendingKeepsCompletedExecution 锁住崩溃恢复的过期列表场景。
//
// RecoverPending 的列表是稍早取的:期间执行可能已经跑完。此时把它
// 标成 FAILED 就是"该成功却失败"。CAS 让这种情况无副作用。
func TestRecoverPendingKeepsCompletedExecution(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	execID := runToCompletion(t, eng)

	// 过期列表项:状态还是 RUNNING
	stale := &model.Execution{ID: execID, State: model.ExecutionRunning}
	if err := eng.recoverExecution(stale); err == nil {
		t.Error("对已完成执行的崩溃恢复标记应当被拒绝")
	}
	after, err := repo.GetExecution(execID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCompleted {
		t.Fatalf("崩溃恢复把已完成执行改成了 %s(want COMPLETED)", after.State)
	}
}

// TestRecoverPendingStillFailsRunningExecution 回归:崩溃恢复的**正常**
// 语义不能被上面的保护误伤 —— 真正中断的执行仍要标成 FAILED(可重试)。
func TestRecoverPendingStillFailsRunningExecution(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	wf := parseYAML(t, eng, simpleAgentSkillYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionRunning)

	if err := eng.RecoverPending(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionFailed {
		t.Fatalf("崩溃恢复后状态 = %s,want FAILED(可重试)", after.State)
	}
	if after.Error == "" {
		t.Error("崩溃恢复应写入可读的失败原因")
	}
}

// TestCompletedExecutionHasNodeStates 锁住"状态与元信息同时可见"。
//
// 状态迁移与元信息落库必须是**同一条语句**:分两次写会出现
// "状态已是 COMPLETED、节点状态还是上一轮(空)"的窗口 —— 轮询执行详情
// 的 UI 会在那个窗口里显示"成功,但一个节点都没跑"。
//
// 这条不变式是 CAS 收敛后的必守项:只把状态写收敛到 CAS 而不管元信息,
// 就会把缺陷从"状态被推翻"换成"状态与详情对不上"。
func TestCompletedExecutionHasNodeStates(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	wf := parseYAML(t, eng, simpleAgentSkillYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e, err := repo.GetExecution(exec.ID)
		if err == nil && e.State == model.ExecutionCompleted {
			if len(e.NodeStates) == 0 {
				t.Fatalf("状态已是 COMPLETED,但节点状态还是空的(状态与元信息必须同时可见)")
			}
			if e.NodeStates["work"] != string(model.NodeSuccess) {
				t.Fatalf("节点状态不对: %v", e.NodeStates)
			}
			return
		}
	}
	t.Fatal("执行未在期限内完成")
}

// TestCancelledExecutionNotRevivedByStaleWrite 锁住"已取消"不可复活。
//
// Cancel 用 CAS 把状态落成 CANCELLED;此后任何过期写都不得把它改回
// RUNNING(持久层的负面终态守卫 + 运行期的状态列免写共同保证)。
func TestCancelledExecutionNotRevivedByStaleWrite(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	wf := parseYAML(t, eng, simpleAgentSkillYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionRunning)
	if err := eng.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCancelled)

	// 过期副本
	eng.save(&model.Execution{ID: exec.ID, State: model.ExecutionRunning})
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Fatalf("过期写把已取消执行改成了 %s(want CANCELLED)", after.State)
	}
}
