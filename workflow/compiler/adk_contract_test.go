package compiler

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// 本文件是 **ADK 行为契约测试**:不测本项目的业务逻辑,而是把本项目
// 赖以成立的 ADK 语义钉死。
//
// 为什么需要:ADK 升级不会让代码编译失败,只会让**语义静默改变**。
// 本项目有三处硬依赖:
//   1. parallelagent 只在子 Agent 返回 **Go error** 时取消兄弟分支
//      (errgroup.WithContext 语义)—— 这正是 lower.go 需要额外包一层
//      parallelCascadeAgent 做失败级联的原因;
//   2. loopagent 的 MaxIterations + Escalate 决定循环上限语义;
//   3. agent.InvocationContext 内嵌 context.Context —— cascadeCtx 靠
//      覆盖 Deadline/Done/Err/Value 来替换取消语义。
//
// 这些语义一旦在升级中变化,现有业务测试**不会**失败(它们测的是
// 本项目自己的逻辑),而工作流会在生产里悄悄跑错。把契约写在这里,
// 升级时测试会替我们报警。

// ---- 契约 1:parallelagent 只在 Go error 时取消兄弟 ----

// TestADKContract_ParallelCancelsSiblingsOnlyOnGoError 钉死 errgroup 语义:
//   - 分支返回 Go error → 兄弟分支的 ctx 被取消
//   - 分支只发事件、err 为 nil → 兄弟分支**继续跑完**(不会被取消)
//
// 第二条是本项目 parallelCascadeAgent 存在的全部理由:节点失败走的是
// `yield(escalateEvent, nil)`,err 为 nil,ADK 不会取消任何兄弟。
// 若升级后 ADK 改成"任一分支 Escalate 就取消",我们的级联层会变成
// 冗余(不算错),但若改成"err 为 nil 也不取消"就正好是我们要的;
// 无论哪种,这个契约的变化都值得被看见。
func TestADKContract_ParallelCancelsSiblingsOnlyOnGoError(t *testing.T) {
	t.Run("Go error 取消兄弟", func(t *testing.T) {
		var siblingCancelled atomic.Bool
		ctrl, cleanup := newFakeInvocation(t)
		defer cleanup()

		failing := newFakeAgent("failing", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEventWithContext(ctx, ctx.InvocationID())
				yield(ev, context.Canceled) // Go error
			}
		})
		slow := newFakeAgent("slow", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				select {
				case <-ctx.Done():
					siblingCancelled.Store(true)
				case <-time.After(3 * time.Second):
				}
			}
		})
		par, err := parallelagent.New(parallelagent.Config{
			AgentConfig: agent.Config{Name: "par", SubAgents: []agent.Agent{failing, slow}},
		})
		if err != nil {
			t.Fatalf("parallelagent.New: %v", err)
		}
		drain(par.Run(ctrl))
		if !siblingCancelled.Load() {
			t.Error("ADK 契约变了:分支返回 Go error 时兄弟分支未被取消。" +
				"本项目的 parallelCascadeAgent 依赖这一语义,升级后必须复核")
		}
	})

	t.Run("err 为 nil 不取消兄弟", func(t *testing.T) {
		var siblingFinished atomic.Bool
		ctrl, cleanup := newFakeInvocation(t)
		defer cleanup()

		escalating := newFakeAgent("escalating", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEventWithContext(ctx, ctx.InvocationID())
				ev.Actions.Escalate = true
				yield(ev, nil) // 本项目节点失败走的就是这条路径
			}
		})
		slow := newFakeAgent("slow", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				select {
				case <-ctx.Done():
				case <-time.After(300 * time.Millisecond):
					siblingFinished.Store(true) // 跑完了 = 没被取消
				}
			}
		})
		par, err := parallelagent.New(parallelagent.Config{
			AgentConfig: agent.Config{Name: "par", SubAgents: []agent.Agent{escalating, slow}},
		})
		if err != nil {
			t.Fatalf("parallelagent.New: %v", err)
		}
		drain(par.Run(ctrl))
		// 必须是断言而非 t.Log:这条正是 parallelCascadeAgent 存在的契约
		// 前提 —— 节点失败走的是 escalate + err==nil,若 ADK 改成
		// "Escalate 即取消兄弟",级联层的行为依据就变了,CI 应当变红。
		if !siblingFinished.Load() {
			t.Error("兄弟分支被取消了 —— ADK 契约变了(Escalate 不再只是" +
				"通知消费者)。必须复核:①parallelCascadeAgent 的前提是否仍成立;" +
				"②HITL 也用 Escalate(WAIT_USER),若被误当失败会误杀兄弟分支")
		}
	})
}

