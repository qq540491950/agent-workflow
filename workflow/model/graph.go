// 工作流图查询的**单一来源**(约定 13:校验期与运行期必须同源)。
//
// 此前 reachesNode / parallelJoin 在 workflow/validator 与 workflow/compiler
// 各有一份逐行同构的实现(注释还自认"与编译器同口径")—— 双实现各自演化
// 就是轮次 27 P0("用户被自己的校验器挡住")的形态。图语义(可达性、
// 并行汇合点判定)是模型级事实,与谁消费无关,统一收在这里。
package model

// Reaches 判断从 from 是否能沿有向边到达 target(seen 为访问过的节点,
// 防环;调用方通常传 map[string]bool{})。
func (w *Workflow) Reaches(from, target string, seen map[string]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, e := range w.OutgoingEdges(from) {
		if w.Reaches(e.To, target, seen) {
			return true
		}
	}
	return false
}

// ParallelJoin 返回并行区(parID,branches 为其出边)的汇合点:
// 入边数>1、且是所有分支起点的公共可达节点。找不到返回空串。
func (w *Workflow) ParallelJoin(parID string, branches []Edge) string {
	for i := range w.Nodes {
		n := &w.Nodes[i]
		if n.ID == parID || len(w.IncomingEdges(n.ID)) <= 1 {
			continue
		}
		all := true
		for _, be := range branches {
			if !w.Reaches(be.To, n.ID, map[string]bool{}) {
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
