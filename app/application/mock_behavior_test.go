package application

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 本文件锁住 P2-5:Mock 行为脚本(behavior)的契约 ——
//
//  1. **逐键合并**:用户只写 decisions 时,默认脚本里的 summary_template
//     必须保留。此前 buildMock 用 `merged[mode] = raw` **整体替换** ——
//     用户写一个键,默认脚本的其余键全丢(约定 14:不该动的却动了)。
//  2. **未知键/错类型在 UpdateConfig 校验期报错**:此前未知键被 `continue`
//     静默丢弃 —— 用户写 "decision"(少个 s),脚本不生效,界面提示"已保存"。
//
// 契约(bbehavior → mode → 脚本字段):
//
//	mode 对象 := {decisions: string[], summary_template: string, fail_with: string}
//
// 例外:**凭据位**(api_key / token 这类会被掩码的键)允许存在 —— behavior
// 是自由 JSON 框,用户可能把凭据放进去,掩码规则见 docs/api.md。

// TestMockBehaviorMergesPerKey 逐键合并:用户没写的键保留默认值。
func TestMockBehaviorMergesPerKey(t *testing.T) {
	// 只写 decisions —— 默认脚本里的 summary_template 必须保留。
	a := buildMock("mock-claude", map[string]any{
		"review": map[string]any{"decisions": []any{"APPROVED"}},
	})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Mode: "review", Task: "任务"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Decision != agent.DecisionApproved {
		t.Errorf("decision = %q,want APPROVED(用户写的值必须生效)", resp.Decision)
	}
	if !strings.Contains(resp.Summary, "发现的问题已修复") {
		t.Errorf("summary = %q,必须保留默认脚本的 summary_template"+
			"(逐键合并,不是整体替换)", resp.Summary)
	}
}

// TestMockBehaviorKeepsOtherModes 默认脚本里没被覆盖的 mode 不受影响。
func TestMockBehaviorKeepsOtherModes(t *testing.T) {
	a := buildMock("mock-claude", map[string]any{
		"review": map[string]any{"decisions": []any{"APPROVED"}},
	})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Mode: "plan", Task: "任务"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Summary, "制定三步实施方案") {
		t.Errorf("summary = %q,plan 的默认脚本必须保留", resp.Summary)
	}
}

// TestUpdateConfigRejectsUnknownBehaviorField 未知键必须在保存时报错。
func TestUpdateConfigRejectsUnknownBehaviorField(t *testing.T) {
	a := newTestApp(t)
	// 先存一份合法配置,用于验证"报错时不该写库"。
	if err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{
		Model: "m",
		Behavior: map[string]any{
			"review": map[string]any{"decisions": []any{"REJECTED"}},
		},
	}); err != nil {
		t.Fatalf("先存合法配置: %v", err)
	}

	err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{
		Model: "m",
		Behavior: map[string]any{
			// "decision" 是 "decisions" 的拼写错误 → 静默丢弃 = 脚本不生效
			"review": map[string]any{"decision": []any{"APPROVED"}},
		},
	})
	if err == nil {
		t.Fatal("未知字段必须报错(此前被静默丢弃,脚本不生效却提示已保存)")
	}
	if !strings.Contains(err.Error(), "decision") {
		t.Errorf("错误信息必须指出出问题的字段,实际: %v", err)
	}
	// 报错时不能写库。
	got, gerr := a.AgentSvc.GetConfig("mock-claude")
	if gerr != nil {
		t.Fatalf("get config: %v", gerr)
	}
	review, _ := got.Behavior["review"].(map[string]any)
	if _, has := review["decision"]; has {
		t.Errorf("保存被拒绝后不该把非法配置写进库: %#v", got.Behavior)
	}
}

// TestUpdateConfigRejectsWrongBehaviorTypes 类型错必须在保存时报错。
func TestUpdateConfigRejectsWrongBehaviorTypes(t *testing.T) {
	cases := []struct {
		name string
		beh  map[string]any
	}{
		{"decisions 不是数组", map[string]any{"review": map[string]any{"decisions": "APPROVED"}}},
		{"decisions 元素不是字符串", map[string]any{"review": map[string]any{"decisions": []any{1, 2}}}},
		{"summary_template 不是字符串", map[string]any{"review": map[string]any{"summary_template": 3}}},
		{"fail_with 不是字符串", map[string]any{"review": map[string]any{"fail_with": true}}},
		{"mode 的值不是对象", map[string]any{"review": "APPROVED"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			if err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{
				Model: "m", Behavior: tc.beh,
			}); err == nil {
				t.Fatalf("类型错必须报错(此前静默丢弃): behavior=%#v", tc.beh)
			}
		})
	}
}

// TestUpdateConfigAllowsKnownBehaviorFields 合法写法必须放行。
func TestUpdateConfigAllowsKnownBehaviorFields(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{
		Model: "m",
		Behavior: map[string]any{
			"review": map[string]any{
				"decisions":        []any{"REJECTED", "APPROVED"},
				"summary_template": "第 {call} 轮",
				"fail_with":        "注入的失败",
			},
		},
	}); err != nil {
		t.Fatalf("合法 behavior 必须放行: %v", err)
	}
}

// TestUpdateConfigAllowsCredentialField 凭据位必须放行(掩码契约)。
//
// behavior 是自由 JSON 框,用户可能把 api_key 这类凭据放进去 —— 掩码规则
// 见 docs/api.md。加了"未知键报错"之后不能把这条既有契约打死。
func TestUpdateConfigAllowsCredentialField(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{
		Model: "m",
		Behavior: map[string]any{
			"review": map[string]any{
				"api_key":   "sk-secret-in-behavior",
				"decisions": []any{"REJECTED"},
			},
		},
	}); err != nil {
		t.Fatalf("凭据位必须放行(掩码契约): %v", err)
	}
}
