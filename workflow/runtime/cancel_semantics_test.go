package runtime

// 本文件钉住"取消执行"的三种语义(终审复审 P1-1)。
//
// 背景:Engine.Cancel 的"运行中"快路径只 cancel() 就 return nil ——
// **不做状态检查**。而终态是**先写库**、e.cancels 里的句柄**后**由
// execute 协程的 defer 摘除,于是存在一个窗口:
//
//	终态写库 → (终态事件同步落库) → execute 返回 → defer 摘除句柄
//
// 窗口内取消一个**已完成**的执行 → 快路径 → 200 假成功("已取消"其实
// 没发生,执行早已 COMPLETED)。实测:紧密轮询下 40/40 次都能落进窗口,
// 且 40/40 次都拿到 200 假成功(改后:0/40)。
//
// 门禁里的 api/server_test.go:TestContract_ExecutionCancel("跑完 → 取消
// → 期望 400 + INVALID_TRANSITION")正落在这个窗口里;它按 50ms 的间隔
// 轮询,窗口命中率很低(实测 500 次不红),但窗口本身是真实存在的 ——
// api/cancel_semantics_test.go 用紧密轮询把它 100% 复现出来。
//
// 修法:快路径也先读库里的状态(唯一裁决来源,约定 4):
//
//   - 运行中 → 取消 ctx,成功(nil);
//   - 已终态(COMPLETED/FAILED/CANCELLED)→ INVALID_TRANSITION 明确错误;
//   - 不存在 → NOT_FOUND(404)。

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// cancelSemanticsYAML 是最小可跑工作流(单个 Agent 节点)。
const cancelSemanticsYAML = `
version: "1"
workflow:
  id: cancel-semantics
  name: Cancel Semantics
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`

// errCode 取结构化错误的 code(非结构化错误返回 "")。
func errCode(err error) string {
	var se *model.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// TestCancelTerminalExecutionIsRejected 终态执行不可取消 → 明确错误。
//
// 注入方式(确定性,不靠 sleep):让执行跑到 COMPLETED,然后**手工把一个
// 取消句柄塞回 e.cancels** —— 精确复刻"终态写库 → defer 摘除句柄"那个
// 窗口的现场(句柄还在,状态已是终态)。
//
// 修复前:快路径只 cancel() 就 return nil → 200 假成功(本用例红)。
// 修复后:快路径先读库 → 状态是 COMPLETED → INVALID_TRANSITION(本用例绿)。
func TestCancelTerminalExecutionIsRejected(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	wf := parseYAML(t, eng, cancelSemanticsYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	// 复刻窗口现场:终态已落库,但句柄还没被 defer 摘掉。
	eng.mu.Lock()
	eng.cancels[exec.ID] = func() {}
	eng.mu.Unlock()

	err = eng.Cancel(context.Background(), exec.ID)
	if err == nil {
		t.Fatal("取消一个已终态(COMPLETED)的执行返回了 nil(200 假成功)")
	}
	if code := errCode(err); code != "INVALID_TRANSITION" {
		t.Errorf("错误码 = %q, want INVALID_TRANSITION(err=%v)", code, err)
	}
	// 状态不得被这次取消改写(终态不可被推翻)。
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCompleted {
		t.Errorf("取消终态执行把状态改成了 %s(want COMPLETED)", after.State)
	}
}

// TestCancelTerminalExecutionNeverFakesSuccess 终态执行**反复**取消,
// 每次都必须拿到明确错误 —— 这条是"窗口期"最直接的回归钉子:
//
// 修复前,只要句柄还在(窗口内),取消就返回 nil(假成功);句柄摘掉后
// 才走 CAS 路径报错。同一份代码在毫秒内给出两种答案 = 不可预期。
//
// 修复后:无论句柄在不在,结论都是 INVALID_TRANSITION。
func TestCancelTerminalExecutionNeverFakesSuccess(t *testing.T) {
	for i := 0; i < 20; i++ {
		eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
		wf := parseYAML(t, eng, cancelSemanticsYAML)
		exec, err := eng.Start(context.Background(), wf, "t", nil)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		waitForState(t, eng, exec.ID, model.ExecutionCompleted)
		// 窗口现场(句柄还在)
		eng.mu.Lock()
		eng.cancels[exec.ID] = func() {}
		eng.mu.Unlock()
		if err := eng.Cancel(context.Background(), exec.ID); err == nil {
			t.Fatalf("第 %d 次:取消已终态执行返回 nil(200 假成功)", i)
		}
		// 句柄摘掉之后(常规路径):同样必须报错
		eng.mu.Lock()
		delete(eng.cancels, exec.ID)
		eng.mu.Unlock()
		if err := eng.Cancel(context.Background(), exec.ID); err == nil {
			t.Fatalf("第 %d 次:句柄摘除后取消已终态执行仍返回 nil", i)
		}
		after, err := repo.GetExecution(exec.ID)
		if err != nil {
			t.Fatalf("get execution: %v", err)
		}
		if after.State != model.ExecutionCompleted {
			t.Fatalf("第 %d 次:状态被改成了 %s(want COMPLETED)", i, after.State)
		}
	}
}

// TestCancelRunningExecutionSucceeds 回归:取消一个**运行中**的执行必须成功,
// 且状态最终落成 CANCELLED(前端"取消执行"按钮的主路径)。
func TestCancelRunningExecutionSucceeds(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {DelayMS: 300, SummaryTemplate: "ok"},
		},
	})
	eng, _, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, cancelSemanticsYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionRunning)

	if err := eng.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("取消运行中的执行失败: %v", err)
	}
	after := waitForState(t, eng, exec.ID, model.ExecutionCancelled)
	if after.State != model.ExecutionCancelled {
		t.Errorf("状态 = %s, want CANCELLED", after.State)
	}
}

