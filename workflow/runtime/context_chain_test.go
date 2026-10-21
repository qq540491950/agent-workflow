package runtime

// 本文件锁住"跨节点上下文链"(第 19 轮 P0)。
//
// 缺陷链(前十六轮全部漏网):
//
//	executor.go 的 agent/skill 节点成功后调用
//	    CtxMgr.SaveNodeOutput(mode, id, result, e.stateView(st))
//	而 stateView 返回的是**新建的副本**(view := map[string]any{}),
//	SaveNodeOutput 把 plan/review/issues/last_output/test_result 写进这个
//	一次性副本 → 副本随即被丢弃,**从未写回 ADK 会话状态**。
//	下游节点渲染时 contextx.Build 读到的 state["plan"] 恒为 nil →
//	{{plan}} / {{review}} / {{issues}} / {{test_result}} 永远渲染成空串。
//
// 症状:Agent 收到**没有方案**的提示词,照常返回 APPROVED/REJECTED,
// 整个执行报告 SUCCESS —— 用户以为"plan→execute→review"链路生效了。
//
// 本文件的用例一律走**生产调用路径**(引擎启动真实工作流 → 节点真实执行
// → Agent 真实收到提示词),而不是直接调 contextx 的纯函数 ——
// 纯函数层的单测(contextx/context_test.go)此前是绿的,却完全没覆盖
// "写进的是副本"这条生产路径(恒真的假保护)。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// chainAgent 记录每个 mode 收到的提示词,并按 mode 返回可配置的结构化输出。
type chainAgent struct {
	id string

	mu      sync.Mutex
	prompts map[string][]string

	// respond 按 (mode, 第几次调用) 返回 Agent 响应。
	respond func(mode string, call int) coreagent.AgentResponse
}

func (a *chainAgent) ID() string   { return a.id }
func (a *chainAgent) Name() string { return a.id }

func (a *chainAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	a.mu.Lock()
	if a.prompts == nil {
		a.prompts = map[string][]string{}
	}
	call := len(a.prompts[req.Mode])
	a.prompts[req.Mode] = append(a.prompts[req.Mode], req.Instructions)
	a.mu.Unlock()

	resp := coreagent.AgentResponse{Status: coreagent.StatusSuccess, Data: map[string]any{}}
	if a.respond != nil {
		resp = a.respond(req.Mode, call)
		resp.Status = coreagent.StatusSuccess
		if resp.Data == nil {
			resp.Data = map[string]any{}
		}
	}
	return &resp, nil
}

// promptOf 返回该 mode 第 n 次调用(0 起)收到的提示词;没有则返回空串。
func (a *chainAgent) promptOf(mode string, n int) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ps := a.prompts[mode]
	if n < 0 || n >= len(ps) {
		return ""
	}
	return ps[n]
}

// callsOf 返回该 mode 被调用的次数。
func (a *chainAgent) callsOf(mode string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.prompts[mode])
}

// promptsOf 返回该 mode 收到的全部提示词(副本)。
func (a *chainAgent) promptsOf(mode string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.prompts[mode]))
	copy(out, a.prompts[mode])
	return out
}