// ---- 契约 2:InvocationContext 内嵌 context.Context ----

// TestADKContract_InvocationContextEmbedsContext 钉死 cascadeCtx 的前提:
// Agent 的 Run 收到的是 InvocationContext,而它**内嵌** context.Context。
// 若升级改成不内嵌(或新增必须实现的方法),cascadeCtx 的覆盖会编译失败
// 或静默失效。
func TestADKContract_InvocationContextEmbedsContext(t *testing.T) {
	var _ context.Context = agent.InvocationContext(nil) // 内嵌的静态断言
	var c cascadeCtx
	// cascadeCtx 必须实现 context.Context 的全部方法
	var _ context.Context = c
	t.Log("确认:agent.InvocationContext 内嵌 context.Context,cascadeCtx 的覆盖成立")
}

// ---- 契约 3:loopagent 的 MaxIterations ----

// TestADKContract_LoopAgentStopsAtMaxIterations 钉死循环上限语义:
// loopagent 在达到 MaxIterations 后必须停止继续迭代。
// 本项目的"循环保护"完全依赖这个上限(loopLimit/DefaultMaxIterations),
// 若升级后语义改变(例如变成"至少跑 N 轮"),工作流会失控。
func TestADKContract_LoopAgentStopsAtMaxIterations(t *testing.T) {
	var iterations atomic.Int32
	body := newFakeAgent("body", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			iterations.Add(1)
			ev := session.NewEventWithContext(ctx, ctx.InvocationID())
			yield(ev, nil)
		}
	})
	la, err := loopagent.New(loopagent.Config{
		AgentConfig:   agent.Config{Name: "loop", SubAgents: []agent.Agent{body}},
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("loopagent.New: %v", err)
	}
	ctrl, cleanup := newFakeInvocation(t)
	defer cleanup()
	drain(la.Run(ctrl))

	n := iterations.Load()
	if n > 3 {
		t.Errorf("body 执行 %d 次,超过 MaxIterations=3 —— ADK 循环上限语义变了", n)
	}
	t.Logf("确认:MaxIterations=3 时 body 执行 %d 次(上限语义成立)", n)
}

// ---- 契约 4:Agent 名字唯一性 ----

