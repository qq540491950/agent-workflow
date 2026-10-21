package logx

import (
	"strings"
	"testing"
)

// 本文件锁住"凭据形态"规则(迭代 12)。
//
// 背景:第十一轮终审实测到一条泄漏 —— Agent 输出里的**裸** `Bearer <JWT>`
// 落进 execution_nodes.output 与 executions.state_json,并经所有 API 出口
// 外泄。根因是两套并行机制:`logx.Mask`(按键名)与
// `agent.MaskCredentialValues`(按值形态)语义不同、覆盖范围不同,后者
// 只用在 2 处 —— 裸值形态谁都不管。
//
// 本文件把"值的形态"规则钉在 logx(唯一定义处),并锁住三条不变式:
//
//  1. **裸值形态必须被脱敏**(Bearer / JWT / 已知前缀 / PEM / Basic);
//  2. **代码标识符与英文句子不得被误伤**(第十轮的取舍必须保留);
//  3. **脱敏必须幂等**(落库侧与读出口各过一次,两次结果逐字节相同)。

// bearerJWT 是终审实测泄漏的那个 JWT(形态取自真实签发的 HS256 token)。
const bearerJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ." +
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

// TestMaskCoversBareCredentialShapes 裸值形态必须被脱敏。
//
// 这一组正是修复前**全部漏掉**的形态(实测:logx.Mask 与
// agent.MaskCredentialValues 都原样返回)。
func TestMaskCoversBareCredentialShapes(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string
	}{
		{"裸 Bearer + JWT", "cli stderr: Bearer " + bearerJWT, bearerJWT},
		{"Authorization 头", "Authorization: Bearer " + bearerJWT, bearerJWT},
		{"Authorization 头 + 短 token", "Authorization: Bearer abc123xyz", "abc123xyz"},
		{"裸 sk-ant", "value: sk-ant-supersecretvalue", "sk-ant-supersecretvalue"},
		{"裸 ghp_", "value: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			"ghp_16C7e42F292c6912E7710c838347Ae178B4a"},
		{"裸 AKIA", "id: AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"裸 eyJ", "value: " + bearerJWT, bearerJWT},
		{"PEM 私钥块", "-----BEGIN RSA PRIVATE KEY-----\n" +
			"MIIEowIBAAKCAQEA7Zx9mQ2pL7vR4tY6uZ8c1d0eF3gH5jK\n" +
			"-----END RSA PRIVATE KEY-----", "MIIEowIBAAKCAQEA7Zx9mQ2pL7vR4tY6uZ8c1d0eF3gH5jK"},
		{"Basic 认证", "Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=",
			"YWRtaW46cGFzc3dvcmQxMjM="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Mask(c.in)
			if strings.Contains(got, c.secret) {
				t.Errorf("凭据明文未脱敏: %q → %q", c.in, got)
			}
			if !strings.Contains(got, maskPlaceholder) {
				t.Errorf("期望出现脱敏占位符: %q → %q", c.in, got)
			}
			// 脱敏不得把上下文整体抹掉:方案词/键名要保留(可读性)
			if strings.Contains(c.in, "Bearer") && !strings.Contains(got, "Bearer") {
				t.Errorf("认证方案词应保留: %q → %q", c.in, got)
			}
		})
	}
}

// TestMaskValueCoversBareCredentialShapes 结构化出口(事件流 / 状态快照)
// 同样必须盖住裸值形态。
//
// 事件流的三个出口(SQLite 落库 / SSE 推送 / Wails IPC)都经 MaskValue
// 汇聚 —— 裸值形态漏在这里 = 凭据进事件表并推给前端。
func TestMaskValueCoversBareCredentialShapes(t *testing.T) {
	in := map[string]any{
		"output": "cli stderr: Bearer " + bearerJWT,
		"nested": map[string]any{"note": "Bearer " + bearerJWT},
		"list":   []any{"Bearer " + bearerJWT},
	}
	out, ok := MaskValue(in).(map[string]any)
	if !ok {
		t.Fatalf("应返回 map[string]any, 实际 %T", MaskValue(in))
	}
	raw := mustJSON(t, out)
	if strings.Contains(raw, bearerJWT) {
		t.Errorf("结构化出口仍含凭据明文: %s", raw)
	}
	if !strings.Contains(raw, maskPlaceholder) {
		t.Errorf("期望出现脱敏占位符: %s", raw)
	}
}