// newChainEngine 造一个带 chainAgent 的引擎(权限已放开)与可读原始列的库句柄。
func newChainEngine(t *testing.T, agt *chainAgent) (*Engine, *persistence.DB) {
	t.Helper()
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "ctx-chain.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	bus := event.NewBus()
	bus.KeepHistory = true
	agents := coreagent.NewRegistry()
	if err := agents.Register(agt); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatalf("register skill: %v", err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	eng.Perms.Set(agt.ID(), permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	return eng, repo
}

// runChainWorkflow 跑一个工作流并等到终态。
func runChainWorkflow(t *testing.T, eng *Engine, yml string) *model.Execution {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("workflow invalid: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "实现登录功能", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return waitForState(t, eng, exec.ID, model.ExecutionCompleted)
}

// TestPlanReachesDownstreamNode 端到端:plan 节点的输出必须出现在下游
// review 节点**实际收到的提示词**里。
//
// 修前:review 收到的提示词是 "方案:"(plan 槽位是空串)—— 本用例变红。
func TestPlanReachesDownstreamNode(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{Summary: "计划摘要", Output: "PLAN-FULL-TEXT"}
		case "review":
			return coreagent.AgentResponse{Summary: "评审通过", Decision: coreagent.DecisionApproved}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-plan
  name: Context Plan
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}}"
edges:
  - from: plan
    to: review
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("review", 0)
	if got == "" {
		t.Fatal("review 节点没有收到任何提示词")
	}
	if !strings.Contains(got, "PLAN-FULL-TEXT") {
		t.Errorf("review 节点收到的提示词里没有上游 plan 的输出: %q", got)
	}
	if strings.Contains(got, "{{plan}}") {
		t.Errorf("提示词里残留未渲染的占位符: %q", got)
	}
}

// TestPlanFallsBackToSummaryWhenOutputEmpty 上游只给了 summary(没有 output)
// 时,plan 槽位必须用 summary,不能是空串。
//
// 这是同一条链上的**第二个断点**:SaveNodeOutput 此前是"output 键存在就
// 用 output"(哪怕它是空串)→ 只产出 summary 的 Agent(绝大多数)写出的
// plan 恒为空串。
func TestPlanFallsBackToSummaryWhenOutputEmpty(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{Summary: "只有摘要的方案"}
		case "review":
			return coreagent.AgentResponse{Summary: "ok"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-plan-summary
  name: Context Plan Summary
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}}"
edges:
  - from: plan
    to: review
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("review", 0)
	if !strings.Contains(got, "只有摘要的方案") {
		t.Errorf("review 节点收到的提示词里没有 plan 的 summary: %q", got)
	}
}