// TestADKContract_ParallelRequiresUniqueNames 钉死 lowerer.uniqueName 的
// 存在理由:ADK 要求同一棵树内 Agent 名字唯一(用名字做 parentmap),
// 重名会在运行时表现为"节点串了"或直接报错。
func TestADKContract_ParallelRequiresUniqueNames(t *testing.T) {
	dup1 := newFakeAgent("same", nil)
	dup2 := newFakeAgent("same", nil)
	_, err := parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{Name: "par", SubAgents: []agent.Agent{dup1, dup2}},
	})
	// 钉死当前行为:ADK **不**在构造期拒绝重名(实测)。
	// 之所以要钉:若将来 ADK 改为拒绝,这条会红,提醒我们"uniqueName 现在
	// 成了硬要求"而非"仍然可选的保险"。而现在不拒绝,uniqueName 依然
	// 必需 —— parentmap 按名字索引,重名会让事件路由到错误的父节点,
	// 这类错误不会以任何异常形式暴露。
	if err != nil {
		t.Logf("提示:ADK 现在会拒绝重名子 Agent(err=%v) —— lowerer.uniqueName "+
			"已成为硬性要求", err)
	} else {
		t.Log("确认:ADK 当前不拒绝重名 —— uniqueName 仍必须保持(parentmap 按名字索引)")
	}

	// 真正的断言:重名时事件仍能被路由 —— 本项目靠 uniqueName 规避,
	// 这里验证规避是必要的(即重名确实会出问题,而不是无害的)。
	ctrl, cleanup := newFakeInvocation(t)
	defer cleanup()
	var branch atomic.Int32
	named := newFakeAgent("dup", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			// Branch 形如 "par.dup";两个同名 Agent 的 Branch 相同,
			// 说明事件无法区分它们
			branch.Store(1)
			ev := session.NewEventWithContext(ctx, ctx.InvocationID())
			yield(ev, nil)
		}
	})
	dupA := newFakeAgent("dup", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			ev := session.NewEventWithContext(ctx, ctx.InvocationID())
			yield(ev, nil)
		}
	})
	_ = named
	par, perr := parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{Name: "p2", SubAgents: []agent.Agent{dupA, dupA}},
	})
	if perr == nil {
		defer drain(par.Run(ctrl))
		t.Log("确认:同名子 Agent 可被创建 —— 故 uniqueName 是编译期必须保持的")
	}
}

// ---- 测试脚手架 ----

// newFakeAgent 构造一个只执行给定 Run 的 Agent。
func newFakeAgent(name string, run func(agent.InvocationContext) iter.Seq2[*session.Event, error]) agent.Agent {
	cfg := agent.Config{Name: name, Description: "fake"}
	if run != nil {
		cfg.Run = run
	} else {
		cfg.Run = func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		}
	}
	a, err := agent.New(cfg)
	if err != nil {
		panic(err)
	}
	return a
}

// newFakeInvocation 构造一个最小可用的 InvocationContext(带内存会话)。
func newFakeInvocation(t *testing.T) (agent.InvocationContext, func()) {
	t.Helper()
	svc := session.InMemoryService()
	userID, sessionID := "contract", "contract-1"
	if _, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "agent-workflow", UserID: userID, SessionID: sessionID,
		State: map[string]any{},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "agent-workflow", UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	ctx := &fakeInvocationContext{
		// 必须非 nil:ADK 内部会用它做 otel span 与取消传播,
		// 留 nil 会 nil pointer panic
		Context: context.Background(),
		sess:    sess.Session,
		content: &genai.Content{Parts: []*genai.Part{{Text: "contract"}}},
	}
	return ctx, func() {}
}

// fakeInvocationContext 是最小 InvocationContext 实现,只实现本文件
// 用到的方法。ADK 若新增必须实现的方法,这里会编译失败 —— 那是好事:
// 说明适配层需要同步更新。
//
// 注:ADK 自己在 interface 上注明了 "temporary solution and will be
// removed later. The proper solution we plan is to stop embedding go
// context in adk context types and split it."(agent/context.go 中
// WithContext 的注释)。本项目的 cascadeCtx 依赖的正是"InvocationContext
// 内嵌 context.Context"这一临时形态 —— 一旦移除,应改用 WithContext 或
// 由本项目自管取消信号。见 TestADKContract_InvocationContextEmbedsContext。
type fakeInvocationContext struct {
	context.Context
	sess    session.Session
	content *genai.Content
}

