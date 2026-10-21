package logx

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
)

// 本文件是"凭据形态"判定与脱敏的**唯一来源**(约定 #8 + 约定 #13 的同源精神)。
//
// ---- 为什么曾经有两套机制 ----
//
// 迭代 12 之前,仓库里有两套并行的掩码规则:
//
//   - logx.Mask / logx.MaskValue:按键名判定(key=value / key: value 形态);
//   - agent.MaskCredentialValues:按**值形态**判定(已知前缀 / JWT / PEM /
//     高熵长串),且只在 2 处被调用(制品出口 + agent 内部)。
//
// 两套语义不同、覆盖范围不同,于是同一个凭据在不同出口的结论不一致。
// 第十一轮终审实测到的泄漏就是这条缝:
//
//	cli stderr: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc
//
//   - logx.Mask:盖不住(没有键名 → 键名规则不触发);
//   - agent.MaskCredentialValues:也盖不住(它的规则要求"键名 + 值"形态,
//     裸 Bearer 没有键名)。
//
// 结果:该 JWT 原样落进 execution_nodes.output 与 executions.state_json,
// 并经所有 API 出口(SSE / 执行详情 / 导出)流到前端。
//
// ---- 现在的分工 ----
//
// **值的形态规则只有这一份**(本文件)。三个出口共用它:
//
//	logx.Mask(s)             日志 / 节点 output / state_json / 事件流
//	logx.MaskValue(v)        结构化(map / slice / 结构体)
//	logx.MaskCredentialValues(s)  只按值形态(制品 diff:必须保住代码可读性)
//
// agent.MaskCredentialValues 变成薄封装(转发到本文件),不再自带一份规则。
//
// ---- 两种严格度(刻意区分,不是两份规则) ----
//
//   - 键名语境(key=value):键名本身就是线索,高熵门槛**宽松**
//     (≥16 字符 + 字母 + 数字 + 大小写混合 或 ≥32 字符);
//   - 裸值语境(没有键名):没有线索,高熵门槛**严格**
//     (≥24 字符 + 大小写混合 + 数字)—— 否则 40 位 git SHA、base64 测试
//     夹具这类正常内容会被抹掉,毁掉 diff 的审计价值。
//
// 判定"是不是凭据形态"的函数是同一个(credentialShape),只有门槛参数不同。

// ---- 占位符 ----

// maskPlaceholder 在 mask.go 定义(= "***")。

// ---------------------------------------------------------------------------
// 预筛
// ---------------------------------------------------------------------------

// credentialShapeHints 是"文本里可能含凭据形态值"的廉价预筛子串(全小写)。
//
// 正则很贵,而事件总线的热路径上每个 payload 都要过一遍 Mask。先用
// strings.Contains 跳过绝大多数正常文本:node 名、summary、英文句子、
// 代码标识符都不含这些子串。
var credentialShapeHints = []string{
	"sk-", "sk_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_",
	"glpat-", "xox", "xapp-", "npm_", "hf_", "ya29.", "eyj", "akia", "asia",
	"aiza", "abia", "aida", "aroa", "anpa", "acca",
	"pk-", "rk-", "dop_v1_", "sq0atp-", "lin_api_", "hvs.",
	"pypi-", "shpat_", "shpca_", "shppa_", "bearer", "basic", "-----begin",
	"hooks.slack.com",
}

// mayContainCredentialShape 廉价预筛:文本里是否可能出现凭据形态的值。
func mayContainCredentialShape(s string) bool {
	lower := strings.ToLower(s)
	for _, h := range credentialShapeHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	// 高熵裸值:必须同时含大写、小写、数字 —— 三个条件缺一即不可能命中。
	return hasUpper(s) && hasLower(s) && hasDigit(s)
}

// ---------------------------------------------------------------------------
// 规则 1:PEM 私钥块
// ---------------------------------------------------------------------------

// pemBlockPattern 匹配 PEM 私钥块(证书不是私钥,不需要脱敏)。
//
// 形如:
//
//	-----BEGIN RSA PRIVATE KEY-----
//	MIIEowIBAAKCAQEA...
//	-----END RSA PRIVATE KEY-----
//
// 捕获组 1 = BEGIN 行,组 2 = END 行(可能不存在 —— 文本被截断)。
// 块体(base64 行)被整体替换为 ***,BEGIN/END 行保留(读者仍看得出
// "这里曾经是一把私钥")。
//
// **迭代 33(P3-1)**:第一次脱敏后,块体已经变成 "***" 且**紧跟在 BEGIN 行
// 之后**(替换串是 "${1}***${2}")。若正则不认这个形态,第二次脱敏会**再追加**
// 一段 "***" → 掩码结果本身仍是"可再掩的形态"(实测:一次 → `…-----***`,
// 两次 → `…-----******`),违反"脱敏幂等"这条已声明的不变量。
// 因此正则额外接受:
//   - BEGIN 行之后**紧跟**的 "***"(第一次脱敏留下的占位符);
//   - 单独成行的 "***"(别的工具留下的同形态占位符)。
//
// 认了它,二次脱敏就是**逐字节相同**的恒等变换。
var pemBlockPattern = regexp.MustCompile(
	`(?m)(-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----)(?:\*\*\*)?(?:\r?\n(?:\*\*\*|[A-Za-z0-9+/=]+))*(\r?\n-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----)?`)

