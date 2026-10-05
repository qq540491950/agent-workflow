package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// 本文件覆盖批次 A1:Agent/Skill 节点返回 WAIT_USER 后,用户答复曾被
// 完全丢弃 —— lower.go 把 _user_input 塞回状态就放行,runAgentNode /
// runSkillNode 从不读它,Agent 被第二次真实调用。
// 在 execute/fix 模式下这等于重复写文件、重复跑测试、重复 git 操作。
//
// 已确认的语义:复用 WAIT_USER 那次的响应,答复作为上下文注入
// ({{user_instruction}}),节点按"已答复"完成,不再重复调 Agent。

// waitUserAgent 第一次调用返回 WAIT_USER 并记账,之后每次调用都记账。
// calls 用于断言"恢复后有没有被重复调用"。
type waitUserAgent struct {
	mu       sync.Mutex
	calls    int
	sideFx   int // 模拟副作用(写文件/提交),每次真实调用 +1
	decision coreagent.AgentDecision
}

func (a *waitUserAgent) ID() string   { return "wait-agent" }
func (a *waitUserAgent) Name() string { return "Wait Agent" }

func (a *waitUserAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	a.mu.Lock()
	a.calls++
	n := a.calls
	a.sideFx++
	a.mu.Unlock()

	if n == 1 {
		return &coreagent.AgentResponse{
			Status:  coreagent.StatusWaitUser,
			Summary: "需要确认执行方案",
			Data:    map[string]any{"responses": []string{"approve", "reject", "instruction"}},
		}, nil
	}
	dec := a.decision
	if dec == "" {
		dec = coreagent.DecisionDone
	}
	return &coreagent.AgentResponse{
		Status:   coreagent.StatusSuccess,
		Decision: dec,
		Summary:  "已执行(第 " + itoa(n) + " 次真实调用)",
		Output:   "side-effect-" + itoa(a.sideFx),
	}, nil
}

