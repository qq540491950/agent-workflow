package agent_test

import (
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 迭代 10(P0-2):制品(git diff)里的凭据脱敏。
//
// 判据是**值的形态**而不是"键名敏感就整值替换"—— 后者会把代码里的
// 标识符一并抹掉(token = getToken() → token = ***),毁掉 diff 的
// 可读性与审计价值。

// TestMaskCredentialValuesMasksCredentialShapes 凭据形态的值必须被替换。
func TestMaskCredentialValuesMasksCredentialShapes(t *testing.T) {
	cases := []string{
		"ANTHROPIC_API_KEY=sk-ant-supersecret",
		"+ANTHROPIC_API_KEY=sk-ant-supersecret",
		"export GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"api_key: aB3xK9mQ2pL7vR4tY6uZ8c1",
		`password = "Xk9mP2qL8vR4tY6uW1zA3Bd"`,
		"authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc",
	}
	for _, c := range cases {
		got := agent.MaskCredentialValues(c)
		if !strings.Contains(got, agent.MaskedValue) {
			t.Errorf("凭据形态未脱敏 %q → %q", c, got)
		}
		if strings.Contains(got, "supersecret") ||
			strings.Contains(got, "16C7e42F292c6912E7710c838347Ae178B4a") ||
			strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") ||
			strings.Contains(got, "aB3xK9mQ2pL7vR4tY6uZ8c1") ||
			strings.Contains(got, "Xk9mP2qL8vR4tY6uW1zA3Bd") ||
			strings.Contains(got, "eyJhbGciOiJIUzI1NiJ9") {
			t.Errorf("脱敏后仍含凭据明文 %q → %q", c, got)
		}
	}
}

// TestMaskCredentialValuesKeepsCodeIdentifiers 可读性回归锁:代码里的
// 标识符必须原样保留(diff 的审计价值在这里)。
func TestMaskCredentialValuesKeepsCodeIdentifiers(t *testing.T) {
	cases := []string{
		"  const token = getToken();",
		"+  const apiKey = cfg.apiKey;",
		"-  password = getPassword();",
		"+  let apiKey = process.env.API_KEY;",
		"  self.auth_token = self.get_token()",
	}
	for _, c := range cases {
		if got := agent.MaskCredentialValues(c); got != c {
			t.Errorf("代码标识符被改动 %q → %q", c, got)
		}
	}
}

// TestMaskCredentialValuesKeepsKeyName 键名必须保留(可读性:读者要看到
// 是哪个键被脱敏了)。
func TestMaskCredentialValuesKeepsKeyName(t *testing.T) {
	got := agent.MaskCredentialValues("ANTHROPIC_API_KEY=sk-ant-supersecret")
	if !strings.Contains(got, "ANTHROPIC_API_KEY=") {
		t.Errorf("键名应保留: %q", got)
	}
	if !strings.Contains(got, agent.MaskedValue) {
		t.Errorf("值应被替换为 %s: %q", agent.MaskedValue, got)
	}
}

// TestMaskCredentialValuesKeepsDiffStructure diff 的结构行不得被破坏。
func TestMaskCredentialValuesKeepsDiffStructure(t *testing.T) {
	diff := `diff --git a/.env b/.env
index 1234567..89abcde 100644
--- a/.env
+++ b/.env
@@ -1,2 +1,3 @@
 ANTHROPIC_API_KEY=sk-ant-supersecret
+export OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz
`
	got := agent.MaskCredentialValues(diff)
	for _, keep := range []string{
		"diff --git a/.env b/.env",
		"index 1234567..89abcde 100644",
		"--- a/.env",
		"+++ b/.env",
		"@@ -1,2 +1,3 @@",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("diff 结构行被破坏,丢失 %q:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "supersecret") || strings.Contains(got, "abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("diff 里仍有凭据明文:\n%s", got)
	}
}

// TestMaskCredentialValuesIdempotent 重复脱敏不得改变结果(读出口与
// 落库侧各过一次,两次结果必须一致)。
func TestMaskCredentialValuesIdempotent(t *testing.T) {
	once := agent.MaskCredentialValues("ANTHROPIC_API_KEY=sk-ant-supersecret")
	twice := agent.MaskCredentialValues(once)
	if once != twice {
		t.Errorf("脱敏不幂等:\n一次: %q\n两次: %q", once, twice)
	}
}

// TestMaskCredentialValuesEmpty 空文本与无凭据文本原样返回。
func TestMaskCredentialValuesEmpty(t *testing.T) {
	if got := agent.MaskCredentialValues(""); got != "" {
		t.Errorf("空文本应原样返回: %q", got)
	}
	plain := "func main() { println(\"hello\") }"
	if got := agent.MaskCredentialValues(plain); got != plain {
		t.Errorf("普通文本应原样返回: %q", got)
	}
}