// ---------------------------------------------------------------------------
// 规则 2:JWT
// ---------------------------------------------------------------------------

// jwtTokenPattern 匹配 JWT(HS/RS/ES 签名都是三段 base64url)。
//
// 头段以 "eyJ" 开头(base64url 编码的 '{"' ),这是 JWT 最稳定的形态特征。
var jwtTokenPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}(?:\.[A-Za-z0-9_-]{2,}){1,2}`)

// jwtValuePattern 是锚定版(判定单个值是不是 JWT 时用)。
var jwtValuePattern = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{6,}(?:\.[A-Za-z0-9_-]{2,}){1,2}$`)

// ---------------------------------------------------------------------------
// 规则 3:webhook URL
// ---------------------------------------------------------------------------

// webhookPattern 匹配 Slack 之类的 incoming webhook URL(整条 URL 都是凭据)。
var webhookPattern = regexp.MustCompile(`https?://hooks\.slack\.com/\S+`)

// ---------------------------------------------------------------------------
// 规则 4:键名 + 值形态(key=value / key: value)
// ---------------------------------------------------------------------------

// credentialAssignPattern 匹配"键名 + 分隔符 + 值"形态的片段。
//
// 键名取 ["']?[A-Za-z_][A-Za-z0-9_.\-]*["']?(允许驼峰、点号与 JSON 的
// 引号),值取引号串或一段不含空白/逗号/分号/右括号的 token —— 与日志里
// 常见的环境变量回显("ANTHROPIC_API_KEY=sk-...")、HTTP 头
// ("Authorization: Bearer x")与 .env 行形态一致。
var credentialAssignPattern = regexp.MustCompile(
	`(?i)(["']?[A-Za-z_][A-Za-z0-9_.\-]*["']?)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;)\]}]+)`)

// ---------------------------------------------------------------------------
// 规则 5:认证方案(Bearer / Basic)
// ---------------------------------------------------------------------------

// authSchemePattern 匹配认证方案 + 凭据:"Bearer <token>" / "Basic <base64>"。
//
// 方案词要求词边界(避免把 "bearerish" 当方案),token 取一段不含空白的
// 可见字符。
var authSchemePattern = regexp.MustCompile(`(?i)\b(bearer|basic)([ \t]+)([A-Za-z0-9_\-~+/=]+)`)

// authSchemeWords 是被认可的认证方案词。
var authSchemeWords = map[string]bool{"bearer": true, "basic": true}

// ---------------------------------------------------------------------------
// 规则 6:裸值(已知前缀 / 严格高熵)
// ---------------------------------------------------------------------------

// credentialTokenPattern 切出文本里的"候选值"(不含点号:点号分隔的是
// 句子成分或路径,不是凭据的一部分)。
//
// **'=' / '+' / '-' 留在字符集里是有意的**(迭代 27 P1-1 的教训):
//
//   - '=' 是 base64 的**合法字符**(PEM 块、Basic <b64> 的填充)。把它从
//     字符集里删掉会把 base64 **截断** → 长串 base64 跌破裸值语境的高熵
//     门槛(≥24)→ 整串明文泄漏(回归锁见 credential_test.go 的
//     TestBareBase64WithPaddingIsNotTruncated)。
//   - '+' / '-' 是 **diff 行前缀**("+AKIA…"),同时也是 base64 的合法字符。
//
// 真正需要切开的是"**键值粘连**"形态(AK=AKIA…):那由
// splitCredentialToken 处理 —— 按**首个** '=' 切,且**只有右侧非空才切**。
var credentialTokenPattern = regexp.MustCompile(`[A-Za-z0-9_\-~+/=]+`)

// strictCredentialPrefixes 是"前缀本身就有辨识度"的已知凭据前缀。
//
// 命中即判定为凭据形态(要求前缀之后的尾巴够长且只含凭据字符)。
var strictCredentialPrefixes = []string{
	"sk-", "sk-ant-",
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
	"xoxb-", "xoxp-", "xoxa-", "xoxr-", "xapp-",
	"ya29.", "eyj",
	"dop_v1_", "sq0atp-", "lin_api_",
}