// TestMaskCredentialValuesKeepsCodeIdentifiers 代码标识符必须原样保留。
//
// **第十轮的取舍**(不能丢):判据是"值的形态"而不是"键名敏感就整值替换"
// —— 后者会把代码里的标识符一并抹掉,毁掉 git diff 的可读性与审计价值。
//
//	const token = getToken();       → 原样
//	const apiKey = cfg.apiKey;      → 原样
//	password = "xxx";               → 原样
func TestMaskCredentialValuesKeepsCodeIdentifiers(t *testing.T) {
	cases := []string{
		"  const token = getToken();",
		"+  const apiKey = cfg.apiKey;",
		"-  password = getPassword();",
		"+  let apiKey = process.env.API_KEY;",
		"  self.auth_token = self.get_token()",
		`  password = "xxx";`,
		"  apiKey = 'hunter2';",
	}
	for _, c := range cases {
		if got := MaskCredentialValues(c); got != c {
			t.Errorf("代码标识符被改动 %q → %q", c, got)
		}
	}
}

// TestMaskKeepsEnglishSentences 英文句子不得被误伤。
//
// 特别地:方案词 "bearer" / "basic" 在普通句子里是**英文单词**,不是凭据。
func TestMaskKeepsEnglishSentences(t *testing.T) {
	cases := []string{
		"The bearer of this document is unknown.",
		"Basic authentication is supported by the API.",
		"Please send the Bearer.",
		"This is a task-management-system for teams.",
		"The basic idea is simple.",
		"workflow completed node=plan duration=42ms",
		"普通文本",
	}
	for _, c := range cases {
		if got := Mask(c); got != c {
			t.Errorf("英文句子被改动 %q → %q", c, got)
		}
		if got := MaskCredentialValues(c); got != c {
			t.Errorf("英文句子被改动(值形态入口) %q → %q", c, got)
		}
	}
}

// TestMaskDoesNotMaskPathsAndIdentifiers 路径与驼峰标识符不得被抹掉。
//
// 回归:裸值高熵规则最初把 Go 测试临时目录名
// ("TestScriptNodeHonorsTimeoutAndWorkingDir1637892132")当成凭据 ——
// 脚本节点因此丢了工作目录,既有测试直接变红。
//
// 两条护栏:路径段(紧邻 "/")与驼峰标识符(大写后紧跟小写连续 ≥4 次)。
func TestMaskDoesNotMaskPathsAndIdentifiers(t *testing.T) {
	cases := []string{
		"/var/folders/j2/1009hrfj2tz2f0cw7vhbzppm0000gn/T/" +
			"TestScriptNodeHonorsTimeoutAndWorkingDir1637892132/001",
		"commit 4a7c9e2b1d8f3a6c5e4d9b2a1c8f7e6d5c4b3a",
		"class TestScriptNodeHonorsTimeoutAndWorkingDir1637892132",
	}
	for _, c := range cases {
		if got := Mask(c); got != c {
			t.Errorf("路径/标识符被改动 %q → %q", c, got)
		}
	}
}

// TestMaskIsIdempotent 脱敏必须幂等:脱敏一次 vs 两次逐字节相同。
//
// 落库侧与读出口各过一次(节点 output 落库时脱敏,API 读出口再脱敏),
// 两次结果必须一致 —— 否则"库里那份"与"接口返回那份"内容不同,
// 审计对不上。
func TestMaskIsIdempotent(t *testing.T) {
	inputs := []string{
		"cli stderr: Bearer " + bearerJWT,
		"Authorization: Bearer " + bearerJWT,
		"ANTHROPIC_API_KEY=sk-ant-supersecretvalue",
		"value: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=",
		"const token = getToken();",
		"普通文本",
	}
	for _, in := range inputs {
		once := Mask(in)
		twice := Mask(once)
		if once != twice {
			t.Errorf("Mask 不幂等:\n输入: %q\n一次: %q\n两次: %q", in, once, twice)
		}
		onceC := MaskCredentialValues(in)
		twiceC := MaskCredentialValues(onceC)
		if onceC != twiceC {
			t.Errorf("MaskCredentialValues 不幂等:\n输入: %q\n一次: %q\n两次: %q", in, onceC, twiceC)
		}
	}
}

