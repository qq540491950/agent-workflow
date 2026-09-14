package compiler

import (
	"strings"
	"testing"

	"agentworkflow/workflow/dsl"
)

func build(t *testing.T, yml string) *Plan {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return BuildPlan(doc.ToModel())
}

// TestPlanLinearChain 线性链编译为单个 seq。
func TestPlanLinearChain(t *testing.T) {
	p := build(t, `
workflow:
  id: linear
  name: Linear
nodes:
  - id: a
    type: human
  - id: b
    type: human
  - id: c
    type: human
edges:
  - from: a
    to: b
  - from: b
    to: c
`)
	if p.Kind != KindSeq || len(p.Children) != 3 {
		t.Fatalf("plan = %+v", p)
	}
	if got := strings.Join(p.Path, ","); got != "a,b,c" {
		t.Errorf("path = %q", got)
	}
}

// TestPlanLoopPattern 验证 Review→Fix 循环被识别为 route + loop。
func TestPlanLoopPattern(t *testing.T) {
	p := build(t, `
version: "1"
workflow:
  id: loop
  name: Loop
settings:
  max_iterations: 7
nodes:
  - id: plan
    type: human
  - id: review
    type: agent
    agent: claude-code
    mode: review
  - id: fix
    type: human
  - id: submit
    type: human
edges:
  - from: plan
    to: review
  - from: review
    condition: decision == "APPROVED"
    to: submit
  - from: review
    condition: decision == "REJECTED"
    to: fix
  - from: fix
    to: review
`)
	dump := DumpPlan(p, 0)
	if !strings.Contains(dump, "route review") {
		t.Fatalf("expected route at review:\n%s", dump)
	}
	if !strings.Contains(dump, "loop(max=7)") {
		t.Fatalf("expected loop with max=7:\n%s", dump)
	}
	// fix 在循环体内,submit 在出口分支
	if !strings.Contains(dump, "single fix") {
		t.Fatalf("fix should be in loop body:\n%s", dump)
	}
	if !strings.Contains(dump, `cond: decision == "APPROVED"`) {
		t.Fatalf("exit branch condition missing:\n%s", dump)
	}
}

// TestPlanParallelPattern 验证 parallel → merge 编译为 par。
func TestPlanParallelPattern(t *testing.T) {
	p := build(t, `
workflow:
  id: par
  name: Par
nodes:
  - id: start
    type: human
  - id: fanout
    type: parallel
  - id: sec
    type: human
  - id: code
    type: human
  - id: test
    type: human
  - id: merge
    type: merge
  - id: decide
    type: human
edges:
  - from: start
    to: fanout
  - from: fanout
    to: sec
  - from: fanout
    to: code
  - from: fanout
    to: test
  - from: sec
    to: merge
  - from: code
    to: merge
  - from: test
    to: merge
  - from: merge
    to: decide
`)
	dump := DumpPlan(p, 0)
	if !strings.Contains(dump, "par") {
		t.Fatalf("expected par plan:\n%s", dump)
	}
	if strings.Count(dump, "single") != 6 {
		t.Fatalf("expected 6 single nodes (start,3 branches,merge,decide):\n%s", dump)
	}
}