// looseCredentialPrefixes 是"前缀可能撞上普通标识符"的凭据前缀。
//
// 这些前缀要求值里**含数字**且总长够长,否则不判定为凭据
// (例:"npm_install_something" 不是凭据)。
var looseCredentialPrefixes = []string{
	"sk_", "pk-", "rk-", "npm_", "hf_", "hvs.", "pypi-",
	"shpat_", "shpca_", "shppa_",
	"akia", "asia", "aiza", "abia",
}

// minStrictTail 是严格前缀之后至少要跟的字符数。
//
// 取 12 是为了不误伤 "sk-config" / "sk-my-app-key" 这类普通标识符。
const minStrictTail = 12

// minLooseLen 是宽松前缀要求的值的最小总长。
const minLooseLen = 16

// awsAccessKeyPattern 匹配 AWS 访问密钥 ID(AKIA/ASIA/AIDA/AROA/ANPA/ABIA
// + 12~16 位大写字母数字)。
var awsAccessKeyPattern = regexp.MustCompile(`^(?:AKIA|ASIA|AIDA|AROA|ANPA|ABIA|ACCA)[0-9A-Z]{12,}$`)

// ---------------------------------------------------------------------------
// 对外入口
// ---------------------------------------------------------------------------

// MaskCredentialValues 只按"值形态"脱敏(键名与上下文原样保留)。
//
// 与 Mask 的区别只有一条:**不看键名**。
//
//   - Mask:键名敏感就整值替换 —— 适合日志/落库(宁可多遮);
//
//   - MaskCredentialValues:只有值本身呈凭据形态才替换 —— 适合制品
//     (git diff):代码里的标识符必须原样保留,否则 diff 的审计价值被毁掉。
//
//     ANTHROPIC_API_KEY=sk-ant-xxx  → ANTHROPIC_API_KEY=***
//     const token = getToken();       → 原样保留
//
// 幂等:所有规则都以 "***" 为终态,而 "***" 本身不呈任何凭据形态 ——
// 二次脱敏逐字节不变(读出口与落库侧各过一次,结果必须一致)。
func MaskCredentialValues(text string) string {
	if text == "" {
		return text
	}
	return maskCredentialShapes(text)
}

// maskCredentialShapes 按"值形态"脱敏的唯一实现。
//
// 规则按顺序执行(先整体形态,后局部形态):
//
//  1. PEM 私钥块 → 块体替换为 ***;
//  2. JWT → 替换;
//  3. webhook URL → 替换;
//  4. 键名 + 值形态 → 只替换值(键名保留);
//  5. 认证方案(Bearer/Basic)→ 只替换方案后的 token;
//  6. 裸值(已知前缀 / 严格高熵)→ 替换。
func maskCredentialShapes(text string) string {
	if text == "" || !mayContainCredentialShape(text) {
		return text
	}
	out := pemBlockPattern.ReplaceAllString(text, "${1}***${2}")
	out = jwtTokenPattern.ReplaceAllString(out, maskPlaceholder)
	out = webhookPattern.ReplaceAllString(out, maskPlaceholder)
	out = maskAssignments(out)
	out = maskAuthSchemes(out)
	out = maskBareValues(out)
	return out
}

// maskAssignments 处理"键名 + 值"形态:键名敏感**且**值呈凭据形态时,
// 只替换值(键名与分隔符保留)。
//
// 这是"第十轮取舍"的核心:键名敏感但值是普通代码时
// (const token = getToken();)必须原样保留 —— 否则 git diff 里
// "改了哪一行"这个审计证据被毁掉。
func maskAssignments(text string) string {
	matches := credentialAssignPattern.FindAllStringSubmatchIndex(text, -1)
	if matches == nil {
		return text
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		keyStart, keyEnd := m[2], m[3]
		valStart, valEnd := m[6], m[7]
		key := strings.Trim(strings.TrimSpace(text[keyStart:keyEnd]), `"'`)
		if keySensitivity(key) == sensNone {
			continue
		}
		raw := text[valStart:valEnd]
		body, quote := splitQuotes(raw)

		// 认证方案形态:值是 "Bearer"/"Basic",真正的凭据在紧随其后的 token。
		if scheme, ok := authSchemeWord(body); ok {
			j := valEnd
			for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
			k := j
			for k < len(text) && !isValueTerminator(text[k]) {
				k++
			}
			if k > j && schemeTokenIsCredential(scheme, key, text[j:k]) {
				b.WriteString(text[last:valStart])
				b.WriteString(raw + " " + maskPlaceholder)
				last = k
			}
			continue
		}

		if !credentialShape(body, true) {
			continue
		}
		b.WriteString(text[last:valStart])
		if quote != 0 {
			b.WriteString(string(quote) + maskPlaceholder + string(quote))
		} else {
			b.WriteString(maskPlaceholder)
		}
		last = valEnd
	}
	b.WriteString(text[last:])
	return b.String()
}