func (a *waitUserAgent) count() (calls, sideFx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, a.sideFx
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// newWaitUserEngine 构造一个注册了 waitUserAgent 的引擎。
func newWaitUserEngine(t *testing.T, a *waitUserAgent) (*Engine, *persistence.DB, *event.Bus) {
	t.Helper()
	repo, err := persistence.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	bus := event.NewBus()
	bus.KeepHistory = true

	agents := coreagent.NewRegistry()
	if err := agents.Register(a); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	_ = agents.Register(mock.New(mock.Options{ID: "mock-a"}))
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		if err := skills.Register(sk); err != nil {
			t.Fatalf("register skill: %v", err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set("wait-agent", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	eng.Perms.Set("workflow", permission.Policy{FilesystemRead: true, GitRead: true, GitCommit: true})
	return eng, repo, bus
}

const waitUserAgentYAML = `
workflow:
  id: wait-agent-wf
  name: Agent 等待用户
nodes:
  - id: work
    name: Work
    type: agent
    agent: wait-agent
    mode: execute
  - id: submit
    name: Submit
    type: skill
    skill: log
edges:
  - from: work
    to: submit
`

func loadWfYAML(t *testing.T, eng *Engine, yml string) *model.Workflow {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("workflow invalid: %+v", res.Errors)
	}
	return wf
}

// TestAgentWaitUserResumeReusesResponse 核心回归:Agent 节点 WAIT_USER
// 被答复后不得被第二次真实调用(副作用只发生一次),且用户答复要进入
// 上下文供后续节点引用。
func TestAgentWaitUserResumeReusesResponse(t *testing.T) {
	ag := &waitUserAgent{}
	eng, _, _ := newWaitUserEngine(t, ag)
	wf := loadWfYAML(t, eng, waitUserAgentYAML)

	exec, err := eng.Start(context.Background(), wf, "写一个功能", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)

	callsBefore, fxBefore := ag.count()
	if callsBefore != 1 || fxBefore != 1 {
		t.Fatalf("等待前应恰好调用 1 次,实际 calls=%d sideFx=%d", callsBefore, fxBefore)
	}

	if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{
		"response":    "instruction",
		"instruction": "改用方案 B",
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED,实际 %s (%s)", final.State, final.Error)
	}

	callsAfter, fxAfter := ag.count()
	if callsAfter != callsBefore {
		t.Errorf("Agent 被重复调用:%d → %d(答复后不得再执行一次)", callsBefore, callsAfter)
	}
	if fxAfter != fxBefore {
		t.Errorf("副作用发生 %d 次,应只有 1 次(execute 模式会重复写文件)", fxAfter)
	}
	// 用户答复进入上下文
	if got, _ := final.StateData["user_instruction"].(string); got != "改用方案 B" {
		t.Errorf("用户答复应写入上下文供后续节点引用,实际 %q", got)
	}
	// 后续节点正常推进
	if final.NodeStates["submit"] != string(model.NodeSuccess) {
		t.Errorf("答复后应继续推进到 submit,实际 %v", final.NodeStates)
	}
}

// TestAgentWaitUserApproveReusesResponse 批准路径同样不得重复调用。
func TestAgentWaitUserApproveReusesResponse(t *testing.T) {
	ag := &waitUserAgent{}
	eng, _, _ := newWaitUserEngine(t, ag)
	wf := loadWfYAML(t, eng, waitUserAgentYAML)

	exec, err := eng.Start(context.Background(), wf, "写一个功能", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)
	before, fxBefore := ag.count()

	if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED,实际 %s (%s)", final.State, final.Error)
	}
	if after, _ := ag.count(); after != before {
		t.Errorf("approve 后 Agent 被重复调用:%d → %d", before, after)
	}
	if _, fxAfter := ag.count(); fxAfter != fxBefore {
		t.Errorf("副作用次数变化:%d → %d", fxBefore, fxAfter)
	}
}

// TestAgentWaitUserRejectFailsExecution reject 必须让工作流失败,
// 而不是"复用上一次的成功响应"把拒绝吞掉。
func TestAgentWaitUserRejectFailsExecution(t *testing.T) {
	ag := &waitUserAgent{}
	eng, _, bus := newWaitUserEngine(t, ag)
	wf := loadWfYAML(t, eng, waitUserAgentYAML)

	exec, err := eng.Start(context.Background(), wf, "写一个功能", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)

	if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{"response": "reject"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionFailed, model.ExecutionCompleted)
	if final.State != model.ExecutionFailed {
		t.Fatalf("reject 后应 FAILED,实际 %s", final.State)
	}
	if !strings.Contains(final.Error, "拒绝") {
		t.Errorf("失败原因应说明用户拒绝,实际 %q", final.Error)
	}
	// 后续节点不得执行
	if _, ran := final.NodeStates["submit"]; ran {
		t.Errorf("reject 后不应继续执行 submit,实际 %v", final.NodeStates)
	}
	// 必须发出 node.failed 事件
	failed := false
	for _, ev := range bus.History() {
		if ev.Type == event.NodeFailed {
			failed = true
		}
	}
	if !failed {
		t.Error("reject 应发出 node.failed 事件")
	}
}

// TestSkillWaitUserResumeReusesResponse Skill 节点的同一语义。
func TestSkillWaitUserResumeReusesResponse(t *testing.T) {
	ws := &countingWaitSkill{}
	repo := openRepoForSkill(t)
	bus := event.NewBus()
	bus.KeepHistory = true
	agents := coreagent.NewRegistry()
	_ = agents.Register(mock.New(mock.Options{ID: "mock-a"}))
	skills := skill.NewRegistry()
	for _, sk := range builtin.All() {
		_ = skills.Register(sk)
	}
	_ = skills.Register(ws)
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)

	wf := loadWfYAML(t, eng, `
workflow:
  id: wait-skill-wf
  name: Skill 等待用户
nodes:
  - id: gate
    name: Gate
    type: skill
    skill: counting-wait
  - id: done
    name: Done
    type: skill
    skill: log
edges:
  - from: gate
    to: done
`)
	exec, err := eng.Start(context.Background(), wf, "提交变更", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)
	before := ws.count()

	if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{"response": "continue"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	final := waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	if final.State != model.ExecutionCompleted {
		t.Fatalf("应 COMPLETED,实际 %s (%s)", final.State, final.Error)
	}
	if after := ws.count(); after != before {
		t.Errorf("Skill 被重复执行:%d → %d", before, after)
	}
}

// countingWaitSkill 第一次返回 WAIT_USER,之后每次都记账。
type countingWaitSkill struct {
	mu    sync.Mutex
	calls int
}

func (s *countingWaitSkill) ID() string          { return "counting-wait" }
func (s *countingWaitSkill) Name() string        { return "Counting Wait" }
func (s *countingWaitSkill) Description() string { return "test skill" }
func (s *countingWaitSkill) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *countingWaitSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if n == 1 {
		return &skill.SkillResponse{Status: "WAIT_USER", Output: "需要人工确认"}, nil
	}
	return &skill.SkillResponse{Status: "SUCCESS", Output: "skill 执行了"}, nil
}

func openRepoForSkill(t *testing.T) *persistence.DB {
	t.Helper()
	repo, err := persistence.Open(t.TempDir() + "/skill.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}
