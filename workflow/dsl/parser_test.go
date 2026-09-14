package dsl

import (
	"testing"

	"agentworkflow/workflow/model"
)

const sampleYAML = `
version: "1"

workflow:
  id: coding-task
  name: Coding Task
  description: 典型编码任务流程

variables:
  max_review_iterations: 5

settings:
  max_iterations: 10
  on_loop_limit: wait_user

nodes:
  - id: plan
    name: 制定方案
    type: agent
    agent: claude-code
    mode: plan
    x: 100
    y: 200
    prompt:
      template: |
        Analyze the task: {{task}}

  - id: execute
    type: agent
    agent: pi-agent
    mode: execute

  - id: review
    type: agent
    agent: claude-code
    mode: review
    timeout_seconds: 600

  - id: fix
    type: agent
    agent: pi-agent
    mode: fix
    retry:
      max_attempts: 3
      backoff: exponential

  - id: submit
    type: skill
    agent: pi-agent
    skill: submit

edges:
  - from: plan
    to: execute
  - from: execute
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

func TestParseYAML(t *testing.T) {
	doc, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if doc.Version != "1" {
		t.Errorf("version = %q, want 1", doc.Version)
	}
	if doc.Workflow.ID != "coding-task" {
		t.Errorf("workflow.id = %q", doc.Workflow.ID)
	}
	if doc.Settings.MaxIterations != 10 {
		t.Errorf("max_iterations = %d, want 10", doc.Settings.MaxIterations)
	}
	if len(doc.Nodes) != 5 {
		t.Fatalf("nodes = %d, want 5", len(doc.Nodes))
	}
	if len(doc.Edges) != 5 {
		t.Fatalf("edges = %d, want 5", len(doc.Edges))
	}
	plan := doc.Nodes[0]
	if plan.Type != "agent" || plan.Rest["agent"] != "claude-code" {
		t.Errorf("plan node config = %+v", plan.Rest)
	}
	if plan.X == nil || *plan.X != 100 {
		t.Errorf("plan node x = %v", plan.X)
	}
}

func TestParseJSON(t *testing.T) {
	doc, err := Parse([]byte(`{
		"version": "1",
		"workflow": {"id": "demo", "name": "Demo"},
		"nodes": [{"id": "a", "type": "human", "name": "等待用户"}],
		"edges": []
	}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if doc.Workflow.ID != "demo" || len(doc.Nodes) != 1 {
		t.Fatalf("unexpected doc: %+v", doc)
	}
	if doc.Nodes[0].Type != "human" {
		t.Errorf("node type = %q", doc.Nodes[0].Type)
	}
}

func TestParseError(t *testing.T) {
	if _, err := Parse([]byte("workflow: [unclosed")); err == nil {
		t.Fatal("expected parse error for invalid yaml")
	}
}

func TestToModelRoundtrip(t *testing.T) {
	doc, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if wf.Settings.MaxIterations != 10 {
		t.Errorf("model max_iterations = %d", wf.Settings.MaxIterations)
	}
	if wf.Nodes[0].Position.X != 100 {
		t.Errorf("position.x = %v", wf.Nodes[0].Position.X)
	}
	if wf.Nodes[0].Name != "制定方案" {
		t.Errorf("node name fallback failed: %q", wf.Nodes[0].Name)
	}
	if wf.Nodes[1].Name != "execute" {
		t.Errorf("node name default should be id: %q", wf.Nodes[1].Name)
	}
	// 回写为 DSL 再解析,保持结构
	doc2 := FromModel(wf)
	wf2 := doc2.ToModel()
	if len(wf2.Nodes) != len(wf.Nodes) || len(wf2.Edges) != len(wf.Edges) {
		t.Fatalf("roundtrip lost structure: %d nodes, %d edges", len(wf2.Nodes), len(wf2.Edges))
	}
	if wf2.Nodes[0].Config["agent"] != "claude-code" {
		t.Errorf("roundtrip lost config: %+v", wf2.Nodes[0].Config)
	}
}

func TestEncodeYAML(t *testing.T) {
	doc, _ := Parse([]byte(sampleYAML))
	wf := doc.ToModel()
	out, err := FromModel(wf).EncodeYAML()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	doc3, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if doc3.Workflow.ID != "coding-task" {
		t.Errorf("reparse id = %q", doc3.Workflow.ID)
	}
}

func TestModelGraph(t *testing.T) {
	wf := &model.Workflow{
		Nodes: []model.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []model.Edge{{From: "a", To: "b"}, {From: "a", To: "c"}},
	}
	if got := len(wf.OutgoingEdges("a")); got != 2 {
		t.Errorf("outgoing = %d", got)
	}
	if got := len(wf.IncomingEdges("b")); got != 1 {
		t.Errorf("incoming = %d", got)
	}
	if _, err := wf.FindNode("zzz"); err == nil {
		t.Error("expected error for missing node")
	}
}
