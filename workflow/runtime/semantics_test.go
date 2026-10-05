package runtime

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
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
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// 本文件覆盖批次 A2/A4/A5/A6:模板缺失变量告警、并行分支失败级联、
// merge 汇总口径、mock 按执行隔离。

// ---- A2:模板缺失变量必须可见 ----

// TestMissingTemplateVarEmitsWarning 验证 prompt 里的缺失变量(拼错或
// 上游未产出)会发出 node.warning 事件。此前 `rendered, _ :=` 把 missing
// 列表直接丢弃,Agent 收到残缺 prompt 并照常返回 SUCCESS/decision,
// 没有任何告警。
func TestMissingTemplateVarEmitsWarning(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, `
workflow:
  id: tpl-warn
  name: 模板告警
nodes:
  - id: plan
    name: Plan
    type: agent
    agent: mock-claude
    mode: plan
    prompt: "环境={{variable.env}} 拼错了={{revwie}}"
  - id: done
    name: Done
    type: skill
    skill: log
edges:
  - from: plan
    to: done
`)
	exec, err := f.engine.Start(context.Background(), wf, "任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("缺变量不应让节点失败(可能是合法可选变量),实际 %s (%s)", final.State, final.Error)
	}

	var warned bool
	var missing []string
	for _, ev := range f.bus.History() {
		if ev.Type != "node.warning" {
			continue
		}
		warned = true
		if ev.NodeID == "plan" {
			missing, _ = ev.Data["missing"].([]string)
		}
	}
	if !warned {
		t.Fatal("缺失模板变量应发出 node.warning 事件")
	}
	found := false
	for _, m := range missing {
		if strings.Contains(m, "revwie") {
			found = true
		}
	}
	if !found {
		t.Errorf("告警应点名缺失变量 revwie,实际 %v", missing)
	}
}

// TestNoWarningWhenTemplateComplete 回归:变量齐全时不得产生噪音告警。
func TestNoWarningWhenTemplateComplete(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, `
workflow:
  id: tpl-ok
  name: 模板齐全
nodes:
  - id: plan
    name: Plan
    type: agent
    agent: mock-claude
    mode: plan
    prompt: "任务={{task}} 轮次={{iteration}}"
  - id: done
    name: Done
    type: skill
    skill: log
edges:
  - from: plan
    to: done
`)
	exec, err := f.engine.Start(context.Background(), wf, "任务", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	for _, ev := range f.bus.History() {
		if ev.Type == "node.warning" {
			t.Errorf("变量齐全时不应告警: %v", ev.Data)
		}
	}
}

// ---- A4:并行分支失败级联 ----

// slowFailAgent:第一个实例所在分支快速失败,另一个所在分支会长时间运行,
// 用于验证失败后兄弟分支被取消(而不是继续跑完)。
type slowFailAgent struct {
	id      string
	delay   time.Duration
	fail    bool
	calls   atomic.Int64
	started chan struct{}
	once    sync.Once
}

func (a *slowFailAgent) ID() string   { return a.id }
func (a *slowFailAgent) Name() string { return a.id }

func (a *slowFailAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	a.calls.Add(1)
	a.once.Do(func() { close(a.started) })
	select {
	case <-time.After(a.delay):
	case <-ctx.Done():
		// 被取消:这正是我们要的行为
		return nil, ctx.Err()
	}
	if a.fail {
		return &coreagent.AgentResponse{
			Status:  coreagent.StatusFailed,
			Error:   "分支失败",
			Summary: "failed on purpose",
		}, nil
	}
	return &coreagent.AgentResponse{Status: coreagent.StatusSuccess, Summary: "ok"}, nil
}