// TestReviewAndIssuesReachDownstream review 节点的结论与问题清单必须
// 到达下游节点(execute 模式的 {{review}} 与 {{issues}})。
func TestReviewAndIssuesReachDownstream(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "review":
			resp := coreagent.AgentResponse{Summary: "REVIEW-CONCLUSION", Decision: coreagent.DecisionRejected}
			resp.Data = map[string]any{"issues": []string{"ISSUE-A", "ISSUE-B"}}
			return resp
		case "execute":
			return coreagent.AgentResponse{Summary: "ok"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-review
  name: Context Review
nodes:
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "任务:{{task}}"
  - id: fix
    type: agent
    agent: chain
    mode: fix
    prompt: "评审:{{review}} 问题:{{issues}}"
edges:
  - from: review
    to: fix
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("fix", 0)
	if !strings.Contains(got, "REVIEW-CONCLUSION") {
		t.Errorf("下游节点收到的提示词里没有 review 结论: %q", got)
	}
	if !strings.Contains(got, "ISSUE-A") || !strings.Contains(got, "ISSUE-B") {
		t.Errorf("下游节点收到的提示词里没有 review 问题清单: %q", got)
	}
}

// TestTestResultReachesDownstream test 节点的输出必须到达下游节点。
func TestTestResultReachesDownstream(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "test":
			return coreagent.AgentResponse{Summary: "测试摘要", Output: "TESTS-PASSED-42"}
		case "review":
			return coreagent.AgentResponse{Summary: "ok"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-test
  name: Context Test
nodes:
  - id: test
    type: agent
    agent: chain
    mode: test
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "测试:{{test_result}}"
edges:
  - from: test
    to: review
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("review", 0)
	if !strings.Contains(got, "TESTS-PASSED-42") {
		t.Errorf("下游节点收到的提示词里没有 test_result: %q", got)
	}
}

// TestSkillOutputReachesDownstream skill 节点的输出必须写进活的会话状态
// (executor.go 的 skill 分支此前同样把结果写进一次性副本)。
func TestSkillOutputReachesDownstream(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-skill
  name: Context Skill
nodes:
  - id: sk
    type: skill
    skill: log
    args:
      message: SKILL-SIDE-EFFECT
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "技能输出:{{last_output}}"
edges:
  - from: sk
    to: review
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("review", 0)
	if !strings.Contains(got, "SKILL-SIDE-EFFECT") {
		t.Errorf("下游节点收到的提示词里没有 skill 节点的输出: %q", got)
	}
}

// TestContextChainDoesNotAffectConditions 写进上下文链的键**不得**影响
// 条件表达式求值(约定 #13:校验期与运行期同源 —— 条件变量集合由
// compiler/conditionenv.go 唯一定义,不含 plan/review/issues/test_result)。
//
// 断言方式:同一个决策值下,带上下文链与不带上下文链的工作流走同一条边。
func TestContextChainDoesNotAffectConditions(t *testing.T) {
	yml := `
version: "1"
workflow:
  id: ctx-cond
  name: Context Condition
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}}"
  - id: yes
    type: agent
    agent: chain
    mode: review
    prompt: "走了 APPROVED 分支"
  - id: no
    type: agent
    agent: chain
    mode: review
    prompt: "走了别的分支"
edges:
  - from: plan
    to: review
  - from: review
    condition: decision == "APPROVED"
    to: yes
  - from: review
    condition: decision == "REJECTED"
    to: no
`
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{Summary: "方案", Output: "PLAN"}
		case "review":
			return coreagent.AgentResponse{Summary: "评审", Decision: coreagent.DecisionApproved}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)
	runChainWorkflow(t, eng, yml)

	if agt.callsOf("review") != 2 { // review + yes 两个节点都是 review 模式
		t.Fatalf("review 模式被调用 %d 次,期望 2(review + 分支终点)", agt.callsOf("review"))
	}
	prompts := agt.promptsOf("review")
	if len(prompts) != 2 {
		t.Fatalf("review 模式提示词条数 = %d,期望 2", len(prompts))
	}
	if !strings.Contains(prompts[1], "走了 APPROVED 分支") {
		t.Errorf("条件求值没有走 APPROVED 分支(上下文链影响了路由): %q", prompts[1])
	}
}

// TestContextChainStateJSONIsMasked 上下文链的键会进入 state_json 落库
// (会话状态 → engine.collectResult → exec.StateData → execMetaArgs 脱敏)。
// 节点输出里的凭据形态不得明文落库(约定 #8)。
func TestContextChainStateJSONIsMasked(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{
				Summary: "方案",
				Output:  "先跑 export ANTHROPIC_API_KEY=" + leakSecret + " 再编译",
			}
		case "review":
			return coreagent.AgentResponse{Summary: "ok"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, repo := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-mask
  name: Context Mask
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}}"
edges:
  - from: plan
    to: review
`
	exec := runChainWorkflow(t, eng, yml)

	var stateJSON string
	if err := repo.SQL().QueryRow(
		`SELECT state_json FROM executions WHERE id=?`, exec.ID).Scan(&stateJSON); err != nil {
		t.Fatalf("读回原始列: %v", err)
	}
	if strings.Contains(stateJSON, leakSecret) {
		t.Errorf("state_json 含凭据明文: %s", stateJSON)
	}
	// 键本身必须在(证明上下文链真的落库了,而不是"因为没写所以没泄漏")。
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stateJSON), &decoded); err != nil {
		t.Fatalf("state_json 不是合法 JSON: %v (%s)", err, stateJSON)
	}
	plan, ok := decoded["plan"].(string)
	if !ok {
		t.Fatalf("state_json 里没有 plan 键(上下文链没落库): %s", stateJSON)
	}
	if !strings.Contains(plan, "ANTHROPIC_API_KEY") {
		t.Errorf("plan 键的内容被整体抹掉了(应保留上下文、只替换值): %q", plan)
	}
	if strings.Contains(plan, leakSecret) {
		t.Errorf("plan 键里仍是凭据明文: %q", plan)
	}
}

// TestShippedExampleWorkflowPropagatesPlan 仓库自带的示例工作流
// (workflows/examples/coding-task.yaml)的 review 节点 prompt 里就写着
// "Plan: {{plan}}" —— 修前它恒为空串(整条跨节点上下文链是死的)。
//
// 本用例**直接跑那个示例文件**:它是报告里点名的触发条件,示例能跑通
// 才说明修复落在了用户真正会用的工作流上。
func TestShippedExampleWorkflowPropagatesPlan(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "workflows", "examples", "coding-task.yaml"))
	if err != nil {
		t.Fatalf("读取示例工作流: %v", err)
	}
	claude := &chainAgent{id: "mock-claude", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{Summary: "计划摘要", Output: "PLAN-BODY-EXAMPLE"}
		case "review":
			return coreagent.AgentResponse{Summary: "评审通过", Decision: coreagent.DecisionApproved}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	pi := &chainAgent{id: "mock-pi", respond: func(mode string, call int) coreagent.AgentResponse {
		return coreagent.AgentResponse{Summary: "执行完成"}
	}}

	repo, err := persistence.Open(filepath.Join(t.TempDir(), "example.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	bus := event.NewBus()
	bus.KeepHistory = true
	agents := coreagent.NewRegistry()
	for _, a := range []coreagent.Agent{claude, pi} {
		if err := agents.Register(a); err != nil {
			t.Fatalf("register agent: %v", err)
		}
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatalf("register skill: %v", err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
	for _, id := range []string{"mock-claude", "mock-pi"} {
		eng.Perms.Set(id, permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	}

	doc, err := dsl.Parse(raw)
	if err != nil {
		t.Fatalf("解析示例工作流: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("示例工作流非法: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "实现登录功能", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	got := claude.promptOf("review", 0)
	if got == "" {
		t.Fatal("review 节点没有收到任何提示词")
	}
	if !strings.Contains(got, "PLAN-BODY-EXAMPLE") {
		t.Errorf("示例工作流的 review 节点没有拿到方案(跨节点上下文链仍然是死的): %q", got)
	}
}

// TestContextChainKeysDoNotCollideWithConditionKeys 上下文链的键不得与
// 条件变量(decision/status/output/iteration/variables)重名 —— 重名会把
// 自由文本写进路由依据,恢复后可能走另一条分支(约定 #14 同族)。
func TestContextChainKeysDoNotCollideWithConditionKeys(t *testing.T) {
	chainKeys := []string{"plan", "review", "issues", "test_result", "last_output"}
	for _, k := range chainKeys {
		for _, ck := range compiler.ConditionEnvKeys() {
			if k == ck {
				t.Errorf("上下文链的键 %q 与条件变量 %q 重名", k, ck)
			}
		}
	}
}

// TestContextChainKeysDoNotAffectConditionEvaluation 直接断言:把上下文链
// 的键写进会话状态后,条件表达式的求值结果**逐条不变**。
//
// 依据:条件表达式的可用变量集合由 compiler/conditionenv.go 唯一定义
// (decision / status / output / iteration / variables),上下文链的键不在其中
// —— 写进会话状态不影响路由(约定 #13 的同源精神)。
func TestContextChainKeysDoNotAffectConditionEvaluation(t *testing.T) {
	base := map[string]any{
		compiler.KeyDecision:  "APPROVED",
		compiler.KeyStatus:    "success",
		compiler.KeyOutput:    "OUT-TEXT",
		compiler.KeyLoopCount: 2,
	}
	vars := map[string]any{"foo": "bar"}
	cases := []string{
		`decision == "APPROVED"`,
		`status == "success"`,
		`output == "OUT-TEXT"`,
		`iteration == 2`,
		`variables.foo == "bar"`,
		`decision == "REJECTED"`,
		`iteration > 5`,
	}

	eval := func(state map[string]any, expr string) bool {
		st := &fakeState{m: state}
		ok, err := compiler.EvalCondition(expr, st, vars)
		if err != nil {
			t.Fatalf("eval %q: %v", expr, err)
		}
		return ok
	}

	// 基线:只有条件变量时的求值结果。
	want := make([]bool, len(cases))
	for i, c := range cases {
		want[i] = eval(base, c)
	}

	// 加上上下文链的键(模拟"节点跑完后会话状态里的样子")。
	withChain := map[string]any{}
	for k, v := range base {
		withChain[k] = v
	}
	withChain["plan"] = "PLAN-TEXT"
	withChain["review"] = "REVIEW-TEXT"
	withChain["issues"] = []string{"ISSUE"}
	withChain["test_result"] = "TEST-TEXT"
	withChain["last_output"] = "LAST-TEXT"

	for i, c := range cases {
		if got := eval(withChain, c); got != want[i] {
			t.Errorf("条件 %q 的求值结果被上下文链的键改变了: got %v want %v", c, got, want[i])
		}
	}
}

// ---- #280:上游没产出时,下游的空上下文必须可见 -------------------------

// warningEvents 返回本次执行**持久化**的 node.warning 事件(按 reason 归类)。
//
// 为什么查库而不是查内存总线:落库的那份才是用户事后能看到的那份
// (审计流 / 事件列表)—— "可见"必须是持久的可见,不是"当时闪了一下"。
func warningEvents(t *testing.T, repo *persistence.DB, execID string) map[string][]map[string]any {
	t.Helper()
	evs, err := repo.ListEvents(execID, 0)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	out := map[string][]map[string]any{}
	for _, ev := range evs {
		if ev["type"] != "node.warning" {
			continue
		}
		data, _ := ev["data"].(map[string]any)
		reason, _ := data["reason"].(string)
		out[reason] = append(out[reason], data)
	}
	return out
}

// slotsOf 取出告警事件里的槽位名列表。
func slotsOf(data map[string]any) []string {
	raw, _ := data["slots"].([]any)
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if str, ok := s.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// TestEmptyUpstreamSlotIsVisible 上游节点**成功但什么都没产出** → 下游
// {{plan}} 渲染成空串 → 必须有**可见信号**。
//
// 修前(实测):state_data.plan="" → 下游提示词里 "方案:" → node.warning
// 事件数 = 0 → 执行落 COMPLETED,**零告警** —— 用户以为"方案传给下游了"。
//
// 语义:**可见但不阻断** —— 事件 + 日志,节点照常成功。理由见
// executor.reportEmptySlots 的注释与 docs/OPTIMIZATION.md #280。
func TestEmptyUpstreamSlotIsVisible(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			// 上游"成功"但没有任何产出:summary 与 output 都是空串。
			return coreagent.AgentResponse{Summary: "", Output: ""}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, repo := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-empty-slot
  name: Empty Slot
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}}"
edges:
  - from: plan
    to: review
`
	exec := runChainWorkflow(t, eng, yml)
	if exec.State != model.ExecutionCompleted {
		t.Fatalf("上游没产出不应让节点失败(可见但不阻断),实际 %s (%s)", exec.State, exec.Error)
	}

	warns := warningEvents(t, repo, exec.ID)
	empties := warns["empty_context_slots"]
	if len(empties) == 0 {
		t.Fatalf("上游没产出时下游必须有可见告警(empty_context_slots),实际告警: %v", warns)
	}
	var hit bool
	for _, data := range empties {
		for _, s := range slotsOf(data) {
			if s == "plan" {
				hit = true
			}
		}
	}
	if !hit {
		t.Errorf("告警必须点名空槽位 plan,实际 %v", empties)
	}
	// 告警必须挂在**下游**节点上(是下游拿到空上下文,不是上游有问题)。
	if node, _ := empties[0]["node"].(string); node != "review" {
		t.Errorf("告警应挂在下游节点 review 上,实际 %q", node)
	}
}

