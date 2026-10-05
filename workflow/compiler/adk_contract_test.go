package compiler

import (
	"context"
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
		if !siblingFinished.Load() {
			t.Log("提示:兄弟分支被取消了 —— 若这是 ADK 新行为,本项目的级联层可能冗余," +
				"但需复核 HITL(Escalate 用于暂停)是否被误当失败")
		} else {
			t.Log("确认:Escalate + err=nil 不取消兄弟(与本项目级联层的存在前提一致)")
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
	// 不要求一定报错(取决于 ADK 版本严格程度),只要求本项目的做法是安全的:
	// lowerer.uniqueName 已在编译期保证唯一,这里记录实际行为供升级时对照。
	t.Logf("重名子 Agent 的行为:err=%v(本项目用 lowerer.uniqueName 预先规避)", err)
	if err == nil {
		t.Log("提示:ADK 当前不拒绝重名 —— 仍必须保持 uniqueName," +
			"因为 parentmap 按名字索引,重名会让事件路由到错误的父节点")
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
