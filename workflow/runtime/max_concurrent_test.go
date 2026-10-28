package runtime

// settings.max_concurrent:每工作流并发上限(fail fast,迭代 36)。
//
// 锁住三条行为:
//  1. 上限 1 + 一个未结束的执行(WAITING_USER)→ 第二次 Start 报
//     WORKFLOW_BUSY(执行不创建);
//  2. 已有执行落定后 → 再次 Start 成功(终态不占额度);
//  3. 默认 0 = 不限(并发启动互不阻拦)。
//
// 检查与首次插入在同一临界区(startMu),并发 Start 不会同时通过。

import (
	"context"
	"testing"

	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

const maxConcurrentYAML = `
version: "1"
workflow:
  id: mc-run
  name: MC Run
settings:
  max_concurrent: 1
nodes:
  - id: gate
    type: human
    prompt: 等待审批
  - id: done
    type: agent
    agent: mock-a
    mode: plan
edges:
  - from: gate
    to: done
`

func parseMaxConcurrentWorkflow(t *testing.T, yml string) *model.Workflow {
	t.Helper()
	return parseInputsWorkflow(t, yml) // 与 inputs 共用解析助手
}

// TestMaxConcurrentRejectsSecondStart 上限 1 时第二个启动被拒。
func TestMaxConcurrentRejectsSecondStart(t *testing.T) {
	eng := newMinimalEngine(t)
	// done 节点的 mock-a 需要 fs:read 权限(与 newFixture 的口径一致)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})
	wf := parseMaxConcurrentWorkflow(t, maxConcurrentYAML)

	first, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	first = waitForState(t, eng, first.ID, model.ExecutionWaitingUser)

	_, err = eng.Start(context.Background(), wf, "t2", nil)
	if err == nil {
		t.Fatal("上限 1 时第二个 Start 必须被拒")
	}
	assertModelErrorCode(t, err, "WORKFLOW_BUSY")

	// 拒绝的那次不得留下执行记录(失败在插入之前)
	n, err := eng.Repo.CountActiveExecutions(wf.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("被拒的启动不得创建执行,活跃执行应 1 个,实际 %d", n)
	}

	// 落定第一个执行 → 额度释放,再次 Start 成功
	if _, err := eng.Resume(context.Background(), first.ID, map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitForState(t, eng, first.ID, model.ExecutionCompleted)
	second, err := eng.Start(context.Background(), wf, "t3", nil)
	if err != nil {
		t.Fatalf("落定后 start: %v", err)
	}
	waitForState(t, eng, second.ID, model.ExecutionCompleted, model.ExecutionWaitingUser)
}

// TestMaxConcurrentZeroMeansUnlimited 默认 0 = 不限。
func TestMaxConcurrentZeroMeansUnlimited(t *testing.T) {
	eng := newMinimalEngine(t)
	wf := parseMaxConcurrentWorkflow(t, `
version: "1"
workflow:
  id: mc-free
  name: MC Free
nodes:
  - id: gate
    type: human
    prompt: 等待审批
edges: []
`)
	a, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start a: %v", err)
	}
	b, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start b(0 = 不限): %v", err)
	}
	waitForState(t, eng, a.ID, model.ExecutionWaitingUser)
	waitForState(t, eng, b.ID, model.ExecutionWaitingUser)
}

// TestCountActiveExecutions 计数口径:非终态计数,终态不计。
func TestCountActiveExecutions(t *testing.T) {
	eng := newMinimalEngine(t)
	wf := parseMaxConcurrentWorkflow(t, `
version: "1"
workflow:
  id: mc-count
  name: MC Count
nodes:
  - id: gate
    type: human
    prompt: 等待审批
edges: []
`)
	a, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	a = waitForState(t, eng, a.ID, model.ExecutionWaitingUser)

	n, err := eng.Repo.CountActiveExecutions(wf.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("WAITING_USER 应计为活跃,实际 %d", n)
	}

	if err := eng.Cancel(context.Background(), a.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForState(t, eng, a.ID, model.ExecutionCancelled)
	n, err = eng.Repo.CountActiveExecutions(wf.ID)
	if err != nil {
		t.Fatalf("count2: %v", err)
	}
	if n != 0 {
		t.Fatalf("CANCELLED 不应计为活跃,实际 %d", n)
	}
}