// TestMaskCredentialValuesKeepsKeyName 值形态入口只替换值,键名保留。
//
// 可读性:读者要看到是**哪个键**被脱敏了。
func TestMaskCredentialValuesKeepsKeyName(t *testing.T) {
	got := MaskCredentialValues("ANTHROPIC_API_KEY=sk-ant-supersecretvalue")
	if !strings.Contains(got, "ANTHROPIC_API_KEY=") {
		t.Errorf("键名应保留: %q", got)
	}
	if !strings.Contains(got, maskPlaceholder) {
		t.Errorf("值应被替换为 %s: %q", maskPlaceholder, got)
	}
}

// ---------------------------------------------------------------------------
// 迭代 27(P1-1):"键值粘连"形态 —— 凭据前的键名/行前缀把它藏住了
// ---------------------------------------------------------------------------

// TestMaskCoversKeyValueGluedCredentialShapes "键值粘连"形态必须被脱敏。
//
// 背景(第二十七轮终审 P1-1):凭据 token 化时把 '=' / '+' 并进了同一个
// token,于是:
//
//	AK=AKIAIOSFODNN7EXAMPLE  →  token = "AK=AKIAIOSFODNN7EXAMPLE"
//	+AKIAIOSFODNN7EXAMPLE    →  token = "+AKIAIOSFODNN7EXAMPLE"
//
// 两者都**不是**凭据形态:AWS 密钥 ID 的规则锚定 ^…$ ('=' / '+' 让它不匹配),
// 已知前缀用 HasPrefix(前缀不在 token 开头),高熵兜底要求小写
// (AWS 密钥 ID 只有大写+数字)→ **四路全不命中** → 凭据明文从
// 节点 output / state_json / 制品 / 执行详情 / 事件流六个出口全部泄漏。
//
// 注意 "X=ghp_…" / "X=eyJ…" 这类形态此前**看起来**是对的,实测是靠
// "整个 token 恰好高熵"蒙对的(不是靠前缀规则)—— 根因与 AKIA 同源,
// 本用例把这一族形态**逐条**钉住,不再靠运气。
func TestMaskCoversKeyValueGluedCredentialShapes(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string
		// keep 是必须**原样保留**的上下文(键名 / diff 行前缀)。
		// 保留它们是"可读性"取舍:读者要看出是哪个键、哪一行被脱敏了。
		keep string
	}{
		{"短键名 + AWS 密钥 ID", "AK=AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE", "AK="},
		{"diff 新增行前缀 + AWS 密钥 ID", "+AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE", "+"},
		{"diff 删除行前缀 + AWS 密钥 ID", "-AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE", "-"},
		{"短键名 + sk-ant", "X=sk-ant-supersecretvalue", "sk-ant-supersecretvalue", "X="},
		{"短键名 + ghp_", "X=ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			"ghp_16C7e42F292c6912E7710c838347Ae178B4a", "X="},
		{"短键名 + JWT", "X=" + bearerJWT, bearerJWT, "X="},
		{"diff 前缀 + ghp_", "+ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			"ghp_16C7e42F292c6912E7710c838347Ae178B4a", "+"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotMask := Mask(c.in)
			if strings.Contains(gotMask, c.secret) {
				t.Errorf("Mask 出口仍含凭据明文: %q → %q", c.in, gotMask)
			}
			if !strings.Contains(gotMask, maskPlaceholder) {
				t.Errorf("Mask 出口期望出现脱敏占位符: %q → %q", c.in, gotMask)
			}
			if !strings.Contains(gotMask, c.keep) {
				t.Errorf("Mask 出口应保留上下文 %q: %q → %q", c.keep, c.in, gotMask)
			}
			gotValues := MaskCredentialValues(c.in)
			if strings.Contains(gotValues, c.secret) {
				t.Errorf("MaskCredentialValues 出口仍含凭据明文: %q → %q", c.in, gotValues)
			}
			if !strings.Contains(gotValues, maskPlaceholder) {
				t.Errorf("MaskCredentialValues 出口期望出现脱敏占位符: %q → %q", c.in, gotValues)
			}
		})
	}
}

