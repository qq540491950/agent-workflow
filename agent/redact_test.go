package agent

import (
	"strings"
	"testing"
)

// 迭代 7 审计新增:注入给子进程的凭据不得原样出现在错误文本里(约定 8)。

// TestRedactorMasksInjectedSecrets 只脱敏"确实注入过的凭据值",
// 不做形态猜测 —— 形态猜测会误伤正常文本。
func TestRedactorMasksInjectedSecrets(t *testing.T) {
	const secret = "sk-ant-supersecret"
	r := NewRedactor(map[string]string{"ANTHROPIC_AUTH_TOKEN": secret})
	got := r.Text("认证失败: ANTHROPIC_AUTH_TOKEN=" + secret)
	if strings.Contains(got, secret) {
		t.Errorf("凭据未被脱敏: %q", got)
	}
	if !strings.Contains(got, MaskedValue) {
		t.Errorf("脱敏后应保留 %s 标记: %q", MaskedValue, got)
	}
}

// TestRedactorKeepsNonSensitiveValues 非凭据的环境变量不得被脱敏
// (否则 stderr 里的正常内容会被搅成一片)。
func TestRedactorKeepsNonSensitiveValues(t *testing.T) {
	r := NewRedactor(map[string]string{
		"MODEL":          "claude-sonnet-4-5",
		"BASE_URL":       "http://proxy.local:8080",
		"PI_BASE_URL":    "http://proxy.local:9000",
		"WORKING_DIR":    "/tmp/work dir",
		"MAX_TOKENS_OUT": "8192",
	})
	in := "model=claude-sonnet-4-5 base=http://proxy.local:8080"
	if got := r.Text(in); got != in {
		t.Errorf("非凭据内容被改动: %q", got)
	}
}

// TestRedactorIgnoresShortValues 过短的值脱敏会大面积误伤
// ("1"、"true" 在 stderr 里到处都有),不参与脱敏。
func TestRedactorIgnoresShortValues(t *testing.T) {
	r := NewRedactor(map[string]string{"MY_TOKEN": "abc"})
	if got := r.Text("token=abc"); got != "token=abc" {
		t.Errorf("过短的值不应被脱敏: %q", got)
	}
}

// TestRedactorSegmentMatch 键名按 "_"/"-" 切段匹配:
// MONKEY 只是碰巧含 "key",不是凭据名,不应脱敏。
func TestRedactorSegmentMatch(t *testing.T) {
	r := NewRedactor(map[string]string{"MONKEY": "banana-value"})
	if got := r.Text("monkey=banana-value"); got != "monkey=banana-value" {
		t.Errorf("非凭据键名不应被脱敏: %q", got)
	}
	r2 := NewRedactor(map[string]string{"ANTHROPIC_API_KEY": "sk-ant-value"})
	if got := r2.Text("key=sk-ant-value"); strings.Contains(got, "sk-ant-value") {
		t.Errorf("凭据未被脱敏: %q", got)
	}
}

// TestRedactorMultipleSources 配置层与请求层的凭据都要脱敏。
func TestRedactorMultipleSources(t *testing.T) {
	r := NewRedactor(
		map[string]string{"ANTHROPIC_API_KEY": "sk-config-value"},
		map[string]string{"PI_AUTH_TOKEN": "sk-request-value"},
	)
	got := r.Text("a=sk-config-value b=sk-request-value")
	if strings.Contains(got, "sk-config-value") || strings.Contains(got, "sk-request-value") {
		t.Errorf("凭据未被脱敏: %q", got)
	}
}
