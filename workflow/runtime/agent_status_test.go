package runtime

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"Agent 返回状态的归一化"(与 Skill 侧同一口径)。
//
// agent.AgentStatus 是字符串类型,外部实现可能返回 "failed" / "wait_user"。
// 运行期此前**精确匹配**大小写:
//
//	switch resp.Status {
//	case coreagent.StatusFailed:   // "failed" 不匹配
//	case coreagent.StatusWaitUser: // "wait_user" 不匹配
//	}                             // 其它一律按成功继续
//
// 于是小写 "failed" 的 Agent 报错时,节点却按**成功**继续往下跑 ——
// 该失败却成功(约定 14),用户看到的是"执行成功"。
// 现在归一化(去空白 + 转大写)后再判定,契约外状态显式失败。

// statusAgent 返回指定状态字符串的 Agent。
type statusAgent struct {
	id     string
	status string
}

func (a *statusAgent) ID() string   { return a.id }
func (a *statusAgent) Name() string { return a.id }

func (a *statusAgent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	return &coreagent.AgentResponse{Status: coreagent.AgentStatus(a.status), Error: "boom"}, nil
}

// runAgentWithStatus 跑一个 agent 节点(node 上只配 mode/prompt)。
func runAgentWithStatus(t *testing.T, status string) compiler.NodeOutcome {
	t.Helper()
	eng := newMinimalEngine(t)
	agt := &statusAgent{id: "st-agt", status: status}
	if err := eng.Agents.Register(agt); err != nil {
		t.Fatal(err)
	}
	eng.Perms.Set("st-agt", permission.Policy{FilesystemRead: true})
	node := &model.Node{ID: "an", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "st-agt", "mode": "plan", "prompt": "任务={{task}}",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-agt"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	return eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
}

// TestAgentStatusNormalized 各种大小写/空白形态的状态必须得到同一个结论。
func TestAgentStatusNormalized(t *testing.T) {
	cases := []struct {
		status string
		want   model.NodeState
	}{
		{"SUCCESS", model.NodeSuccess},
		{"success", model.NodeSuccess},
		{" SUCCESS ", model.NodeSuccess},
		{"FAILED", model.NodeFailed},
		{"failed", model.NodeFailed},
		{" Failed ", model.NodeFailed},
		{"WAIT_USER", model.NodeWaiting},
		{"wait_user", model.NodeWaiting},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			out := runAgentWithStatus(t, tc.status)
			if out.State != tc.want {
				t.Errorf("Agent 状态 %q → 节点状态 %s,want %s", tc.status, out.State, tc.want)
			}
			if tc.want == model.NodeFailed && out.Error == "" {
				t.Errorf("Agent 状态 %q:失败节点必须带失败原因", tc.status)
			}
			if tc.want == model.NodeWaiting {
				if _, ok := out.Data["responses"]; !ok {
					t.Errorf("Agent 状态 %q:等待节点必须带 responses 选项,实际 Data=%v", tc.status, out.Data)
				}
			}
		})
	}
}

// TestAgentStatusUnknownFails 契约外状态必须显式失败(约定 14)。
func TestAgentStatusUnknownFails(t *testing.T) {
	for _, status := range []string{"weird", "BANANA"} {
		t.Run(status, func(t *testing.T) {
			out := runAgentWithStatus(t, status)
			if out.State != model.NodeFailed {
				t.Fatalf("未知状态 %q → 节点状态 %s,want %s", status, out.State, model.NodeFailed)
			}
			if !strings.Contains(out.Error, "未知状态") {
				t.Errorf("失败原因应写明是未知状态,实际 %q", out.Error)
			}
			if !strings.Contains(out.Error, status) {
				t.Errorf("失败原因应点名状态值 %q,实际 %q", status, out.Error)
			}
		})
	}
}

// TestAgentStatusEmptyFails 空状态同样是契约外取值:Agent 没报告状态,
// 工作流无从判断成败,不能假装成功。
func TestAgentStatusEmptyFails(t *testing.T) {
	out := runAgentWithStatus(t, "")
	if out.State != model.NodeFailed {
		t.Fatalf("空状态 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if !strings.Contains(out.Error, "未返回状态") {
		t.Errorf("失败原因应写明 Agent 没有返回状态,实际 %q", out.Error)
	}
}

// TestAgentNotCalledAfterRenderFailure 渲染失败时 Agent 不得被调用
// (回归:防止"渲染失败 → 仍然调用 Agent"的半吊子修法)。
func TestAgentNotCalledAfterRenderFailure(t *testing.T) {
	eng := newMinimalEngine(t)
	agt := &recordingAgent{id: "rec"}
	if err := eng.Agents.Register(agt); err != nil {
		t.Fatal(err)
	}
	eng.Perms.Set("rec", permission.Policy{FilesystemRead: true})
	eng.Tpl = &failingRenderer{err: errTemplate}
	node := &model.Node{ID: "an2", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "rec", "mode": "plan", "prompt": "任务={{task}}",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-agt"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("渲染失败 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if n := atomic.LoadInt32(&agt.calls); n != 0 {
		t.Errorf("渲染失败后不应调用 Agent,实际调用 %d 次", n)
	}
}