// maskAuthSchemes 处理**没有键名**的认证方案形态:"Bearer <token>"。
//
// 这是第十一轮终审实测到的泄漏形态(裸 Bearer <JWT> 落库并经所有 API
// 出口外泄)。
//
// 方案词本身保留(读者要看得出这是哪种认证),只替换后面的 token。
func maskAuthSchemes(text string) string {
	return authSchemePattern.ReplaceAllStringFunc(text, func(m string) string {
		g := authSchemePattern.FindStringSubmatch(m)
		if g == nil {
			return m
		}
		scheme := strings.ToLower(g[1])
		if !schemeTokenIsCredential(scheme, "", g[3]) {
			return m
		}
		return g[1] + g[2] + maskPlaceholder
	})
}

// maskBareValues 处理**没有任何键名线索**的裸值形态。
//
// 门槛比键名语境严格得多(见文件头说明):裸值没有线索,宁可漏一个
// 短凭据,也不抹掉 git SHA / base64 夹具这类正常内容。
//
// 两条上下文护栏(实测踩过的坑):
//
//   - **路径段**:裸值夹在 "/" 之间时,它是路径的一段而不是凭据
//     (实测:Go 测试临时目录名 "TestScriptNodeHonors...1637892132"
//     被当成凭据抹掉,脚本节点的输出因此丢了工作目录);
//   - **驼峰标识符**:串呈"驼峰词序列"时,它是类名 / 测试名而不是随机凭据
//     (判据见 isCamelIdentifier —— 迭代 33 已收窄:旧口径"起伏 ≥4 次"
//     让 97% 的随机 base64 蒙混过关)。
//
// 已知前缀 / JWT / AWS 密钥这些"硬性"形态不受这两条护栏约束 ——
// 它们在路径里也仍然是凭据。
//
// **判定顺序**(迭代 27 P1-1 的修法):
//
//  1. 先按 **token 切分**后的片段判定(键值粘连 / diff 行前缀形态);
//  2. 片段都判不出时,再退回**整段**判定(宁可多遮,不许漏)。
//
// 第 1 步是必须的:此前只判整段,而"AK=AKIA…"的整段既不匹配 AWS 密钥
// (规则锚定 ^…$)、又匹配不上已知前缀(前缀不在 token 开头)、还不满足
// 高熵门槛(没有小写)→ **四路全不命中** → 凭据从六个出口全部泄漏。
func maskBareValues(text string) string {
	matches := credentialTokenPattern.FindAllStringIndex(text, -1)
	if matches == nil {
		return text
	}
	var b strings.Builder
	last := 0
	replace := func(start, end int) {
		b.WriteString(text[last:start])
		b.WriteString(maskPlaceholder)
		last = end
	}
	for _, m := range matches {
		tok := text[m[0]:m[1]]
		// 整段是否处在路径语境:片段继承它的保护(否则 "/dir/<高熵>=1"
		// 形态会比整段判定**多**遮一次,改变既有行为)。
		wholeIsPath := isPathSegment(text, m[0], m[1])
		masked := false
		for _, cand := range splitCredentialToken(tok, m[0]) {
			if !candidateIsCredential(text, cand, wholeIsPath) {
				continue
			}
			replace(cand.start, cand.end)
			masked = true
		}
		if masked {
			continue
		}
		// 片段都判不出 → 退回整段判定(既有行为,逐字节保持不变)。
		if candidateIsCredential(text, newCredentialCandidate(m[0], m[1], tok), wholeIsPath) {
			replace(m[0], m[1])
		}
	}
	b.WriteString(text[last:])
	return b.String()
}

// credentialCandidate 是一个待判定的候选片段(裸值形态判定的一个单元)。
type credentialCandidate struct {
	// start/end 是片段在原文中的位置(就地替换用)。
	start, end int
	// text 是片段原文(长度判定 / 驼峰判定用)。
	text string
	// core 是**形态判定**用的形态:去掉末尾的 base64 填充('=')。
	// AWS 密钥 ID / 已知前缀都不接受尾随的 '=',填充必须先摘掉。
	core string
}

// newCredentialCandidate 造一个候选片段。
func newCredentialCandidate(start, end int, text string) credentialCandidate {
	return credentialCandidate{
		start: start,
		end:   end,
		text:  text,
		core:  strings.TrimRight(text, "="),
	}
}

