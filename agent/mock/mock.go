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
	calls     map[string]int // mode -> 已调用次数(无 ExecutionID 的共享空间)
	summaries map[string][]string
	// runs 按 ExecutionID 隔离的计数空间。同一个 Mock 实例被多个执行
	// 并发使用时(同一工作流跑两次、嵌套工作流),决策序列互不干扰。
	runs map[string]*runState
}

// runState 是一次执行(或无执行上下文的直接调用)的计数空间。
type runState struct {
	calls     map[string]int
	summaries map[string][]string
	// shared 为真时 maps 由 Agent 自身持有(Calls/Summaries 能读到)
	shared bool
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
		runs:      map[string]*runState{},
	}
}

// ID 实现 agent.Agent。
func (a *Agent) ID() string { return a.opt.ID }

// Name 实现 agent.Agent。
func (a *Agent) Name() string { return a.opt.Name }

// Calls 返回指定 mode 的调用次数(测试断言用)。
// 注意:只统计无 ExecutionID 的共享空间;工作流执行中的调用按
// executionID 隔离,需用 CallsFor(execID, mode) 断言。
func (a *Agent) Calls(mode string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[mode]
}

// CallsFor 返回某次执行内指定 mode 的调用次数(测试断言用)。
func (a *Agent) CallsFor(executionID, mode string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rs, ok := a.runs[executionID]; ok {
		return rs.calls[mode]
	}
	return 0
}

// SummariesFor 返回某次执行内指定 mode 的历次摘要(测试断言用)。
func (a *Agent) SummariesFor(executionID, mode string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rs, ok := a.runs[executionID]; ok {
		out := make([]string, len(rs.summaries[mode]))
		copy(out, rs.summaries[mode])
		return out
	}
	return nil
}

// Summaries 返回指定 mode 的历次摘要(测试断言用)。
func (a *Agent) Summaries(mode string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.summaries[mode]))
	copy(out, a.summaries[mode])
	return out
}

// ResetMock 清空调用计数与历史,使决策序列从头开始。
// 每次工作流执行前由 runtime 调用,保证多次运行行为一致。
//
// 只清共享空间(无 ExecutionID 的直接调用,如单测与 Agents 页 Test),
// **不动按执行隔离的空间**:同一工作流并发启动两次时,第二次执行
// 若把第一次正在跑的序列清零,会出现"第二次运行的第 N 次调用拿到
// 第 1 次的决策"。带 ExecutionID 的调用天然从零开始,无需重置。
func (a *Agent) ResetMock() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = map[string]int{}
	a.summaries = map[string][]string{}
}

// maxRunSpaces 限制按执行隔离的计数空间数量。
// 桌面应用是长生命周期进程,每次执行永久留一个 runState 会无界增长
// (这是 ExecutionID 隔离引入的新问题,原共享空间不存在)。
// 超限后按 FIFO 复用最旧的空间 —— 那个执行早已结束,复用它无害。
const maxRunSpaces = 256

// stateFor 返回本次调用应使用的计数空间。
func (a *Agent) stateFor(executionID string) *runState {
	if executionID == "" {
		// 共享空间:直接复用 Agent 自身的两个 map(保持既有 API 行为)
		return &runState{calls: a.calls, summaries: a.summaries, shared: true}
	}
	if rs, ok := a.runs[executionID]; ok {
		return rs
	}
	// 超限:丢弃最旧的一个空间(FIFO),再登记新的
	if len(a.runs) >= maxRunSpaces {
		for k := range a.runs { // map 迭代顺序随机,任意一个都够用
			delete(a.runs, k)
			break
		}
	}
	rs := &runState{calls: map[string]int{}, summaries: map[string][]string{}}
	a.runs[executionID] = rs
	return rs
}

// Execute 实现 agent.Agent:按脚本生成结构化响应。
func (a *Agent) Execute(ctx context.Context, req agent.AgentRequest) (*agent.AgentResponse, error) {
	a.mu.Lock()
	rs := a.stateFor(req.ExecutionID)
	call := rs.calls[req.Mode]
	rs.calls[req.Mode] = call + 1
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
		a.record(rs, req.Mode, resp)
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
	// iteration 可能经 JSON 持久化往返变成 float64
	switch v := req.Context["iteration"].(type) {
	case int:
		iter = v
	case float64:
		iter = int(v)
	}
	resp.Summary = render(script.SummaryTemplate, req.Task, iter, call+1)
	if resp.Summary == "" {
		resp.Summary = fmt.Sprintf("mock[%s] mode=%s call=%d decision=%s", a.opt.ID, req.Mode, call+1, resp.Decision)
	}
	resp.Output = render(script.OutputTemplate, req.Task, iter, call+1)
	if len(script.Issues) > 0 {
		resp.Data["issues"] = script.Issues
	}
	a.record(rs, req.Mode, resp)
	return resp, nil
}

func (a *Agent) record(rs *runState, mode string, resp *agent.AgentResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rs.summaries[mode] = append(rs.summaries[mode], resp.Summary)
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
