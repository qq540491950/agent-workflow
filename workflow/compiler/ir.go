// Package compiler 把 Workflow DSL 图编译为可执行的 ADK Agent 树。
//
// 编译原则(与需求保持一致):
//   - 静态结构(线性链) → ADK SequentialAgent
//   - 并行区域(parallel → merge) → ADK ParallelAgent
//   - 运行时决策(条件路由、Review→Fix 循环) → ADK 自定义 Agent +
//     LoopAgent(MaxIterations + Escalate 语义)
//
// 编译分为两步: graph.go 构建 Plan IR(纯数据,可测试),
// lower.go 将 IR 降级为 ADK agent 树。
package compiler

import "agentworkflow/workflow/model"

// PlanKind 是 IR 节点种类。
type PlanKind string

const (
	KindSingle PlanKind = "single" // 单个节点
	KindSeq    PlanKind = "seq"    // 顺序执行(ADK SequentialAgent)
	KindPar    PlanKind = "par"    // 并行分支(ADK ParallelAgent)
	KindRoute  PlanKind = "route"  // 条件路由 + 可选循环(动态)
)

// RouteBranch 是路由的一个出口分支。
type RouteBranch struct {
	Condition string // expr 表达式;空串表示无条件(兜底)
	Target    *Plan
}

// LoopSpec 描述 route 检测到的循环(如 Review→Fix→Review)。
type LoopSpec struct {
	// Body 是循环体内链路(不含决策节点本身)。
	Body *Plan
	// RevisitNodeID 每轮循环重新执行的决策节点(如 review)。
	RevisitNodeID string
	// MaxIterations 最大迭代数(防无限循环)。
	MaxIterations int
	// OnLoopLimit 达到上限后的策略: wait_user | fail。
	OnLoopLimit string
}

// Plan 是编译中间表示(IR)。
type Plan struct {
	Kind     PlanKind
	NodeID   string  // KindSingle/KindRoute 的节点 ID
	Children []*Plan // KindSeq/KindPar 的子计划
	Branches []RouteBranch
	Loop     *LoopSpec
	// NodeID 序列(仅 KindSeq 用于调试展示)。
	Path []string
}

// Flatten 返回 IR 中出现的全部节点 ID(测试与展示用)。
func (p *Plan) Flatten() []string {
	if p == nil {
		return nil
	}
	switch p.Kind {
	case KindSingle:
		return []string{p.NodeID}
	case KindRoute:
		out := []string{p.NodeID}
		for _, b := range p.Branches {
			out = append(out, b.Target.Flatten()...)
		}
		if p.Loop != nil {
			out = append(out, p.Loop.Body.Flatten()...)
			out = append(out, p.Loop.RevisitNodeID)
		}
		return out
	default:
		var out []string
		for _, c := range p.Children {
			out = append(out, c.Flatten()...)
		}
		return out
	}
}

// irBuilder 从 Workflow 图构建 Plan IR。
type irBuilder struct {
	wf *model.Workflow
	// maxIterations 为循环默认上限。
	maxIterations int
	onLoopLimit   string
	// stopped 为 chain 构建时的临时停止集合(仅 chainStop 设置)。
	stopped map[string]bool
}

// BuildPlan 将已通过校验的 Workflow 编译为 Plan IR。
func BuildPlan(wf *model.Workflow) *Plan {
	maxIter := wf.Settings.MaxIterations
	if maxIter <= 0 {
		maxIter = 5
	}
	onLoopLimit := wf.Settings.OnLoopLimit
	if onLoopLimit == "" {
		onLoopLimit = "wait_user"
	}
	b := &irBuilder{wf: wf, maxIterations: maxIter, onLoopLimit: onLoopLimit}

	start := b.findStart()
	if start == "" {
		return &Plan{Kind: KindSeq}
	}
	return b.chain(start, map[string]bool{})
}

// findStart 返回第一个无入边的节点(校验器保证至多一个)。
func (b *irBuilder) findStart() string {
	for _, n := range b.wf.Nodes {
		if len(b.wf.IncomingEdges(n.ID)) == 0 {
			return n.ID
		}
	}
	// 全部有入边(校验已拒绝;兜底取首节点)
	if len(b.wf.Nodes) > 0 {
		return b.wf.Nodes[0].ID
	}
	return ""
}

