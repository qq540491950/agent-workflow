package agent_test

import (
	"context"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
)

func TestRegistryRegisterGetList(t *testing.T) {
	r := agent.NewRegistry()
	a1 := mock.New(mock.Options{ID: "mock-a", Name: "A"})
	a2 := mock.New(mock.Options{ID: "mock-b", Name: "B"})
	if err := r.Register(a1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(a2); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(a1); err == nil {
		t.Fatal("expected duplicate register error")
	}
	got, err := r.Get("mock-a")
	if err != nil || got.ID() != "mock-a" {
		t.Fatalf("get: %v %v", got, err)
	}
	if _, err := r.Get("nope"); err == nil {
		t.Fatal("expected not found error")
	}
	list := r.List()
	if len(list) != 2 || list[0].ID() != "mock-a" || list[1].ID() != "mock-b" {
		t.Fatalf("list order/content wrong: %+v", list)
	}
}

func TestMockAgentDecisionSequence(t *testing.T) {
	m := mock.New(mock.Options{
		ID: "mock-review",
		Scripts: map[string]*mock.Script{
			"review": {Decisions: []agent.AgentDecision{agent.DecisionRejected, agent.DecisionApproved}},
		},
	})
	ctx := context.Background()

	// 第一次 REJECTED
	r1, err := m.Execute(ctx, agent.AgentRequest{Mode: "review", Task: "do it"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r1.Decision != agent.DecisionRejected {
		t.Fatalf("first decision = %s, want REJECTED", r1.Decision)
	}
	// 第二次 APPROVED
	r2, _ := m.Execute(ctx, agent.AgentRequest{Mode: "review", Task: "do it"})
	if r2.Decision != agent.DecisionApproved {
		t.Fatalf("second decision = %s, want APPROVED", r2.Decision)
	}
	// 第三次:序列耗尽,停留在 APPROVED
	r3, _ := m.Execute(ctx, agent.AgentRequest{Mode: "review", Task: "do it"})
	if r3.Decision != agent.DecisionApproved {
		t.Fatalf("third decision = %s, want APPROVED (sticky)", r3.Decision)
	}
	if m.Calls("review") != 3 {
		t.Errorf("calls = %d, want 3", m.Calls("review"))
	}
}

func TestMockAgentFailureInjection(t *testing.T) {
	m := mock.New(mock.Options{
		ID: "mock-fail",
		Scripts: map[string]*mock.Script{
			"execute": {FailWith: "boom"},
		},
	})
	resp, err := m.Execute(context.Background(), agent.AgentRequest{Mode: "execute"})
	if err != nil {
		t.Fatalf("execute should not return go error: %v", err)
	}
	if resp.Status != agent.StatusFailed || resp.Decision != agent.DecisionError || resp.Error != "boom" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestBuiltinSkills(t *testing.T) {
	reg := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := reg.Register(s); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}
	if len(reg.List()) != 3 {
		t.Fatalf("skills registered = %d, want 3", len(reg.List()))
	}
	sub, _ := reg.Get("submit")
	resp, err := sub.Execute(context.Background(), skill.SkillRequest{
		Args:    map[string]any{"message": "feat: demo"},
		Context: map[string]any{"summary": "ok"},
	})
	if err != nil || resp.Status != "SUCCESS" {
		t.Fatalf("submit: %v %+v", err, resp)
	}
	if resp.Data["commit_message"] != "feat: demo" {
		t.Errorf("commit_message = %v", resp.Data["commit_message"])
	}

	rt, _ := reg.Get("run-test")
	failResp, _ := rt.Execute(context.Background(), skill.SkillRequest{Args: map[string]any{"fail": true}})
	if failResp.Status != "FAILED" {
		t.Errorf("injected test failure not reported: %+v", failResp)
	}
}
