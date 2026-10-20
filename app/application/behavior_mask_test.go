package application

import (
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 回归(迭代 14B):behavior 里的凭据 —— GET 必须掩码,且 GET→PUT 往返
// 不许把凭据覆盖成哨兵。
//
// behavior 是自由结构(map[string]any),前端给的是自由 JSON 输入框,
// 用户可以往里放 api_key 这类凭据。此前 Masked() 不碰 behavior →
// GET 明文回显(约定 8 的字面缺口)。
func TestAgentConfigBehaviorMaskRoundTrip(t *testing.T) {
	a := newTestApp(t)

	const secret = "sk-secret-in-behavior"
	if err := a.AgentSvc.UpdateConfig("mock-claude", agent.AgentConfig{
		Model: "m",
		Behavior: map[string]any{
			"review": map[string]any{
				"api_key":   secret,
				"decisions": []any{"REJECTED", "APPROVED"},
			},
		},
	}); err != nil {
		t.Fatalf("update config: %v", err)
	}

	// GET:掩码
	got, err := a.AgentSvc.GetConfig("mock-claude")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	review, ok := got.Behavior["review"].(map[string]any)
	if !ok {
		t.Fatalf("GET 返回的 behavior 结构不对: %#v", got.Behavior)
	}
	if review["api_key"] != agent.MaskedValue {
		t.Errorf("GET 必须把 behavior 里的凭据掩成 %q,实际 %#v", agent.MaskedValue, review["api_key"])
	}
	// 非凭据内容不受影响
	if _, has := review["decisions"]; !has {
		t.Errorf("behavior 的非凭据字段不应被抹掉: %#v", review)
	}

	// PUT:前端把整份配置原样回传 → 凭据必须保住
	if err := a.AgentSvc.UpdateConfig("mock-claude", got); err != nil {
		t.Fatalf("update config(roundtrip): %v", err)
	}
	raw, err := a.Repo.GetAgentConfig("mock-claude")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	rawStr := string(raw)
	if !strings.Contains(rawStr, secret) {
		t.Errorf("GET→PUT 往返后凭据被哨兵覆盖了,库里内容: %s", rawStr)
	}
	if strings.Contains(rawStr, agent.MaskedValue) {
		t.Errorf("库里不该出现字面量 %q: %s", agent.MaskedValue, rawStr)
	}
}