// splitCredentialToken 把一个裸值 token 切成候选片段。
//
// **迭代 27 P1-1 的修法** —— "键值粘连"形态:
//
//	AK=AKIAIOSFODNN7EXAMPLE → 键 "AK" + 值 "AKIAIOSFODNN7EXAMPLE"
//	+AKIAIOSFODNN7EXAMPLE   → 值 "AKIAIOSFODNN7EXAMPLE"(去掉 diff 行前缀)
//
// 规则(每条都有对应的回归用例):
//
//  1. **行首 diff 前缀**('+' / '-')是行标记,不是凭据字符 → 判定时摘掉
//     (但**保留在输出里**:diff 的行标记是审计证据);
//     **迭代 33(P3-2)**:前缀可以**连续**出现(diff 里嵌套的补丁行会写成
//     "++AKIA…")—— 旧口径只摘一个,剩下的 '+' 让 AWS 密钥的锚定规则
//     (^AKIA…$)不匹配 → 整串原样输出。逐个摘掉即可。
//  2. **键值分隔符**:按**首个** '=' 切开 —— 但**只有右侧非空才切**;
//  3. **base64 填充**:末尾的 '=' / '==' 是 base64 的填充,不是分隔符
//     → 先摘掉末尾的 '=' 再找分隔符(base64 因此**不被截断**);
//  4. 右侧若还含 '='(键值粘连套娃:foo=bar=AKIA…)→ 递归继续切。
func splitCredentialToken(tok string, base int) []credentialCandidate {
	// 1. 行首 diff 前缀(可连续多个)
	off := 0
	for off < len(tok) && (tok[off] == '+' || tok[off] == '-') {
		off++
	}
	if off >= len(tok) {
		// 整串都是 '+' / '-'(如 "++"):不是凭据形态,也不能产出空片段
		// (空片段会让调用方把 "***" 插进原文)。
		return nil
	}
	core := tok[off:]
	// 3. 末尾 base64 填充
	body := strings.TrimRight(core, "=")
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return []credentialCandidate{newCredentialCandidate(base+off, base+len(tok), core)}
	}
	// 2/4. 切开
	left := body[:eq]
	right := core[eq+1:]
	out := make([]credentialCandidate, 0, 2)
	if left != "" {
		out = append(out, newCredentialCandidate(base+off, base+off+len(left), left))
	}
	out = append(out, splitCredentialToken(right, base+off+eq+1)...)
	return out
}

// candidateIsCredential 判定一个候选片段是不是凭据形态。
//
// 判据与整段判定**同源**(同一条 credentialShapeHard + 高熵规则):
//
//   - **硬性形态**(PEM / AWS 密钥 ID / JWT / 已知前缀)→ 是凭据;
//   - **高熵长串** → 是凭据,但受"路径段"与"驼峰标识符"两条护栏约束。
//
// wholeIsPath:整段 token 是否处在路径语境 —— 片段继承它的保护
// (否则 "/dir/<高熵>=1" 形态会比整段判定**多**遮一次,改变既有行为)。
func candidateIsCredential(text string, cand credentialCandidate, wholeIsPath bool) bool {
	if credentialShapeHard(cand.core) {
		return true
	}
	if !highEntropy(cand.text, false) {
		return false
	}
	if wholeIsPath {
		return false
	}
	return !isPathSegment(text, cand.start, cand.end) && !isCamelIdentifier(cand.text)
}

// ---------------------------------------------------------------------------
// 形态判定
// ---------------------------------------------------------------------------

// credentialShape 判定一个值是否呈"凭据形态"。
//
// loose=true 用宽松的高熵门槛(有键名线索时),loose=false 用严格门槛
// (裸值时)。其余判据与门槛无关。
func credentialShape(v string, loose bool) bool {
	if credentialShapeHard(v) {
		return true
	}
	if looksLikePath(v) {
		return false // 路径不是凭据
	}
	return highEntropy(v, loose)
}

// credentialShapeHard 判定一个值是否呈"硬性"凭据形态(与上下文无关):
// PEM 块 / AWS 密钥 / JWT / 已知前缀。
//
// 这类形态在路径里、代码里也仍然是凭据,不受路径/驼峰护栏约束。
func credentialShapeHard(v string) bool {
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "-----BEGIN") {
		return true // PEM 私钥/证书块
	}
	if awsAccessKeyPattern.MatchString(v) {
		return true
	}
	if jwtValuePattern.MatchString(v) {
		return true
	}
	return hasKnownCredentialPrefix(v)
}

