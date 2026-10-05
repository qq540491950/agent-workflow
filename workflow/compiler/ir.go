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
	// KindSwitch 纯条件选择:只按条件挑一条链执行,自身不执行任何节点。
	// 与 KindRoute 的区别是不含决策节点(不会重新执行它),用于一个决策点
	// 存在多条回边时,在循环体内选择本轮走哪条回边。
	KindSwitch PlanKind = "switch"
	// KindWarning 只发告警事件、不执行任何节点。用于"编译期发现拓扑
	// 问题但不妨碍执行"的场景(如并行分支不收敛)。
	KindWarning PlanKind = "warning"
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
	// Unconverged 标记并行区没有公共汇合点(各分支终点互不相同)。
	// 此时分支之后的链不会进入计划,必须由 lower 阶段发告警 ——
	// 静默丢弃等于"该跑的没跑却报成功"。
	Unconverged bool
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
	case KindSwitch:
		var out []string
		for _, b := range p.Branches {
			out = append(out, b.Target.Flatten()...)
		}
		return out
	case KindWarning:
		// 不执行任何节点,不产生节点 ID
		return nil
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
	return b.chainStop(fromID, ancestors, nil)
}

// chainStop 是 chain 的完整形式:stop 为本层的停止集合(并行区汇合点 /
// 循环回边目标),命中即停止向下展开。
//
// stop 必须显式沿调用链传递,不能放在 irBuilder 上当共享字段:内层
// chainStop(嵌套并行 / 分支内路由)返回时会把共享字段清空,外层尚未
// 展开完的 stop 集合就此丢失 —— 分支随后一路走过汇合点及其后所有节点,
// 那些节点在分支内被重复执行一次。并行区里 ADK 会并发跑分支,重复执行
// 意味着并发写同一批状态。
func (b *irBuilder) chainStop(fromID string, ancestors map[string]bool, stop map[string]bool) *Plan {
	var children []*Plan
	var path []string
	visited := map[string]bool{}
	cur := fromID
	for {
		if cur == "" || visited[cur] || ancestors[cur] {
			break
		}
		if stop != nil && stop[cur] {
			break
		}
		// 并行区域:par + 汇合后的后续链(需要真正的多分支才有并行语义)
		if node, _ := b.wf.FindNode(cur); node.Type == model.NodeTypeParallel && len(b.wf.OutgoingEdges(cur)) >= 2 {
			par := b.parallel(cur, ancestors, stop)
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
			children = append(children, b.route(cur, ancestors, stop))
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
func (b *irBuilder) parallel(parID string, ancestors map[string]bool, outerStop map[string]bool) []*Plan {
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
		// 分支的停止集合 = 内层汇合点 ∪ 外层停止集合。
		// 只放内层汇合点会让分支一路走到外层的汇合点及其后节点 ——
		// 那些节点本该在并行区之后执行一次,却会在分支内被重复执行,
		// 而并行分支是并发跑的,等于并发写同一批状态。
		stop := copySet(outerStop)
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
		// 汇合点之后的链同样受外层停止集合约束
		plans = append(plans, b.chainStop(join, newAncestors, outerStop))
		return plans
	}
	// 不收敛:各分支的终点互不相同,没有公共汇合点。
	//
	// 此前塞一个合成的 "<parID>.merge" 占位节点并丢掉并行区之后的链 ——
	// 用户在工作流里并不存在这个节点,它落到 lower 时变成 noop,而
	// 分支之后的节点一个都没进 Plan。执行会报成功,但那一段根本没跑。
	// 现在不造假节点:直接返回并行区本身,并在 Plan 上标记不收敛,
	// 由 lower 阶段发告警事件(执行仍继续跑各分支,只是提醒用户
	// 拓扑没有收口,后续链不会执行)。
	plans[0].Unconverged = true
	plans[0].Branches = nil
	plans = append(plans, &Plan{
		Kind:   KindWarning,
		NodeID: parID,
		Branches: []RouteBranch{{
			Condition: "",
			Target:    &Plan{Kind: KindSeq}, // 空动作,仅用于承载告警
		}},
	})
	return plans
}

// route 编译决策节点:执行节点后按条件选择出口;检测回边形成循环。
func (b *irBuilder) route(decisionID string, ancestors map[string]bool, stop map[string]bool) *Plan {
	out := b.wf.OutgoingEdges(decisionID)

	// 分类出口:loop 回边 vs exit 出口
	var exits []model.Edge
	var loopEdges []model.Edge
	for i := range out {
		e := out[i]
		if b.reaches(e.To, decisionID, map[string]bool{}) {
			loopEdges = append(loopEdges, e)
		} else {
			exits = append(exits, e)
		}
	}

	rp := &Plan{Kind: KindRoute, NodeID: decisionID}
	newAncestors := copySet(ancestors)
	newAncestors[decisionID] = true

	// exit 分支:出口链同样受外层停止集合约束(并行分支内的路由,
	// 出口不能越过并行区的汇合点)
	for _, e := range exits {
		rp.Branches = append(rp.Branches, RouteBranch{
			Condition: e.Condition,
			Target:    b.chainStop(e.To, newAncestors, stop),
		})
	}

	// loop 分支。回边可能有多条(如 review --REJECTED--> fix 与
	// review --REFACTOR--> refactor 都回到 review):此前只保留第一条,
	// 其余既不进 exits 也不进 loop —— 对应子图永远不会进入 Plan、
	// 永不执行,而校验器的可达性是纯图连通性判断,该节点"可达"、
	// 校验通过,用户完全无从察觉。合并为一个循环体:回边条件作为
	// 并列分支,任一成立即可进入循环(语义上等价于"这些路径都要回到
	// 决策点重跑")。
	if len(loopEdges) > 0 {
		// 循环体的停止集合 = 决策节点自身(防无限展开)∪ 外层停止集合
		bodyStop := copySet(stop)
		bodyStop[decisionID] = true
		spec := &LoopSpec{
			RevisitNodeID: decisionID,
			MaxIterations: b.maxIterations,
			OnLoopLimit:   b.onLoopLimit,
		}
		if len(loopEdges) == 1 {
			spec.Body = b.chainStop(loopEdges[0].To, newAncestors, bodyStop)
		} else {
			// 多条回边:用 switch 表达"本轮走哪条回边" —— 每条回边的条件
			// 对应一个分支,任一成立即执行该分支并回到决策点。
			spec.Body = &Plan{Kind: KindSwitch}
			for _, e := range loopEdges {
				spec.Body.Branches = append(spec.Body.Branches, RouteBranch{
					Condition: e.Condition,
					Target:    b.chainStop(e.To, newAncestors, bodyStop),
				})
			}
		}
		rp.Loop = spec
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