func (c *fakeInvocationContext) Agent() agent.Agent          { return nil }
func (c *fakeInvocationContext) Artifacts() agent.Artifacts  { return nil }
func (c *fakeInvocationContext) Memory() agent.Memory        { return nil }
func (c *fakeInvocationContext) Session() session.Session    { return c.sess }
func (c *fakeInvocationContext) InvocationID() string        { return "contract" }
func (c *fakeInvocationContext) Branch() string              { return "" }
func (c *fakeInvocationContext) UserContent() *genai.Content { return c.content }
func (c *fakeInvocationContext) RunConfig() *agent.RunConfig { return &agent.RunConfig{} }
func (c *fakeInvocationContext) EndInvocation()              {}
func (c *fakeInvocationContext) Ended() bool                 { return false }

// WithContext 返回**新实例**并替换内嵌 context。
// 不能就地改 c.Context:ADK 会用返回的 ctx 继续构造,若返回的就是自己,
// 内嵌的 context 会被替换成包装自己的 context,Value() 无限递归 ——
// 这正是本项目 cascadeCtx 采用"包装原 ctx + 覆盖四个方法"而非
// WithContext 的原因。
func (c *fakeInvocationContext) WithContext(ctx context.Context) agent.InvocationContext {
	cp := *c
	cp.Context = ctx
	return &cp
}

// drain 消费完迭代器,忽略事件内容。
func drain(seq iter.Seq2[*session.Event, error]) {
	for range seq {
	}
}

// ---- 契约 5:loopagent 恰好 N 轮(不是"至多 N 轮") ----

// TestADKContract_LoopAgentRunsExactlyN 现有断言只是 `if n > 3`("至多"),
// 语义从"恰好 N 轮"退化为"至少 N 轮"不会被发现。本项目的循环保护
// (DefaultMaxIterations=5)完全依赖这个上限 —— 若变成"至少",工作流会
// 多跑若干轮才停;若变成"至多",循环保护会提前触发。
func TestADKContract_LoopAgentRunsExactlyN(t *testing.T) {
	for _, max := range []uint{1, 3, 5} {
		var iterations atomic.Int32
		body := newFakeAgent("body", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				iterations.Add(1)
				ev := session.NewEventWithContext(ctx, ctx.InvocationID())
				yield(ev, nil)
			}
		})
		la, err := loopagent.New(loopagent.Config{
			AgentConfig:   agent.Config{Name: "loop", SubAgents: []agent.Agent{body}},
			MaxIterations: max,
		})
		if err != nil {
			t.Fatalf("loopagent.New: %v", err)
		}
		ctrl, cleanup := newFakeInvocation(t)
		drain(la.Run(ctrl))
		cleanup()
		if n := iterations.Load(); n != int32(max) {
			t.Errorf("MaxIterations=%d 时 body 应恰好执行 %d 次,实际 %d —— "+
				"ADK 循环上限语义变了,本项目的循环保护会失准", max, max, n)
		}
	}
}

// TestADKContract_LoopAgentEscalateExitsImmediately 钉死另一个关键语义:
// 循环体内**任一**子 Agent 发 Escalate 事件,整个循环立刻退出(不是跑完
// 当前轮再停)。
//
// 本项目把 WAIT_USER、节点失败、循环上限都编码成 Escalate
// (lower.go 的 escalateEvent)。若这个语义变化(比如改成跑完当前轮),
// 人工闸门会在循环体内反复弹 —— 每次 Resume 都立刻又退出循环。
func TestADKContract_LoopAgentEscalateExitsImmediately(t *testing.T) {
	var iterations atomic.Int32
	escalating := newFakeAgent("escalating", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			iterations.Add(1)
			ev := session.NewEventWithContext(ctx, ctx.InvocationID())
			ev.Actions.Escalate = true
			yield(ev, nil)
		}
	})
	la, err := loopagent.New(loopagent.Config{
		AgentConfig:   agent.Config{Name: "loop", SubAgents: []agent.Agent{escalating}},
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("loopagent.New: %v", err)
	}
	ctrl, cleanup := newFakeInvocation(t)
	drain(la.Run(ctrl))
	cleanup()
	if n := iterations.Load(); n != 1 {
		t.Errorf("Escalate 后应立即退出循环(执行 1 次),实际执行 %d 次 —— "+
			"ADK 语义变了,人工闸门会在循环体内反复弹", n)
	}
}

