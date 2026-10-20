package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/session"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

// DefaultMaxIterations 是循环保护默认上限(engine 的 loopLimit 兜底同源)。
const DefaultMaxIterations = 5

// 会话状态键(执行状态通过 ADK Session State 持久化)。
const (
	KeyTask         = "task"
	KeyAbort        = "_abort"         // 非空 = 执行中断(失败/等待),后续节点跳过
	KeyResumeNode   = "_resume_node"   // 恢复执行时从该节点开始
	KeyUserInput    = "_user_input"    // 人工节点的用户响应
	KeyLoopExited   = "_loop_exited"   // loop 因出口条件成立而退出
	KeyLoopStuck    = "_loop_stuck"    // 检测到连续相同 Review,陷入停滞
	KeyLoopCount    = "_loop_count"    // 当前循环轮数
	KeyDecision     = "decision"       // 最近一次 Agent 决策
	KeyLoopDecision = "_loop_decision" // 循环体内用于选择回边分支的决策快照
	KeyStatus       = "status"         // 最近一次 Agent 状态
	KeyOutput       = "output"         // 最近一次 Agent 输出
	KeyCurrentNode  = "_current_node"
)

// StatusKey / ResultKey 生成节点级状态键。
func StatusKey(nodeID string) string { return "node:" + nodeID + ":status" }

// AnsweredKey 生成"该等待节点已被用户答复"的状态键(值为 WAIT_USER
// 那次的结果 JSON 快照,供恢复后复用,不再重复调用 Agent/Skill)。
func AnsweredKey(nodeID string) string { return "node:" + nodeID + ":answered" }
func ResultKey(nodeID string) string   { return "node:" + nodeID + ":result" }

// RouteBranchKey 生成"路由出口分支选择"的状态键。
//
// 值是出口分支的**下标**(枚举,不是自由文本):路由一旦选定出口,
// 这个选择就是本次执行的既成事实。恢复执行必须**回放**它,而不是
// 拿脱敏后的 output 重新求值 —— 重新求值的输入已经不是当初那一份
// (output 在 state_json 里是脱敏副本),条件可能翻面,于是"人批准 A
// 分支,恢复后走 B 分支"(约定 #14:走错分支比崩溃更危险)。
//
// 见 routeAgent 的 selectExitBranch 与 runtime/state_mask.go 的
// "output 键的双重身份"一节。
func RouteBranchKey(decisionNodeID string) string {
	return "route:" + decisionNodeID + ":branch"
}

// NodeRunner 由 runtime 实现:真正执行一个节点的业务逻辑。
// compiler 只负责编排,不感知 Agent/Skill/Git 的实现细节。
type NodeRunner interface {
	ExecuteNode(ctx context.Context, env *RunEnv, node *model.Node, st StateAccess) NodeOutcome
}

// NodeOutcome 是节点执行结果。
type NodeOutcome struct {
	State    model.NodeState
	Decision string
	Summary  string
	Output   string
	Data     map[string]any
	Error    string
}

// StateAccess 是节点执行器可用的状态读写接口(写入会被 ADK 持久化)。
type StateAccess interface {
	Get(key string) (any, bool)
	Set(key string, val any) error
	// Delete 清除一次性状态(如人工输入 _user_input)。
	// ADK State 接口本身没有删除语义,实现约定写入 nil 等价于删除,
	// 且 Get 对 nil 值返回 false(见 stateAccess.Get)。
	Delete(key string) error
}

// stateAccess 基于 ADK InvocationContext 的实现:
// 写入同时进入 EventActions.StateDelta(随事件持久化)与活动会话状态。
type stateAccess struct {
	ctx     agent.InvocationContext
	actions *session.EventActions
}

func (s *stateAccess) Get(key string) (any, bool) {
	v, err := s.ctx.Session().State().Get(key)
	if err != nil || v == nil {
		return nil, false
	}
	return v, true
}

func (s *stateAccess) Set(key string, val any) error {
	if s.actions != nil && s.actions.StateDelta != nil {
		s.actions.StateDelta[key] = val
	}
	return s.ctx.Session().State().Set(key, val)
}

// Delete 以 nil 覆盖实现删除:ADK 的 session.State 只有 Get/Set/All。
// 约定 nil 即"不存在",Get 会把它与缺失键同等对待。
func (s *stateAccess) Delete(key string) error {
	if s.actions != nil && s.actions.StateDelta != nil {
		delete(s.actions.StateDelta, key)
	}
	return s.ctx.Session().State().Set(key, nil)
}

// RunEnv 是一次执行运行的编译/执行环境。
type RunEnv struct {
	Exec          *model.Execution
	WF            *model.Workflow
	Runner        NodeRunner
	Bus           *event.Bus
	Vars          map[string]any
	MaxIterations int
	// Perms 运行期权限管理器(全局克隆 + 工作流覆盖);nil 时回退全局。
	Perms RunPermissionChecker

	// execMu 保护 Exec 的节点级写入:parallel 分支由 ADK 并发执行,
	// 直接写 Exec.CurrentNodeID / Exec.NodeStates 是数据竞争。
	execMu sync.Mutex
	// abort 记录最近一次中断原因(FAILED:node / WAIT_USER / LOOP_LIMIT:node)。
	// 并行区用它做失败级联;受 execMu 保护。
	abort string
	// lastCheckpoint 上次落库检查点的时间、lastCheckpointNode 上次的
	// 节点 ID(受 execMu 保护)。后者用于识别"换了节点",必须独立于
	// Exec.CurrentNodeID —— 后者在本函数调用前已被设成同一个值。
	lastCheckpoint     time.Time
	lastCheckpointNode string
	// OnNodeCheckpoint 在节点开始执行时被调用(节流后)。用于让 runtime
	// 把"当前在跑哪个节点"落库:此前 CurrentNodeID 只存在内存里,运行中
	// 崩溃时库中仍是 Start 时的空值,RecoverPending 标 FAILED 之后
	// RetryNode 恒返回 NO_FAILED_NODE("无法定位失败节点")。
	// 逐节点写库会显著拖慢执行(每个节点两次事务),故由 runtime 侧按
	// 时间节流。
	OnNodeCheckpoint func(nodeID string)
}

// checkpointInterval 是节点检查点的最小间隔(毫秒)。
// 与其让 runtime 自己判断,不如由 compiler 在这里做节流:
// 并行分支会同时调用,去重逻辑放在 SetCurrentNode 之后更自然。
const checkpointInterval = 2 * time.Second