// TestCancelWaitingUserExecutionActuallyCancels 等待人工输入(HITL 暂停)的
// 执行必须**真的**被取消 —— 状态落成 CANCELLED。
//
// 修复前有个同族的假成功:句柄还在(协程刚写完 WAITING_USER)时走快路径,
// 只 cancel() 就返回 nil —— 而协程早已收尾,ctx 取消改变不了任何东西,
// 执行仍然停在 WAITING_USER(用户以为取消了,实际还在等输入)。
func TestCancelWaitingUserExecutionActuallyCancels(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	exec := &model.Execution{
		ID: "exec-waiting", WorkflowID: "wf", State: model.ExecutionWaitingUser,
		Task: "t", CreatedAt: time.Now().Format(time.RFC3339Nano),
	}
	if err := repo.SaveExecution(exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	// 复刻窗口现场:句柄还在(协程刚写完 WAITING_USER)。
	eng.mu.Lock()
	eng.cancels[exec.ID] = func() {}
	eng.mu.Unlock()

	if err := eng.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("取消等待输入的执行失败: %v", err)
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Errorf("状态 = %s, want CANCELLED(取消必须真的生效)", after.State)
	}
}

// TestCancelMissingExecutionIsNotFound 不存在的执行 → NOT_FOUND(404)。
func TestCancelMissingExecutionIsNotFound(t *testing.T) {
	eng, _, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	err := eng.Cancel(context.Background(), "exec-ghost")
	if err == nil {
		t.Fatal("取消不存在的执行返回了 nil(应为 404)")
	}
	if code := errCode(err); code != "NOT_FOUND" {
		t.Errorf("错误码 = %q, want NOT_FOUND(err=%v)", code, err)
	}
}

