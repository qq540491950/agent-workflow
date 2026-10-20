package agent

import (
	"encoding/json"
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

// ---- 覆盖缺口(迭代 14 复审) ----
//
// 此前只测"凭据值**恰好等于**注入值",没测部分包含 / JSON / 多行形态,
// 也没钉死 minSecretLen 的边界 —— 覆盖缺口让"脱敏其实漏了"这件事看不出来。

// TestRedactorMasksSecretEmbeddedInLongerString 凭据值嵌在更长字符串
// **中间**时也必须被脱敏。
//
// 真实 stderr 里凭据常以 "prefix-sk-xxx-suffix" 的形态出现(日志前缀、
// 拼接的 URL、JSON 字段值),只测"整串相等"会漏掉这一类。
//
// 实现用的是**精确值替换**(strings.ReplaceAll),天然覆盖部分包含;
// 这里把它钉死 —— 将来若改成"形态猜测"式的实现,这条会变红。
func TestRedactorMasksSecretEmbeddedInLongerString(t *testing.T) {
	const secret = "sk-secret-value"
	r := NewRedactor(map[string]string{"ANTHROPIC_AUTH_TOKEN": secret})
	got := r.Text("prefix-" + secret + "-suffix")
	if strings.Contains(got, secret) {
		t.Errorf("嵌在更长串里的凭据未被脱敏: %q", got)
	}
	if !strings.Contains(got, MaskedValue) {
		t.Errorf("脱敏后应保留 %s 标记: %q", MaskedValue, got)
	}
	// 脱敏只换值,不得吞掉前后文
	if !strings.Contains(got, "prefix-") || !strings.Contains(got, "-suffix") {
		t.Errorf("脱敏不应吞掉前后文: %q", got)
	}
}

// TestRedactorMasksSecretInJSON 凭据值出现在 JSON 里必须被脱敏,且
// 脱敏后的文本**仍是合法 JSON**(就地替换不得破坏结构)。
func TestRedactorMasksSecretInJSON(t *testing.T) {
	const secret = "sk-json-secret"
	r := NewRedactor(map[string]string{"ANTHROPIC_API_KEY": secret})
	in := `{"error":"auth failed for ` + secret + `","code":401}`
	got := r.Text(in)
	if strings.Contains(got, secret) {
		t.Errorf("JSON 里的凭据未被脱敏: %q", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("脱敏破坏了 JSON 结构: %q", got)
	}
	if !strings.Contains(got, `"code":401`) {
		t.Errorf("脱敏不应改动无关字段: %q", got)
	}
}

// TestRedactorMasksSecretInMultilineText 凭据值出现在多行文本里
// (CLI 的 stderr 常是多行)必须被脱敏。
func TestRedactorMasksSecretInMultilineText(t *testing.T) {
	const secret = "sk-multiline-secret"
	r := NewRedactor(map[string]string{"PI_AUTH_TOKEN": secret})
	in := "line1: ok\nline2: token=" + secret + "\nline3: retry\n"
	got := r.Text(in)
	if strings.Contains(got, secret) {
		t.Errorf("多行文本里的凭据未被脱敏: %q", got)
	}
	if !strings.Contains(got, "line1: ok") || !strings.Contains(got, "line3: retry") {
		t.Errorf("脱敏不应改动无关行: %q", got)
	}
}

// TestRedactorMinSecretLenBoundary minSecretLen=6 的边界。
//
// 短于 6 字符的值**不**参与脱敏 —— 这是有意设计(见 redact.go 的注释):
// "1"、"true" 这类值在 stderr 里到处都有,脱敏它们会把正常文本搅成一片
// "***"。这里把这条设计钉死,免得将来有人"顺手"把它删掉(那样会误伤
// 正常文本)或改小(那样会漏)。
func TestRedactorMinSecretLenBoundary(t *testing.T) {
	// 5 字符:低于阈值,不脱敏
	r5 := NewRedactor(map[string]string{"MY_TOKEN": "abcde"})
	if got := r5.Text("token=abcde"); got != "token=abcde" {
		t.Errorf("低于 minSecretLen 的值不应被脱敏: %q", got)
	}
	// 正好 6 字符:达到阈值,参与脱敏
	r6 := NewRedactor(map[string]string{"MY_TOKEN": "abcdef"})
	got := r6.Text("token=abcdef")
	if strings.Contains(got, "abcdef") {
		t.Errorf("达到 minSecretLen 的值应被脱敏: %q", got)
	}
	if !strings.Contains(got, MaskedValue) {
		t.Errorf("脱敏后应保留 %s 标记: %q", MaskedValue, got)
	}
}

// TestRedactorReplacesLongerSecretFirst 长的凭据值必须**先**被替换。
//
// 短值若是长值的子串,先替换短值会把长值拆散("sk-abc-def" → "***-def"),
// 长值就不再完整出现 —— 脱敏被削弱。NewRedactor 按长度降序排序,这里钉死
// 这个顺序(断言精确结果:整个长值被一次性替换)。
func TestRedactorReplacesLongerSecretFirst(t *testing.T) {
	r := NewRedactor(map[string]string{
		"SHORT_TOKEN": "sk-abc",
		"LONG_TOKEN":  "sk-abc-def",
	})
	got := r.Text("token=sk-abc-def")
	if got != "token="+MaskedValue {
		t.Errorf("长凭据应被完整替换一次,got = %q", got)
	}
}