// maybeCheckpoint 通知 runtime 把"当前在跑哪个节点"落库。
//
// 节流规则:同一节点重复进入按时间节流(2s),**换节点立即落盘**。
// 换节点必须立即写 —— 崩溃后要能定位到真正卡住的那个节点;若纯按时间
// 节流,快速链里后续节点都不落库,崩溃时库里的 current_node_id 仍指向
// 更早的节点,RetryNode 于是从错误的位置重试。
//
// 比较对象必须是 lastCheckpointNode 而不是 Exec.CurrentNodeID:调用点
// 在本函数**之前**已调 SetCurrentNode(nodeID),两者恒等,拿它比较会让
// "换节点"分支永远走不到(实测:该实现退化为纯 2s 节流,4 个秒级节点里
// 只有链首与收尾被记录,中间的 n2/n3/n4 全丢)。
func (e *RunEnv) maybeCheckpoint(nodeID string) {
	if e.OnNodeCheckpoint == nil {
		return
	}
	e.execMu.Lock()
	if nodeID == e.lastCheckpointNode &&
		time.Since(e.lastCheckpoint) < checkpointInterval {
		e.execMu.Unlock()
		return
	}
	e.lastCheckpoint = time.Now()
	e.lastCheckpointNode = nodeID
	e.execMu.Unlock()
	e.OnNodeCheckpoint(nodeID)
}

// SetCurrentNode 并发安全地更新当前节点。
func (e *RunEnv) SetCurrentNode(nodeID string) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	e.Exec.CurrentNodeID = nodeID
}

// SetNodeState 并发安全地记录节点状态。
func (e *RunEnv) SetNodeState(nodeID, state string) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.Exec.NodeStates == nil {
		e.Exec.NodeStates = map[string]string{}
	}
	e.Exec.NodeStates[nodeID] = state
}

// abortReason 读取当前的中断原因(供并行区失败级联监视器使用)。
// 并发安全:abort 标记写在 ADK 会话状态里,这里读的是 Runtime 侧
// 维护的快照(由 nodeAgent 写入 SetAbort 时同步更新)。
func (e *RunEnv) abortReason() (string, bool) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.abort == "" {
		return "", false
	}
	return e.abort, true
}

// SetAbort 记录中断原因(并发安全)。
func (e *RunEnv) SetAbort(reason string) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	e.abort = reason
}

// runNodeGuarded 执行单个节点并兜住 panic。
//
// 为什么不复用 runtime.execute 的 defer recover():ADK 的 parallelagent
// 在**自己的** goroutine 里跑各分支(`errgroup.Go`),而 `recover()` 在
// adk 生产代码中零命中 —— 分支内的 panic 会绕过执行协程的兜底直接杀掉
// 整个进程(桌面模式即整应用退出),违反 AGENTS.md 硬性约定 #12。
// 实测:分支内 Agent 适配器 panic → `panic: boom from agent adapter`,
// 栈顶是 `created by errgroup.(*Group).Go`,进程确实被杀。
//
// 兜在这里而不是只加在 execute:节点是 panic 的实际发生地,无论它跑在
// 执行协程(顺序链)还是 ADK 的分支协程(并行区)都能覆盖。
// panic 转成 FAILED outcome 后,上层既有的 KeyAbort 流程会把执行落库
// FAILED,用户能看到"哪个节点炸了"而不是整个应用消失。
func (e *RunEnv) runNodeGuarded(ctx agent.InvocationContext, node *model.Node, st StateAccess) (out NodeOutcome) {
	env := e
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("节点 %s 执行 panic: %v", node.ID, r)
			logx.Error("节点 panic(已兜底)", "execution", env.Exec.ID,
				"node", node.ID, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			env.Bus.Emit(event.New(event.NodeFailed, env.Exec.ID, node.ID, map[string]any{
				"node": node.ID, "error": msg,
			}))
			out = NodeOutcome{State: model.NodeFailed, Error: msg}
		}
	}()
	return env.Runner.ExecuteNode(ctx, env, node, st)
}

// RunPermissionChecker 由 runtime 注入的策略检查接口。
type RunPermissionChecker interface {
	Check(agentID string, a permission.Action) error
}

// Compile 将 Workflow 编译为 ADK 根 Agent。
// 每次 Run 创建新的编译产物(闭包绑定本次 Execution)。
func Compile(env *RunEnv) (agent.Agent, error) {
	plan := BuildPlan(env.WF)
	l := &lowerer{env: env}
	root, err := l.lower(plan, "workflow")
	if err != nil {
		return nil, err
	}
	return root, nil
}

type lowerer struct {
	env  *RunEnv
	seqN int
	// usedNames 保证整棵 Agent 树内名字唯一(ADK parentmap 要求)。
	usedNames map[string]int
}

// uniqueName 返回树内唯一的 Agent 名。
func (l *lowerer) uniqueName(base string) string {
	if l.usedNames == nil {
		l.usedNames = map[string]int{}
	}
	n := l.usedNames[base]
	l.usedNames[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s.%d", base, n)
}

// lower 将 IR 计划降级为 ADK Agent。
func (l *lowerer) lower(p *Plan, name string) (agent.Agent, error) {
	a, err := l.lowerInner(p, name)
	if err != nil {
		return nil, err
	}
	// 出口统一包一层 panic 兜底。
	//
	// 为什么不能在 engine.execute 一处了事:并行分支由 ADK 在自己的 goroutine
	// 里跑(`errgroup.Go`),而 recover() 在 adk 生产代码中零命中 —— 分支内的
	// panic 绕过执行协程的兜底直接杀进程(#84)。runNodeGuarded 只包住了
	// "节点执行"这一个点,但分支内跑的**结构体 Agent**(routeAgent 的条件
	// 求值、loopGuard 的 expr 运行、loopResetter 的状态操作)同样可能 panic
	// (如畸形表达式、损坏的状态快照),它们不在 runNodeGuarded 覆盖范围内。
	//
	// 在 lower() 出口统一包装,保证降级出来的每一棵子树都被兜住 ——
	// 新增 Agent 类型时也不会漏。
	return l.guardAgent(a, name), nil
}

// guardAgent 给已降级的 Agent 包一层 panic 兜底(纵深防御)。
//
// 已知它**不替代** runNodeGuarded:节点执行那一步(Agent/Skill 适配器
// 调用)仍由 runNodeGuarded 覆盖,实测关掉本层后 Skill 路径的 panic 依旧
// 被兜住。本层覆盖的是 runNodeGuarded **之外**的代码 ——
// routeAgent/loopGuard/switchAgent/loopResetter 里的表达式求值、状态操作等,
// 理论上也能 panic(畸形表达式、损坏的状态快照)。它们没有实测到 panic,
// 故这是一层为"未来新增/修改的结构体 Agent"准备的兜底,而不是已证的缺陷修复。
//
// 放在 lower() 出口而非逐个 Agent 包:新增 Agent 类型时不会漏。
func (l *lowerer) guardAgent(a agent.Agent, name string) agent.Agent {
	env := l.env
	guarded, err := agent.New(agent.Config{
		Name:        l.uniqueName(name + ".guard"),
		Description: "panic 兜底",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				defer func() {
					if r := recover(); r != nil {
						msg := fmt.Sprintf("子图 %s 执行 panic: %v", name, r)
						logx.Error("子图 panic(已兜底)", "execution", env.Exec.ID,
							"agent", name, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
						env.Bus.Emit(event.New("runtime.error", env.Exec.ID, "", map[string]any{
							"error": msg, "agent": name,
						}))
						// 中止整条执行:子图没跑完,后续节点不应继续。
						// 置 KeyAbort 让 engine 落 FAILED 并带上原因。
						env.Bus.Emit(event.New(event.NodeFailed, env.Exec.ID, "", map[string]any{
							"error": msg,
						}))
						env.SetAbort("FAILED:" + name)
					}
				}()
				for ev, err := range a.Run(ctx) {
					if !yield(ev, err) {
						return
					}
				}
			}
		},
	})
	if err != nil {
		// 兜底层自身构造失败(理论上只有 Name 冲突):退回原 Agent,
		// 不能因为"加一层保护"反而让工作流跑不起来。
		return a
	}
	return guarded
}

