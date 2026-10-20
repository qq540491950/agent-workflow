package runtime

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"另外两处同类静默丢弃"(约定 14:该失败却成功等同 P0)。
//
// 1. agent 节点的 prompt 写成对象却没有可用的 template:
//    此前 prompt 变成空串 —— Agent 收到**空提示词**照常执行。
// 2. subworkflow 节点的 input 不是对象:
//    此前 input 变成 nil —— 子工作流收到空输入,用户写的输入被丢弃。

// TestAgentPromptObjectWithoutTemplateFails prompt 对象缺 template → 节点失败。
func TestAgentPromptObjectWithoutTemplateFails(t *testing.T) {
	cases := []struct {
		name   string
		prompt map[string]any
	}{
		{"缺 template 键", map[string]any{"templat": "写周报"}},
		{"template 不是字符串", map[string]any{"template": 42}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newMinimalEngine(t)
			agt := &recordingAgent{id: "rec"}
			if err := eng.Agents.Register(agt); err != nil {
				t.Fatal(err)
			}
			eng.Perms.Set("rec", permission.Policy{FilesystemRead: true})
			node := &model.Node{ID: "pn", Type: model.NodeTypeAgent, Config: map[string]any{
				"agent": "rec", "mode": "plan", "prompt": tc.prompt,
			}}
			env := &compiler.RunEnv{
				Exec: &model.Execution{ID: "exec-prompt"}, WF: &model.Workflow{},
				Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
			}
			out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
			if out.State != model.NodeFailed {
				t.Fatalf("prompt %s → 节点状态 %s,want %s", tc.name, out.State, model.NodeFailed)
			}
			if !strings.Contains(out.Error, "prompt") {
				t.Errorf("失败原因应点名 prompt,实际 %q", out.Error)
			}
			if agt.instructions != "" {
				t.Errorf("失败时不应把提示词交给 Agent,实际 %q", agt.instructions)
			}
		})
	}
}

// TestAgentPromptObjectWithTemplateOK 正常写法 {template: ...} 不受影响。
func TestAgentPromptObjectWithTemplateOK(t *testing.T) {
	eng := newMinimalEngine(t)
	agt := &recordingAgent{id: "rec"}
	if err := eng.Agents.Register(agt); err != nil {
		t.Fatal(err)
	}
	eng.Perms.Set("rec", permission.Policy{FilesystemRead: true})
	node := &model.Node{ID: "pn2", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "rec", "mode": "plan",
		"prompt": map[string]any{"template": "任务={{task}}"},
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-prompt"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	st := &fakeState{m: map[string]any{"task": "写周报"}}
	out := eng.ExecuteNode(context.Background(), env, node, st)
	if out.State != model.NodeSuccess {
		t.Fatalf("prompt 对象写法 → 节点状态 %s,want %s (%s)", out.State, model.NodeSuccess, out.Error)
	}
	if !strings.Contains(agt.instructions, "写周报") {
		t.Errorf("Agent 应收到渲染后的提示词,实际 %q", agt.instructions)
	}
}

// TestSubworkflowInputWrongTypeFails subworkflow 的 input 不是对象 → 节点失败。
func TestSubworkflowInputWrongTypeFails(t *testing.T) {
	eng := newMinimalEngine(t)
	var gotInput map[string]any
	eng.SubworkflowRunner = func(ctx context.Context, wfID, task string, input map[string]any) (map[string]any, error) {
		gotInput = input
		return map[string]any{"ok": true}, nil
	}
	node := &model.Node{ID: "sw", Type: model.NodeTypeSubWorkflow, Config: map[string]any{
		"workflow_id": "child", "input": "not-a-map",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-sw"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("input 类型非法 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if !strings.Contains(out.Error, "input") {
		t.Errorf("失败原因应点名 input,实际 %q", out.Error)
	}
	if gotInput != nil {
		t.Errorf("失败时不应调用子工作流,实际收到 %v", gotInput)
	}
}

// TestSubworkflowInputMapReachesChild 正常对象输入必须原样传给子工作流。
func TestSubworkflowInputMapReachesChild(t *testing.T) {
	eng := newMinimalEngine(t)
	var gotInput map[string]any
	eng.SubworkflowRunner = func(ctx context.Context, wfID, task string, input map[string]any) (map[string]any, error) {
		gotInput = input
		return map[string]any{"ok": true}, nil
	}
	node := &model.Node{ID: "sw2", Type: model.NodeTypeSubWorkflow, Config: map[string]any{
		"workflow_id": "child", "input": map[string]any{"k": "v"},
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-sw"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeSuccess {
		t.Fatalf("合法 input → 节点状态 %s,want %s (%s)", out.State, model.NodeSuccess, out.Error)
	}
	if gotInput["k"] != "v" {
		t.Errorf("子工作流应收到 input.k=v,实际 %v", gotInput)
	}
}
