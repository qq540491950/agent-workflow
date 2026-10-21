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