// lowerInner 是 lower 的实际降级逻辑(panic 兜底在 lower 出口统一包)。
func (l *lowerer) lowerInner(p *Plan, name string) (agent.Agent, error) {
	name = l.uniqueName(name)
	switch p.Kind {
	case KindSingle:
		node, err := l.findNode(p.NodeID)
		if err != nil {
			return l.noopAgent(name) // 汇合占位节点
		}
		return l.nodeAgent(node, "")
	case KindSeq:
		var subs []agent.Agent
		for i, c := range p.Children {
			a, err := l.lower(c, fmt.Sprintf("%s.seq%d", name, i))
			if err != nil {
				return nil, err
			}
			subs = append(subs, a)
		}
		return sequentialagent.New(sequentialagent.Config{
			AgentConfig: agent.Config{Name: name, Description: "顺序执行", SubAgents: subs},
		})
	case KindPar:
		var subs []agent.Agent
		for i, c := range p.Children {
			a, err := l.lower(c, fmt.Sprintf("%s.branch%d", name, i))
			if err != nil {
				return nil, err
			}
			subs = append(subs, a)
		}
		// 失败级联:nodeAgent 失败时只 yield escalateEvent 且 err==nil,
		// ADK parallelagent 的 errgroup 仅在 Go error 时 cancel 兄弟分支 ——
		// 于是分支 A 失败后分支 B 仍完整跑完(真实 Agent 场景下等于继续
		// 烧 token、继续改文件)。这里让整个并行区共享一个可取消的 ctx,
		// 任一分支失败即取消其余分支。
		parAgt, err := parallelagent.New(parallelagent.Config{
			AgentConfig: agent.Config{Name: name, Description: "并行分支", SubAgents: subs},
		})
		if err != nil {
			return nil, err
		}
		return l.parallelCascadeAgent(parAgt, name)
	case KindRoute:
		return l.routeAgent(p, name)
	case KindSwitch:
		return l.switchAgent(p, name)
	case KindWarning:
		return l.warningAgent(p, name)
	default:
		return l.noopAgent(name)
	}
}

// parallelCascadeAgent 包一层:给并行区一个共享的派生 context,
// 任一分支出现 KeyAbort=FAILED 时取消它,兄弟分支随之中止。
//
// 判定依据是运行期记录的 abort 原因(而非 ADK 的 err 通道),因为节点失败
// 走的是 escalate + err==nil 的路径。WAIT_USER 同样写 abort 但语义不同
// (暂停等人工,不是失败),故只认 FAILED 前缀 —— 人工闸门在并行区内
// 不会误杀兄弟分支。
func (l *lowerer) parallelCascadeAgent(inner agent.Agent, name string) (agent.Agent, error) {
	env := l.env
	return agent.New(agent.Config{
		Name:        l.uniqueName(name + ".cascade"),
		Description: "并行分支(失败级联)",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				// 共享取消器:分支失败时由监视协程 cancel
				parCtx, cancelPar := context.WithCancel(ctx)
				defer cancelPar()
				watchDone := make(chan struct{})
				defer close(watchDone)
				go func() {
					tick := time.NewTicker(20 * time.Millisecond)
					defer tick.Stop()
					for {
						select {
						case <-watchDone:
							return
						case <-parCtx.Done():
							return
						case <-tick.C:
							if v, ok := env.abortReason(); ok &&
								strings.HasPrefix(v, "FAILED:") {
								logx.Warn("并行分支失败,取消其余分支",
									"execution", env.Exec.ID, "parallel", name, "abort", v)
								cancelPar()
								return
							}
						}
					}
				}()
				// ADK 的 Run 收 InvocationContext(内嵌 context.Context),
				// 故用包装类型只替换取消语义,其余方法原样透传。
				for ev, err := range inner.Run(cascadeCtx{InvocationContext: ctx, ctx: parCtx}) {
					if !yield(ev, err) {
						return
					}
				}
			}
		},
	})
}

// cascadeCtx 把 InvocationContext 的取消/截止语义换成并行区共享的那个,
// 其余方法(Agent/Session/Branch/…)原样透传。
type cascadeCtx struct {
	agent.InvocationContext
	ctx context.Context
}

func (c cascadeCtx) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c cascadeCtx) Done() <-chan struct{}       { return c.ctx.Done() }
func (c cascadeCtx) Err() error                  { return c.ctx.Err() }
func (c cascadeCtx) Value(key any) any           { return c.ctx.Value(key) }

