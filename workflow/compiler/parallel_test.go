package compiler

import (
	"sort"
	"testing"

	"agentworkflow/workflow/dsl"
)

func mustPlan(t *testing.T, yml string) *Plan {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return BuildPlan(doc.ToModel())
}

func flattenSet(p *Plan) map[string]int {
	out := map[string]int{}
	for _, id := range p.Flatten() {
		out[id]++
	}
	return out
}

// TestNestedParallelKeepsOuterStopSet 暴露 B1:`irBuilder.stopped` 是共享
// 字段,内层 chainStop 返回时把它置 nil,外层的 stop 集合(并行区汇合点)
// 就此丢失 —— 于是并行分支 1 内部的嵌套并行结束后,外层分支会一路走过
// 汇合点及其后所有节点。
//
// 期望:汇合点 merge 及其之后的节点只出现在并行区**之后**一次,
// 不应出现在任何分支内部。
func TestNestedParallelKeepsOuterStopSet(t *testing.T) {
	plan := mustPlan(t, `
workflow:
  id: nested-par
  name: 并行套并行
nodes:
  - id: outer
    type: parallel
  - id: b1
    type: parallel
  - id: b1x
    type: skill
    skill: log
  - id: b1y
    type: skill
    skill: log
  - id: inner_join
    type: merge
  - id: b1tail
    type: skill
    skill: log
  - id: b2
    type: skill
    skill: log
  - id: merge
    type: merge
  - id: after
    type: skill
    skill: log
edges:
  - from: outer
    to: b1
  - from: b1
    to: b1x
  - from: b1
    to: b1y
  - from: b1x
    to: inner_join
  - from: b1y
    to: inner_join
  - from: inner_join
    to: b1tail
  - from: outer
    to: b2
  - from: b1tail
    to: merge
  - from: b2
    to: merge
  - from: merge
    to: after
`)
	counts := flattenSet(plan)
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	t.Logf("plan nodes = %v", ids)

	// 外层汇合点与其后节点在整个计划里各只应出现一次。
	// 若 stopped 被内层清空,b1 分支会继续走到 merge/after,
	// 它们就会出现两次(一次在分支内、一次在并行区之后)。
	for _, id := range []string{"merge", "after"} {
		if counts[id] != 1 {
			t.Errorf("节点 %q 出现 %d 次,应为 1 —— 外层 stop 集合在内层 chainStop 后丢失,"+
				"并行分支越过了汇合点(plan=%v)", id, counts[id], ids)
		}
	}
	// 分支 1 的收尾节点应当在分支内,而不是并行区之后
	if counts["b1tail"] != 1 {
		t.Errorf("节点 b1tail 出现 %d 次,应为 1", counts["b1tail"])
	}
}

// TestNonConvergingParallelIsReported 暴露 B2:并行分支不收敛时
// (`join == ""`),当前实现直接把分支之后的链丢掉,并塞一个合成的
// `<parID>.merge` 占位节点 —— 后续节点一个都不执行,执行却报成功。
// 期望:计划里必须能看到"未收敛"这件事,而不是悄悄少一段。
func TestNonConvergingParallelIsReported(t *testing.T) {
	plan := mustPlan(t, `
workflow:
  id: diverging
  name: 不收敛并行
nodes:
  - id: fanout
    type: parallel
  - id: a
    type: skill
    skill: log
  - id: b
    type: skill
    skill: log
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
	ids := plan.Flatten()
	t.Logf("plan nodes = %v", ids)
	for _, want := range []string{"a", "b", "tail_a", "tail_b"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("节点 %q 未进入执行计划(plan=%v)", want, ids)
		}
	}
	// 不得出现合成的假节点
	for _, id := range ids {
		if id == "fanout.merge" {
			t.Errorf("计划里出现合成的占位节点 %q(用户在工作流里并不存在它)", id)
		}
	}
}
