package agent

import (
	"regexp"
	"strings"
)

// 本文件是"自由文本里的凭据形态"判定与脱敏(约定 8:敏感信息不进日志、
// API 返回掩码)。
//
// 为什么不用 logx.Mask:logx.Mask 的规则是"键名敏感就整值替换",会把
// **代码里的标识符**一并抹掉 —— 而 git diff 制品正是代码/配置片段:
//
//	token = getToken()        → logx.Mask: token = ***
//	const apiKey = cfg.key    → logx.Mask: const apiKey = ***
//
// 那样会把审计证据("改了什么")毁掉 —— 第八轮把"artifact 不脱敏"钉死
// 在测试里,理由正是"不要破坏 git diff 产物的可读性"。
//
// 本文件的规则是**按值的形态**判定:键名指向凭据 **且** 值本身呈凭据
// 形态时才替换,代码标识符原样保留:
//
//	ANTHROPIC_API_KEY=sk-ant-xxx  → ANTHROPIC_API_KEY=***
//	authorization: Bearer eyJ...   → authorization: Bearer ***
//	token = getToken()             → 原样保留
//
// 键名判定复用 IsSensitiveEnvName(与 ExtraArgs 的掩码同一份来源),
// 不另写一份白名单(约定 13 的同源精神)。

// credentialAssignPattern 匹配"键名 + 分隔符 + 值"形态的片段。
//
// 键名取 [A-Za-z_][A-Za-z0-9_.-]*(允许驼峰与点号),值取引号串或
// 一段不含空白/逗号/分号/右括号的 token —— 与日志里常见的环境变量
// 回显("ANTHROPIC_API_KEY=sk-...")、HTTP 头("Authorization: Bearer x")
// 与 .env 行形态一致。
var credentialAssignPattern = regexp.MustCompile(
	`(?i)([A-Za-z_][A-Za-z0-9_.\-]*)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;)\]}]+)`)

// credentialValuePrefixes 是"值"呈凭据形态的**前缀白名单**。
//
// 这些前缀是各服务凭据的公开形态(sk-/ghp_/AKIA/eyJ(JWT)/...),命中即
// 判定为凭据,与键名无关(键名判定已在上一层做过)。
var credentialValuePrefixes = []string{
	"sk-", "sk_", "skant-", "sk-ant-",
	"pk-", "rk-",
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
	"xoxb-", "xoxp-", "xoxa-", "xoxr-", "xapp-",
	"akia", "asia", "aiza", "ya29.", "eyj",
	"npm_", "hf_", "dop_v1_", "sq0atp-", "lin_api_", "hvs.",
	"pypi-", "hooks.slack.com",
}

// credentialKeyHints 是"文本里可能含有凭据键名"的廉价预筛子串。
// 正则很贵,先用 strings.Contains 跳过绝大多数普通文本(diff 的文件名、
// 行号、代码标识符等)。
var credentialKeyHints = []string{
	"key", "token", "password", "passwd", "pwd",
	"secret", "credential", "auth",
}

// authSchemeWords 是"认证方案"单词(Bearer/Basic):值是方案名时,
// 真正的凭据在紧随其后的那个 token 里。
var authSchemeWords = []string{"bearer", "basic"}

// MaskCredentialValues 脱敏文本里"凭据形态"的值,返回新字符串。
//
// 只替换**值**,键名与上下文原样保留 —— 可读性(审计价值)不退化。
// 非凭据形态的值(代码标识符、普通配置值)一律原样保留。
func MaskCredentialValues(text string) string {
	if text == "" || !mayContainSensitiveKey(text) {
		return text
	}
	matches := credentialAssignPattern.FindAllStringSubmatchIndex(text, -1)
	if matches == nil {
		return text
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		key := text[m[2]:m[3]]
		if !IsSensitiveEnvName(key) {
			continue
		}
		valStart, valEnd := m[6], m[7]
		raw := text[valStart:valEnd]
		// 认证方案形态:值是 "Bearer"/"Basic",真正的凭据在下一个 token
		if _, ok := authSchemeWord(raw); ok {
			j := valEnd
			for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
			k := j
			for k < len(text) && !isValueTerminator(text[k]) {
				k++
			}
			if k > j && looksLikeCredentialValue(unquote(text[j:k])) {
				b.WriteString(text[last:valStart])
				b.WriteString(raw + " " + MaskedValue)
				last = k
			}
			continue
		}
		masked, ok := maskCredentialValue(raw)
		if !ok {
			continue
		}
		b.WriteString(text[last:valStart])
		b.WriteString(masked)
		last = valEnd
	}
	b.WriteString(text[last:])
	return b.String()
}

// maskCredentialValue 判断一个值是否呈凭据形态,是则返回掩码后的值
// (引号保留),否则返回 false。
func maskCredentialValue(raw string) (string, bool) {
	body := raw
	quote := byte(0)
	if len(body) >= 2 && (body[0] == '"' || body[0] == '\'') && body[len(body)-1] == body[0] {
		quote = body[0]
		body = body[1 : len(body)-1]
	}
	if !looksLikeCredentialValue(body) {
		return "", false
	}
	if quote != 0 {
		return string(quote) + MaskedValue + string(quote), true
	}
	return MaskedValue, true
}

// looksLikeCredentialValue 判断一个值是否呈"凭据形态"。
//
// 两条判据(任一命中即算):
//
//  1. 已知凭据前缀(sk-/ghp_/AKIA/eyJ(JWT)/PEM 私钥块 ...);
//  2. 高熵长串:够长 + 同时含字母与数字 + 大小写混合(或超长的纯字母数字)。
//
// 刻意**不**判定的形态(它们在代码/配置里极常见,误伤会毁掉 diff):
//
//	token = getToken()        ← 函数调用
//	const apiKey = cfg.apiKey ← 成员引用
//	password = "hunter2"      ← 短字符串(长度不足高熵门槛)
func looksLikeCredentialValue(v string) bool {
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "-----BEGIN") {
		return true // PEM 私钥/证书块
	}
	lower := strings.ToLower(v)
	for _, p := range credentialValuePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	if len(v) >= 16 && hasLetter(v) && hasDigit(v) {
		if (hasUpper(v) && hasLower(v)) || len(v) >= 32 {
			return true
		}
	}
	return false
}

// authSchemeWord 判断一个值是不是认证方案单词(Bearer/Basic)。
func authSchemeWord(raw string) (string, bool) {
	w := strings.ToLower(unquote(strings.TrimSpace(raw)))
	for _, s := range authSchemeWords {
		if w == s {
			return w, true
		}
	}
	return "", false
}

// isValueTerminator 判断字符是否是值的终止符。
func isValueTerminator(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ';', ')', ']', '}', '"', '\'':
		return true
	}
	return false
}

// mayContainSensitiveKey 廉价预筛:文本里是否可能出现凭据键名。
func mayContainSensitiveKey(s string) bool {
	lower := strings.ToLower(s)
	for _, h := range credentialKeyHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

func hasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func hasUpper(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func hasLower(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return true
		}
	}
	return false
}