// warningAgent 处理"并行分支不收敛":各分支照常跑完,但执行显式失败。
//
// 语义依据 AGENTS.md 硬性约定 #14("该失败却成功"等同 P0):执行结束前若
// 仍有未执行的可达节点(条件出口全不匹配、**并行不收敛**、回边被丢弃),
// 必须显式失败并给出可读原因,不允许默认收尾落 COMPLETED。
// 此前只发 runtime.warning 然后让执行落 COMPLETED —— 各分支确实跑完了,
// 但它们**之后的链一个都没跑**,用户看到"成功"却不知道缺了一截,
// 事件流无订阅者时更是完全无感。与 "无匹配出口"(#42)同款处理:
// 置 KeyAbort + 写明原因,由 engine 落库 FAILED。
func (l *lowerer) warningAgent(p *Plan, name string) (agent.Agent, error) {
	env := l.env
	msg := fmt.Sprintf("节点 %s 之后的链路不会执行:该处没有公共汇合点"+
		"(各并行分支终点互不相同),请把分支连到同一个 merge 节点上", p.NodeID)
	return agent.New(agent.Config{
		Name:        l.uniqueName(name + ".warn"),
		Description: "并行不收敛",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				actions := &session.EventActions{StateDelta: map[string]any{}}
				st := &stateAccess{ctx: ctx, actions: actions}
				if aborted(st) {
					return
				}
				logx.Warn(msg, "execution", env.Exec.ID, "node", p.NodeID)
				env.Bus.Emit(event.New("runtime.warning", env.Exec.ID, p.NodeID, map[string]any{
					"node": p.NodeID, "reason": "parallel_not_converged", "message": msg,
				}))
				// 约定 #14:不能只告警。置 KeyAbort 让 engine 落 FAILED,
				// 并把原因写进 _error,用户在执行详情里能直接看到该修哪里。
				st.Set(KeyAbort, "FAILED:"+p.NodeID)
				env.SetAbort("FAILED:" + p.NodeID)
				st.Set("_error", msg)
				env.SetNodeState(p.NodeID, string(model.NodeFailed))
				env.Bus.Emit(event.New(event.NodeFailed, env.Exec.ID, p.NodeID, map[string]any{
					"node": p.NodeID, "error": msg,
				}))
				yield(escalateEvent(ctx, actions), nil)
			}
		},
	})
}

// switchAgent 纯条件选择:按状态求值条件挑一条链执行,自身不执行任何
// 节点。用于一个决策点存在多条回边时,在循环体内选择本轮走哪条回边。
// 条件都不成立时什么都不做(由循环守卫决定退出或继续下一轮)。
func (l *lowerer) switchAgent(p *Plan, name string) (agent.Agent, error) {
	env := l.env
	var subs []agent.Agent
	for i, b := range p.Branches {
		a, err := l.lower(b.Target, fmt.Sprintf("%s.alt%d", name, i))
		if err != nil {
			return nil, err
		}
		subs = append(subs, a)
	}
	conditions := make([]string, len(p.Branches))
	for i, b := range p.Branches {
		conditions[i] = b.Condition
	}
	return agent.New(agent.Config{
		Name:        l.uniqueName(name),
		Description: "条件选择",
		SubAgents:   subs,
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				actions := &session.EventActions{StateDelta: map[string]any{}}
				st := &stateAccess{ctx: ctx, actions: actions}
				if aborted(st) {
					return
				}
				for i, cond := range conditions {
					if cond == "" {
						continue
					}
					// 用决策快照求值:循环体内的 KeyDecision 已被
					// loopResetter 清空(见 loopResetter 注释)。
					ok, err := evalConditionWithDecision(cond, st, env.Vars)
					if err != nil || !ok {
						continue
					}
					env.Bus.Emit(event.New(event.NodeCompleted, env.Exec.ID, "", map[string]any{
						"switch": name, "branch": i, "condition": cond,
					}))
					for ev, err := range subs[i].Run(ctx) {
						if !yield(ev, err) {
							return
						}
					}
					yield(stateEvent(ctx, actions), nil)
					return
				}
				yield(stateEvent(ctx, actions), nil)
			}
		},
	})
}