// TestMaskCoversKeyValueGluedCredentialsAtOutlets 端到端:三个出口都不得
// 留下"键值粘连"凭据的明文。
//
// 出口在代码里收敛成三个入口(逐条对照真实调用点):
//
//	节点 output / state_json / 日志 / 执行详情 → logx.Mask
//	事件流(SQLite 落库 + SSE 推送 + Wails IPC) → logx.MaskValue
//	制品(git diff)                          → logx.MaskCredentialValues
//
// 调用点:workflow/runtime/executor.go(节点 output)、
// workflow/runtime/state_mask.go(state_json)、event/event.go(事件流)、
// agent/redact.go(制品)、app/application/application.go(API 读出口)。
func TestMaskCoversKeyValueGluedCredentialsAtOutlets(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	const glued = "AK=" + secret

	// 出口 1:节点 output / state_json / 日志 / API 读出口。
	if got := Mask(glued); strings.Contains(got, secret) {
		t.Errorf("节点 output 出口仍含凭据明文: %q", got)
	}
	// 出口 2:事件流(结构化载荷)。
	out, ok := MaskValue(map[string]any{"output": glued}).(map[string]any)
	if !ok {
		t.Fatalf("MaskValue 应返回 map[string]any, 实际 %T", MaskValue(map[string]any{}))
	}
	raw := mustJSON(t, out)
	if strings.Contains(raw, secret) {
		t.Errorf("事件流出口仍含凭据明文: %s", raw)
	}
	// 出口 3:制品(git diff)。
	if got := MaskCredentialValues(glued); strings.Contains(got, secret) {
		t.Errorf("制品出口仍含凭据明文: %q", got)
	}
}

// TestAWSAccessKeyPatternIsNotRedundant awsAccessKeyPattern **不是冗余规则**。
//
// 第二十七轮终审指出:"把 awsAccessKeyPattern 删掉,没有任何测试变红" ——
// 虚假信心。本用例给出**只有它能抓住**的形态:不含数字的 AWS 密钥 ID。
//
// 判据:宽松前缀规则("akia" 等)要求值里**含数字**,高熵规则也要求含数字
// → 无数字的 AWS 密钥 ID 只有 awsAccessKeyPattern 能命中。删掉它 → 本用例红。
func TestAWSAccessKeyPatternIsNotRedundant(t *testing.T) {
	// AKIA + 16 位大写字母(无数字)。
	const keyID = "AKIAABCDEFGHIJKL"
	// 前提自检:这条值**不含数字** —— 否则宽松前缀规则也能命中,
	// 本用例就退化成"测不出 awsAccessKeyPattern 的作用"了。
	if hasDigit(keyID) {
		t.Fatalf("测试前提不成立:%q 含数字(应选一个无数字的 AWS 密钥 ID)", keyID)
	}
	for _, in := range []string{keyID, "id: " + keyID, "+" + keyID, "AK=" + keyID} {
		got := Mask(in)
		if strings.Contains(got, keyID) {
			t.Errorf("无数字的 AWS 密钥 ID 未脱敏(只有 awsAccessKeyPattern 能抓住它): %q → %q", in, got)
		}
		if !strings.Contains(got, maskPlaceholder) {
			t.Errorf("期望出现脱敏占位符: %q → %q", in, got)
		}
	}
}

