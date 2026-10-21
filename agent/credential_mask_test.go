package agent_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/logx"
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

// ---------------------------------------------------------------------------
// 迭代 12:裸值形态(Bearer / JWT / 已知前缀 / PEM)此前谁都不管 ——
// 第十一轮终审实测到 `Bearer <JWT>` 原样落进 execution_nodes.output 与
// executions.state_json,并经所有 API 出口外泄。
// ---------------------------------------------------------------------------

// bearerJWT 是终审实测泄漏的那个 JWT。
const bearerJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ." +
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

// TestMaskCredentialValuesCoversBareShapes 裸值形态必须被脱敏。
//
// 这一组在迭代 12 之前**全部漏掉**:agent.MaskCredentialValues 的规则要求
// "键名 + 值"形态,裸值没有键名 —— 于是谁都不管。
func TestMaskCredentialValuesCoversBareShapes(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string
	}{
		{"裸 Bearer + JWT", "cli stderr: Bearer " + bearerJWT, bearerJWT},
		{"裸 JWT", "value: " + bearerJWT, bearerJWT},
		{"裸 sk-ant", "value: sk-ant-supersecretvalue", "sk-ant-supersecretvalue"},
		{"裸 ghp_", "value: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			"ghp_16C7e42F292c6912E7710c838347Ae178B4a"},
		{"裸 AKIA", "id: AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"Authorization 头", "Authorization: Bearer " + bearerJWT, bearerJWT},
		{"Basic 头", "Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=",
			"YWRtaW46cGFzc3dvcmQxMjM="},
		{"PEM 私钥块", "-----BEGIN RSA PRIVATE KEY-----\n" +
			"MIIEowIBAAKCAQEA7Zx9mQ2pL7vR4tY6uZ8c1d0eF3gH5jK\n" +
			"-----END RSA PRIVATE KEY-----", "MIIEowIBAAKCAQEA7Zx9mQ2pL7vR4tY6uZ8c1d0eF3gH5jK"},
	}
	for _, c := range cases {
		got := agent.MaskCredentialValues(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("[%s] 凭据明文未脱敏: %q → %q", c.name, c.in, got)
		}
		if !strings.Contains(got, agent.MaskedValue) {
			t.Errorf("[%s] 期望出现脱敏占位符: %q → %q", c.name, c.in, got)
		}
	}
}

// TestMaskCredentialValuesKeepsEnglishSentences 英文句子不得被误伤。
//
// 方案词 bearer / basic 在普通句子里是英文单词,不是凭据。
func TestMaskCredentialValuesKeepsEnglishSentences(t *testing.T) {
	cases := []string{
		"The bearer of this document is unknown.",
		"Basic authentication is supported by the API.",
		"Please send the Bearer.",
		"This is a task-management-system for teams.",
	}
	for _, c := range cases {
		if got := agent.MaskCredentialValues(c); got != c {
			t.Errorf("英文句子被改动 %q → %q", c, got)
		}
	}
}

// TestMaskCredentialValuesIdempotentOnBareShapes 裸值形态的脱敏也必须幂等。
func TestMaskCredentialValuesIdempotentOnBareShapes(t *testing.T) {
	for _, in := range []string{
		"cli stderr: Bearer " + bearerJWT,
		"value: sk-ant-supersecretvalue",
		"Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
	} {
		once := agent.MaskCredentialValues(in)
		twice := agent.MaskCredentialValues(once)
		if once != twice {
			t.Errorf("脱敏不幂等:\n输入: %q\n一次: %q\n两次: %q", in, once, twice)
		}
	}
}

// ---------------------------------------------------------------------------
// 迭代 17(#263):值的形态规则**只有一份来源**的跨包回归锁
// ---------------------------------------------------------------------------

// TestMaskCredentialValuesMatchesLogxSingleSource agent 侧的值形态入口必须
// 与 logx 侧**逐字节相同**。
//
// **为什么必须放在 agent 侧**:比对要同时看到两个入口,而 `agent` 依赖
// `logx`(**单向**,没有环)—— `logx` 反过来 import `agent` 会成环。
// 因此跨包比对只能写在 agent 侧(能同时 import 两边的包)。
//
// **回归背景(#263)**:此前的"单源"回归锁在 `logx/credential_test.go` 里,
// 断言的是 `MaskCredentialValues(in)` 与 `MaskCredentialValues(in)` ——
// **同一个函数比较两次**,恒真 → 永远不会失败 → 将来有人在 agent 侧
// **重新写一份**值形态实现,没有任何测试会红(今天 agent 侧确实只是薄封装
// `return logx.MaskCredentialValues(text)`,但那不是被断言保证的)。
//
// 断言的语义:**两个不同函数的输出逐字节相同** —— 只有"同一份实现"才
// 能保证这一点。
func TestMaskCredentialValuesMatchesLogxSingleSource(t *testing.T) {
	inputs := []string{
		// 键名 + 值形态
		"ANTHROPIC_API_KEY=sk-ant-supersecret",
		"export GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"api_key: aB3xK9mQ2pL7vR4tY6uZ8c1Xy",
		`password = "Xk9mP2qL8vR4tY6uW1zA3Bd"`,
		"authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc",
		// 裸值形态
		"cli stderr: Bearer " + bearerJWT,
		"value: " + bearerJWT,
		"value: sk-ant-supersecretvalue",
		"value: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"id: AKIAIOSFODNN7EXAMPLE",
		"Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		// 低熵值(制品出口刻意不遮)
		"password: hunter2",
		"token=abc123 secret=xyz789",
		// 必须原样保留的代码标识符 / 英文句子
		"  const token = getToken();",
		"+  const apiKey = cfg.apiKey;",
		"-  password = getPassword();",
		"  self.auth_token = self.get_token()",
		"The bearer of this document is unknown.",
		"Basic authentication is supported by the API.",
		"普通文本",
		"",
	}
	for _, in := range inputs {
		got := agent.MaskCredentialValues(in)
		want := logx.MaskCredentialValues(in)
		if got != want {
			t.Errorf("agent 与 logx 的值形态入口不一致(同一输入必须逐字节相同):\n"+
				"输入: %q\nagent: %q\nlogx:  %q", in, got, want)
		}
	}
}

// TestSingleSourceAssertionIsNotDegenerate 防"单源回归锁"退化成恒真。
//
// #263 的教训:回归锁必须**能失败**。这里从两个角度钉住:
//
//  1. **两个入口不是同一个函数对象** —— 若哪天有人把
//     `agent.MaskCredentialValues` 改成 `logx.MaskCredentialValues` 的别名
//     (`var MaskCredentialValues = logx.MaskCredentialValues`),
//     跨入口比对就变成"同一个函数比较两次",必须被抓住。
//  2. **本文件里的单源用例确实同时调用了两个入口** —— 直接读本文件的
//     AST(不看运行结果):只调一个入口的"比对"必然恒真。
func TestSingleSourceAssertionIsNotDegenerate(t *testing.T) {
	// (1) 两个入口是**不同的函数对象**。
	if reflect.ValueOf(agent.MaskCredentialValues).Pointer() ==
		reflect.ValueOf(logx.MaskCredentialValues).Pointer() {
		t.Error("agent.MaskCredentialValues 与 logx.MaskCredentialValues 是同一个函数对象:" +
			"跨入口比对会退化成恒真")
	}

	// (2) 单源用例里必须同时出现两个入口的调用。
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("取不到本测试文件的路径")
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, thisFile, nil, 0)
	if err != nil {
		t.Fatalf("解析本测试文件失败: %v", err)
	}
	var foundAgent, foundLogx bool
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestMaskCredentialValuesMatchesLogxSingleSource" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || sel.Sel.Name != "MaskCredentialValues" {
				return true
			}
			switch x.Name {
			case "agent":
				foundAgent = true
			case "logx":
				foundLogx = true
			}
			return true
		})
	}
	if !foundAgent || !foundLogx {
		t.Errorf("单源用例必须同时调用 agent.MaskCredentialValues 与 logx.MaskCredentialValues"+
			"(agent=%v logx=%v):只调一个入口的比对必然恒真", foundAgent, foundLogx)
	}
}