func (l *lowerer) findNode(id string) (*model.Node, error) {
	for i := range l.env.WF.Nodes {
		if l.env.WF.Nodes[i].ID == id {
			return &l.env.WF.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("node %q not found", id)
}

// noopAgent 什么都不做(汇合占位)。
func (l *lowerer) noopAgent(name string) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        name,
		Description: "noop",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
}

// eventYield 构造携带 StateDelta 的事件。
func stateEvent(ctx agent.InvocationContext, actions *session.EventActions) *session.Event {
	ev := session.NewEventWithContext(ctx, ctx.InvocationID())
	ev.Actions = *actions
	return ev
}

// escalateEvent 构造触发 Escalate 的事件(附带 StateDelta)。
func escalateEvent(ctx agent.InvocationContext, actions *session.EventActions) *session.Event {
	ev := stateEvent(ctx, actions)
	ev.Actions.Escalate = true
	return ev
}

// nodeAgent 为单个节点创建 ADK 自定义 Agent。
// Run 内置:中断守卫 / 恢复跳过(已完成节点直接跳过) / 状态机 / 事件发射。
func (l *lowerer) nodeAgent(node *model.Node, suffix string) (agent.Agent, error) {
	env := l.env
	return agent.New(agent.Config{
		Name:        l.uniqueName("node:" + node.ID + suffix),
		Description: node.Name,
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				actions := &session.EventActions{StateDelta: map[string]any{}}
				st := &stateAccess{ctx: ctx, actions: actions}

				// 中断守卫:失败/等待人工后,链上后续节点全部跳过
				if v, ok := st.Get(KeyAbort); ok && v != nil && fmt.Sprint(v) != "" {
					return
				}
				// 恢复跳过:已成功的节点在 resume 重放时直接跳过
				if nodeAlreadyDone(st, node.ID) {
					return
				}
				// 人工/等待节点恢复:收到用户输入后重新执行。
				// 标记 _answered:该节点此前已返回过 WAIT_USER 并把 prompt
				// 交给用户看过,用户现在的答复是"对这个 prompt 的回应",
				// 不是"让我把同样的活再干一遍"。execute/fix 模式的 Agent
				// 第二次调用会重复写文件、重复跑测试、重复 git 操作。
				// 语义(已与产品确认):复用已有响应,答复作为上下文注入,
				// 节点按"已答复"完成,不再重复调 Agent。
				if s, ok := st.Get(StatusKey(node.ID)); ok && s == string(model.NodeWaiting) {
					resume, _ := st.Get(KeyResumeNode)
					if fmt.Sprint(resume) != node.ID {
						return // 未被指定恢复的等待节点保持等待
					}
					st.Set(KeyResumeNode, "")
					answered, _ := st.Get(ResultKey(node.ID))
					st.Set(KeyUserInput, mustUserInput(st))
					st.Set(AnsweredKey(node.ID), answered)
				}

				st.Set(StatusKey(node.ID), string(model.NodeRunning))
				st.Set(KeyCurrentNode, node.ID)
				env.SetCurrentNode(node.ID)
				env.SetNodeState(node.ID, string(model.NodeRunning))
				env.maybeCheckpoint(node.ID)
				env.Bus.Emit(event.New(event.NodeStarted, env.Exec.ID, node.ID, map[string]any{
					"node": node.ID, "name": node.Name, "type": string(node.Type),
				}))

				if isAgentLike(node) {
					env.Bus.Emit(event.New(event.AgentStarted, env.Exec.ID, node.ID, map[string]any{
						"agent": fmt.Sprint(node.Config["agent"]), "mode": fmt.Sprint(node.Config["mode"]),
					}))
				}

				outcome := env.runNodeGuarded(ctx, node, st)

				// 写回状态。节点状态机此前从未接线(CanTransitionNode 只在
				// 测试里被调用):任何来源的写入 —— resume 回放、循环重置
				// (StatusKey 被清空)、Delete 写 nil —— 都不过校验,非法转换
				// 既不被拒绝也不留痕。终态之间不可再流转,违反即记
				// runtime.error 让问题可见,但不阻断执行(状态已是既成事实)。
				if !model.CanTransitionNode(model.NodeRunning, outcome.State) {
					env.Bus.Emit(event.New("runtime.error", env.Exec.ID, node.ID, map[string]any{
						"error": fmt.Sprintf("节点状态非法转换 %s → %s",
							model.NodeRunning, outcome.State),
					}))
				}
				st.Set(StatusKey(node.ID), string(outcome.State))
				if raw, err := json.Marshal(outcome); err == nil {
					st.Set(ResultKey(node.ID), string(raw))
				}
				env.SetNodeState(node.ID, string(outcome.State))

				switch outcome.State {
				case model.NodeSuccess:
					if outcome.Decision != "" {
						st.Set(KeyDecision, outcome.Decision)
					}
					st.Set(KeyStatus, "success")
					st.Set(KeyOutput, outcome.Output)
					data := map[string]any{"node": node.ID, "summary": outcome.Summary}
					if outcome.Decision != "" {
						data["decision"] = outcome.Decision
					}
					env.Bus.Emit(event.New(event.NodeCompleted, env.Exec.ID, node.ID, data))
					if outcome.Decision == string(coreagent.DecisionApproved) {
						env.Bus.Emit(event.New(event.ReviewApproved, env.Exec.ID, node.ID, data))
					}
					if outcome.Decision == string(coreagent.DecisionRejected) {
						env.Bus.Emit(event.New(event.ReviewRejected, env.Exec.ID, node.ID, data))
					}
					if isAgentLike(node) {
						env.Bus.Emit(event.New(event.AgentCompleted, env.Exec.ID, node.ID, data))
					}
				case model.NodeWaiting:
					// 人工交互:暂停执行,等待用户提供输入
					st.Set(KeyResumeNode, node.ID)
					st.Set(KeyAbort, "WAIT_USER")
					env.SetAbort("WAIT_USER")
					env.Bus.Emit(event.New(event.NodeWaiting, env.Exec.ID, node.ID, map[string]any{
						"node": node.ID, "prompt": outcome.Summary,
					}))
					env.Bus.Emit(event.New(event.HumanInputRequired, env.Exec.ID, node.ID, map[string]any{
						"node": node.ID, "prompt": outcome.Summary,
						"responses": outcome.Data["responses"],
					}))
					yield(escalateEvent(ctx, actions), nil)
					return
				case model.NodeFailed:
					st.Set(KeyAbort, "FAILED:"+node.ID)
					env.SetAbort("FAILED:" + node.ID)
					if outcome.Error != "" {
						st.Set("_error", outcome.Error)
					}
					env.Bus.Emit(event.New(event.NodeFailed, env.Exec.ID, node.ID, map[string]any{
						"node": node.ID, "error": outcome.Error, "summary": outcome.Summary,
					}))
					yield(escalateEvent(ctx, actions), nil)
					return
				}

				yield(stateEvent(ctx, actions), nil)
			}
		},
	})
}

