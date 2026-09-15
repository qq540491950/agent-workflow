package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/expr-lang/expr"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/session"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

// 会话状态键(执行状态通过 ADK Session State 持久化)。
const (
	KeyTask        = "task"
	KeyAbort       = "_abort"       // 非空 = 执行中断(失败/等待),后续节点跳过
	KeyResumeNode  = "_resume_node" // 恢复执行时从该节点开始
	KeyUserInput   = "_user_input"  // 人工节点的用户响应
	KeyLoopExited  = "_loop_exited" // loop 因出口条件成立而退出
	KeyLoopStuck   = "_loop_stuck"  // 检测到连续相同 Review,陷入停滞
	KeyLoopCount   = "_loop_count"  // 当前循环轮数
	KeyDecision    = "decision"     // 最近一次 Agent 决策
	KeyStatus      = "status"       // 最近一次 Agent 状态
	KeyOutput      = "output"       // 最近一次 Agent 输出
	KeyCurrentNode = "_current_node"
)

// StatusKey / ResultKey 生成节点级状态键。
func StatusKey(nodeID string) string { return "node:" + nodeID + ":status" }
func ResultKey(nodeID string) string { return "node:" + nodeID + ":result" }

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
}

// stateAccess 基于 ADK InvocationContext 的实现:
// 写入同时进入 EventActions.StateDelta(随事件持久化)与活动会话状态。
type stateAccess struct {
	ctx     agent.InvocationContext
	actions *session.EventActions
}