// TestBareBase64WithPaddingIsNotTruncated 裸 base64(带 '=' 填充)不得被截断。
//
// 回归风险(本轮修法的核心约束):凭据 token 化的字符集里**必须保留** '='
// —— 它是 base64 的合法字符。若把它删掉,这串 base64 会被截短成 22 字符
// → 跌破裸值语境的高熵门槛(≥24)→ **整串明文泄漏**。
//
// 因此本用例用的是**恰好 24 字符**(含 2 个填充)的 base64:去掉 '=' 就跌破门槛。
func TestBareBase64WithPaddingIsNotTruncated(t *testing.T) {
	// 16 字节的 base64 —— 24 字符,末尾两个 '=' 填充。
	// 夹具的挑选有两条硬要求(否则本用例就测不出"截断"这件事):
	//   1. 去掉末尾填充后长度 **< 24** → 截断即跌破裸值高熵门槛;
	//   2. **驼峰起伏 < 4 次** → 不被"驼峰标识符"护栏挡下(否则护栏会
	//      替高熵规则把这条拦掉,用例就测错了东西)。
	const b64 = "i5Ul4g/aaJJ/Ky/4Nvc1eA=="
	if len(b64) != 24 {
		t.Fatalf("测试前提不成立:base64 夹具长度应为 24(含填充),实际 %d", len(b64))
	}
	if len(strings.TrimRight(b64, "=")) >= 24 {
		t.Fatalf("测试前提不成立:去掉填充后仍 ≥24 字符,截断也过不了门槛")
	}
	if humps := countHumps(b64); humps >= 4 {
		t.Fatalf("测试前提不成立:夹具的驼峰起伏为 %d(≥4 会被驼峰护栏挡下)", humps)
	}
	for _, in := range []string{b64, "value " + b64, "AK=" + b64} {
		got := Mask(in)
		if strings.Contains(got, b64) {
			t.Errorf("裸 base64 未脱敏(= 被截断 → 跌破高熵门槛): %q → %q", in, got)
		}
		if !strings.Contains(got, maskPlaceholder) {
			t.Errorf("期望出现脱敏占位符: %q → %q", in, got)
		}
	}
}

// countHumps 统计"大写字母后紧跟小写字母"的次数(驼峰起伏)。
//
// 与 credential.go 的 isCamelIdentifier 同一口径 —— 用例用它自检
// "夹具会不会被驼峰护栏挡下"(护栏一旦变化,本用例的前提自检会先红)。
func countHumps(v string) int {
	n := 0
	for i := 1; i < len(v); i++ {
		if v[i-1] >= 'A' && v[i-1] <= 'Z' && v[i] >= 'a' && v[i] <= 'z' {
			n++
		}
	}
	return n
}

// TestBasicAuthBase64IsMasked "Basic <base64>" 形态必须被脱敏。
//
// 本轮(迭代 27)的修法有一条硬约束:凭据 token 化的字符集里**必须保留** '='
// —— 它是 base64 的合法字符。本用例锁住两件事:
//
//  1. 带 '=' 填充的 base64(既有形态)仍被脱敏 —— 不得因为切分规则而截断;
//  2. 短一档的 base64("dXNlcjpwYXNz" = base64("user:pass"))也被脱敏 ——
//     它没有填充、没有数字、长度 12,前三档判据都判不出。
//
// 反向对照(不得误伤):"Basic authentication" 这类英文短语**不能**被当成
// 凭据 —— 判据要求 base64 能**解出含 ':' 的字节**,英文短语解不出来。
func TestBasicAuthBase64IsMasked(t *testing.T) {
	cred := []string{
		"Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=", // 带填充
		"Basic YWRtaW46cGFzc3dvcmQxMjM=",                // 裸形态 + 填充
		"Basic dXNlcjpwYXNz",                            // 裸形态,无填充(短一档)
		"Basic dXNlcjpwYXNz=",                           // 同一串,带填充
	}
	for _, in := range cred {
		got := Mask(in)
		if !strings.Contains(got, maskPlaceholder) {
			t.Errorf("Basic base64 未脱敏: %q → %q", in, got)
		}
		if strings.Contains(got, "dXNlcjpwYXNz") || strings.Contains(got, "YWRtaW46cGFzc3dvcmQxMjM") {
			t.Errorf("Basic base64 明文仍在: %q → %q", in, got)
		}
	}
	// 反向对照:英文短语不得被误伤。
	for _, in := range []string{
		"Basic authentication is supported by the API.",
		"Basic Tokenization is the standard here.",
		"Basic application layer",
	} {
		if got := Mask(in); got != in {
			t.Errorf("英文短语被误伤: %q → %q", in, got)
		}
	}
}

