// Package mock 提供可在不启动 Claude Code / Pi Agent 的情况下
// 完整测试 Workflow 的 Mock Agent 实现。
package mock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"agentworkflow/agent"
	"agentworkflow/workflow/model"
)

// Script 定义 MockAgent 对某种 mode 的应答脚本。
type Script struct {
	// Decision 序列:每次该 mode 被调用时按顺序弹出;
	// 序列耗尽后若 Repeat 为 true 则循环使用最后一个,否则保持最后一个。
	Decisions []agent.AgentDecision
	// Status 覆盖默认 SUCCESS。
	Status agent.AgentStatus
	// SummaryTemplate / OutputTemplate 支持引用 {task} {iteration} {call}。
	SummaryTemplate string
	OutputTemplate  string
	// Issues 作为 Data 附带返回(模拟 Review 问题列表)。
	Issues []map[string]any
	// DelayMS 模拟执行耗时。
	DelayMS int
	// FailWith 非空时返回 FAILED 与错误信息。
	FailWith string
}

// Options 是 MockAgent 的配置。
type Options struct {
	ID   string
	Name string
	// Scripts 按 mode 定义应答;未命中的 mode 使用 Default。
	Scripts map[string]*Script
	Default *Script
}

// Agent 是可脚本化的 Mock 实现,用于集成测试与 UI 演示。
type Agent struct {
	opt Options

	mu        sync.Mutex
	calls     map[string]int // mode -> 已调用次数
	summaries map[string][]string
}

// New 创建 MockAgent。
func New(opt Options) *Agent {
	if opt.ID == "" {
		opt.ID = "mock"
	}
	if opt.Name == "" {
		opt.Name = "Mock Agent"
	}
	return &Agent{
		opt:       opt,
		calls:     map[string]int{},
		summaries: map[string][]string{},
	}
}

// ID 实现 agent.Agent。
func (a *Agent) ID() string { return a.opt.ID }

// Name 实现 agent.Agent。
func (a *Agent) Name() string { return a.opt.Name }

// Calls 返回指定 mode 的调用次数(测试断言用)。
func (a *Agent) Calls(mode string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[mode]
}

// Summaries 返回指定 mode 的历次摘要(测试断言用)。
func (a *Agent) Summaries(mode string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.summaries[mode]))
	copy(out, a.summaries[mode])
	return out
}

// Execute 实现 agent.Agent:按脚本生成结构化响应。
func (a *Agent) Execute(ctx context.Context, req agent.AgentRequest) (*agent.AgentResponse, error) {
	a.mu.Lock()
	call := a.calls[req.Mode]
	a.calls[req.Mode] = call + 1
	a.mu.Unlock()

	script := a.opt.Scripts[req.Mode]
	if script == nil {
		script = a.opt.Default
	}
	if script == nil {
		script = &Script{}
	}

	if script.DelayMS > 0 {
		select {
		case <-time.After(time.Duration(script.DelayMS) * time.Millisecond):
		case <-ctx.Done():
			return nil, model.NewError(model.KindCancelledError, "AGENT_CANCELLED", "mock agent cancelled")
		}
	}

	resp := &agent.AgentResponse{
		Status: agent.StatusSuccess,
		Data:   map[string]any{},
	}
	if script.Status != "" {
		resp.Status = script.Status
	}
	if script.FailWith != "" {
		resp.Status = agent.StatusFailed
		resp.Error = script.FailWith
		resp.Decision = agent.DecisionError
		resp.Summary = "mock failure: " + script.FailWith
		a.record(req.Mode, resp)
		return resp, nil
	}

	// 决策:按序列弹出,耗尽后停在最后一个
	if len(script.Decisions) > 0 {
		idx := call
		if idx >= len(script.Decisions) {
			idx = len(script.Decisions) - 1
		}
		resp.Decision = script.Decisions[idx]
	}
	if resp.Decision == "" {
		resp.Decision = agent.DecisionDone
	}

	iter := 0
	if v, ok := req.Context["iteration"].(int); ok {
		iter = v
	}
	resp.Summary = render(script.SummaryTemplate, req.Task, iter, call+1)
	if resp.Summary == "" {
		resp.Summary = fmt.Sprintf("mock[%s] mode=%s call=%d decision=%s", a.opt.ID, req.Mode, call+1, resp.Decision)
	}
	resp.Output = render(script.OutputTemplate, req.Task, iter, call+1)
	if len(script.Issues) > 0 {
		resp.Data["issues"] = script.Issues
	}
	a.record(req.Mode, resp)
	return resp, nil
}

func (a *Agent) record(mode string, resp *agent.AgentResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.summaries[mode] = append(a.summaries[mode], resp.Summary)
}

func render(tpl, task string, iteration, call int) string {
	out := tpl
	out = replaceAll(out, "{task}", task)
	out = replaceAll(out, "{iteration}", fmt.Sprint(iteration))
	out = replaceAll(out, "{call}", fmt.Sprint(call))
	return out
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
