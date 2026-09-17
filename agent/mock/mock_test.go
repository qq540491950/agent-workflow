package mock

import (
	"context"
	"sync"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/workflow/model"
)

func TestDefaultDecisionDone(t *testing.T) {
	a := New(Options{})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Task: "t", Mode: "review"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Decision != agent.DecisionDone || resp.Status != agent.StatusSuccess {
		t.Errorf("resp = %+v", resp)
	}
}

func TestDecisionSequence(t *testing.T) {
	a := New(Options{
		Scripts: map[string]*Script{
			"review": {Decisions: []agent.AgentDecision{agent.DecisionRejected, agent.DecisionApproved}},
		},
	})
	ctx := context.Background()
	r1, _ := a.Execute(ctx, agent.AgentRequest{Mode: "review"})
	r2, _ := a.Execute(ctx, agent.AgentRequest{Mode: "review"})
	r3, _ := a.Execute(ctx, agent.AgentRequest{Mode: "review"})
	if r1.Decision != agent.DecisionRejected {
		t.Errorf("call1 = %s, want REJECTED", r1.Decision)
	}
	if r2.Decision != agent.DecisionApproved {
		t.Errorf("call2 = %s, want APPROVED", r2.Decision)
	}
	if r3.Decision != agent.DecisionApproved {
		t.Errorf("call3 = %s, want APPROVED(序列耗尽保持最后一个)", r3.Decision)
	}
	if a.Calls("review") != 3 {
		t.Errorf("Calls(review) = %d, want 3", a.Calls("review"))
	}
}

func TestResetMock(t *testing.T) {
	a := New(Options{
		Scripts: map[string]*Script{
			"review": {Decisions: []agent.AgentDecision{agent.DecisionRejected, agent.DecisionApproved}},
		},
	})
	ctx := context.Background()
	a.Execute(ctx, agent.AgentRequest{Mode: "review"})
	a.Execute(ctx, agent.AgentRequest{Mode: "review"})
	a.ResetMock()
	if a.Calls("review") != 0 {
		t.Errorf("after reset Calls = %d, want 0", a.Calls("review"))
	}
	if len(a.Summaries("review")) != 0 {
		t.Errorf("after reset Summaries = %v", a.Summaries("review"))
	}
	r, _ := a.Execute(ctx, agent.AgentRequest{Mode: "review"})
	if r.Decision != agent.DecisionRejected {
		t.Errorf("after reset first decision = %s, want REJECTED(序列从头开始)", r.Decision)
	}
}

func TestModeFallbackToDefault(t *testing.T) {
	a := New(Options{Default: &Script{SummaryTemplate: "default for {mode-less} {call}"}})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Mode: "unknown-mode"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary == "" {
		t.Error("default script not applied")
	}
}

func TestFailWith(t *testing.T) {
	a := New(Options{Scripts: map[string]*Script{
		"execute": {FailWith: "boom"},
	}})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Mode: "execute"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != agent.StatusFailed || resp.Error != "boom" || resp.Decision != agent.DecisionError {
		t.Errorf("resp = %+v", resp)
	}
}

func TestTemplateRender(t *testing.T) {
	a := New(Options{Scripts: map[string]*Script{
		"review": {SummaryTemplate: "task={task} iter={iteration} call={call}", Issues: []map[string]any{
			{"severity": "high", "file": "a.go"},
		}},
	}})
	resp, _ := a.Execute(context.Background(), agent.AgentRequest{
		Task: "fix bug", Mode: "review", Context: map[string]any{"iteration": 2},
	})
	if resp.Summary != "task=fix bug iter=2 call=1" {
		t.Errorf("summary = %q", resp.Summary)
	}
	issues, ok := resp.Data["issues"].([]map[string]any)
	if !ok || len(issues) != 1 || issues[0]["file"] != "a.go" {
		t.Errorf("issues = %v", resp.Data["issues"])
	}
}

func TestDelayCancel(t *testing.T) {
	a := New(Options{Scripts: map[string]*Script{
		"execute": {DelayMS: 5000},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		cancel()
	}()
	_, err := a.Execute(ctx, agent.AgentRequest{Mode: "execute"})
	if !model.IsKind(err, model.KindCancelledError) {
		t.Errorf("err = %v, want CancelledError", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	a := New(Options{Default: &Script{DelayMS: 1}})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Execute(context.Background(), agent.AgentRequest{Mode: "review"})
		}()
	}
	wg.Wait()
	if a.Calls("review") != 50 {
		t.Errorf("Calls = %d, want 50", a.Calls("review"))
	}
}

// 回归:iteration 经 JSON 持久化往返变 float64,模板 {iteration} 曾渲染 0。
func TestTemplateIterationFloat64(t *testing.T) {
	a := New(Options{Scripts: map[string]*Script{
		"review": {SummaryTemplate: "iter={iteration}"},
	}})
	resp, _ := a.Execute(context.Background(), agent.AgentRequest{
		Mode: "review", Context: map[string]any{"iteration": float64(3)},
	})
	if resp.Summary != "iter=3" {
		t.Errorf("summary = %q, want iter=3", resp.Summary)
	}
}
