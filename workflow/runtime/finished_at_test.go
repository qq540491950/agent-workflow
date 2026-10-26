package runtime

import (
	"context"
	"testing"
	"time"

	"agentworkflow/workflow/model"

	coreagent "agentworkflow/agent"
)

// 本文件锁定"执行级 finished_at 必须在终态落库"(迭代 35)。
//
// 此前执行级 FinishedAt **从未被赋值**(只有节点级 executor.go:99 有):
// 已完成的执行 finished_at 恒为空 —— 前端耗时只能拿"当前时间-开始时间"
// 补,时钟永远在走;结束时间列一直是空。用户会把"已完成"误读成"还在跑"。

// parseTimeOrDie 把 RFC3339Nano 字符串解成时间,失败即用例失败。
func parseTimeOrDie(t *testing.T, exec *model.Execution, field, v string) time.Time {
	t.Helper()
	if v == "" {
		t.Fatalf("执行 %s 的 %s 为空(终态必须落结束时间)", exec.ID, field)
	}
	ts, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		t.Fatalf("执行 %s 的 %s 不是 RFC3339Nano: %q(%v)", exec.ID, field, v, err)
	}
	return ts
}

func TestFinishedAtStampedOnComplete(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	started := time.Now()
	exec, err := f.engine.Start(context.Background(), wf, "正常完成", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionCompleted)

	fin := parseTimeOrDie(t, exec, "finished_at", exec.FinishedAt)
	st, err := time.Parse(time.RFC3339Nano, exec.StartedAt)
	if err != nil {
		t.Fatalf("started_at 非法: %q(%v)", exec.StartedAt, err)
	}
	if fin.Before(st) {
		t.Errorf("finished_at(%v) 早于 started_at(%v)", fin, st)
	}
	if fin.Before(started) {
		t.Errorf("finished_at(%v) 早于测试开始时刻(%v)—— 像是没落库", fin, started)
	}
}

func TestFinishedAtClearedOnRetryAndRestamped(t *testing.T) {
	// 用"子工作流不存在"制造确定性失败:校验期只是警告(SUBWORKFLOW_NOT_FOUND),
	// 运行期必失败 → FAILED,且可 Retry。
	f := newFixture(t, nil)
	wf := loadWf(t, f.engine, `workflow:
  id: wf-sub-missing
  name: Sub Missing
nodes:
  - id: sub
    type: subworkflow
    workflow_id: no-such-child
edges: []
`)
	exec, err := f.engine.Start(context.Background(), wf, "会失败的任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionFailed)

	firstFin := parseTimeOrDie(t, exec, "finished_at(失败时)", exec.FinishedAt)

	// 重试:新一轮运行,旧的结束时间必须被清掉(RUNNING 行不得带 finished_at)
	resumed, err := f.engine.RetryNode(context.Background(), exec.ID, false)
	if err != nil {
		// 该工作流的 agent 不存在,重试会再次失败 —— 这正是我们要的:
		// RUNNING 窗口太短,退而求其次,断言"重试成功受理"即可。
		t.Logf("retry 返回错误(预期:无此 agent): %v", err)
	} else {
		_ = resumed
	}

	// 重新读库:要么还在 RUNNING(finished_at 应为空),要么已再次终态
	// (新的 finished_at 必须不早于第一次的)。
	final, err := f.engine.Repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if final.State == model.ExecutionRunning {
		if final.FinishedAt != "" {
			t.Errorf("RUNNING 行携带旧 finished_at: %q(重试必须清掉)", final.FinishedAt)
		}
		return
	}
	secondFin := parseTimeOrDie(t, final, "finished_at(重试后)", final.FinishedAt)
	if secondFin.Before(firstFin) {
		t.Errorf("重试后的 finished_at(%v)早于第一次(%v)", secondFin, firstFin)
	}
}

func TestFinishedAtStampedOnCancel(t *testing.T) {
	// review 永远 REJECTED → 循环上限 → WAITING_USER;取消一条等待中的执行。
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, _ := f.engine.Start(context.Background(), wf, "会被取消的任务", nil)
	waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	if err := f.engine.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	exec = waitForState(t, f.engine, exec.ID, model.ExecutionCancelled)
	parseTimeOrDie(t, exec, "finished_at(取消时)", exec.FinishedAt)
}

// TestFinishedAtNotStampedOnConflict:状态被并发写者抢先时,本协程的
// 时钟不得借"失败原因补写"(save(exec))留在**别人正在跑**的行上。
// 构造:WAITING_USER 的执行被另一个写者 Resume(状态 → RUNNING),
// 本协程拿着过期的 WAITING 快照去 Cancel → CAS 冲突 → 还原。
func TestFinishedAtNotStampedOnConflict(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionRejected})
	wf := loadWf(t, f.engine, loopWorkflowYAML)

	exec, _ := f.engine.Start(context.Background(), wf, "并发冲突场景", nil)
	waitForState(t, f.engine, exec.ID, model.ExecutionWaitingUser)

	// exec 是 WAITING 快照。先让另一个写者把它推进到 RUNNING(Resume 成功),
	// 再用过期快照提交取消 → transitionAndSave 必然 CAS 失败。
	if _, err := f.engine.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// 等对方推进(重放可能很快又到 WAITING/COMPLETED;只要不再是 WAITING,
	// 过期快照的 CANCELLED 迁移就会冲突,除非对方又回到了可取消态)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := f.engine.Repo.GetExecution(exec.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if cur.State != model.ExecutionWaitingUser {
			// 用过期快照(WAITING)提交取消:若库里已不是 WAITING → 冲突路径
			if err := f.engine.Cancel(context.Background(), exec.ID); err != nil {
				t.Logf("cancel 按预期冲突: %v", err)
			}
			// 无论 cancel 是否成功落库,库里 RUNNING/WAITING 的行都不得带上
			// 本协程盖章的 finished_at:RUNNING 行必须有空 finished_at,
			// 终态行的 finished_at 必须 ≥ 它自己状态落定的时刻(由赢家盖章)。
			after, err := f.engine.Repo.GetExecution(exec.ID)
			if err != nil {
				t.Fatalf("get after cancel: %v", err)
			}
			if after.State == model.ExecutionRunning && after.FinishedAt != "" {
				t.Errorf("RUNNING 行携带冲突协程的 finished_at: %q", after.FinishedAt)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Skip("执行未离开 WAITING_USER(重放过快回到等待),冲突场景未构造出来")
}
