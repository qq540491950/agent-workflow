package validator

import (
	"testing"

	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

func mustWf(t *testing.T, yml string) *model.Workflow {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return doc.ToModel()
}

func has(res *Result, code string) bool {
	for _, e := range res.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

const validWorkflowYAML = `
version: "1"
workflow:
  id: demo
  name: Demo
  settings:
    max_iterations: 5
nodes:
  - id: plan
    type: agent
    agent: claude-code
    mode: plan
  - id: review
    type: agent
    agent: claude-code
    mode: review
  - id: fix
    type: agent
    agent: mock
    mode: fix
  - id: submit
    type: skill
    skill: submit
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
`

// TestValidWorkflow 通过的样例(含 Review→Fix 循环)。
func TestValidWorkflow(t *testing.T) {
	wf := mustWf(t, validWorkflowYAML)
	res := Validate(wf, agentResolver([]string{"claude-code", "mock"}), skillResolver([]string{"submit"}))
	if !res.Valid {
		t.Fatalf("expected valid, got errors: %+v", res.Errors)
	}
}

func TestMissingAgentAndSkill(t *testing.T) {
	wf := mustWf(t, validWorkflowYAML)
	res := Validate(wf, agentResolver([]string{"claude-code"}), skillResolver(nil))
	if !has(res, "AGENT_NOT_FOUND") {
		t.Fatalf("expected AGENT_NOT_FOUND, got %+v", res.Errors)
	}
	if !has(res, "SKILL_NOT_FOUND") {
		t.Fatalf("expected SKILL_NOT_FOUND, got %+v", res.Errors)
	}
	for _, e := range res.Errors {
		if e.Code == "AGENT_NOT_FOUND" && e.Node != "fix" {
			t.Errorf("AGENT_NOT_FOUND should locate node 'fix', got node=%q", e.Node)
		}
	}
}

func TestInvalidEdgeAndUnreachable(t *testing.T) {
	yml := `
workflow:
  id: demo
  name: Demo
nodes:
  - id: a
    type: human
  - id: b
    type: human
  - id: c
    type: human
edges:
  - from: a
    to: ghost
  - from: island
    to: b
`
	wf := mustWf(t, yml)
	res := Validate(wf, nil, nil)
	if !has(res, "INVALID_EDGE") {
		t.Fatalf("expected INVALID_EDGE: %+v", res.Errors)
	}
	if !has(res, "UNREACHABLE_NODE") {
		t.Fatalf("expected UNREACHABLE_NODE: %+v", res.Errors)
	}
	if !has(res, "ISOLATED_NODE") {
		t.Fatalf("expected ISOLATED_NODE: %+v", res.Errors)
	}
	// a 与 c 都是起始节点(a 有出边无入边, c 完全无边)
	if !has(res, "MULTIPLE_START") {
		t.Fatalf("expected MULTIPLE_START: %+v", res.Errors)
	}
}

func TestCycleWithoutConditionAndNoExit(t *testing.T) {
	yml := `
workflow:
  id: loop
  name: Loop
  settings:
    max_iterations: 3
nodes:
  - id: a
    type: human
  - id: b
    type: human
edges:
  - from: a
    to: b
  - from: b
    to: a
`
	wf := mustWf(t, yml)
	res := Validate(wf, nil, nil)
	if !has(res, "NO_START") {
		t.Fatalf("expected NO_START: %+v", res.Errors)
	}
	if !has(res, "CYCLE_WITHOUT_CONDITION") {
		t.Fatalf("expected CYCLE_WITHOUT_CONDITION: %+v", res.Errors)
	}
	if !has(res, "NO_EXIT_PATH") {
		t.Fatalf("expected NO_EXIT_PATH: %+v", res.Errors)
	}
}

func TestLoopWithoutMaxIterationsWarns(t *testing.T) {
	yml := `
workflow:
  id: loop3
  name: Loop3
nodes:
  - id: start
    type: human
  - id: work
    type: human
  - id: gate
    type: condition
    expression: iteration < 3
  - id: end
    type: human
edges:
  - from: start
    to: work
  - from: work
    to: gate
  - from: gate
    condition: iteration < 3
    to: work
  - from: gate
    condition: iteration >= 3
    to: end
`
	wf := mustWf(t, yml)
	res := Validate(wf, nil, nil)
	if !res.Valid {
		t.Fatalf("expected valid workflow: %+v", res.Errors)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Code == "LOOP_WITHOUT_MAX_ITERATIONS" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected LOOP_WITHOUT_MAX_ITERATIONS warning, got %+v", res.Warnings)
	}
}

func TestInvalidExpression(t *testing.T) {
	yml := `
workflow:
  id: cond
  name: Cond
nodes:
  - id: a
    type: human
  - id: b
    type: condition
    expression: "decision ==== "
edges:
  - from: a
    to: b
`
	wf := mustWf(t, yml)
	res := Validate(wf, nil, nil)
	if !has(res, "INVALID_EXPRESSION") {
		t.Fatalf("expected INVALID_EXPRESSION: %+v", res.Errors)
	}
}

func TestUnknownNodeTypeAndMissingConfig(t *testing.T) {
	yml := `
workflow:
  id: x
  name: X
nodes:
  - id: a
    type: quantum
  - id: b
    type: agent
    mode: plan
  - id: c
    type: git
    operation: rebase
edges:
  - from: a
    to: b
  - from: b
    to: c
`
	wf := mustWf(t, yml)
	res := Validate(wf, agentResolver([]string{"claude-code"}), nil)
	if !has(res, "UNKNOWN_NODE_TYPE") {
		t.Fatalf("expected UNKNOWN_NODE_TYPE: %+v", res.Errors)
	}
	if !has(res, "MISSING_AGENT") {
		t.Fatalf("expected MISSING_AGENT: %+v", res.Errors)
	}
	if !has(res, "INVALID_GIT_OPERATION") {
		t.Fatalf("expected INVALID_GIT_OPERATION: %+v", res.Errors)
	}
}

func TestErrorStructure(t *testing.T) {
	wf := mustWf(t, validWorkflowYAML)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if len(res.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	e := res.Errors[0]
	if e.Code == "" || e.Message == "" {
		t.Fatalf("error must have code and message: %+v", e)
	}
}

// ---- helpers ----

type resSet map[string]bool

func agentResolver(ids []string) AgentResolver {
	set := resSet{}
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

func skillResolver(ids []string) SkillResolver {
	set := resSet{}
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}
