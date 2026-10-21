package runtime

import (
	"context"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"**运行期**钳制 timeout_seconds"(#312)。
//
// 背景:校验期(#311)已经把取值范围收敛到 0..86400,但校验期只拦**新写/新改**
// 的工作流 —— **存量**工作流里早就写着超大值(它们是在上界存在之前存进去的),
// 运行期仍会照读。于是:
//
//   - raw = 10000000000        → time.Duration(raw)*time.Second = -2346317h47m53.7s
//     (负数)→ context.WithTimeout 拿到负 deadline → 节点**立即超时**;
//   - raw = 4611686018427387904 → 换算后是 0s → deadline = 现在 → **立即超时**;
//   - raw = 1e20(float)        → int(1e20) 溢出成负 → 适配器的 `timeout <= 0`
//     判真 → **静默改用适配器默认超时**(用户配的值根本没生效)。
//
// 三条路径的共同点:用户配的超时**没有按自己配的值生效**,且界面/日志里
// 看不出任何异常(约定 14:"该失败却成功"同族 —— 静默改写用户配置)。
//
// 运行期的口径(**唯一来源**:workflow/compiler 的 TimeoutSecondsMin/Max):
//
//   - 超上界(> 86400)→ 钳到 86400(不是 fail closed:超大值原本表现为
//     "立即超时",钳到 24h 是修复而不是失败);
//   - 负值 → 归 0(= 不设节点超时,与运行期 `req.Timeout > 0` 的口径一致);
//   - 非数值 / 非整数 → 归 0(校验期已拦,运行期兜底:不得静默改用别的值)。
//
// 断言一律走**可观测边界**(运行期交给适配器的 req.Timeout / script 节点
// 真实的执行结果),不直接调用内部助手 —— 这样断言的是行为,不是实现。

// spyAgent 记录收到的 AgentRequest:用来断言运行期**交给适配器之前**
// 已经把超时钳制到合法范围。
type spyAgent struct {
	mu  sync.Mutex
	req coreagent.AgentRequest
}

func (s *spyAgent) ID() string   { return "spy-a" }
func (s *spyAgent) Name() string { return "Spy Agent" }

func (s *spyAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	s.mu.Lock()
	s.req = req
	s.mu.Unlock()
	return &coreagent.AgentResponse{
		Status:   coreagent.StatusSuccess,
		Decision: coreagent.DecisionDone,
		Summary:  "ok",
	}, nil
}

// timeout 返回本次收到的超时秒数。
func (s *spyAgent) timeout() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.req.Timeout
}

// newSpyEngine 造一个带 spy Agent 的运行期(其它 Agent/Skill 用内置的)。
func newSpyEngine(t *testing.T, spy *spyAgent) *Engine {
	t.Helper()
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	agents := coreagent.NewRegistry()
	if err := agents.Register(spy); err != nil {
		t.Fatal(err)
	}
	if err := agents.Register(mock.New(mock.Options{ID: "mock-a"})); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	eng := NewEngine(agents, skills, permission.NewManager(), event.NewBus(), git.New(t.TempDir()), repo)
	eng.Perms.Set("spy-a", permission.Policy{FilesystemRead: true})
	return eng
}

// TestAgentNodeTimeoutIsClampedToValidRange 运行期交给适配器的超时必须是
// **合法范围**内的值(0..86400),而不是把用户的原始值原样传下去。
//
// 走 ExecuteNode 而不是 Start:Start 会先跑校验期(而校验期正是会拦下这些
// 值的那一层)—— 这里要验的是**存量**工作流的运行期行为。
func TestAgentNodeTimeoutIsClampedToValidRange(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		{"正常值原样保留", 30, 30},
		{"JSON 往返后的整数(float64)", float64(30), 30},
		{"YAML 整数(uint64)", uint64(30), 30},
		{"0 = 不设节点超时", 0, 0},
		{"上界原样保留", 86400, 86400},
		{"超上界 → 钳到上界", 86401, 86400},
		{"超大整数 → 钳到上界", 10000000000, 86400},
		{"超大 uint64 → 钳到上界", uint64(4611686018427387904), 86400},
		{"1e20(float) → 钳到上界(不是负数/0)", float64(1e20), 86400},
		{"负值 → 0(不设节点超时)", -1, 0},
		{"负的大整数 → 0", int64(-1) << 40, 0},
		{"小数秒 → 0(不静默截断改写)", 30.5, 0},
		{"字符串 → 0(不得悄悄转成秒数)", "30", 0},
		{"NaN → 0", math.NaN(), 0},
		{"+Inf → 钳到上界", math.Inf(1), 86400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyAgent{}
			eng := newSpyEngine(t, spy)
			node := &model.Node{ID: "n1", Type: model.NodeTypeAgent, Config: map[string]any{
				"agent": "spy-a", "mode": "plan", "timeout_seconds": tc.val,
			}}
			env := &compiler.RunEnv{
				Exec: &model.Execution{ID: "exec-timeout"}, WF: &model.Workflow{},
				Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
			}
			out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
			if out.State != model.NodeSuccess {
				t.Fatalf("节点状态 = %s(%s),want SUCCESS", out.State, out.Error)
			}
			if got := spy.timeout(); got != tc.want {
				t.Errorf("运行期交给适配器的 timeout = %d,want %d(原始配置值 %v)", got, tc.want, tc.val)
			}
		})
	}
}

// TestScriptNodeHugeTimeoutDoesNotExpireImmediately script 节点的超大
// timeout_seconds 不得让节点**立即超时**。
//
// 这一条是最贴近用户可见后果的:超大的值换算成 time.Duration 后溢出成负数,
// context.WithTimeout 拿到负 deadline → 命令刚起就被杀掉 → 节点 FAILED。
// 钳到上界后命令正常跑完(节点 SUCCESS)。
func TestScriptNodeHugeTimeoutDoesNotExpireImmediately(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.AllowScriptNodes = func() bool { return true }
	node := &model.Node{ID: "bg", Type: model.NodeTypeScript, Config: map[string]any{
		"command": "echo done", "timeout_seconds": 10000000000,
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-script-timeout"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	t0 := time.Now()
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeSuccess {
		t.Fatalf("超大 timeout_seconds 让 script 节点 %s(%s),want SUCCESS"+
			"(溢出成负 deadline → 立即超时);耗时 %v",
			out.State, out.Error, time.Since(t0))
	}
}