// TestMaskValueShapeRuleIsSingleSource 值的形态规则只有一份来源。
//
// **回归背景(#263)**:本用例此前断言的是
// `MaskCredentialValues(in)` 与 `MaskCredentialValues(in)` —— **同一个函数
// 比较两次**,恒真 → 永远不会失败 → "两份实现各自演化就会出现日志侧脱敏了、
// 制品侧没脱敏的半吊子防护"这件事**没有任何测试挡着**。
//
// 现在断言的是**两个不同的入口**:
//
//	Mask                 键名规则 ∪ 值形态规则
//	MaskCredentialValues 只看值形态(制品出口)
//
// 两者共用同一份值形态实现(`maskCredentialShapes`),因此:
//
//  1. **键名规则不触发**的输入上,两者必须**逐字节相同**;
//  2. 任何输入上,`Mask` 都不得比 `MaskCredentialValues` **少遮**(值形态
//     规则只有一份 → 更严的那个入口不可能漏掉更松的那个已遮住的内容)。
//     第 2 条在下面的**覆盖矩阵**里逐形态断言(矩阵的每一行都同时跑两个出口)。
//
// 跨包的那一半(`agent` 薄封装 vs `logx` 唯一来源)在
// `agent/credential_mask_test.go` 里断言 —— **logx 不能 import agent**
// (agent 依赖 logx,反向 import 成环),比对必须放在能同时 import 两边的包里。
func TestMaskValueShapeRuleIsSingleSource(t *testing.T) {
	// 键名规则**不触发**的输入:不含 api_key/token/password/secret 这类键名。
	// 这组上两个入口必须逐字节相同(值形态规则同源 → 输出一致)。
	same := []string{
		"cli stderr: Bearer " + bearerJWT,
		"value: " + bearerJWT,
		"value: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"id: AKIAIOSFODNN7EXAMPLE",
		"value: sk-ant-supersecretvalue",
		"value: aB3xK9mQ2pL7vR4tY6uZ8c1Xy",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"普通文本",
		"",
	}
	for _, in := range same {
		if got, want := Mask(in), MaskCredentialValues(in); got != want {
			t.Errorf("两个入口不一致(键名规则未触发的输入上必须逐字节相同):\n"+
				"输入: %q\nMask: %q\nMaskCredentialValues: %q", in, got, want)
		}
	}
}