func TestParallelFailureCancelsSiblings(t *testing.T) {
	repo, err := persistence.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })

	fast := &slowFailAgent{id: "fast-fail", delay: 30 * time.Millisecond, fail: true, started: make(chan struct{})}
	slow := &slowFailAgent{id: "slow-ok", delay: 30 * time.Second, started: make(chan struct{})}

	bus := event.NewBus()
	bus.KeepHistory = true
	agents := coreagent.NewRegistry()
	for _, a := range []coreagent.Agent{fast, slow} {
		if err := agents.Register(a); err != nil {
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
	for _, id := range []string{"fast-fail", "slow-ok"} {
		eng.Perms.Set(id, permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	}

	wf := loadWfYAML(t, eng, `
workflow:
  id: par-cascade
  name: 并行失败级联
nodes:
  - id: fanout
    type: parallel
  - id: bad
    type: agent
    agent: fast-fail
    mode: plan
  - id: slow
    type: agent
    agent: slow-ok
    mode: plan
  - id: merge
    type: merge
edges:
  - from: fanout
    to: bad
  - from: fanout
    to: slow
  - from: bad
    to: merge
  - from: slow
    to: merge
`)

	start := time.Now()
	exec, err := eng.Start(context.Background(), wf, "并行失败", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	elapsed := time.Since(start)

	if final.State != model.ExecutionFailed {
		t.Fatalf("分支失败后执行应 FAILED,实际 %s", final.State)
	}
	// 关键:慢分支(30s)必须被取消。若无级联,执行会等满 30s。
	if elapsed > 10*time.Second {
		t.Errorf("慢分支未被取消,执行耗时 %v(应远小于慢分支的 30s)", elapsed)
	}
	// 取消后不应被记为"成功"
	if final.NodeStates["slow"] == string(model.NodeSuccess) {
		t.Errorf("被取消的分支不应记为 SUCCESS,实际 %q", final.NodeStates["slow"])
	}
}

// ---- A5:merge 汇总口径 ----

// TestMergeReportsSkippedBranch 验证分支被 enabled:false 跳过时,
// merge 不再输出"全部成功"。此前 StatusKey 查不到即算成功。
func TestMergeReportsSkippedBranch(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, `
workflow:
  id: merge-skip
  name: 分支跳过
nodes:
  - id: fanout
    type: parallel
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
  - id: b
    type: agent
    agent: mock-claude
    mode: plan
    enabled: false
  - id: merge
    type: merge
  - id: done
    type: skill
    skill: log
edges:
  - from: fanout
    to: a
  - from: fanout
    to: b
  - from: a
    to: merge
  - from: b
    to: merge
  - from: merge
    to: done
`)
	exec, err := f.engine.Start(context.Background(), wf, "跳过分支", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("主动跳过的分支不应让执行失败,实际 %s (%s)", final.State, final.Error)
	}
	if got := final.NodeStates["b"]; got != string(model.NodeSkipped) {
		t.Errorf("enabled:false 的分支应记为 SKIPPED,实际 %q", got)
	}
	// merge 摘要必须体现"跳过",而不是"全部成功"
	res := findNodeResult(t, f.repo, exec.ID, "merge")
	if strings.Contains(res, "全部成功") {
		t.Errorf("存在被跳过的分支时 merge 不应报'全部成功',实际 %q", res)
	}
	if !strings.Contains(res, "跳过") {
		t.Errorf("merge 摘要应标注跳过分支,实际 %q", res)
	}
}

// findNodeResult 读取某节点的明细结果 JSON。
func findNodeResult(t *testing.T, repo *persistence.DB, execID, nodeID string) string {
	t.Helper()
	nodes, err := repo.ListExecutionNodes(execID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	for _, n := range nodes {
		if n.NodeID == nodeID {
			return n.ResultJSON
		}
	}
	t.Fatalf("未找到节点 %s 的明细", nodeID)
	return ""
}

// ---- A6:mock 按执行隔离 ----

// TestMockIsolatedPerExecution 验证同一工作流并发执行两次时,
// 两次执行的 mock 决策序列互不干扰。此前共享计数 + Start 时 ResetMock
// 会让第二次执行清掉第一次的序列,出现"第二次的第 N 次调用拿到第 1 次
// 的决策"。
func TestMockIsolatedPerExecution(t *testing.T) {
	repo, err := persistence.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })

	// review 决策序列:REJECTED → APPROVED。两次并发执行各自应从
	// REJECTED 开始,而不是其中一次直接从 APPROVED 开始。
	planAgt := mock.New(mock.Options{ID: "mock-claude", Scripts: map[string]*mock.Script{
		"plan":   {SummaryTemplate: "计划"},
		"review": {Decisions: []coreagent.AgentDecision{coreagent.DecisionRejected, coreagent.DecisionApproved}},
	}})
	pagt := mock.New(mock.Options{ID: "mock-pi", Scripts: map[string]*mock.Script{
		"execute": {SummaryTemplate: "执行"},
		"fix":     {SummaryTemplate: "修复"},
	}})
	bus := event.NewBus()
	agents := coreagent.NewRegistry()
	_ = agents.Register(planAgt)
	_ = agents.Register(pagt)
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		_ = skills.Register(sk)
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true, GitRead: true})
	eng.Perms.Set("mock-pi", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	wf := loadWfYAML(t, eng, loopWorkflowYAML)

	// 先后启动两次(不 ResetMock 干扰)
	e1, err := eng.Start(context.Background(), wf, "第一次", nil)
	if err != nil {
		t.Fatalf("start 1: %v", err)
	}
	e2, err := eng.Start(context.Background(), wf, "第二次", nil)
	if err != nil {
		t.Fatalf("start 2: %v", err)
	}
	waitForState(t, eng, e1.ID, model.ExecutionCompleted, model.ExecutionFailed)
	waitForState(t, eng, e2.ID, model.ExecutionCompleted, model.ExecutionFailed)

	// 每次执行的 review 都应恰好调用 2 次(REJECTED 一轮 + APPROVED 一轮)
	for _, e := range []string{e1.ID, e2.ID} {
		if got := planAgt.CallsFor(e, "review"); got != 2 {
			t.Errorf("执行 %s 的 review 调用 %d 次,应为 2(两次执行的序列必须独立)", e, got)
		}
		if got := pagt.CallsFor(e, "fix"); got != 1 {
			t.Errorf("执行 %s 的 fix 调用 %d 次,应为 1", e, got)
		}
	}
}