// routeAgent 编译决策路由:先执行决策节点,再按条件选择出口;
// 存在回边时用 LoopAgent 实现循环(带最大迭代数与停滞检测)。
func (l *lowerer) routeAgent(p *Plan, name string) (agent.Agent, error) {
	env := l.env
	decisionNode, err := l.findNode(p.NodeID)
	if err != nil {
		return nil, err
	}

	var subs []agent.Agent
	firstNode, err := l.nodeAgent(decisionNode, "")
	if err != nil {
		return nil, err
	}
	subs = append(subs, firstNode)

	var exitAgents []agent.Agent
	var exitBranches []RouteBranch
	for _, b := range p.Branches {
		a, err := l.lower(b.Target, name+".exit")
		if err != nil {
			return nil, err
		}
		exitAgents = append(exitAgents, a)
		exitBranches = append(exitBranches, b)
		subs = append(subs, a)
	}

	var loopAgent agent.Agent
	var loopNodeIDs []string
	if p.Loop != nil {
		bodyAgt, err := l.lower(p.Loop.Body, name+".loopbody")
		if err != nil {
			return nil, err
		}
		revisit, err := l.nodeAgent(decisionNode, "#revisit") // 新实例,避免同一 Agent 双父
		if err != nil {
			return nil, err
		}
		guard, err := l.loopGuard(p, name)
		if err != nil {
			return nil, err
		}
		maxIter := p.Loop.MaxIterations
		if maxIter <= 0 {
			maxIter = DefaultMaxIterations
		}
		// resetter 在每轮循环开始时清除循环体内节点的完成标记,
		// 使 fix/review 等节点能够真实地重新执行。
		loopNodeIDs = p.Loop.Body.Flatten()
		resetter, err := l.loopResetter(loopNodeIDs, p.Loop.RevisitNodeID, name)
		if err != nil {
			return nil, err
		}
		loopAgent, err = loopagent.New(loopagent.Config{
			AgentConfig: agent.Config{
				Name:        name + ".loop",
				Description: fmt.Sprintf("循环(最多 %d 轮)", maxIter),
				SubAgents:   []agent.Agent{resetter, bodyAgt, revisit, guard},
			},
			MaxIterations: uint(maxIter),
		})
		if err != nil {
			return nil, err
		}
		subs = append(subs, loopAgent)
	}

	routeAgt, err := agent.New(agent.Config{
		Name:        name,
		Description: "路由:" + decisionNode.ID,
		SubAgents:   subs,
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				actions := &session.EventActions{StateDelta: map[string]any{}}
				st := &stateAccess{ctx: ctx, actions: actions}
				if aborted(st) {
					return
				}

				// 0. 恢复消费:此路由曾因循环上限/人工节点暂停,
				//    用户输入决定了继续方向。
				if resumeNode, ok := st.Get(KeyResumeNode); ok && fmt.Sprint(resumeNode) == decisionNode.ID {
					st.Set(KeyResumeNode, "")
					resp, instruction := "", ""
					if ui, ok := st.Get(KeyUserInput); ok {
						if m, ok := ui.(map[string]any); ok {
							resp, _ = m["response"].(string)
							instruction, _ = m["instruction"].(string)
						} else {
							resp = fmt.Sprint(ui)
						}
					}
					switch strings.ToLower(resp) {
					case "approve":
						st.Set(KeyDecision, string(coreagent.DecisionApproved))
						st.Set(KeyStatus, "success")
					case "reject":
						st.Set(KeyAbort, "FAILED:"+decisionNode.ID)
						env.SetAbort("FAILED:" + decisionNode.ID)
						st.Set("_error", "用户在节点 "+decisionNode.ID+" 拒绝继续")
						yield(stateEvent(ctx, actions), nil)
						return
					default: // continue / instruction:再来一轮修复
						st.Set(KeyDecision, "")
						if instruction != "" {
							st.Set("user_instruction", instruction)
						} else if resp != "" && resp != "continue" {
							st.Set("user_instruction", resp)
						}
					}
				}

				// 1. 执行决策节点(nodeAgent 自带跳过/恢复逻辑)
				//
				// 决策节点是否会被"恢复跳过"跳过,必须在执行**之前**判定:
				// 跳过 = 本轮没有重新产出 output/decision → 条件求值的输入
				// 与当初选定分支时的那一份**不是同一份**(output 在
				// state_json 里是脱敏副本,见 runtime/state_mask.go),
				// 重新求值可能走另一条边(约定 #14:走错分支比崩溃更危险)。
				decisionSkipped := nodeAlreadyDone(st, decisionNode.ID)
				for ev, err := range firstNode.Run(ctx) {
					if !yield(ev, err) {
						return
					}
				}
				if aborted(st) {
					yield(stateEvent(ctx, actions), nil)
					return
				}

				// 2. 评估出口分支
				if idx, ok := l.selectExitBranch(exitBranches, st, decisionNode.ID, decisionSkipped); ok {
					for ev, err := range exitAgents[idx].Run(ctx) {
						if !yield(ev, err) {
							return
						}
					}
					yield(stateEvent(ctx, actions), nil)
					return
				}

				// 3. 循环
				if loopAgent != nil {
					st.Set(KeyLoopExited, false)
					st.Set(KeyLoopStuck, false)
					for ev, err := range loopAgent.Run(ctx) {
						if !yield(ev, err) {
							return
						}
					}
					exited, _ := st.Get(KeyLoopExited)
					stuck, _ := st.Get(KeyLoopStuck)
					if exited == true {
						// loop guard 已确认出口条件成立
						//
						// 这里**必须重新求值**,不能回放:循环体里的 revisit
						// 节点重新执行过决策节点,output/decision 是本轮的新值,
						// 与循环开始前的那一份不同。求值结果照样记进状态,
						// 供后续恢复回放(见 selectExitBranch)。
						if idx, ok := l.selectExitBranch(exitBranches, st, decisionNode.ID, false); ok {
							for ev, err := range exitAgents[idx].Run(ctx) {
								if !yield(ev, err) {
									return
								}
							}
						}
						yield(stateEvent(ctx, actions), nil)
						return
					}
					// 达到最大迭代数或检测到停滞
					env.Bus.Emit(event.New(event.WorkflowWaitingUser, env.Exec.ID, decisionNode.ID, map[string]any{
						"reason": loopLimitReason(stuck == true, p.Loop.OnLoopLimit),
						"node":   decisionNode.ID,
					}))
					if p.Loop.OnLoopLimit == "fail" {
						st.Set(KeyAbort, "LOOP_LIMIT:"+decisionNode.ID)
						env.SetAbort("LOOP_LIMIT:" + decisionNode.ID)
						st.Set("_error", "循环达到最大迭代数: "+decisionNode.ID)
						yield(escalateEvent(ctx, actions), nil)
						return
					}
					// 默认 wait_user:暂停等待人工干预
					st.Set(KeyResumeNode, decisionNode.ID)
					st.Set(KeyAbort, "WAIT_USER")
					env.SetAbort("WAIT_USER")
					env.Bus.Emit(event.New(event.HumanInputRequired, env.Exec.ID, decisionNode.ID, map[string]any{
						"node":      decisionNode.ID,
						"prompt":    fmt.Sprintf("工作流在节点 %s 循环 %d 轮后仍未通过,请人工处理", decisionNode.ID, p.Loop.MaxIterations),
						"responses": []string{"approve", "reject", "continue", "instruction"},
					}))
					yield(escalateEvent(ctx, actions), nil)
					return
				}

				// 4. 无匹配出口且无循环:走第一个无条件出口(若有)
				for i, b := range exitBranches {
					if b.Condition == "" {
						for ev, err := range exitAgents[i].Run(ctx) {
							if !yield(ev, err) {
								return
							}
						}
						yield(stateEvent(ctx, actions), nil)
						return
					}
				}

				// 5. 决策点没有任何出口匹配、也没有兜底无条件分支,且不存在
				//    循环:此前直接收尾,执行被标 COMPLETED —— 后续节点
				//    一个都没跑,UI 却显示"成功"。这是"静默成功"最危险的一类
				//    缺陷(条件写错、decision 值对不上、上游返回空决策都会
				//    命中)。显式判失败:告诉用户哪个决策点没有可用出口。
				l.abortNoRoute(ctx, actions, st, env, decisionNode.ID, exitBranches)
				yield(escalateEvent(ctx, actions), nil)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	return routeAgt, nil
}

// abortNoRoute 记录"决策点无可用出口"的中止:置 KeyAbort 与可读的
// _error,并发出节点失败事件,让 engine 落库 FAILED 而不是 COMPLETED。
func (l *lowerer) abortNoRoute(ctx agent.InvocationContext, actions *session.EventActions,
	st StateAccess, env *RunEnv, nodeID string, branches []RouteBranch) {
	conds := make([]string, 0, len(branches))
	for _, b := range branches {
		if b.Condition != "" {
			conds = append(conds, b.Condition)
		}
	}
	decision := stringOf(st, KeyDecision)
	msg := fmt.Sprintf("决策节点 %s 没有匹配的出口分支(decision=%q)", nodeID, decision)
	if len(conds) > 0 {
		msg += fmt.Sprintf(",已尝试条件: %s", strings.Join(conds, " | "))
	}
	msg += ";请补一条无条件默认分支,或修正条件表达式"
	st.Set(KeyAbort, "FAILED:"+nodeID)
	env.SetAbort("FAILED:" + nodeID)
	st.Set("_error", msg)
	// 必须同时写 StatusKey,不能只写 NodeStates:
	//   - 决策节点自己已经把 StatusKey 写成 SUCCESS(它确实是成功的 ——
	//     Agent 返回了决策,只是没有出口能走),不覆盖的话 StateData 里
	//     留的是 SUCCESS;
	//   - engine.collectResult 结束时用 StateData **整体替换** NodeStates,
	//     于是只写 NodeStates 的那个 FAILED 会被覆盖回 SUCCESS —— UI 上
	//     "决策点失败"就退化成了成功(实测)。
	//   - merge 节点读的是 StatusKey,不双写则下游同样看不到失败。
	st.Set(StatusKey(nodeID), string(model.NodeFailed))
	env.SetNodeState(nodeID, string(model.NodeFailed))
	env.Bus.Emit(event.New(event.NodeFailed, env.Exec.ID, nodeID, map[string]any{
		"node": nodeID, "error": msg,
	}))
}

// loopResetter 在每轮循环开始时清除循环体节点的完成状态,
// 让它们能被真实地重新执行(而不是被"已完成跳过"逻辑略过)。
func (l *lowerer) loopResetter(bodyNodeIDs []string, revisitNodeID, name string) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        l.uniqueName(name + ".reset"),
		Description: "循环迭代重置",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				actions := &session.EventActions{StateDelta: map[string]any{}}
				st := &stateAccess{ctx: ctx, actions: actions}
				if aborted(st) {
					return
				}
				ids := append([]string{revisitNodeID}, bodyNodeIDs...)
				for _, id := range ids {
					st.Set(StatusKey(id), "")
					st.Set(ResultKey(id), "")
				}
				// 决策快照:清空 KeyDecision 之前先留一份,供多条回边的
				// switch 选择本轮分支(值来自刚执行完的 review;第 1 轮来自
				// 循环外执行的决策节点)。不回存的话 switch 读到的永远是
				// 空串,所有回边条件都不成立,一条分支都不会走。
				if d, ok := st.Get(KeyDecision); ok && d != nil && fmt.Sprint(d) != "" {
					st.Set(KeyLoopDecision, fmt.Sprint(d))
				}
				st.Set(KeyDecision, "")
				// 轮数计数(值可能经 JSON 往返变成 float64)
				if n, ok := st.Get(KeyLoopCount); ok {
					if i, ok := IntOfOk(n); ok {
						st.Set(KeyLoopCount, i+1)
					} else {
						st.Set(KeyLoopCount, 1)
					}
				} else {
					st.Set(KeyLoopCount, 1)
				}
				yield(stateEvent(ctx, actions), nil)
			}
		},
	})
}