// chain 构建从 fromID 开始的线性计划;遇到决策点并入 route;
// 遇到 stopSet(汇合点/回边目标)即停止。
func (b *irBuilder) chain(fromID string, ancestors map[string]bool) *Plan {
	var children []*Plan
	var path []string
	visited := map[string]bool{}
	cur := fromID
	for {
		if cur == "" || visited[cur] || ancestors[cur] {
			break
		}
		if b.stopped != nil && b.stopped[cur] {
			break
		}
		// 并行区域:par + 汇合后的后续链(需要真正的多分支才有并行语义)
		if node, _ := b.wf.FindNode(cur); node.Type == model.NodeTypeParallel && len(b.wf.OutgoingEdges(cur)) >= 2 {
			par := b.parallel(cur, ancestors)
			if par != nil {
				children = append(children, par...)
				path = append(path, cur)
				break // parallel() 已包含汇合点之后的链
			}
		}
		out := b.wf.OutgoingEdges(cur)
		visited[cur] = true
		path = append(path, cur)

		// 决策点:多条出边或带条件出边
		if len(out) > 1 || (len(out) == 1 && out[0].Condition != "") {
			children = append(children, b.route(cur, ancestors))
			path = append(path, cur+"(route)")
			break
		}

		children = append(children, &Plan{Kind: KindSingle, NodeID: cur})
		if len(out) == 0 {
			break
		}
		cur = out[0].To
	}
	return &Plan{Kind: KindSeq, Children: children, Path: path}
}

// parallel 编译 parallel 节点:每个出边是一条分支,分支在汇合点
// (入边>1 的节点)处结束;返回 [par, 汇合点后续链]。
func (b *irBuilder) parallel(parID string, ancestors map[string]bool) []*Plan {
	parNode, err := b.wf.FindNode(parID)
	if err != nil {
		return nil
	}
	out := b.wf.OutgoingEdges(parID)
	if len(out) < 2 {
		// 退化:没有真正的并行分支,按普通节点处理
		return []*Plan{{Kind: KindSingle, NodeID: parID}}
	}

	// 汇合点:所有分支的公共后继(取第一条分支可达的、入边数>1 的节点)
	join := b.findJoin(parID, out)
	_ = parNode

	var branches []*Plan
	newAncestors := copySet(ancestors)
	newAncestors[parID] = true
	for _, e := range out {
		stop := map[string]bool{}
		if join != "" {
			stop[join] = true
		}
		branches = append(branches, b.chainStop(e.To, newAncestors, stop))
	}
	plans := []*Plan{{
		Kind:     KindPar,
		NodeID:   parID,
		Children: branches,
	}}
	if join != "" {
		plans = append(plans, b.chain(join, newAncestors))
	} else {
		plans = append(plans, &Plan{Kind: KindSingle, NodeID: parID + ".merge"})
	}
	return plans
}

// chainStop 与 chain 相同,但额外在 stopSet 命中时停止。
func (b *irBuilder) chainStop(fromID string, ancestors map[string]bool, stopSet map[string]bool) *Plan {
	b.stopped = stopSet
	p := b.chain(fromID, ancestors)
	b.stopped = nil
	return p
}

// route 编译决策节点:执行节点后按条件选择出口;检测回边形成循环。
func (b *irBuilder) route(decisionID string, ancestors map[string]bool) *Plan {
	out := b.wf.OutgoingEdges(decisionID)

	// 分类出口:loop 回边 vs exit 出口
	var exits []model.Edge
	var loopEdge *model.Edge
	for i := range out {
		e := out[i]
		if b.reaches(e.To, decisionID, map[string]bool{}) {
			if loopEdge == nil {
				cp := e
				loopEdge = &cp
			}
		} else {
			exits = append(exits, e)
		}
	}

	rp := &Plan{Kind: KindRoute, NodeID: decisionID}
	newAncestors := copySet(ancestors)
	newAncestors[decisionID] = true

	// exit 分支
	for _, e := range exits {
		rp.Branches = append(rp.Branches, RouteBranch{
			Condition: e.Condition,
			Target:    b.chain(e.To, newAncestors),
		})
	}

	// loop 分支
	if loopEdge != nil {
		stop := map[string]bool{decisionID: true}
		body := b.chainStop(loopEdge.To, newAncestors, stop)
		rp.Loop = &LoopSpec{
			Body:          body,
			RevisitNodeID: decisionID,
			MaxIterations: b.maxIterations,
			OnLoopLimit:   b.onLoopLimit,
		}
		// loop 退出条件 = 任一 exit 分支条件成立。
		// guard 在降级阶段生成;IR 不需要显式存条件。
	}
	return rp
}

// reaches 判断 from 是否能沿边到达 target(有向可达性)。
func (b *irBuilder) reaches(from, target string, seen map[string]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, e := range b.wf.OutgoingEdges(from) {
		if b.reaches(e.To, target, seen) {
			return true
		}
	}
	return false
}

// findJoin 寻找并行分支的汇合点:入边数>1、且是所有分支起点的公共可达节点。
func (b *irBuilder) findJoin(parID string, branches []model.Edge) string {
	for _, n := range b.wf.Nodes {
		if n.ID == parID || len(b.wf.IncomingEdges(n.ID)) <= 1 {
			continue
		}
		all := true
		for _, be := range branches {
			if !b.reaches(be.To, n.ID, map[string]bool{}) {
				all = false
				break
			}
		}
		if all {
			return n.ID
		}
	}
	return ""
}

func copySet(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m)+1)
	for k := range m {
		out[k] = true
	}
	return out
}
