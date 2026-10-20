package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"prompt 模板渲染失败"的语义(约定 14:该失败却成功等同 P0)。
//
// 此前运行期用 template.Engine.Render:渲染失败(内部 panic 被兜底成
// error)时 Render **吞掉 error 并回退原始模板**,于是:
//
//	Agent 收到的是一段**没渲染过的模板文本** → 照常执行 → 照常 SUCCESS
//
// 用户以为提示词生效了,实际根本没渲染,而且没有任何提示。
// 现在运行期改用 RenderErr:渲染失败 → 节点失败 + 可读原因。
//
// 为什么需要"会失败的渲染器"这个缝:template.Engine 的失败入口只有
// 渲染期 panic,而普通 DSL 输入**无法**稳定触发它(实测:畸形模板
// "{{v"、会 panic 的 fmt.Formatter、空数据,RenderErr 一律返回 nil
// error)。没有缝就只能靠注入一个失败的渲染器来验证这条路径。

// errTemplate 是模拟模板渲染失败的错误。
var errTemplate = errors.New("模板语法错误: 未闭合的 {{")

// failingRenderer 是一个可控失败的模板渲染器。
//
// failOn 非空时只对包含该子串的模板失败(用来分别验证 prompt 与
// instructions 两处渲染点);为空时全部模板都失败。
type failingRenderer struct {
	err    error
	failOn string
}

func (f *failingRenderer) Render(tpl string, data map[string]any) (string, []string) {
	return tpl, nil
}

func (f *failingRenderer) RenderErr(tpl string, data map[string]any) (string, []string, error) {
	if f.failOn == "" || strings.Contains(tpl, f.failOn) {
		return "", nil, f.err
	}
	return tpl, nil, nil
}

// recordingAgent 记录被调用的次数与收到的 instructions。
type recordingAgent struct {
	id           string
	calls        int32
	instructions string
}

func (a *recordingAgent) ID() string   { return a.id }
func (a *recordingAgent) Name() string { return a.id }

func (a *recordingAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	atomic.AddInt32(&a.calls, 1)
	a.instructions = req.Instructions
	return &coreagent.AgentResponse{Status: coreagent.StatusSuccess, Summary: "ok"}, nil
}

// newTplEngine 造一个带 recordingAgent 的引擎(权限已放开)。
func newTplEngine(t *testing.T) (*Engine, *recordingAgent, *event.Bus) {
	t.Helper()
	eng := newMinimalEngine(t)
	agt := &recordingAgent{id: "rec"}
	if err := eng.Agents.Register(agt); err != nil {
		t.Fatal(err)
	}
	eng.Perms.Set("rec", permission.Policy{FilesystemRead: true})
	bus := event.NewBus()
	bus.KeepHistory = true
	eng.Bus = bus
	return eng, agt, bus
}

// TestTemplateRenderFailureFailsNode 渲染失败 → 节点失败,Agent 不得被调用。
func TestTemplateRenderFailureFailsNode(t *testing.T) {
	eng, agt, bus := newTplEngine(t)
	eng.Tpl = &failingRenderer{err: errTemplate}

	node := &model.Node{ID: "n1", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "rec", "mode": "plan", "prompt": "任务={{task}}",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-tpl", Task: "写周报"},
		WF:   &model.Workflow{}, Runner: eng, Bus: bus, Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("模板渲染失败 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	// 失败原因必须可读:写明是哪个模板、错在哪、哪个节点
	if !strings.Contains(out.Error, "模板渲染失败") {
		t.Errorf("失败原因应说明模板渲染失败,实际 %q", out.Error)
	}
	if !strings.Contains(out.Error, "未闭合的 {{") {
		t.Errorf("失败原因应带上底层错误,实际 %q", out.Error)
	}
	if !strings.Contains(out.Error, "n1") {
		t.Errorf("失败原因应点名节点,实际 %q", out.Error)
	}
	// 关键:Agent 绝不能被调用 —— 否则就是"带着没渲染的提示词照常执行"
	if n := atomic.LoadInt32(&agt.calls); n != 0 {
		t.Errorf("渲染失败后不应调用 Agent,实际调用 %d 次", n)
	}
}

// TestTemplateRenderFailureInInstructionsFailsNode instructions 渲染失败同样
// 必须让节点失败(此前是第二处静默兜底)。
func TestTemplateRenderFailureInInstructionsFailsNode(t *testing.T) {
	eng, agt, bus := newTplEngine(t)
	// 只让 instructions 里的模板失败,prompt 渲染正常
	eng.Tpl = &failingRenderer{err: errTemplate, failOn: "额外"}

	node := &model.Node{ID: "n2", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "rec", "mode": "plan",
		"prompt":       "任务={{task}}",
		"instructions": "额外要求={{boom}}",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-tpl", Task: "写周报"},
		WF:   &model.Workflow{}, Runner: eng, Bus: bus, Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("instructions 渲染失败 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if !strings.Contains(out.Error, "模板渲染失败") {
		t.Errorf("失败原因应说明模板渲染失败,实际 %q", out.Error)
	}
	if !strings.Contains(out.Error, "instructions") {
		t.Errorf("失败原因应说明是 instructions 模板,实际 %q", out.Error)
	}
	if n := atomic.LoadInt32(&agt.calls); n != 0 {
		t.Errorf("渲染失败后不应调用 Agent,实际调用 %d 次", n)
	}
}

// TestTemplateRenderOKNoWarning 回归:渲染正常时不得多出告警,提示词必须
// 真的被替换(约定:变量齐全时不得刷噪音告警)。
func TestTemplateRenderOKNoWarning(t *testing.T) {
	eng, agt, bus := newTplEngine(t)

	node := &model.Node{ID: "n3", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "rec", "mode": "plan", "prompt": "任务={{task}}",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-tpl", Task: "写周报"},
		WF:   &model.Workflow{}, Runner: eng, Bus: bus, Vars: map[string]any{},
	}
	st := &fakeState{m: map[string]any{"task": "写周报"}}
	out := eng.ExecuteNode(context.Background(), env, node, st)
	if out.State != model.NodeSuccess {
		t.Fatalf("渲染正常 → 节点状态 %s,want %s (%s)", out.State, model.NodeSuccess, out.Error)
	}
	if n := atomic.LoadInt32(&agt.calls); n != 1 {
		t.Fatalf("Agent 调用次数 = %d,want 1", n)
	}
	if !strings.Contains(agt.instructions, "写周报") {
		t.Errorf("Agent 应收到渲染后的提示词,实际 %q", agt.instructions)
	}
	if strings.Contains(agt.instructions, "{{") {
		t.Errorf("提示词里残留未渲染的占位符: %q", agt.instructions)
	}
	for _, ev := range bus.History() {
		if ev.Type == "node.warning" {
			t.Errorf("渲染正常时不应告警: %v", ev.Data)
		}
	}
}