// TestCredentialShapeCoverageMatrix 是本轮的**覆盖矩阵**(形态 × 出口)。
//
// 出口在代码里收敛成两个入口:
//
//	Mask                日志 / 节点 output / state_json / 事件流 / 执行详情
//	MaskCredentialValues 制品(git diff):只看值形态,保住代码可读性
//
// 每个 (形态, 入口) 都**显式**登记期望:未覆盖的格子必须写清理由。
// 不能靠"默认都覆盖"蒙混 —— 矩阵里每一条"未覆盖"都是有意的取舍,
// 将来谁改了门槛,这条用例会立刻变红,取舍必须被重新论证一遍。
func TestCredentialShapeCoverageMatrix(t *testing.T) {
	const (
		// 高熵长串:无前缀、无键名。
		//
		// 长度必须 ≥24:这是**裸值语境**的高熵门槛(比键名语境严)。
		// 24 以下不脱敏 —— 理由见 credential.go 的 highEntropy 注释:
		// "TestScriptNode1637892132" 这类"驼峰 + 数字后缀"的标识符
		// 就在 20~23 字符区间,门槛再低就会把它们抹掉(实测踩过)。
		// 24 以上的长度已经足够长,驼峰标识符(大小写起伏 ≥4 次)另有护栏。
		highEntropy = "aB3xK9mQ2pL7vR4tY6uZ8c1Xy"
		// PEM 私钥块。
		pemBlock = "-----BEGIN RSA PRIVATE KEY-----\n" +
			"MIIEowIBAAKCAQEA7Zx9mQ2pL7vR4tY6uZ8c1d0eF3gH5jK\n" +
			"-----END RSA PRIVATE KEY-----"
		pemBody = "MIIEowIBAAKCAQEA7Zx9mQ2pL7vR4tY6uZ8c1d0eF3gH5jK"
	)

	type row struct {
		shape string
		in    string
		// secret 是必须消失的凭据明文。
		secret string
		// maskedByMask / maskedByValues:两个出口是否必须脱敏。
		maskedByMask   bool
		maskedByValues bool
		// why 未覆盖时的理由(覆盖时为空)。
		why string
	}
	rows := []row{
		{
			shape: "API_KEY=sk-xxx(key=value)", in: "API_KEY=sk-live-9f8e7d6c5e4d",
			secret: "sk-live-9f8e7d6c5e4d", maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "sk-ant-xxx(裸值)", in: "value sk-ant-supersecretvalue",
			secret: "sk-ant-supersecretvalue", maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "ghp_xxx(裸值)", in: "value ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			secret:       "ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "AKIA...(裸值)", in: "id AKIAIOSFODNN7EXAMPLE",
			secret: "AKIAIOSFODNN7EXAMPLE", maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "eyJ...(裸 JWT)", in: "value " + bearerJWT,
			secret: bearerJWT, maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "Bearer eyJ...(裸方案词)", in: "cli stderr: Bearer " + bearerJWT,
			secret: bearerJWT, maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "Authorization: Bearer eyJ...(头形态)", in: "Authorization: Bearer " + bearerJWT,
			secret: bearerJWT, maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "Authorization: Bearer abc123xyz(头形态,短 token)",
			in:    "Authorization: Bearer abc123xyz", secret: "abc123xyz",
			maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "Basic <base64>(头形态)", in: "Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM=",
			secret: "YWRtaW46cGFzc3dvcmQxMjM=", maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "PEM 私钥块", in: pemBlock, secret: pemBody,
			maskedByMask: true, maskedByValues: true,
		},
		{
			shape: "password: hunter2(冒号形态,低熵)",
			in:    "password: hunter2", secret: "hunter2",
			maskedByMask: true, maskedByValues: false,
			why: "制品出口只看值形态:低熵值不呈凭据形态 → 不脱敏。" +
				"这是第十轮的取舍(diff 里 `password = \"xxx\"` 多半是代码/占位符," +
				"抹掉会毁掉审计证据);DB/API/日志出口走键名规则,照常脱敏。",
		},
		{
			shape: "token=xxx / secret=xxx(低熵)", in: "token=abc123 secret=xyz789",
			secret: "abc123", maskedByMask: true, maskedByValues: false,
			why: "同上:低熵值在制品出口不脱敏(DB/API/日志出口脱敏)。",
		},
		{
			shape: "高熵长串(无前缀无键名)", in: "value " + highEntropy,
			secret: highEntropy, maskedByMask: true, maskedByValues: true,
		},
		{
			// 迭代 27(P1-1)新增:键名把凭据"粘"住了 —— 凭据前的短键名
			// 进了同一个 token,四路形态判据全不命中 → 六个出口全泄漏。
			shape: "AK=AKIA…(键值粘连)", in: "AK=AKIAIOSFODNN7EXAMPLE",
			secret: "AKIAIOSFODNN7EXAMPLE", maskedByMask: true, maskedByValues: true,
		},
		{
			// 迭代 27(P1-1)新增:diff 行前缀把凭据"粘"住了(同源根因)。
			shape: "+AKIA…(diff 行前缀)", in: "+AKIAIOSFODNN7EXAMPLE",
			secret: "AKIAIOSFODNN7EXAMPLE", maskedByMask: true, maskedByValues: true,
		},
	}

	for _, r := range rows {
		t.Run(r.shape, func(t *testing.T) {
			gotMask := Mask(r.in)
			if strings.Contains(gotMask, r.secret) != !r.maskedByMask {
				t.Errorf("Mask 出口与矩阵不符(期望脱敏=%v): %q → %q",
					r.maskedByMask, r.in, gotMask)
			}
			gotValues := MaskCredentialValues(r.in)
			if strings.Contains(gotValues, r.secret) != !r.maskedByValues {
				t.Errorf("MaskCredentialValues 出口与矩阵不符(期望脱敏=%v): %q → %q",
					r.maskedByValues, r.in, gotValues)
			}
			if r.why == "" && r.maskedByValues && !strings.Contains(gotValues, maskPlaceholder) {
				t.Errorf("期望出现脱敏占位符: %q → %q", r.in, gotValues)
			}
			// 值形态规则只有一份来源 → 更严的入口(Mask = 键名 ∪ 值形态)
			// 不可能比更松的入口(MaskCredentialValues = 只看值形态)**少遮**:
			// 若这里红,说明有人给两个入口写了**两份**值形态实现。
			if r.maskedByValues && strings.Contains(gotMask, r.secret) {
				t.Errorf("Mask 出口比 MaskCredentialValues 少遮(值形态规则不同源): %q → %q",
					r.in, gotMask)
			}
		})
	}
}