// TestMockSharedSpaceStillResets 回归:无 ExecutionID 的直接调用
// (单测 / Agents 页 Test)仍走共享空间,ResetMock 照旧清零。
func TestMockSharedSpaceStillResets(t *testing.T) {
	m := mock.New(mock.Options{ID: "m", Scripts: map[string]*mock.Script{
		"review": {Decisions: []coreagent.AgentDecision{coreagent.DecisionRejected, coreagent.DecisionApproved}},
	}})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _ = m.Execute(ctx, coreagent.AgentRequest{Mode: "review"})
	}
	if m.Calls("review") != 2 {
		t.Fatalf("共享空间调用 = %d", m.Calls("review"))
	}
	m.ResetMock()
	if m.Calls("review") != 0 {
		t.Errorf("ResetMock 应清空共享空间,实际 %d", m.Calls("review"))
	}
	// 重置后第一次调用应重新拿到序列第一个决策
	resp, _ := m.Execute(ctx, coreagent.AgentRequest{Mode: "review"})
	if resp.Decision != coreagent.DecisionRejected {
		t.Errorf("重置后应从序列首个决策开始,实际 %s", resp.Decision)
	}
}

var _ = dsl.Parse

// ---- B2:并行分支不收敛必须告警 ----

// TestNonConvergingParallelWarns 验证并行分支不收敛时执行仍能跑完各分支,
// 但必须发出 runtime.warning 告警说明"分支之后的链不会执行"。
// 此前塞一个合成的 `fanout.merge` 占位节点(落到 lower 变 noop),
// 后续链被静默丢弃,执行报成功。
func TestNonConvergingParallelWarns(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, `
workflow:
  id: diverging
  name: 不收敛并行
nodes:
  - id: fanout
    type: parallel
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
  - id: b
    type: agent
    agent: mock-claude
    mode: plan
  - id: tail_a
    type: skill
    skill: log
  - id: tail_b
    type: skill
    skill: log
edges:
  - from: fanout
    to: a
  - from: fanout
    to: b
  - from: a
    to: tail_a
  - from: b
    to: tail_b
`)
	exec, err := f.engine.Start(context.Background(), wf, "不收敛", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("不收敛不应让执行失败(各分支照常跑),实际 %s (%s)", final.State, final.Error)
	}
	// 各分支都跑到了
	for _, id := range []string{"a", "b", "tail_a", "tail_b"} {
		if final.NodeStates[id] != string(model.NodeSuccess) {
			t.Errorf("节点 %s 应执行完成,实际 %q", id, final.NodeStates[id])
		}
	}
	// 但必须有告警
	warned := false
	for _, ev := range f.bus.History() {
		if ev.Type == "runtime.warning" && ev.NodeID == "fanout" {
			warned = true
		}
	}
	if !warned {
		t.Error("并行分支不收敛必须发 runtime.warning 告警(此前静默丢弃后续链)")
	}
}

