package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
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

// TestPanicInsideParallelBranchDoesNotKillProcess 轮次 29(#84):
// 并行分支内的 panic 必须被兜住,不能杀掉整个进程。
//
// ADK 的 parallelagent 在**自己的** goroutine 里跑各分支(errgroup.Go),
// 而 recover() 在 adk 生产代码中零命中 —— 分支内的 panic 绕过 runtime.execute
// 的 defer recover()(那只覆盖执行协程),直接杀进程,违反 AGENTS.md 硬性
// 约定 #12(桌面模式即整应用退出)。
//
// 修复前实测:`panic: boom from agent adapter`,栈顶
// `created by errgroup.(*Group).Go`,测试进程被杀。
func TestPanicInsideParallelBranchDoesNotKillProcess(t *testing.T) {
	repo, err := persistence.Open(t.TempDir() + "/t.db")
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
	if err := agents.Register(mock.New(mock.Options{ID: "mock-claude"})); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("panic-agent", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	eng.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: par-panic
  name: 并行 panic
nodes:
  - id: fanout
    type: parallel
  - id: bad
    type: agent
    agent: panic-agent
    mode: plan
  - id: ok
    type: agent
    agent: mock-claude
    mode: plan
  - id: merge
    type: merge
edges:
  - from: fanout
    to: bad
  - from: fanout
    to: ok
  - from: bad
    to: merge
  - from: ok
    to: merge
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}

	// 能走到这里说明进程没被 panic 杀掉 —— 这就是本用例的核心断言
	exec, err := eng.Start(context.Background(), wf, "分支 panic", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Fatalf("分支 panic 后执行应 FAILED,实际 %s (%s)", final.State, final.Error)
	}
	// 失败原因要指名道姓,而不是一句空错误
	if !strings.Contains(final.Error, "panic") {
		t.Errorf("错误信息应说明 panic,实际 %q", final.Error)
	}
	// 更严:失败原因不得被**兄弟分支的取消错误**顶掉。
	//
	// 并行区的失败级联(compiler 的 parallelCascadeAgent)在任一分支失败后
	// 取消共享 ctx,而 ADK 的 parallelagent 在分支 ctx 被取消时会把该分支
	// 的**终态事件**一起丢掉(parallelagent.runSubAgent 的 select:
	// `<-ctx.Done()` 与 `results <- …` 同时就绪时随机选一个)。失败信息只
	// 存在于事件里的 StateDelta(节点状态的唯一来源),事件一丢,真因就整段
	// 消失,用户只看到 "failed to run sub-agent …: context canceled"
	// ——真因被掩盖(约定 #14)。
	if strings.Contains(final.Error, "context canceled") {
		t.Errorf("失败原因不应被兄弟分支的取消错误污染,实际 %q", final.Error)
	}
	// 失败原因必须指向真正出问题的节点
	if !strings.Contains(final.Error, "bad") {
		t.Errorf("失败原因应指出出问题的节点 bad,实际 %q", final.Error)
	}
	if got := final.NodeStates["bad"]; got != string(model.NodeFailed) {
		t.Errorf("出问题的节点应记 FAILED,实际 %q", got)
	}
	// 必须发出 node.failed 事件
	sawFailed := false
	for _, ev := range bus.History() {
		if ev.Type == event.NodeFailed && ev.NodeID == "bad" {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Error("应发出 node.failed 事件(node=bad)")
	}
}

// slowThenPanicAgent 先慢跑(给检查点落库留时间),随后 panic —— 模拟
// "Agent 跑着跑着炸了"的真实场景。
type slowThenPanicAgent struct{ inner *mock.Agent }

func (a *slowThenPanicAgent) ID() string   { return a.inner.ID() }
func (a *slowThenPanicAgent) Name() string { return a.inner.Name() }
func (a *slowThenPanicAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	select {
	case <-time.After(300 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	panic("slow agent blew up")
}

// TestCrashMidExecutionStillLocatesFailedNode 轮次 29(#85):
// 运行中崩溃后,RetryNode 必须能定位失败节点。
//
// 此前 CurrentNodeID 只存在于内存,运行中从不落库 → 崩溃后库中仍是
// Start 时的空值 → RecoverPending 标 FAILED → RetryNode 恒返回
// NO_FAILED_NODE("无法定位失败节点")。也就是说崩溃恢复后**无法从失败处
// 重试**,用户只能整条重跑。
//
// 注意:现有的崩溃模拟用例(executor_node_test.go)是把执行**跑完**再改库
// 状态,current_node_id 早已被正常路径写过,因此覆盖不到真实场景。
func TestCrashMidExecutionStillLocatesFailedNode(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "crash.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()

	agents := coreagent.NewRegistry()
	slow := &slowThenPanicAgent{inner: mock.New(mock.Options{ID: "slow-boom"})}
	if err := agents.Register(slow); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("slow-boom", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: crash-locate
  name: 崩溃定位
nodes:
  - id: work
    type: agent
    agent: slow-boom
    mode: plan
  - id: done
    type: skill
    skill: log
edges:
  - from: work
    to: done
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	exec, err := eng.Start(context.Background(), wf, "崩溃定位", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// 关键断言点在**节点仍在跑的时候**:真崩溃(进程被强杀)不会有任何
	// 收尾代码执行,只有运行期检查点能把"卡在哪个节点"写进库。
	//
	// 窗口必须比 Agent 的存活时间(300ms)短,否则会等到 panic 收尾之后
	// 才断言 —— 那时 failExecution → save(exec) 会顺手把内存里的
	// CurrentNodeID 写进去,测到的就不是真崩溃路径了。
	// 实测(无检查点时):t=80/160/240ms 期间 current_node_id 恒为空串,
	// 直到 t=320ms panic 被兜住后才变成 "work"。
	deadline := time.Now().Add(250 * time.Millisecond)
	var locatedEarly bool
	for time.Now().Before(deadline) {
		if e, err := eng.Repo.GetExecution(exec.ID); err == nil && e.CurrentNodeID == "work" {
			locatedEarly = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locatedEarly {
		t.Error("节点执行期间 current_node_id 未落库 —— 真崩溃(无收尾代码)后" +
			"无法定位失败节点,RetryNode 会返回 NO_FAILED_NODE")
	}

	// 兜住 panic 后执行应落 FAILED(这一段与检查点无关,是 #84 的行为)
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Fatalf("应 FAILED,实际 %s", final.State)
	}
}

// quickNode 立刻返回成功的 Agent(用于构造多节点快速链)。
type quickNode struct{ id string }

func (a *quickNode) ID() string   { return a.id }
func (a *quickNode) Name() string { return a.id }
func (a *quickNode) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	// 每节点 30ms:让 4 个节点串起来约 120ms —— 远小于 2s 节流窗口,
	// 却足够测试轮询观察中间节点
	select {
	case <-time.After(30 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &coreagent.AgentResponse{Status: coreagent.StatusSuccess, Decision: coreagent.DecisionDone}, nil
}

// TestCheckpointFollowsFastNodeChanges 轮次 31(#86):检查点必须跟随
// "当前在跑哪个节点",即使节点切换得比 2s 节流还快。
//
// 缺陷(本项目自己引入):maybeCheckpoint 用 Exec.CurrentNodeID 判断
// "是否换了节点",但调用点在此**之前**已 SetCurrentNode(nodeID) ——
// 两者恒等,"换节点立即落盘"这条分支永远走不到,节流退化成纯 2s 定时。
// 后果:快速链里第 2..N 个节点都不落库,崩溃时 current_node_id 仍指向
// 第 1 个节点,RetryNode 从错误的位置重试。
//
// 单节点用例覆盖不到(首个节点必然触发落库),故这里用 4 个秒级完成的
// 节点:全程远小于 2s 节流窗口。
func TestCheckpointFollowsFastNodeChanges(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "ck.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()

	agents := coreagent.NewRegistry()
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		if err := agents.Register(&quickNode{id: id}); err != nil {
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
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		eng.Perms.Set(id, permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	}

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: fast-chain
  name: 快速链
nodes:
  - id: n1
    type: agent
    agent: n1
    mode: plan
  - id: n2
    type: agent
    agent: n2
    mode: plan
  - id: n3
    type: agent
    agent: n3
    mode: plan
  - id: n4
    type: agent
    agent: n4
    mode: plan
  - id: tail
    type: skill
    skill: log
edges:
  - from: n1
    to: n2
  - from: n2
    to: n3
  - from: n3
    to: n4
  - from: n4
    to: tail
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	exec, err := eng.Start(context.Background(), wf, "快速链", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 关键:必须在**执行过程中**观察,而不是执行结束后 ——
	// 收尾的 save(exec) 会把内存里的 CurrentNodeID 一并写入,无论检查点
	// 是否生效,执行结束后都是 "tail",测不出差异。
	//
	// 做法:让链中途的某个节点阻塞,由测试在它运行时读库。
	// 期望:读到的是**当前正在跑的那个节点**,而不是链首。
	seen := map[string]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e, err := eng.Repo.GetExecution(exec.ID)
		if err != nil {
			break
		}
		if e.CurrentNodeID != "" {
			seen[e.CurrentNodeID] = true
		}
		if e.State == model.ExecutionCompleted || e.State == model.ExecutionFailed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)

	// 检查点必须观察到**不止链首**的节点:若被 2s 节流卡住,4 个秒级节点
	// 全程只会看到 n1,崩溃后从 n1 重试,把后面几个白跑一遍。
	// 坏实现(拿 Exec.CurrentNodeID 比较)只观察到 {n1, tail}:n1 是首次落库,
	// tail 是收尾 save 写的,中间的 n2/n3/n4 全被 2s 节流吃掉 ——
	// 崩溃发生在 n3 时库里仍指向 n1,RetryNode 从 n1 重试。
	for _, want := range []string{"n1", "n2", "n3", "n4"} {
		if !seen[want] {
			t.Errorf("检查点从未观察到节点 %s(实际观察到 %v) —— "+
				"换节点未立即落盘,节流退化成纯 2s 定时,崩溃后会从链首重试",
				want, seen)
		}
	}
}

// panickingSkill 调用即 panic,用于验证 Skill 节点跑在**并行分支内**时
// 进程依然存活。
//
// 如实说明:这一路径实际已被 runNodeGuarded 覆盖(实测关掉 lower 出口的
// 兜底层,本用例仍通过)。保留它是因为"Skill 在并行分支内 panic"是最容易
// 触发的真实场景,值得持续守住;lower 出口那层兜底覆盖的是本用例触不到的
// 代码(结构体 Agent 内部的表达式求值等),属纵深防御。
type panickingSkill struct {
	mu    sync.Mutex
	calls int
}

func (s *panickingSkill) ID() string          { return "panic-skill" }
func (s *panickingSkill) Name() string        { return "Panic Skill" }
func (s *panickingSkill) Description() string { return "test" }

func (s *panickingSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if n >= 1 {
		panic("skill blew up on call " + itoaForTest(n))
	}
	return &skill.SkillResponse{Status: "SUCCESS", Output: "ok"}, nil
}

func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestPanicInsideParallelBranchSkillIsContained 验证结构体 Agent 的 panic
// 也被兜住:Skill 节点跑在并行分支内时 panic,进程必须存活。
func TestPanicInsideParallelBranchSkillIsContained(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "sk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()
	bus.KeepHistory = true

	agents := coreagent.NewRegistry()
	if err := agents.Register(mock.New(mock.Options{ID: "mock-claude"})); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatal(err)
		}
	}
	if err := skills.Register(&panickingSkill{}); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	eng.Perms.Set("workflow", permission.Policy{FilesystemRead: true, GitRead: true, GitCommit: true})

	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: par-skill-panic
  name: 并行 skill panic
nodes:
  - id: fanout
    type: parallel
  - id: ok
    type: agent
    agent: mock-claude
    mode: plan
  - id: boom
    type: skill
    skill: panic-skill
  - id: merge
    type: merge
edges:
  - from: fanout
    to: ok
  - from: fanout
    to: boom
  - from: ok
    to: merge
  - from: boom
    to: merge
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}

	// 进程存活即核心断言
	exec, err := eng.Start(context.Background(), wf, "skill panic", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Errorf("Skill panic 后执行应 FAILED,实际 %s (%s)", final.State, final.Error)
	}
	// 失败原因必须说明是 Skill 炸了(此前这里只 t.Logf,等于没断言:
	// 并行区的级联取消会让失败分支的终态事件与取消赛跑,输的那条事件
	// 整段丢失,真因会退化成兄弟分支的 "context canceled" —— 约定 #14)。
	if !strings.Contains(final.Error, "panic") && !strings.Contains(final.Error, "blown") {
		t.Errorf("错误信息应说明 Skill panic,实际 %q", final.Error)
	}
	if strings.Contains(final.Error, "context canceled") {
		t.Errorf("失败原因不应被兄弟分支的取消错误污染,实际 %q", final.Error)
	}
	if final.NodeStates["boom"] != string(model.NodeFailed) {
		t.Errorf("出问题的 Skill 节点应记 FAILED,实际 %q", final.NodeStates["boom"])
	}
}
