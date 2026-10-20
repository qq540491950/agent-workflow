package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// 回归(迭代 14B):AgentConfig.Behavior 必须纳入掩码面。
//
// Behavior 是**自由结构**(map[string]any,没有固定 schema),前端给的是
// 一个自由 JSON 输入框(frontend/src/pages/Agents.tsx),用户可以往里放
// 任何键值 —— 包括 api_key / token 这类凭据。此前 Masked() 只掩
// Env/ExtraArgs,Behavior 原样回显 → GET 接口明文回显凭据(约定 8)。
func TestMaskedMasksBehaviorCredentials(t *testing.T) {
	cfg := AgentConfig{
		Model: "m",
		Env:   map[string]string{"ANTHROPIC_API_KEY": "sk-real-secret"},
		Behavior: map[string]any{
			"review": map[string]any{
				// 键名敏感 → 整值替换
				"api_key": "sk-in-behavior",
				// 值本身呈凭据形态 → 就地替换
				"summary_template": "调用时带上 ANTHROPIC_API_KEY=sk-in-template",
				// 普通演示脚本 → 不受影响
				"decisions": []any{"REJECTED", "APPROVED"},
			},
		},
	}
	masked := cfg.Masked()

	raw, err := json.Marshal(masked.Behavior)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	for _, secret := range []string{"sk-in-behavior", "sk-in-template"} {
		if strings.Contains(got, secret) {
			t.Errorf("掩码后仍含明文凭据 %q: %s", secret, got)
		}
	}
	// 演示脚本不能被掩掉(否则 Mock 的行为配置就没了)
	if !strings.Contains(got, "REJECTED") || !strings.Contains(got, "summary_template") {
		t.Errorf("非凭据内容不应被掩掉: %s", got)
	}
	// Env 仍照旧掩码
	if masked.Env["ANTHROPIC_API_KEY"] != MaskedValue {
		t.Errorf("Env 应掩码为 %q,实际 %q", MaskedValue, masked.Env["ANTHROPIC_API_KEY"])
	}
}

// 掩码不得**改坏**行为脚本:键名不敏感、值不是凭据形态的内容原样保留。
func TestMaskedKeepsPlainBehavior(t *testing.T) {
	cfg := AgentConfig{
		Behavior: map[string]any{
			"plan": map[string]any{
				"summary_template": "分析任务:{task};制定三步实施方案",
				"decisions":        []any{"REJECTED", "APPROVED"},
			},
		},
	}
	masked := cfg.Masked()
	raw, err := json.Marshal(masked.Behavior)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	for _, want := range []string{"分析任务:{task};制定三步实施方案", "REJECTED", "APPROVED"} {
		if !strings.Contains(got, want) {
			t.Errorf("普通行为配置不应被掩掉(缺 %q): %s", want, got)
		}
	}
}

// 回归(迭代 14B):GET→PUT 往返不许把凭据覆盖成哨兵。
//
// 前端把整份配置原样回传(打开配置拿到掩码 → 只改模型 → 保存),
// behavior 里的 "***" 必须按**同路径**换回已存原值。
func TestMergeMaskedBehaviorKeepsStoredValue(t *testing.T) {
	stored := map[string]any{
		"review": map[string]any{
			"api_key": "sk-real",
		},
	}
	submitted := map[string]any{
		"review": map[string]any{
			"api_key":          MaskedValue,
			"summary_template": "新模板",
		},
	}
	merged := MergeMaskedBehavior(submitted, stored)

	review, ok := merged["review"].(map[string]any)
	if !ok {
		t.Fatalf("review 应保持 map,实际 %#v", merged["review"])
	}
	if review["api_key"] != "sk-real" {
		t.Errorf("api_key 应保留已存值,实际 %#v", review["api_key"])
	}
	if review["summary_template"] != "新模板" {
		t.Errorf("非掩码字段应保留提交值,实际 %#v", review["summary_template"])
	}
}

// 同名字段在不同 mode 下互相独立:不能把 A mode 的凭据搬到 B mode。
func TestMergeMaskedBehaviorNoCrossModeLeak(t *testing.T) {
	stored := map[string]any{
		"review": map[string]any{"api_key": "sk-review"},
	}
	submitted := map[string]any{
		"review": map[string]any{"api_key": MaskedValue},
		"plan":   map[string]any{"api_key": MaskedValue},
	}
	merged := MergeMaskedBehavior(submitted, stored)

	review := merged["review"].(map[string]any)
	if review["api_key"] != "sk-review" {
		t.Errorf("review.api_key 应保留已存值,实际 %#v", review["api_key"])
	}
	plan := merged["plan"].(map[string]any)
	if plan["api_key"] != MaskedValue {
		t.Errorf("plan.api_key 无已存值,应保留哨兵(不得借用 review 的值),实际 %#v", plan["api_key"])
	}
}

// 没有已存值时保留哨兵(不静默删掉用户的键)。
func TestMergeMaskedBehaviorWithoutStoredValue(t *testing.T) {
	submitted := map[string]any{
		"review": map[string]any{"api_key": MaskedValue},
	}
	merged := MergeMaskedBehavior(submitted, nil)
	review := merged["review"].(map[string]any)
	if review["api_key"] != MaskedValue {
		t.Errorf("无已存值时应保留哨兵,实际 %#v", review["api_key"])
	}
}

// nil 提交 = 不改 behavior。
func TestMergeMaskedBehaviorNil(t *testing.T) {
	if got := MergeMaskedBehavior(nil, map[string]any{"a": "b"}); got != nil {
		t.Errorf("nil 提交应返回 nil,实际 %#v", got)
	}
}