// ---- 状态类型稳定性(轮次 29 第 1 项)----

// TestStateTypesStableAcrossPersistence 类型在持久化往返前后必须一致。
//
// 背景:StateData 执行期来自 ADK 内存(int),经 SQLite state_json 往返后
// encoding/json 把数字全解成 float64 —— 同一字段的类型随生命周期变化,
// 于是每个读取点都得写双类型兼容(compiler.IntOfOk 就是这么来的),
// 漏一处就是静默 bug:轮次 10/24/25/27 都踩过"循环计数回落导致过早触发
// 循环保护"。现在在解码入口(model.NormalizeStateJSON)钉死一次。
//
// 本用例**直接断言 int 而不经过 IntOfOk** —— 归一失效时会立刻失败。
func TestStateTypesStableAcrossPersistence(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{
		coreagent.DecisionRejected, coreagent.DecisionApproved,
	})
	wf := loadWf(t, f.engine, loopWorkflowYAML)
	exec, err := f.engine.Start(context.Background(), wf, "类型稳定", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)

	// 从库里读回(走完整的 JSON 往返)
	got, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, key := range []string{compiler.KeyLoopCount, "node:review:attempt"} {
		v, ok := got.StateData[key]
		if !ok {
			t.Errorf("状态键 %q 丢失", key)
			continue
		}
		if _, isInt := v.(int); !isInt {
			t.Errorf("状态键 %q 往返后应为 int,实际 %T(%v) —— "+
				"归一生效前这里是 float64,读取点必须写双类型兼容",
				key, v, v)
		}
	}
	// 列表路径同样稳定(Executions 页/Dashboard 走它)
	list, err := f.repo.ListExecutions("", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range list {
		if v, ok := e.StateData[compiler.KeyLoopCount]; ok {
			if _, isInt := v.(int); !isInt {
				t.Errorf("列表路径的 %q 应为 int,实际 %T", compiler.KeyLoopCount, v)
			}
		}
	}
}

// TestIntOfOkStillCoversDefensivePath 回归:IntOfOk 本身不能删 ——
// 内存态(collectResult 直接从 ADK 读)与归一后的读回态并存,
// 保留双类型兼容作为纵深防御,只是不再是"每个读取点都必须记得写"。
func TestIntOfOkStillCoversDefensivePath(t *testing.T) {
	for _, v := range []any{3, int64(3), float64(3)} {
		n, ok := compiler.IntOfOk(v)
		if !ok || n != 3 {
			t.Errorf("IntOfOk(%T(%v)) = %d,%v;want 3,true", v, v, n, ok)
		}
	}
	if _, ok := compiler.IntOfOk("x"); ok {
		t.Error("IntOfOk 对非数值应返回 false")
	}
}

// ---- 派生视图一致性(轮次 29 #S3/#S5)----