func (s *stateAccess) Get(key string) (any, bool) {
	v, err := s.ctx.Session().State().Get(key)
	if err != nil {
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
	name = l.uniqueName(name)
	switch p.Kind {
	case KindSingle:
		node, err := l.findNode(p.NodeID)
		if err != nil {
			return l.noopAgent(name), nil // 汇合占位节点
		}
		return l.nodeAgent(node, ""), nil
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
		return parallelagent.New(parallelagent.Config{
			AgentConfig: agent.Config{Name: name, Description: "并行分支", SubAgents: subs},
		})
	case KindRoute:
		return l.routeAgent(p, name)
	default:
		return l.noopAgent(name), nil
	}
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
func (l *lowerer) noopAgent(name string) agent.Agent {
	a, _ := agent.New(agent.Config{
		Name:        name,
		Description: "noop",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
	return a
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
func (l *lowerer) nodeAgent(node *model.Node, suffix string) agent.Agent {
	env := l.env
	a, err := agent.New(agent.Config{
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
				if s, ok := st.Get(StatusKey(node.ID)); ok {
					if s == string(model.NodeSuccess) || s == string(model.NodeSkipped) {
						return
					}
				}
				// 人工节点恢复:等待中的节点收到用户输入后重新执行
				if s, ok := st.Get(StatusKey(node.ID)); ok && s == string(model.NodeWaiting) {
					resume, _ := st.Get(KeyResumeNode)
					if fmt.Sprint(resume) != node.ID {
						return // 未被指定恢复的等待节点保持等待
					}
					st.Set(KeyResumeNode, "")
					st.Set(KeyUserInput, mustUserInput(st))
				}

				st.Set(StatusKey(node.ID), string(model.NodeRunning))
				st.Set(KeyCurrentNode, node.ID)
				env.Exec.CurrentNodeID = node.ID
				env.Exec.NodeStates[node.ID] = string(model.NodeRunning)
				env.Bus.Emit(event.New(event.NodeStarted, env.Exec.ID, node.ID, map[string]any{
					"node": node.ID, "name": node.Name, "type": string(node.Type),
				}))

				if isAgentLike(node) {
					env.Bus.Emit(event.New(event.AgentStarted, env.Exec.ID, node.ID, map[string]any{
						"agent": fmt.Sprint(node.Config["agent"]), "mode": fmt.Sprint(node.Config["mode"]),
					}))
				}

				outcome := env.Runner.ExecuteNode(ctx, env, node, st)

				// 写回状态
				st.Set(StatusKey(node.ID), string(outcome.State))
				if raw, err := json.Marshal(outcome); err == nil {
					st.Set(ResultKey(node.ID), string(raw))
				}
				env.Exec.NodeStates[node.ID] = string(outcome.State)

				switch outcome.State {
				case model.NodeSuccess:
					if outcome.Decision != "" {
						st.Set(KeyDecision, outcome.Decision)
					}
					st.Set(KeyStatus, "success")
					st.Set(KeyOutput, outcome.Output)
					env.Exec.NodeStates[node.ID] = string(model.NodeSuccess)
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
	if err != nil {
		// agent.New 仅在名字为空/子代理重复时失败,这里名字恒非空
		panic(err)
	}
	return a
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
	firstNode := l.nodeAgent(decisionNode, "")
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
		revisit := l.nodeAgent(decisionNode, "#revisit") // 新实例,避免同一 Agent 双父
		guard := l.loopGuard(p, name)
		maxIter := p.Loop.MaxIterations
		if maxIter <= 0 {
			maxIter = 5
		}
		// resetter 在每轮循环开始时清除循环体内节点的完成标记,
		// 使 fix/review 等节点能够真实地重新执行。
		loopNodeIDs = p.Loop.Body.Flatten()
		resetter := l.loopResetter(loopNodeIDs, p.Loop.RevisitNodeID, name)
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
				if idx, ok := l.matchBranch(exitBranches, st); ok {
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
						if idx, ok := l.matchBranch(exitBranches, st); ok {
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
						st.Set("_error", "循环达到最大迭代数: "+decisionNode.ID)
						yield(escalateEvent(ctx, actions), nil)
						return
					}
					// 默认 wait_user:暂停等待人工干预
					st.Set(KeyResumeNode, decisionNode.ID)
					st.Set(KeyAbort, "WAIT_USER")
					env.Bus.Emit(event.New(event.HumanInputRequired, env.Exec.ID, decisionNode.ID, map[string]any{
						"node":      decisionNode.ID,
						"prompt":    fmt.Sprintf("工作流在节点 %s 循环 %d 轮后仍未通过,请人工处理", decisionNode.ID, p.Loop.MaxIterations),
						"responses": []string{"approve", "reject", "continue", "instruction"},
					}))
					yield(escalateEvent(ctx, actions), nil)
					return
				}

				// 4. 无匹配出口且无循环:走第一个无条件出口(若有),否则结束
				for i, b := range exitBranches {
					if b.Condition == "" {
						for ev, err := range exitAgents[i].Run(ctx) {
							if !yield(ev, err) {
								return
							}
						}
						break
					}
				}
				yield(stateEvent(ctx, actions), nil)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	return routeAgt, nil
}

// loopResetter 在每轮循环开始时清除循环体节点的完成状态,
// 让它们能被真实地重新执行(而不是被"已完成跳过"逻辑略过)。
func (l *lowerer) loopResetter(bodyNodeIDs []string, revisitNodeID, name string) agent.Agent {
	a, _ := agent.New(agent.Config{
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
				st.Set(KeyDecision, "")
				// 轮数计数
				if n, ok := st.Get(KeyLoopCount); ok {
					if i, ok := n.(int); ok {
						st.Set(KeyLoopCount, i+1)
					}
				} else {
					st.Set(KeyLoopCount, 1)
				}
				yield(stateEvent(ctx, actions), nil)
			}
		},
	})
	return a
}

// loopGuard 是循环体内的守卫:出口条件成立 → Escalate 退出循环;
// 检测到连续相同 Review(停滞)→ Escalate 并标记 stuck。
func (l *lowerer) loopGuard(p *Plan, name string) agent.Agent {
	env := l.env
	var exitConds []string
	for _, b := range p.Branches {
		if b.Condition != "" {
			exitConds = append(exitConds, b.Condition)
		}
	}
	revisitID := p.Loop.RevisitNodeID

	a, _ := agent.New(agent.Config{
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
				history := readHistory(st, histKey)
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
	return a
}

func readHistory(st StateAccess, key string) []string {
	v, ok := st.Get(key)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
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

// EvalCondition 用 expr 引擎对状态求值条件表达式。
// 可用变量: decision / status / output / iteration / variables.xxx。
func EvalCondition(expression string, st StateAccess, vars map[string]any) (bool, error) {
	env := map[string]any{
		"decision":  stringOf(st, KeyDecision),
		"status":    stringOf(st, KeyStatus),
		"output":    stringOf(st, KeyOutput),
		"iteration": intOf(st, KeyLoopCount),
		"variables": vars,
	}
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
	if v, ok := st.Get(key); ok {
		if i, ok := v.(int); ok {
			return i
		}
	}
	return 0
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