// TestEmptySlotWarningAbsentWhenUpstreamProduced 上游**有产出**时不得
// 出现空槽位告警(否则就是噪音,和"缺失必须可见"一样有害)。
func TestEmptySlotWarningAbsentWhenUpstreamProduced(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{Summary: "计划摘要", Output: "PLAN-FULL-TEXT"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, repo := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-slot-filled
  name: Slot Filled
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}}"
edges:
  - from: plan
    to: review
`
	exec := runChainWorkflow(t, eng, yml)
	if warns := warningEvents(t, repo, exec.ID); len(warns["empty_context_slots"]) != 0 {
		t.Errorf("上游有产出时不得报空槽位告警,实际 %v", warns["empty_context_slots"])
	}
}

// TestEmptySlotAndMissingVarAreDistinguishable "上游没产出"(键存在但值为
// 空)与"变量名拼错"(键不存在)必须能被区分开 —— 否则用户无从下手
// (reportMissingVars 的注释里写的就是这个诉求)。
func TestEmptySlotAndMissingVarAreDistinguishable(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "plan":
			return coreagent.AgentResponse{Summary: "", Output: ""}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, repo := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-slot-vs-typo
  name: Slot Versus Typo
nodes:
  - id: plan
    type: agent
    agent: chain
    mode: plan
    prompt: "任务:{{task}}"
  - id: review
    type: agent
    agent: chain
    mode: review
    prompt: "方案:{{plan}} 拼错了:{{revwie}}"
edges:
  - from: plan
    to: review
`
	exec := runChainWorkflow(t, eng, yml)
	warns := warningEvents(t, repo, exec.ID)

	if len(warns["missing_template_vars"]) == 0 {
		t.Errorf("拼错的变量名必须照旧报 missing_template_vars,实际告警: %v", warns)
	}
	if len(warns["empty_context_slots"]) == 0 {
		t.Errorf("空槽位必须报 empty_context_slots,实际告警: %v", warns)
	}
	// 两条路径的 reason 必须不同(否则用户分不清"拼错了"与"上游没产出")。
	if len(warns["missing_template_vars"]) > 0 && len(warns["empty_context_slots"]) > 0 {
		return
	}
	t.Errorf("两类告警必须同时存在且 reason 不同,实际 %v", warns)
}