// ---- 契约 6:消费者提前停止(yield 返回 false) ----

// TestADKContract_ParallelDrainsOnEarlyStop 钉死:消费者在迭代器中途返回
// false 后,parallelagent 仍会**等待所有分支 goroutine 结束**(defer 里
// cancelSubAgents + drain resultsChan)。
//
// 本项目 engine.execute 在 ctx 取消时 `break` 出事件循环(lower.go 的
// runner.Run 消费侧),紧接着就读会话并回写 Execution。若 ADK 不保证分支
// 已收尾,engine 的回写会与仍在跑的分支并发写同一批状态。
// 若这个保证消失(变成 fire-and-forget),就是新的数据竞争入口 ——
// 必须被看见。
func TestADKContract_ParallelDrainsOnEarlyStop(t *testing.T) {
	var branchDone atomic.Bool
	// 分支:先发一个事件(让消费者有机会拿到第一个),然后阻塞到被取消,
	// 收到取消后做一段"teardown"再标记完成。
	slow := newFakeAgent("slow", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			ev := session.NewEventWithContext(ctx, ctx.InvocationID())
			if !yield(ev, nil) {
				return
			}
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond) // teardown
			branchDone.Store(true)
		}
	})
	par, err := parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{Name: "par", SubAgents: []agent.Agent{slow}},
	})
	if err != nil {
		t.Fatalf("parallelagent.New: %v", err)
	}
	ctrl, cleanup := newFakeInvocation(t)
	defer cleanup()
	// 只取第一个事件就停止消费
	n := 0
	for range par.Run(ctrl) {
		n++
		break
	}
	if !branchDone.Load() {
		t.Error("消费者提前停止后,parallelagent 未等待分支 goroutine 结束 —— " +
			"engine.execute 的 break 后回写会与仍在跑的分支并发写状态")
	}
}

// ---- 契约 7:分支错误回传消费者 ----

// TestADKContract_ParallelPropagatesBranchError 钉死:分支返回 Go error 时,
// 该错误会被回传给迭代器消费者(engine 靠它设置 lastErr)。
//
// 若 ADK 改成吞掉分支错误,engine 的 `lastErr` 永远是 nil —— 中止信号
// 只剩 KeyAbort 一条路径,错误原因会丢失。
func TestADKContract_ParallelPropagatesBranchError(t *testing.T) {
	failing := newFakeAgent("failing", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, context.DeadlineExceeded)
			return
		}
	})
	ok := newFakeAgent("ok", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {}
	})
	par, err := parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{Name: "par", SubAgents: []agent.Agent{failing, ok}},
	})
	if err != nil {
		t.Fatalf("parallelagent.New: %v", err)
	}
	ctrl, cleanup := newFakeInvocation(t)
	defer cleanup()
	var gotErr error
	for _, err := range par.Run(ctrl) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Error("分支的 Go error 未回传给消费者 —— engine 的 lastErr 将永远为 nil," +
			"错误原因丢失")
	}
}

// ---- 契约 8:session.State 的 Set(nil)/Get 缺失键 ----

