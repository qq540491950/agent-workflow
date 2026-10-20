package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// blockingAgent 一直阻塞到 ctx 被取消才返回 —— 模拟"并行分支里还在跑的
// 兄弟分支"。它被级联取消后返回 ctx.Err(),引擎会把这次失败记成
// **取消类**(不是"这个节点坏了"),因此它不该参与失败原因的竞争。
type blockingAgent struct{ id string }

func (a *blockingAgent) ID() string   { return a.id }
func (a *blockingAgent) Name() string { return a.id }
func (a *blockingAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestParallelBranchFailureReasonPointsAtFailingBranch 并行分支失败的
// 原因必须指向**真正出问题的那个分支**,不能被兄弟分支的取消错误顶掉。
//
// 缺陷族(约定 #14):并行区的失败级联(compiler 的 parallelCascadeAgent)
// 在任一分支失败后取消共享 ctx,而 ADK 的 parallelagent 在分支 ctx 被
// 取消时会把该分支的**终态事件**一起丢掉(parallelagent.runSubAgent 的
// select:`<-ctx.Done()` 与 `results <- …` 同时就绪时随机选一个)。
// 失败信息只存在于事件里的 StateDelta(节点状态的唯一来源),事件一丢,
// 真因(节点 panic 的原文)就整段消失,用户只看到
// "failed to run sub-agent …: context canceled"。
//
// 本用例比 TestPanicInsideParallelBranchDoesNotKillProcess 多两个
// **还在跑**的兄弟分支:它们必然被级联取消,于是"被取消分支的错误"与
// "真因"正面相遇 —— 谁胜出必须是确定的,且胜出的必须是真因。
func TestParallelBranchFailureReasonPointsAtFailingBranch(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "par.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()
	bus.KeepHistory = true

	agents := coreagent.NewRegistry()
	if err := agents.Register(panicAgent{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"slow-1", "slow-2"} {
		if err := agents.Register(&blockingAgent{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	for _, id := range []string{"panic-agent", "slow-1", "slow-2"} {
		eng.Perms.Set(id, permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	}

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: par-fail-reason
  name: 并行失败原因
nodes:
  - id: fanout
    type: parallel
  - id: bad
    type: agent
    agent: panic-agent
    mode: plan
  - id: slow1
    type: agent
    agent: slow-1
    mode: plan
  - id: slow2
    type: agent
    agent: slow-2
    mode: plan
  - id: merge
    type: merge
edges:
  - from: fanout
    to: bad
  - from: fanout
    to: slow1
  - from: fanout
    to: slow2
  - from: bad
    to: merge
  - from: slow1
    to: merge
  - from: slow2
    to: merge
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}

	exec, err := eng.Start(context.Background(), wf, "并行分支失败原因", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Fatalf("分支失败后执行应 FAILED,实际 %s (%s)", final.State, final.Error)
	}
	// 真因必须胜出:错误信息要指名道姓说明是哪个节点炸了、怎么炸的。
	if !strings.Contains(final.Error, "panic") {
		t.Errorf("失败原因应说明 panic,实际 %q", final.Error)
	}
	if !strings.Contains(final.Error, "bad") {
		t.Errorf("失败原因应指出出问题的节点 bad,实际 %q", final.Error)
	}
	// 被取消的兄弟分支的错误**不参与竞争**:不能出现在失败原因里。
	if strings.Contains(final.Error, "context canceled") {
		t.Errorf("失败原因不应被兄弟分支的取消错误污染,实际 %q", final.Error)
	}
	// 出问题的节点必须记 FAILED(否则用户看到"失败,但没有节点失败")。
	if got := final.NodeStates["bad"]; got != string(model.NodeFailed) {
		t.Errorf("出问题的节点应记 FAILED,实际 %q", got)
	}
}

// TestRepairFailureStateOverridesCancelledSiblingAbort 锁死"台账优先于
// 会话状态"的纠正口径:会话状态可能被**被取消的兄弟分支**的终态事件
// 串位(它写 _abort="FAILED:<兄弟>"、_error="context canceled"),
// 此时必须以台账(引擎亲眼见到的节点 outcome)为准。
//
// 这是并行区失败级联最隐蔽的一种形态:失败分支的事件被丢掉,被取消分支的
// escalate 事件却落地了 —— 真因完全没进会话状态。
func TestRepairFailureStateOverridesCancelledSiblingAbort(t *testing.T) {
	eng := newMinimalEngine(t)
	exec := &model.Execution{ID: "exec-parallel-1", StateData: map[string]any{
		// 被取消的兄弟分支的事件落地:中断原因与失败原因都指向它。
		compiler.KeyAbort: "FAILED:slow1",
		"_error":          "context canceled",
	}}
	// 台账:bad 是确凿失败(panic),slow1 只是被级联取消。
	eng.failures.record(exec.ID, "bad", "节点 bad 执行 panic: boom from agent adapter", false)
	eng.failures.record(exec.ID, "slow1", "context canceled", true)

	eng.repairFailureState(exec)

	if got := exec.StateData[compiler.KeyAbort]; got != "FAILED:bad" {
		t.Errorf("中断原因应指向真正失败的节点 bad,实际 %v", got)
	}
	if got := fmt.Sprint(exec.StateData["_error"]); !strings.Contains(got, "panic") {
		t.Errorf("失败原因应是 panic 的原文,实际 %q", got)
	}
	if got := exec.StateData[compiler.StatusKey("bad")]; got != string(model.NodeFailed) {
		t.Errorf("出问题的节点状态应补 FAILED,实际 %v", got)
	}
}

// TestRepairFailureStateKeepsGenuineAbort 会话状态已经指向真正的失败
// 节点时,纠正不得制造无谓差异(中断原因保留)。
func TestRepairFailureStateKeepsGenuineAbort(t *testing.T) {
	eng := newMinimalEngine(t)
	exec := &model.Execution{ID: "exec-parallel-2", StateData: map[string]any{
		compiler.KeyAbort: "FAILED:bad",
		"_error":          "节点 bad 执行 panic: boom from agent adapter",
	}}
	eng.failures.record(exec.ID, "bad", "节点 bad 执行 panic: boom from agent adapter", false)

	eng.repairFailureState(exec)

	if got := exec.StateData[compiler.KeyAbort]; got != "FAILED:bad" {
		t.Errorf("中断原因应保留 FAILED:bad,实际 %v", got)
	}
	if got := fmt.Sprint(exec.StateData["_error"]); !strings.Contains(got, "panic") {
		t.Errorf("失败原因应保留 panic 原文,实际 %q", got)
	}
}

// TestRepairFailureStateOverridesWaitingUserAbort 并行区里一个分支失败、
// 另一个分支在等人工时,失败不能被 WAIT_USER 盖住 —— 否则执行停在
// "等用户输入"上,明明已经失败却没人被告知(约定 #14)。
func TestRepairFailureStateOverridesWaitingUserAbort(t *testing.T) {
	eng := newMinimalEngine(t)
	exec := &model.Execution{ID: "exec-parallel-3", StateData: map[string]any{
		compiler.KeyAbort: "WAIT_USER",
		"_error":          "",
	}}
	eng.failures.record(exec.ID, "bad", "节点 bad 执行 panic: boom from agent adapter", false)

	eng.repairFailureState(exec)

	if got := exec.StateData[compiler.KeyAbort]; got != "FAILED:bad" {
		t.Errorf("确凿失败应盖过 WAIT_USER,实际 %v", got)
	}
	if got := fmt.Sprint(exec.StateData["_error"]); !strings.Contains(got, "panic") {
		t.Errorf("失败原因应是 panic 的原文,实际 %q", got)
	}
}

// TestFailureReasonDeterministicAcrossBranches 多个分支同时失败时,
// 收集到哪一个不该取决于调度顺序 —— 同一个工作流两次运行给出的失败
// 原因必须一致(否则没法比对、没法复现)。
func TestFailureReasonDeterministicAcrossBranches(t *testing.T) {
	eng := newMinimalEngine(t)
	exec := &model.Execution{ID: "exec-parallel-4", StateData: map[string]any{}}

	// 两次登记顺序相反,结果必须一致。
	eng.failures.record(exec.ID, "zeta", "节点 zeta 失败: 原因 Z", false)
	eng.failures.record(exec.ID, "alpha", "节点 alpha 失败: 原因 A", false)
	first := failureReason(eng.failures.list(exec.ID))

	eng.failures.reset(exec.ID)
	eng.failures.record(exec.ID, "alpha", "节点 alpha 失败: 原因 A", false)
	eng.failures.record(exec.ID, "zeta", "节点 zeta 失败: 原因 Z", false)
	second := failureReason(eng.failures.list(exec.ID))

	if first != second {
		t.Errorf("失败原因不应依赖登记顺序:\n第一次 %q\n第二次 %q", first, second)
	}
	if !strings.Contains(first, "alpha") || !strings.Contains(first, "zeta") {
		t.Errorf("多个失败都应出现在原因里,实际 %q", first)
	}
}

// TestFailureLedgerExcludesCancelledBranches 被取消的分支不参与失败原因:
// 它的 "context canceled" 是派生错误,不是失败原因。
func TestFailureLedgerExcludesCancelledBranches(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.failures.record("exec-x", "slow1", "context canceled", true)
	if got := eng.failures.list("exec-x"); len(got) != 0 {
		t.Errorf("取消类失败不应出现在确凿失败列表里,实际 %+v", got)
	}
	if got := failureReason(eng.failures.list("exec-x")); got != "" {
		t.Errorf("只有取消类时失败原因应为空,实际 %q", got)
	}
}

// TestFailureLedgerEmptyReasonGetsReadableFallback 失败必须给出可读原因
// (约定 #14):空原因会让用户只看到"失败"两个字,无从下手。
func TestFailureLedgerEmptyReasonGetsReadableFallback(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.failures.record("exec-y", "broken", "", false)
	got := failureReason(eng.failures.list("exec-y"))
	if got == "" {
		t.Fatal("空失败原因应被补成可读文本")
	}
	if !strings.Contains(got, "broken") {
		t.Errorf("补出的失败原因应点名节点,实际 %q", got)
	}
}

// TestFailureLedgerDoesNotLeakAcrossExecutions 失败台账按执行隔离:
// 一次执行的失败不能影响另一次执行(否则重跑/并发跑同一个工作流时,上一次
// 的失败原因会串进这一次)。
func TestFailureLedgerDoesNotLeakAcrossExecutions(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.failures.record("exec-first", "bad", "节点 bad 执行 panic: boom", false)
	if got := eng.failures.list("exec-second"); len(got) != 0 {
		t.Errorf("另一次执行不应看到本次的失败,实际 %+v", got)
	}
	eng.failures.reset("exec-first")
	if got := eng.failures.list("exec-first"); len(got) != 0 {
		t.Errorf("清零后不应再看到失败,实际 %+v", got)
	}
}

// failWithReasonAgent 立刻失败,并在失败原因里带上自己的 ID —— 让测试能
// 断言"失败原因指向哪个分支"。
type failWithReasonAgent struct{ id string }

func (a *failWithReasonAgent) ID() string   { return a.id }
func (a *failWithReasonAgent) Name() string { return a.id }
func (a *failWithReasonAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	return &coreagent.AgentResponse{
		Status: coreagent.StatusFailed,
		Error:  "分支 " + a.id + " 故意失败",
	}, nil
}

// TestParallelTwoBranchesFailReportsRealCause 两个并行分支**同时**失败时,
// 收集到哪一个不该取决于调度顺序,且失败原因必须是真因(不能是
// "context canceled" 这种派生错误)。
//
// 注意:断言只要求"至少指出一个真失败分支" —— 两个分支是否都算"确凿
// 失败"取决于级联取消落在它们之间的哪个时刻(先失败的那个会触发级联,
// 后失败的那个可能已经被取消),那是语义上正确的:后一个是被前一个连累的。
// 但**不能**出现"两个都没提到、只剩一句 context canceled"的结果。
func TestParallelTwoBranchesFailReportsRealCause(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "par2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()
	bus.KeepHistory = true

	agents := coreagent.NewRegistry()
	for _, id := range []string{"fail-b1", "fail-b2"} {
		if err := agents.Register(&failWithReasonAgent{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	for _, id := range []string{"fail-b1", "fail-b2"} {
		eng.Perms.Set(id, permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	}

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: par-two-fail
  name: 并行双失败
nodes:
  - id: fanout
    type: parallel
  - id: b1
    type: agent
    agent: fail-b1
    mode: plan
  - id: b2
    type: agent
    agent: fail-b2
    mode: plan
  - id: merge
    type: merge
edges:
  - from: fanout
    to: b1
  - from: fanout
    to: b2
  - from: b1
    to: merge
  - from: b2
    to: merge
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}

	exec, err := eng.Start(context.Background(), wf, "并行双失败", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Fatalf("分支失败后执行应 FAILED,实际 %s (%s)", final.State, final.Error)
	}
	if !strings.Contains(final.Error, "fail-b1") && !strings.Contains(final.Error, "fail-b2") {
		t.Errorf("失败原因应指出真正失败的分支,实际 %q", final.Error)
	}
	if strings.Contains(final.Error, "context canceled") {
		t.Errorf("失败原因不应被取消错误污染,实际 %q", final.Error)
	}
}

// TestUserCancelWinsOverBranchFailure 用户取消与分支失败撞在一起时,
// 执行应落 CANCELLED(用户的裁决优先),而不是把"取消"当成一次失败来报错。
func TestUserCancelWinsOverBranchFailure(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()

	agents := coreagent.NewRegistry()
	if err := agents.Register(&blockingAgent{id: "slow-1"}); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("slow-1", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: cancel-race
  name: 取消与失败竞态
nodes:
  - id: work
    type: agent
    agent: slow-1
    mode: plan
edges: []
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	exec, err := eng.Start(context.Background(), wf, "取消与失败竞态", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 等节点真的跑起来再取消,确保撞的是"运行中"而不是"还没开始"。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e, err := eng.Repo.GetExecution(exec.ID); err == nil && e.CurrentNodeID == "work" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := eng.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionCancelled, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionCancelled {
		t.Errorf("用户取消后执行应 CANCELLED,实际 %s (%s)", final.State, final.Error)
	}
	// 取消不是失败:不该凭空造出一条失败原因。
	if strings.Contains(final.Error, "context canceled") {
		t.Errorf("取消不应被记成一次失败,实际 %q", final.Error)
	}
}