// TestCancelStuckRunningExecutionIsCancellable "卡死"在 RUNNING 的执行
// (终态提交 CAS 冲突时状态会停在 RUNNING,且协程已收尾、没有句柄)
// 也必须能被用户取消,否则用户只能看着一个永远"运行中"的执行。
func TestCancelStuckRunningExecutionIsCancellable(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	exec := &model.Execution{
		ID: "exec-stuck", WorkflowID: "wf", State: model.ExecutionRunning,
		Task: "t", CreatedAt: time.Now().Format(time.RFC3339Nano),
	}
	if err := repo.SaveExecution(exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	if err := eng.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("取消卡死执行失败: %v", err)
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Errorf("状态 = %s, want CANCELLED", after.State)
	}
}

// countingAgent 包一层 Agent,统计 Execute 的真实调用次数。
//
// 为什么不用 mock 自己的计数:mock 的计数空间**按 executionID 隔离**,
// 而一次执行结束后该空间会被回收 —— 第二次执行会重新开一份,计数从 1
// 重新开始,分不清"跑了一次"还是"跑了两次"。这里用外部原子计数。
type countingAgent struct {
	inner coreagent.Agent
	calls atomic.Int64
}

func (c *countingAgent) ID() string   { return c.inner.ID() }
func (c *countingAgent) Name() string { return c.inner.Name() }

func (c *countingAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	c.calls.Add(1)
	return c.inner.Execute(ctx, req)
}

// TestExecuteAbortsWhenAlreadyCancelled 协程启动**之前**状态已被取消 →
// 立刻收尾,工作流一个节点都不跑。
//
// 背景:取消请求先到、执行协程后启动的窗口(Resume/Retry 的 CAS 把状态
// 改成 RUNNING 之后、句柄注册之前;Start 的落库与句柄注册之间同理)。
// 若协程照常把工作流跑完,终态提交会撞上一个已被裁决的状态 →
//
//	库里写着"已取消",可活其实干完了 —— 状态与事实不符(约定 14),
//	而且副作用已经发生(用户看到"已取消",文件却已经被改了)。
//
// 修复前:Agent 被调用、终态提交 CAS 失败(计入 StateCommitFailures)。
// 修复后:协程启动即核对状态,发现已取消 → 直接返回,Agent 不被调用。
func TestExecuteAbortsWhenAlreadyCancelled(t *testing.T) {
	counter := &countingAgent{inner: mock.New(mock.Options{ID: "mock-a"})}
	eng, repo, _ := newEngineWithAgent(t, counter)
	wf := parseYAML(t, eng, cancelSemanticsYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)
	if calls := counter.calls.Load(); calls != 1 {
		t.Fatalf("前置条件不成立:首次执行的 Agent 调用数 = %d, want 1", calls)
	}

	// 把状态改成"已取消"(模拟取消先到、协程后启动的现场)。
	ok, err := repo.CasExecutionState(exec.ID,
		[]model.ExecutionState{model.ExecutionCompleted}, model.ExecutionCancelled)
	if err != nil || !ok {
		t.Fatalf("把状态改成已取消失败: ok=%v err=%v", ok, err)
	}

	// 清掉"节点已完成"的标记:重放会跳过已完成的节点,不清掉就测不到
	// "活到底干了没有"(第二次执行根本不会碰那个节点)。
	again, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	delete(again.StateData, compiler.StatusKey("work"))
	again.State = model.ExecutionCancelled
	if err := repo.SaveExecution(again); err != nil {
		t.Fatalf("写回执行记录: %v", err)
	}

	// 同步再走一次执行(相当于那个"晚启动"的协程)。
	eng.execute(context.Background(), again, "resume", nil)

	if calls := counter.calls.Load(); calls != 1 {
		t.Errorf("已取消的执行仍跑了工作流(Agent 调用数 = %d, want 1):状态与事实不符", calls)
	}
	if eng.StateCommitFailures() != 0 {
		t.Errorf("已取消的执行仍尝试提交终态(CAS 冲突 %d 次)", eng.StateCommitFailures())
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if after.State != model.ExecutionCancelled {
		t.Errorf("状态 = %s, want CANCELLED(不得被推翻)", after.State)
	}
}