// TestSyncNodeStatesRebuildsFromScratch 验证派生视图是**全量重建**。
//
// 此前是"有则覆盖":StateData 里被 delete 掉的 key,其旧值会永远留在
// NodeStates 里。RetryNode(skip=false) 正是 `delete(StateData, StatusKey)`
// —— 旧代码下节点在 UI 上仍显示上一次的 FAILED,直到下一次成功执行。
func TestSyncNodeStatesRebuildsFromScratch(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, loopWorkflowYAML)
	exec, err := f.engine.Start(context.Background(), wf, "全量重建", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)

	// 手工构造"StateData 已删 key、NodeStates 残留旧值"的场景:
	// 取一个真实节点(在 Snapshot.Nodes 里),把它的 StatusKey 从 StateData
	// 删掉 —— 这正是 RetryNode(skip=false) 干的事。
	target := ""
	for _, n := range wf.Nodes {
		if n.ID == "fix" {
			target = n.ID
		}
	}
	if target == "" {
		t.Fatal("fixture 缺少 fix 节点")
	}
	got, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.NodeStates[target] = string(model.NodeFailed) // 残留旧值
	delete(got.StateData, compiler.StatusKey(target))
	if err := f.repo.SaveExecution(got); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 触发一次派生重建:再跑一次同步路径不方便,直接验证同一条不变量 ——
	// 全量重建的结果里,StateData 已无的节点不得出现在视图中。
	// 这里用 Engine 的公开行为:重放一次执行会让 collectResult 重建视图。
	back, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get back: %v", err)
	}
	// 记录:重建只遍历 Snapshot.Nodes,因此"不属于工作流的 key"本就不该
	// 被写进视图;而"属于工作流但 StateData 已删"的 key,重建后必然消失。
	// 手工校验重建函数的不变量(直接构造最小 Execution 调用它)。
	e2 := f.engine
	probe := &model.Execution{
		NodeStates: map[string]string{target: string(model.NodeFailed), "stale": "FAILED"},
		Iterations: map[string]int{"loop": 99},
		StateData:  map[string]any{},
		Snapshot:   wf,
	}
	e2.syncNodeStates(probe)
	if _, still := probe.NodeStates[target]; still {
		t.Error("StateData 已删的节点状态仍残留在视图中(增量覆盖而非全量重建)")
	}
	if len(probe.NodeStates) != 0 {
		t.Errorf("全量重建应得到空视图,实际 %v", probe.NodeStates)
	}
	if len(probe.Iterations) != 0 {
		t.Errorf("Iterations 也应全量重建,实际 %v", probe.Iterations)
	}
	_ = back
	_ = final
}

// TestNilStateDoesNotBecomeLiteralNil 验证 Delete 伪造的 nil 不会被
// 渲染成字面量 "<nil>" 当成节点状态。
//
// 背景:stateAccess.Delete 以 `Set(key, nil)` 伪造删除(ADK 的 State 只有
// Get/Set/All),Get 把 nil 当缺失。若派生时 `fmt.Sprint(nil)`,UI 上会出现
// 状态为 "<nil>" 的节点。
func TestNilStateDoesNotBecomeLiteralNil(t *testing.T) {
	f := newFixture(t, []coreagent.AgentDecision{coreagent.DecisionApproved})
	wf := loadWf(t, f.engine, loopWorkflowYAML)
	exec, err := f.engine.Start(context.Background(), wf, "nil 检查", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, f.engine, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)

	got, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 模拟 Delete 的效果:StateData 里某个 StatusKey 被置 nil
	got.StateData[compiler.StatusKey("plan")] = nil
	if err := f.repo.SaveExecution(got); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := f.repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get back: %v", err)
	}
	if v, ok := back.NodeStates["plan"]; ok && v == "<nil>" {
		t.Error("nil 状态被渲染成字面量 \"<nil>\"")
	}
	for id, v := range back.NodeStates {
		if v == "<nil>" {
			t.Errorf("节点 %s 的状态是字面量 \"<nil>\"", id)
		}
	}
}