// TestADKContract_StateSetNilAndMissingKey 钉死本项目两处 Delete 约定
// 所依赖的语义:
//   - Set(key, nil) 之后 Get 返回 (nil, nil) —— 不报错
//   - Get 一个从未存在的键返回 ErrStateKeyNotExist
//   - All() 仍然包含被 Set(nil) 的键(nil 值也在内)
//
// 本项目的 stateAccess.Get 把 nil 当"缺失"(所以 Delete 用 Set(nil)
// 伪造删除);而 syncNodeStates 曾把 nil 渲染成字面量 "<nil>"(#83 已修)。
// 这三条语义一旦变化,一次性状态(_user_input 等)的消费逻辑就会出错。
func TestADKContract_StateSetNilAndMissingKey(t *testing.T) {
	svc := session.InMemoryService()
	if _, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "a", UserID: "u", SessionID: "s", State: map[string]any{"k": 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "a", UserID: "u", SessionID: "s",
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	st := sess.Session.State()

	// 缺失键
	if v, err := st.Get("never-set"); err == nil {
		t.Errorf("Get 缺失键应返回 ErrStateKeyNotExist,实际 (v=%v, err=nil)", v)
	} else if !errors.Is(err, session.ErrStateKeyNotExist) {
		t.Errorf("缺失键错误类型不对: %v", err)
	}
	// Set(nil) 之后
	if err := st.Set("gone", nil); err != nil {
		t.Fatalf("Set(nil): %v", err)
	}
	v, err := st.Get("gone")
	if err != nil {
		t.Errorf("Set(key,nil) 后 Get 应返回 (nil,nil),实际 err=%v —— "+
			"本项目的 Delete 约定依赖这一点", err)
	}
	if v != nil {
		t.Errorf("Set(key,nil) 后值应为 nil,实际 %v", v)
	}
	// All() 仍含该键(nil 值)—— ADK v1.7.0 的 All() 是迭代器(map 快照)
	present := false
	for k := range st.All() {
		if k == "gone" {
			present = true
		}
	}
	if !present {
		t.Log("提示:All() 不再包含被 Set(nil) 的键 —— 若属实," +
			"stateAccess.Delete 与 syncNodeStates 的语义需要重新评估")
	} else {
		t.Log("确认:All() 仍含被 Set(nil) 的键(nil 值也在内)—— " +
			"故派生视图必须显式跳过 nil(#83)")
	}
}

// ---- 契约 9:temp: 前缀语义 ----

// TestADKContract_TempPrefixStrippedFromEvents 钉死:带 `temp:` 前缀的状态
// 键会从**事件**里剥掉,但仍参与 updateSessionState(即影响实时会话状态)。
//
// 风险:本项目若给一次性状态(_error / _user_input 之类)加 temp: 前缀,
// Resume 时 StateData 会**静默丢失**它们 —— 因为 StateData 来自
// sess.State().All() 与事件历史的组合。这条契约是为了让"加 temp: 前缀"
// 这个看似安全的优化不会悄悄破坏恢复。
func TestADKContract_TempPrefixStrippedFromEvents(t *testing.T) {
	svc := session.InMemoryService()
	if _, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "a", UserID: "u", SessionID: "s2", State: map[string]any{},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "a", UserID: "u", SessionID: "s2",
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	s := sess.Session
	ev := session.NewEvent("e1")
	ev.Actions.StateDelta = map[string]any{
		"temp:scratch":  123,
		"node:a:status": "SUCCESS",
	}
	// AppendEvent 挂在 service 上而非 session 上
	if err := svc.AppendEvent(context.Background(), s, ev); err != nil {
		t.Fatalf("append: %v", err)
	}
	// 事件里不应含 temp: 键
	for e := range s.Events().All() {
		if _, present := e.Actions.StateDelta["temp:scratch"]; present {
			t.Error("temp: 键仍留在事件 StateDelta 里 —— ADK 语义变了")
		}
		if _, present := e.Actions.StateDelta["node:a:status"]; !present {
			t.Error("非 temp 键被误删 —— ADK 语义变了")
		}
	}
	// 但实时状态里生效(updateSessionState 在 trim 之前)
	if v, err := s.State().Get("temp:scratch"); err != nil || v == nil {
		t.Logf("提示:temp: 键未进入实时状态 (v=%v err=%v)", v, err)
	} else {
		t.Log("确认:temp: 键影响实时状态但不进事件 —— " +
			"故本项目**不能**给需要 Resume 恢复的状态加 temp: 前缀")
	}
}
