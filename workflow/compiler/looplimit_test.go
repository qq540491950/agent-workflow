package compiler

import (
	"fmt"
	"testing"
)

// 本文件锁住 on_loop_limit 在**编译期**的归一化行为。
//
// 与 workflow/validator/looplimit_test.go 是同一份白名单的两端
// (约定 13:校验期与运行期同源):校验期报出非法值,编译期对历史快照里
// 遗留的非法值 fail closed(按 fail 处理),不再静默落回 wait_user ——
// 用户写错一个字母,执行却停在"等待人工介入",既不是他要的语义,
// 也没有任何提示(约定 14)。

// findLoop 找出计划里第一个循环规格。
func findLoop(p *Plan) *LoopSpec {
	if p == nil {
		return nil
	}
	if p.Loop != nil {
		return p.Loop
	}
	for _, c := range p.Children {
		if l := findLoop(c); l != nil {
			return l
		}
	}
	for _, b := range p.Branches {
		if l := findLoop(b.Target); l != nil {
			return l
		}
	}
	return nil
}

const loopLimitYAML = `
version: "1"
settings:
  max_iterations: 3
  on_loop_limit: %s
workflow:
  id: loop-limit
  name: Loop Limit
nodes:
  - id: review
    type: agent
    agent: claude-code
    mode: review
  - id: fix
    type: human
  - id: submit
    type: human
edges:
  - from: review
    condition: decision == "APPROVED"
    to: submit
  - from: review
    condition: decision == "REJECTED"
    to: fix
  - from: fix
    to: review
`

// TestLoopLimitIllegalValueFailsClosed 非法取值必须按 fail 处理(不得落回 wait_user)。
func TestLoopLimitIllegalValueFailsClosed(t *testing.T) {
	p := build(t, fmt.Sprintf(loopLimitYAML, "fl"))
	l := findLoop(p)
	if l == nil {
		t.Fatalf("未识别出循环:\n%s", DumpPlan(p, 0))
	}
	if l.OnLoopLimit != "fail" {
		t.Errorf("非法 on_loop_limit 应按 %q 处理,实际 = %q", "fail", l.OnLoopLimit)
	}
}

// TestLoopLimitLegalValuePreserved 合法取值必须原样保留。
func TestLoopLimitLegalValuePreserved(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"fail", "fail"},
		{"wait_user", "wait_user"},
	} {
		p := build(t, fmt.Sprintf(loopLimitYAML, tc.in))
		l := findLoop(p)
		if l == nil {
			t.Fatalf("未识别出循环:\n%s", DumpPlan(p, 0))
		}
		if l.OnLoopLimit != tc.want {
			t.Errorf("on_loop_limit=%q 应保留为 %q,实际 = %q", tc.in, tc.want, l.OnLoopLimit)
		}
	}
}