// loopGuard 是循环体内的守卫:出口条件成立 → Escalate 退出循环;
// 检测到连续相同 Review(停滞)→ Escalate 并标记 stuck。
func (l *lowerer) loopGuard(p *Plan, name string) (agent.Agent, error) {
	env := l.env
	var exitConds []string
	for _, b := range p.Branches {
		if b.Condition != "" {
			exitConds = append(exitConds, b.Condition)
		}
	}
	revisitID := p.Loop.RevisitNodeID

	return agent.New(agent.Config{
		Name:        name + ".guard",
		Description: "循环守卫",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				actions := &session.EventActions{StateDelta: map[string]any{}}
				st := &stateAccess{ctx: ctx, actions: actions}
				if aborted(st) {
					return
				}

				// 停滞检测:决策节点连续两轮结果完全相同
				histKey := "revisit:" + revisitID + ":history"
				summary := ""
				if raw, ok := st.Get(ResultKey(revisitID)); ok {
					var res NodeOutcome
					if json.Unmarshal([]byte(fmt.Sprint(raw)), &res) == nil {
						summary = res.Summary
					}
				}
				history, histErr := readHistory(st, histKey)
				if histErr != nil {
					// 状态类型不对 = 状态坏了。显式失败(不静默当成"没有历史"),
					// 理由见 readHistory 的注释:历史为空会让停滞检测失效,
					// 人工介入(HITL)的 responses 选项随之消失。
					msg := histErr.Error()
					st.Set(KeyAbort, "FAILED:"+revisitID)
					env.SetAbort("FAILED:" + revisitID)
					st.Set("_error", msg)
					st.Set(StatusKey(revisitID), string(model.NodeFailed))
					env.SetNodeState(revisitID, string(model.NodeFailed))
					env.Bus.Emit(event.New(event.NodeFailed, env.Exec.ID, revisitID, map[string]any{
						"node": revisitID, "error": msg,
					}))
					yield(escalateEvent(ctx, actions), nil)
					return
				}
				history = append(history, summary)
				if len(history) > 3 {
					history = history[len(history)-3:]
				}
				st.Set(histKey, history)
				if len(history) >= 2 && history[len(history)-1] == history[len(history)-2] && history[len(history)-1] != "" {
					st.Set(KeyLoopStuck, true)
					yield(escalateEvent(ctx, actions), nil)
					return
				}

				// 出口条件检查
				for _, cond := range exitConds {
					ok, err := EvalCondition(cond, st, env.Vars)
					if err != nil {
						continue
					}
					if ok {
						st.Set(KeyLoopExited, true)
						yield(escalateEvent(ctx, actions), nil)
						return
					}
				}
				yield(stateEvent(ctx, actions), nil)
			}
		},
	})
}

// readHistory 读回循环守卫的历史记录(状态里的字符串数组)。
//
// 类型不对(状态被写成字符串/对象/含非字符串元素)→ **显式失败**。
// 此前一律 `_ = json.Unmarshal(raw, &out)`,错误被吞 → out 变成空切片,
// 于是:
//   - 停滞检测(len(history) >= 2)永远不成立 → 循环不标记 stuck;
//   - 守卫不升级为人工介入 → 等待用户时的 prompts/**responses 选项**
//     随之消失(用户看不到该确认什么);
//   - 循环一路跑到底,执行还可能落成 COMPLETED —— "该失败却成功"
//     (约定 14:静默成功比崩溃更危险)。
//
// 因此这里把错误**交回调用方**,由调用方显式失败并给出可读原因。
func readHistory(st StateAccess, key string) ([]string, error) {
	v, ok := st.Get(key)
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("循环历史 %q 无法序列化: %w", key, err)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("循环历史 %q 的类型不是字符串数组: %w", key, err)
	}
	return out, nil
}

func mustUserInput(st StateAccess) map[string]any {
	v, ok := st.Get(KeyUserInput)
	if !ok {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"response": fmt.Sprint(v)}
}

func aborted(st StateAccess) bool {
	v, ok := st.Get(KeyAbort)
	return ok && v != nil && fmt.Sprint(v) != ""
}

// matchBranch 返回第一个条件成立的分支索引(无条件分支作为兜底)。
// 先匹配有条件分支,再兜底无条件分支。
func (l *lowerer) matchBranch(branches []RouteBranch, st StateAccess) (int, bool) {
	fallback := -1
	for i, b := range branches {
		if b.Condition == "" {
			if fallback < 0 {
				fallback = i
			}
			continue
		}
		ok, err := EvalCondition(b.Condition, st, l.env.Vars)
		if err == nil && ok {
			return i, true
		}
	}
	if fallback >= 0 {
		return fallback, true
	}
	return -1, false
}

