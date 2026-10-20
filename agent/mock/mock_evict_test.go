package mock

import (
	"context"
	"testing"
	"time"

	"agentworkflow/agent"
)

// 迭代 7 审计新增:按执行隔离的计数空间在超限时会被淘汰 —— 但不能
// 淘汰**仍在使用**的空间,否则并发执行时第二次调用会拿到第一次的决策
// (决策序列被清零,工作流走错分支)。

// TestEvictionSkipsInUseSpaces 空间上限为 1 时,正在执行的 A 不得被
// B 的到来挤掉:A 的第二次调用必须继续沿用原序列。
func TestEvictionSkipsInUseSpaces(t *testing.T) {
	old := maxRunSpaces
	maxRunSpaces = 1
	defer func() { maxRunSpaces = old }()

	a := New(Options{Scripts: map[string]*Script{
		"slow": {DelayMS: 300, Decisions: []agent.AgentDecision{agent.DecisionRejected, agent.DecisionApproved}},
		"fast": {Decisions: []agent.AgentDecision{agent.DecisionDone}},
	}})

	ctx := context.Background()
	done := make(chan agent.AgentDecision, 1)
	go func() {
		r1, _ := a.Execute(ctx, agent.AgentRequest{Mode: "slow", ExecutionID: "A"})
		r2, _ := a.Execute(ctx, agent.AgentRequest{Mode: "slow", ExecutionID: "A"})
		done <- r1.Decision
		done <- r2.Decision
	}()

	// 等 A 占住空间后再让 B 进来
	time.Sleep(80 * time.Millisecond)
	if _, err := a.Execute(ctx, agent.AgentRequest{Mode: "fast", ExecutionID: "B"}); err != nil {
		t.Fatalf("execute B: %v", err)
	}

	first := <-done
	second := <-done
	if first != agent.DecisionRejected {
		t.Errorf("A 第一次决策 = %s, want REJECTED", first)
	}
	if second != agent.DecisionApproved {
		t.Errorf("A 第二次决策 = %s, want APPROVED —— 仍在使用的空间被淘汰,序列被清零", second)
	}
}