// looksLikePath 判定一个值是否更像路径(而不是凭据)。
//
// 含 "/" 或 "\" 的值通常是路径:路径里也可能有高熵串(目录名、文件名),
// 但路径本身不是凭据。
func looksLikePath(v string) bool {
	return strings.Contains(v, "/") || strings.Contains(v, `\`)
}

// isPathSegment 判定一个裸值是否处在路径语境(紧邻 "/")。
//
// **迭代 33(P1-1)补**:整段**就是**一个路径时(以 "/" 开头、含多个 "/"),
// 前后都没有 "/" 可供邻接判断 —— 这类 token 必须靠"路径名形态"识别。
//
// 判据:token 含 "/" 且有**一个 ≥12 字符的段**,该段**只由小写字母与数字**
// 组成(临时目录名 "1009hrfj2tz2f0cw7vhbzppm0000gn"、node_modules 之类)。
// 随机 base64 的段是大小写混排的 —— 12 个字符全落在 {a-z,0-9} 的概率
// ≈ (36/64)^12 ≈ 6e-5,实测 50000 个随机样本里只多命中 3 个(0.006%)。
func isPathSegment(text string, start, end int) bool {
	if start > 0 && text[start-1] == '/' {
		return true
	}
	if end < len(text) && text[end] == '/' {
		return true
	}
	if start >= end {
		return false
	}
	return isPathShaped(text[start:end])
}

// isPathShaped 判定一个 token 是不是"路径"(含 "/" 且含路径名形态的段)。
func isPathShaped(tok string) bool {
	if !strings.Contains(tok, "/") {
		return false
	}
	for _, seg := range strings.Split(tok, "/") {
		if len(seg) < minPathNameLen {
			continue
		}
		ok := true
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// minPathNameLen 是"路径名段"的最短长度(见 isPathSegment 的说明)。
const minPathNameLen = 12

// ---------------------------------------------------------------------------
// 驼峰护栏(迭代 33 收窄)
// ---------------------------------------------------------------------------

// 驼峰护栏的判据常量。
const (
	// minCamelHumps 是"词首大写"起伏的最少次数(与旧口径一致)。
	minCamelHumps = 4
	// minCamelWordLen 是"驼峰词"的最短长度(旧口径没有这条)。
	minCamelWordLen = 2
	// maxCamelDigitGroups 是"数字段"的最多处数(旧口径没有这条)。
	maxCamelDigitGroups = 1
)

// isCamelIdentifier 判定一个高熵长串更像"驼峰标识符"(类名 / 测试名)
// 而不是随机凭据。
//
// ---- 它护着什么(不能丢) ----
//
//	git diff 里的代码标识符必须原样保留 —— 抹掉它就毁掉了"改了哪一行"
//
// 这个审计证据。仓库里真实存在的例子:
//
//	TestScriptNodeHonorsTimeoutAndWorkingDir1637892132
//	TestBasicAuthBase64IsMasked
//	getUserAccessTokenFromSha256
//
// ---- 迭代 33(P1-1)为什么必须收窄 ----
//
// 旧口径只有一条:"大写字母后紧跟小写字母"连续出现 ≥4 次即豁免。
// 注释里写的是"随机凭据的字母与数字交错,这种起伏很少出现" —— **与实测相反**:
//
//	随机字符里"大写后紧跟小写"的概率 ≈ 26/64 × 26/64 ≈ 16.5%;
//	32~64 字符的随机串几乎必然攒够 4 次。
//
// 实测(迭代 33):200 个随机 base64 样本里 **194 个**满足旧口径 →
// 驼峰护栏事实上等于"高熵串豁免",随机凭据 97% 原样输出(裸值形态的
// 凭据从 node output / state_json / 事件流 / SSE / git diff 六个出口全泄漏)。
//
// ---- 新的判据(五条同时成立才豁免) ----
//
//  1. **起伏 ≥4**:仍是"词首大写"的必要条件(类名/测试名都满足);
//  2. **驼峰词序列**:串能切成若干"词",每词 = 大写段 + 小写段 + 数字段;
//     大写段可为空,但**只允许出现在首词**(之后每个词必须以大写段开头)
//     —— `getUserAccessToken` / `TestJSONBody` 都满足;
//  3. **每个词 ≥2 字符**:随机 base64 切出来的词大量是 1 个字符;
//  4. **每个词都含小写字母**:纯大写词(没有小写)是 base64 的随机大写段,
//     真实标识符里只有缩写会这样,而缩写后面必然跟小写词
//     (JSONBody → "JSONBody" 是一个词);
//  5. **数字段 ≤1 处**:标识符里的数字是版本号/时间戳这类**单一后缀**
//     (…Dir1637892132 / …Returns413 / Sha256);随机 base64 的数字**散落**
//     在串的各处。
//
// ---- 实测(2026-10,迭代 33) ----
//
//	随机 base64 20000 样本:仍被豁免 2 个(0.01%);
//	仓库里真实的长标识符(20 个,含数字+大小写混合+≥24 字符):0 个被误伤;
//	仓库里 3 个 JWT 片段(必须遮):0 个被豁免。
//
// 残余边界(有意取舍,登记在案):仍有约 0.01%~0.08% 的随机 base64
// (构造得**恰好**像驼峰词序列)会被豁免 —— 纯语法判据无法做到 100%。
// 这类串若落在**键名语境**(key=value)仍会被规则 4 遮住(键名规则不看
// 驼峰护栏);只有"裸值 + 恰好像标识符"这一个角落会漏。
func isCamelIdentifier(v string) bool {
	if camelHumps(v) < minCamelHumps {
		return false
	}
	words, ok := splitCamelWords(v)
	if !ok {
		return false
	}
	for _, w := range words {
		if len(w.text) < minCamelWordLen || w.lower == 0 {
			return false
		}
	}
	return camelDigitGroups(v) <= maxCamelDigitGroups
}

// camelWord 是"驼峰词"的构成统计。
type camelWord struct {
	// text 是这个词的原文。
	text string
	// upper/lower/digit 是三段的字符数。
	upper int
	lower int
	digit int
}

// splitCamelWords 把串切成"驼峰词序列"。
//
// 词的形式:**大写段 + 小写段 + 数字段**(三段都可为空,但不能全空)。
// 大写段为空(词以小写开头)**只允许出现在首词** —— 之后的词必须以
// 大写段开头,否则它就不是"驼峰"结构。
//
// 返回 ok=false 表示串**不是**驼峰词序列(例如中间出现 '/' 这样的
// 分隔符、或大写段后跟数字再跟小写 —— 那种形态随机 base64 里到处都是)。
func splitCamelWords(v string) ([]camelWord, bool) {
	var out []camelWord
	i, n := 0, len(v)
	first := true
	for i < n {
		start := i
		for i < n && v[i] >= 'A' && v[i] <= 'Z' {
			i++
		}
		w := camelWord{upper: i - start}
		lowerStart := i
		for i < n && v[i] >= 'a' && v[i] <= 'z' {
			i++
		}
		w.lower = i - lowerStart
		digitStart := i
		for i < n && v[i] >= '0' && v[i] <= '9' {
			i++
		}
		w.digit = i - digitStart
		if w.upper == 0 && !(first && w.lower > 0) {
			return nil, false
		}
		first = false
		w.text = v[start:i]
		out = append(out, w)
	}
	return out, true
}

// camelHumps 统计"大写字母后紧跟小写字母"的次数(驼峰起伏)。
//
// 与旧口径逐字节一致 —— 收窄的是**豁免条件**,不是起伏本身的算法。
func camelHumps(v string) int {
	n := 0
	for i := 1; i < len(v); i++ {
		if v[i-1] >= 'A' && v[i-1] <= 'Z' && v[i] >= 'a' && v[i] <= 'z' {
			n++
		}
	}
	return n
}

// camelDigitGroups 统计"连续数字段"的处数。
//
// 标识符里的数字通常只有一处(版本号 / 时间戳 / 哈希长度);随机 base64
// 的数字散落各处 —— 这是收窄驼峰护栏最有效的一条。
func camelDigitGroups(v string) int {
	n := 0
	inDigits := false
	for i := 0; i < len(v); i++ {
		isDigit := v[i] >= '0' && v[i] <= '9'
		if isDigit && !inDigits {
			n++
		}
		inDigits = isDigit
	}
	return n
}

// hasKnownCredentialPrefix 判定值是否带已知的凭据前缀。
func hasKnownCredentialPrefix(v string) bool {
	lower := strings.ToLower(v)
	for _, p := range strictCredentialPrefixes {
		if !strings.HasPrefix(lower, p) {
			continue
		}
		tail := v[len(p):]
		if len(tail) >= minStrictTail && credentialTailChars(tail) {
			return true
		}
	}
	for _, p := range looseCredentialPrefixes {
		if !strings.HasPrefix(lower, p) {
			continue
		}
		if len(v) >= minLooseLen && hasDigit(v) {
			return true
		}
	}
	return false
}

// credentialTailChars 判定前缀之后的尾巴是否只由凭据字符组成
// (字母/数字/下划线/连字符)。
//
// 点号与斜杠被排除:"sk-config.yaml" / "sk-my/path" 是文件名或路径,
// 不是凭据。
func credentialTailChars(tail string) bool {
	for i := 0; i < len(tail); i++ {
		c := tail[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

// highEntropy 判定一个值是不是"高熵长串"。
//
// 判据:够长 + 同时含字母与数字 + 大小写混合(或超长的纯字母数字)。
//
// loose=true(键名语境):≥16 字符,且(大小写混合 或 ≥32 字符)。
// loose=false(裸值语境):≥24 字符 + 大小写混合 + 含数字 ——
// 更严的门槛是为了不误伤 git SHA 与 base64 夹具。
//
// 刻意**不**判定的形态(它们在代码/配置里极常见,误伤会毁掉 diff):
//
//	token = getToken()        ← 函数调用
//	const apiKey = cfg.apiKey ← 成员引用
//	password = "hunter2"      ← 短字符串(长度不足高熵门槛)
func highEntropy(v string, loose bool) bool {
	if len(v) < 16 || !hasLetter(v) || !hasDigit(v) {
		return false
	}
	if loose {
		return (hasUpper(v) && hasLower(v)) || len(v) >= 32
	}
	return len(v) >= 24 && hasUpper(v) && hasLower(v)
}

// ---------------------------------------------------------------------------
// 认证方案
// ---------------------------------------------------------------------------

// authSchemeWord 判断一个值是不是认证方案单词(Bearer/Basic)。
func authSchemeWord(raw string) (string, bool) {
	w := strings.ToLower(strings.TrimSpace(raw))
	if authSchemeWords[w] {
		return w, true
	}
	return "", false
}

// schemeTokenIsCredential 判定认证方案后面的 token 是不是凭据。
//
// key 非空表示这个形态来自"键名 + 值"语境(如 Authorization: Bearer x);
// key 为空表示裸形态("... Bearer x")。
//
// 判定:
//   - Authorization 头形态:一律是凭据(方案词后面跟的就是凭据,不看形状);
//   - Bearer:token 本身呈凭据形态;
//   - Basic:token 是 base64 形态的 user:pass。
func schemeTokenIsCredential(scheme, key, tok string) bool {
	if key != "" && isAuthorizationKey(key) {
		return true
	}
	switch scheme {
	case "bearer":
		return credentialShape(tok, true)
	case "basic":
		return basicAuthToken(tok)
	}
	return false
}

// basicAuthToken 判定一个 token 是不是 Basic 认证的 base64 凭据。
//
// 刻意要求较强的信号(base64 里含数字 / 有 '=' 填充 / 大小写混合且够长):
// "Basic authentication" 这类英文短语不能被当成凭据。
//
// **第四档(user:pass 形态,迭代 27 补)**:短一档的 base64
// ("dXNlcjpwYXNz" = 12 字符,无填充、无数字)上面三条都判不出,但它**确实**
// 是 Basic 凭据。判据必须**解出 user:pass 的结构**:base64 解码后含 ':'
// → 才是凭据。否则英文短语("Basic Tokenization"、"Basic application")
// 会被误伤 —— 它们也是合法 base64 字符集,只是解不出 ':'。
func basicAuthToken(tok string) bool {
	if len(tok) < 8 {
		return false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '+' || c == '/' || c == '=' || c == '_' || c == '-':
		default:
			return false
		}
	}
	if strings.HasSuffix(tok, "=") {
		return true // 有 base64 填充
	}
	if hasDigit(tok) {
		return len(tok) >= 12
	}
	if hasUpper(tok) && hasLower(tok) && len(tok) >= 16 {
		return true
	}
	return looksLikeUserPassBase64(tok)
}

// looksLikeUserPassBase64 判定一个 token 是不是 base64 编码的 user:pass。
//
// 判据:能解出字节(base64 / base64url,带填充与无填充都认),且解出来的
// 字节里含 ':' —— 那才是 Basic 凭据的结构特征。
func looksLikeUserPassBase64(tok string) bool {
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		raw, err := enc.DecodeString(tok)
		if err != nil {
			continue
		}
		if bytes.ContainsRune(raw, ':') {
			return true
		}
	}
	return false
}

// isAuthorizationKey 判定键名是不是 Authorization 头(这类键后面的值
// 一律是凭据,不看值的形状)。
func isAuthorizationKey(key string) bool {
	return strings.Contains(strings.ToLower(key), "authorization")
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// splitQuotes 拆出值的引号与引号内的内容。
func splitQuotes(raw string) (body string, quote byte) {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return raw[1 : len(raw)-1], raw[0]
	}
	return raw, 0
}

// isValueTerminator 判断字符是否是值的终止符。
func isValueTerminator(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ';', ')', ']', '}', '"', '\'':
		return true
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