// selectExitBranch 选定出口分支,并把选择记录到状态里。
//
// 两条路径:
//
//   - **回放**:该路由此前已选定过出口(状态里有记录),且本轮决策节点
//     被"恢复跳过"跳过(状态已是 success/skipped —— 本轮没有重新产出
//     output/decision)→ 回放上一轮的选择,**不重新求值**。
//     理由:条件求值的输入在恢复路径上已经不是当初那一份 —— output 在
//     state_json 里是**脱敏副本**(落库前脱敏,见 runtime/state_mask.go),
//     且它可能被后续节点覆盖成别的值。重新求值可能走另一条边:审批流里
//     就是"人批准 A 分支,恢复后走 B 分支"(约定 #14:走错分支比崩溃更
//     危险 —— 用户看不到任何异常)。
//   - **求值**:没有记录,或决策节点本轮重新执行过(输入变了)
//     → 按条件求值,并把选中的下标记下来(供后续恢复回放)。
//
// 记录的是**分支下标**(枚举),不含任何自由文本:不落凭据(约定 #8),
// 又足以在恢复时精确回到同一条分支。
func (l *lowerer) selectExitBranch(branches []RouteBranch, st StateAccess,
	decisionNodeID string, decisionSkipped bool) (int, bool) {
	if decisionSkipped {
		if idx, ok := recordedExitBranch(st, decisionNodeID); ok && idx >= 0 && idx < len(branches) {
			return idx, true
		}
	}
	idx, ok := l.matchBranch(branches, st)
	if !ok {
		return idx, false
	}
	// 记录路由依据:结构化枚举(分支下标),不落凭据。
	_ = st.Set(RouteBranchKey(decisionNodeID), idx)
	return idx, true
}

// recordedExitBranch 读出状态里记录的出口分支下标。
//
// 值经 JSON 往返会变成 float64(约定:JSON 往返类型),故用 IntOfOk 读。
func recordedExitBranch(st StateAccess, decisionNodeID string) (int, bool) {
	v, ok := st.Get(RouteBranchKey(decisionNodeID))
	if !ok || v == nil {
		return 0, false
	}
	idx, ok := IntOfOk(v)
	if !ok {
		return 0, false
	}
	return idx, true
}

// nodeAlreadyDone 判断节点是否已完成(恢复重放时会被直接跳过)。
//
// 与 nodeAgent 的跳过规则**同源**(两处各写一份必然漂移 —— 约定 #13
// 的同源精神):状态是 success / skipped 即视为已完成。
func nodeAlreadyDone(st StateAccess, nodeID string) bool {
	s, ok := st.Get(StatusKey(nodeID))
	if !ok || s == nil {
		return false
	}
	switch fmt.Sprint(s) {
	case string(model.NodeSuccess), string(model.NodeSkipped):
		return true
	}
	return false
}

// EvalCondition 用 expr 引擎对状态求值条件表达式。
// 可用变量: decision / status / output / iteration / variables.xxx。
//
// 可用变量集合的唯一来源是 conditionenv.go(校验期共用同一份定义),
// 两侧不允许各写一份 —— 见 AGENTS.md 硬性约定 #13。
func EvalCondition(expression string, st StateAccess, vars map[string]any) (bool, error) {
	return runCondition(expression, buildConditionEnv(st, vars, ""))
}

// evalConditionWithDecision 与 EvalCondition 相同,但 decision 取循环
// 体内的决策快照(KeyLoopDecision)。循环体每轮开头会清空 KeyDecision,
// 多回边分支必须据此判断本轮该走哪条回边。
func evalConditionWithDecision(expression string, st StateAccess, vars map[string]any) (bool, error) {
	decision := stringOf(st, KeyDecision)
	if snap, ok := st.Get(KeyLoopDecision); ok && snap != nil && fmt.Sprint(snap) != "" {
		decision = fmt.Sprint(snap)
	}
	return runCondition(expression, buildConditionEnv(st, vars, decision))
}

func runCondition(expression string, env expressionEnv) (bool, error) {
	program, err := expr.Compile(expression, expr.Env(env))
	if err != nil {
		return false, err
	}
	out, err := expr.Run(program, env)
	if err != nil {
		return false, err
	}
	switch v := out.(type) {
	case bool:
		return v, nil
	case string:
		return v != "", nil
	default:
		return out != nil, nil
	}
}

func stringOf(st StateAccess, key string) string {
	if v, ok := st.Get(key); ok {
		return fmt.Sprint(v)
	}
	return ""
}

func intOf(st StateAccess, key string) int {
	v, _ := st.Get(key)
	i, _ := IntOfOk(v)
	return i
}

// IntOfOk 把状态值转换为 int。恢复执行时状态经 JSON 持久化往返,
// int 会变成 float64,读取必须双类型兼容(executor.go 的 attempt 同理)。
func IntOfOk(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	}
	return 0, false
}

func isAgentLike(n *model.Node) bool {
	return n.Type == model.NodeTypeAgent
}

func loopLimitReason(stuck bool, onLoopLimit string) string {
	if stuck {
		return "stuck: 连续相同的 Review 结果,已停止重试"
	}
	return "max_iterations reached: " + onLoopLimit
}

// DumpPlan 调试用:打印 IR 结构。
func DumpPlan(p *Plan, indent int) string {
	pad := strings.Repeat("  ", indent)
	switch p.Kind {
	case KindSingle:
		return pad + "single " + p.NodeID + "\n"
	case KindSeq:
		var b strings.Builder
		b.WriteString(pad + "seq\n")
		for _, c := range p.Children {
			b.WriteString(DumpPlan(c, indent+1))
		}
		return b.String()
	case KindPar:
		var b strings.Builder
		b.WriteString(pad + "par\n")
		for _, c := range p.Children {
			b.WriteString(DumpPlan(c, indent+1))
		}
		return b.String()
	case KindRoute:
		var b strings.Builder
		b.WriteString(pad + "route " + p.NodeID + "\n")
		for _, br := range p.Branches {
			b.WriteString(pad + "  cond: " + br.Condition + "\n")
			b.WriteString(DumpPlan(br.Target, indent+2))
		}
		if p.Loop != nil {
			b.WriteString(pad + "  loop(max=" + fmt.Sprint(p.Loop.MaxIterations) + ")\n")
			b.WriteString(DumpPlan(p.Loop.Body, indent+2))
		}
		return b.String()
	}
	return pad + "?\n"
}
