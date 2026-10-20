package agent

import (
	"sort"
	"strings"

	"agentworkflow/logx"
)

// AgentConfig 是每个 Agent 的运行配置:模型、端点、超时与调用级环境变量。
//
// 隔离原则:这些配置只影响本应用对该 Agent 的调用方式
// (CLI 参数 + 子进程环境变量),绝不读写用户的本地配置文件
// (~/.claude、~/.pi 等),也不写入工作流 DSL。
type AgentConfig struct {
	// Model 默认模型(如 claude-sonnet-4-5),节点可通过 config.model 覆盖。
	Model string `json:"model"`
	// BaseURL API 端点(可选)。claude-code 映射为 ANTHROPIC_BASE_URL,
	// pi-agent 映射为 PI_BASE_URL;留空使用各自的默认端点。
	BaseURL string `json:"base_url"`
	// TimeoutSeconds 默认超时;0 表示使用适配器内置默认值。
	TimeoutSeconds int `json:"timeout_seconds"`
	// ExtraArgs 追加 CLI 参数。
	ExtraArgs []string `json:"extra_args"`
	// Env 调用级环境变量覆盖(如 ANTHROPIC_AUTH_TOKEN)。值属于敏感信息,
	// 通过 API 返回时会被掩码;持久化于本地 SQLite,不进入日志与工作流。
	Env map[string]string `json:"env"`
	// DefaultWorkingDir 是**死设置**:运行期从不读取它。
	//
	// claude.Config / pi.Config 都没有该字段,应用层也不传 —— 节点级
	// config.working_dir 为空时回退到全局 Git 工作目录
	// (workflow/runtime/executor.go 的回退链)。
	//
	// 保留字段只为兼容存量配置与 API 契约(删掉会让存量 Agent 配置
	// 解析失败);新值一律被拒绝(app/application:AgentService.UpdateConfig),
	// 避免"存下了、提示已保存、实际没生效"(约定 14)。
	DefaultWorkingDir string `json:"default_working_dir"`
	// Behavior 仅用于 Mock Agent 的演示脚本:
	// {"review": {"decisions": ["REJECTED","APPROVED"], "summary_template": "..."}}
	//
	// 注意:它是**自由结构**(map[string]any,没有固定 schema),前端给的是
	// 一个自由 JSON 输入框(frontend/src/pages/Agents.tsx),用户可以往里
	// 放任何东西 —— 包括 api_key / token 这类凭据。所以它必须纳入掩码
	// 面(见 Masked)。
	Behavior map[string]any `json:"behavior,omitempty"`
}

// Masked 返回掩码后的配置副本(GET 一律 ***)。
//
// Env 的值整体替换为 ***;ExtraArgs 里**凭据形态**的参数值同样要替换 ——
// 配置项本身就允许放 "--api-key sk-xxx",此前只有 Env 被掩码,
// 这条路径让凭据原样出现在 API 响应里(约定 8)。
//
// **Behavior 也要掩**:它是自由结构(map[string]any,无固定 schema),
// 前端是一个自由 JSON 输入框,用户可以往里放任何键值 —— 包括凭据。
// 此前 Behavior 原样回显,凭据从 GET 接口明文出去(约定 8 的字面缺口)。
//
// 掩码口径与事件流 / 日志**同源**(logx.MaskValue):
//   - 键名敏感(api_key / token / password …)→ 整值替换为 ***;
//   - 值本身呈凭据形态(如 "ANTHROPIC_API_KEY=sk-xxx")→ 就地替换,
//     上下文保持可读。
//
// 因此 Mock 的演示脚本(decisions / summary_template / fail_with)
// 不受影响 —— 那几个键既不敏感,值也不是凭据形态。
func (c AgentConfig) Masked() AgentConfig {
	out := c
	if c.Env != nil {
		out.Env = make(map[string]string, len(c.Env))
		for k := range c.Env {
			out.Env[k] = MaskedValue
		}
	}
	out.ExtraArgs = MaskExtraArgs(c.ExtraArgs)
	if c.Behavior != nil {
		out.Behavior = maskBehavior(c.Behavior)
	}
	return out
}

// maskBehavior 对 Behavior 做结构化脱敏(与事件流 / 日志同一份规则)。
func maskBehavior(b map[string]any) map[string]any {
	masked, ok := logx.MaskValue(b).(map[string]any)
	if !ok {
		return b
	}
	return masked
}

// MergeMaskedBehavior 把提交值里的掩码哨兵 "***" 换回**同路径**的已存值。
//
// 语义与 Env / ExtraArgs 同源(约定 5):"***" = 保留原值。
//
// 为什么需要:前端把整份配置原样回传(Agents.tsx:打开配置拿到掩码 →
// 只改模型 → 保存),不合并的话 GET 返回的 "***" 会被原样写回库里 ——
// 凭据被哨兵覆盖,用户下一次调用就没凭据了,而界面显示"已保存"。
//
// **按同路径**取已存值(而不是整体替换,也不是按 mode 混用):behavior 是
// 嵌套的 map[mode]map[field]value,同名字段在不同 mode 下互相独立。
//
// 没有已存值时的处理与 Env 不同:Env 是"丢弃"(该键值对整体消失),
// 这里**保留哨兵原样** —— behavior 是用户的自由 JSON,悄悄删掉一个键
// 会改动用户的脚本;保留 "***" 至少是可见的、可再次编辑的状态。
func MergeMaskedBehavior(submitted, stored map[string]any) map[string]any {
	if submitted == nil {
		return nil
	}
	out := make(map[string]any, len(submitted))
	for k, v := range submitted {
		sv, has := stored[k]
		out[k] = mergeMaskedValue(v, sv, has)
	}
	return out
}

// mergeMaskedValue 递归地把哨兵换回同路径的已存值。
//
// hasStored 区分"没有这个键"与"这个键的值是 nil" —— 前者要保留哨兵
// (没有可沿用的原值),后者沿用 nil。
func mergeMaskedValue(submitted, stored any, hasStored bool) any {
	switch sv := submitted.(type) {
	case string:
		if sv == MaskedValue {
			if !hasStored {
				return sv // 没有已存值 → 保留哨兵(不静默删掉用户的键)
			}
			return stored
		}
		return sv
	case map[string]any:
		sm, _ := stored.(map[string]any)
		out := make(map[string]any, len(sv))
		for k, v := range sv {
			one, has := sm[k]
			out[k] = mergeMaskedValue(v, one, has)
		}
		return out
	case []any:
		sa, _ := stored.([]any)
		out := make([]any, len(sv))
		for i, v := range sv {
			var one any
			has := false
			if i < len(sa) {
				one = sa[i]
				has = true
			}
			out[i] = mergeMaskedValue(v, one, has)
		}
		return out
	}
	return submitted
}

// MaskExtraArgs 掩码 CLI 参数里的凭据值。
//
// 两类判定,缺一不可:
//
//  1. **按参数名**:--api-key / --token=xxx 这类名字本身指向凭据的选项,
//     它的值一律替换为 ***(名字就是线索,与值长什么样无关)。
//  2. **按值的形态**:名字不敏感、但值本身呈凭据形态的,同样替换为 ***。
//     例:--header "Authorization: Bearer ghp_xxx"、--foo sk-ant-xxx。
//
// 为什么第 2 条必须走 logx(约定 13 的同源精神):"值呈凭据形态"这条规则
// 全仓只有一份(logx/credential.go 是唯一定义处),另写一份必然漂移。
// 此前 extra_args 只实现了"按名"那一半 —— 同一个请求里 --api-key 与 env
// 都是 ***,而 --header 的凭据**明文**出了 GET 响应(#255 同类:behavior
// 只关了"按名"那一半)。
//
// 判定对象是**整个参数值**:值里只要含凭据形态,整个值替换为 ***,
// 而不是就地替换其中的子串。理由:
//
//   - **哨兵语义要唯一**:全仓的 "***" 只表示一件事 —— "这个值被掩了,
//     回写时沿用已存值"(约定 5)。就地替换会让同一个参数里出现半掩半明
//     的值("Authorization: Bearer ***"),回写时无法判断"这一段是不是
//     掩码",合并逻辑就得另立一套部分还原规则(而"用户自己写的 ***"
//     与"掩码产生的 ***"将无法区分)。
//   - **就地替换会改写用户的原文**:--description "rotate ghp_xxx soon"
//     会被存成 "rotate *** soon"(回写匹配不上时),用户的配置被静默改坏。
//
// 误伤边界:普通参数值(--description "some text"、--model claude-sonnet-4-5)
// 不呈凭据形态 → 原样保留。判定用的是 logx 的门槛:裸值要 ≥24 字符且
// 大小写混合 + 含数字;已知前缀 / PEM / JWT / webhook 形态不看门槛;
// 键名语境(Authorization: …)按认证方案判定。
//
// 幂等:"***" 本身不呈任何凭据形态 → 二次掩码逐字节不变。
//
// 解析规则由 scanExtraArgs 提供(掩码、合并、剔除三处共用同一份),
// 避免"掩码认得、合并不认"的分裂(约定 13 的同源精神)。
func MaskExtraArgs(args []string) []string {
	if args == nil {
		return nil
	}
	out := make([]string, len(args))
	copy(out, args)
	slots := scanExtraArgs(args)
	for i, s := range slots {
		switch s.kind {
		case argOptValue:
			// 选项的值:按名(名字指向凭据)或按形态(值本身是凭据)都要掩。
			if valueWouldBeMasked(s, args[i]) {
				out[i] = MaskedValue
			}
		case argOptInline:
			raw := args[i]
			if valueWouldBeMasked(s, raw[s.valStart:s.valEnd]) {
				out[i] = raw[:s.valStart] + MaskedValue + raw[s.valEnd:]
			}
		case argPlain:
			// 独立元素(不是任何选项的值):它本身可能就是凭据
			// (例:extra_args 里直接放了一个 token)。
			if valueWouldBeMasked(s, args[i]) {
				out[i] = MaskedValue
			}
		}
	}
	return out
}

// valueWouldBeMasked 判定一个值位**会不会被掩码**。
//
// 这是掩码与合并共用的**唯一口径**(约定 13 的同源精神):
// 掩码按它决定哪些值变成 "***";合并按它决定哪些已存值进入回填池。
// 两边口径一旦不一致就会出现"掩码认得、合并不认"的分裂 ——
// 掩过的值回传时找不到原值 → 凭据被丢弃(Agent 每次调用都不带鉴权参数,
// 而用户只看到"已保存")。
//
//   - 选项名指向凭据(--api-key)→ 值一律算"会被掩码"(名字就是线索);
//   - 否则看**值的形态**(走 logx 的唯一规则)→ 呈凭据形态才算。
//
// raw 是值位的**原文**(可能带引号):引号是参数的语法外壳,判定前先剥掉 ——
// 否则同一个值会因为加没加引号而得出两种结论。
func valueWouldBeMasked(slot argSlot, raw string) bool {
	if slot.cred {
		return true
	}
	return isCredentialShaped(unquote(strings.TrimSpace(raw)))
}

// isCredentialShaped 判定一个值是否呈凭据形态。
//
// 判定完全交给 logx(全仓唯一的值形态规则,logx/credential.go):
// logx.MaskCredentialValues 是"就地替换"型的 —— 返回值与入参不同,就
// 说明值里有凭据形态。这里只取它的**判断**,不取它的替换结果(掩码时
// 我们替换整个值,而不是就地替换子串,理由见 MaskExtraArgs)。
func isCredentialShaped(v string) bool {
	if v == "" {
		return false
	}
	return logx.MaskCredentialValues(v) != v
}

// MergeExtraArgs 把前端回传的 ExtraArgs 与**已存** ExtraArgs 合并。
//
// 语义与 Env 同源(约定 5:掩码语义):
//
//   - 值恰为 MaskedValue("***")→ 沿用**已存同名选项**的值;
//   - 已存没有同名选项 → 丢弃整个选项(名与值都不留,与 Env 的
//     "无原值的 *** 丢弃"一致);
//   - 其它值 → 原样采用(用户新填的值)。
//
// 为什么按**选项名**对齐而不是按下标:ExtraArgs 是位置相关的 []string,
// 用户增删参数后下标必然错位 —— 按下标合并会把 A 选项的凭据写到
// B 选项的位置(比"明文覆盖"的原缺陷更糟)。按名字对齐时,一个凭据
// 值只可能在**同名**选项之间流动,--api-key 的值永远不会被写成
// --token 的值。
//
// **按名合并与掩码规则必须自洽**(迭代 19 / P1-2):掩码现在也认"值形态"
// (名字不敏感但值是凭据),于是"名字不敏感的选项"也可能出现 ***。
// 若合并只认"凭据名",这类选项的 *** 就认不出来 →
//
//   - 当成用户新填的值入库:库里存进字面量 *** → 下发给 CLI 的参数是
//     "--header ***" → 鉴权失败,而用户只看到"已保存";
//   - 或者直接丢弃:库里真实的凭据被抹掉 → Agent 每次调用都不带凭据。
//
// 所以合并按名对齐的取值范围与掩码口径**完全一致**:已存值只有在
// "会被掩码"时(名字指向凭据,或值呈凭据形态)才进入回填池。
//
// 结果里不会残留字面量 "***":掩码哨兵不是真值,下发到 CLI 只会导致
// 鉴权失败。
func MergeExtraArgs(submitted, stored []string) []string {
	if submitted == nil {
		return nil
	}
	byName := storedOptionValues(stored)
	standalones := storedStandaloneValues(stored)
	used := map[string]int{}
	usedStandalone := 0
	slots := scanExtraArgs(submitted)
	out := make([]string, 0, len(submitted))
	for i := 0; i < len(submitted); i++ {
		s := slots[i]
		switch s.kind {
		case argOptName:
			// 值在下一个元素:由扫描结果判定,不靠"下一个就是我的值"的
			// 猜测(--verbose 这种布尔开关后面跟的是另一个选项名)。
			if i+1 < len(submitted) && slots[i+1].kind == argOptValue {
				val := submitted[i+1]
				if !isMaskedValue(val) {
					out = append(out, submitted[i], val)
					i++
					continue
				}
				// "***" = 沿用已存同名选项的值;没有就丢弃整个选项
				if v, ok := takeStoredValue(byName, used, s.name); ok {
					out = append(out, submitted[i], v)
				}
				i++ // 跳过值元素
				continue
			}
			out = append(out, submitted[i]) // 选项没有值:原样保留
		case argOptValue:
			// 孤立的值元素(前面没有选项名):原样保留
			out = append(out, submitted[i])
		case argOptInline:
			raw := submitted[i]
			val := raw[s.valStart:s.valEnd]
			if !isMaskedValue(val) {
				out = append(out, raw)
				continue
			}
			if v, ok := takeStoredValue(byName, used, s.name); ok {
				out = append(out, raw[:s.valStart]+v+raw[s.valEnd:])
				continue
			}
			// 无原值:丢弃整个选项
		case argPlain:
			// 独立元素(不是任何选项的值):被掩码时按**被掩码的独立元素的
			// 次序**对齐已存值(按下标对齐会在用户增删参数后错位)。
			if isMaskedValue(submitted[i]) {
				if v, ok := takeStandaloneValue(standalones, &usedStandalone); ok {
					out = append(out, v)
				}
				continue
			}
			out = append(out, submitted[i])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SanitizeExtraArgs 剔除凭据位上的字面量 MaskedValue。
//
// 纵深防御:库里若已经存着 "--api-key ***"(旧版本 GET→PUT 往返的产物),
// 直接下发会让每次 CLI 调用都带 --api-key *** → 鉴权失败,而用户
// 只看到"已保存"。剔除整个选项(名与值)而不是只留一个光秃秃的选项名
// —— 悬空的 "--api-key" 同样会让 CLI 报错。
//
// 覆盖所有**会被掩码**的值位(不只凭据名):按形态被掩的值位同样是哨兵,
// 下发给 CLI 一样是废值。
func SanitizeExtraArgs(args []string) []string {
	if args == nil {
		return nil
	}
	slots := scanExtraArgs(args)
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch slots[i].kind {
		case argOptName:
			if i+1 < len(args) && slots[i+1].kind == argOptValue {
				if isMaskedValue(args[i+1]) {
					i++ // 丢弃名与值
					continue
				}
				out = append(out, args[i], args[i+1])
				i++
				continue
			}
			out = append(out, args[i])
		case argOptValue:
			out = append(out, args[i])
		case argOptInline:
			if isMaskedValue(args[i][slots[i].valStart:slots[i].valEnd]) {
				continue // 丢弃整个选项
			}
			out = append(out, args[i])
		case argPlain:
			if isMaskedValue(args[i]) {
				continue // 独立元素里的哨兵:同样不能下发
			}
			out = append(out, args[i])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// storedOptionValues 收集已存 ExtraArgs 里**会被掩码**的选项值:键是
// 归一化后的选项名,值按出现顺序排列(同名选项多次出现时依次取用)。
//
// 收集范围与掩码口径一致(迭代 19 / P1-2):
//
//   - 名字指向凭据的选项(--api-key):值一律会被掩码 → 一律收集;
//   - 名字不敏感的选项(--header):只有值**本身呈凭据形态**时才会被掩码
//     → 只有这种值才收集(否则会把普通值也拉进回填池,用户新填的
//     "--header ***" 会被回填成一个毫不相干的旧值)。
//
// 已存值本身就是 "***" 的条目视为"没有真值"(旧版本往返 bug 的产物),
// 不参与合并 —— 否则会把掩码哨兵当成凭据继续下发。
//
// 存的是**原文**(含引号):往返必须逐字节还原,否则 "--api-key \"sk-x\""
// 回来变成 "--api-key sk-x" —— 引号是参数值的一部分(参数不经 shell),
// 去掉就换了值。
func storedOptionValues(stored []string) map[string][]string {
	out := map[string][]string{}
	if stored == nil {
		return out
	}
	slots := scanExtraArgs(stored)
	for i, s := range slots {
		switch s.kind {
		case argOptValue:
			if isMaskedValue(stored[i]) {
				continue
			}
			if !valueWouldBeMasked(s, stored[i]) {
				continue // 这个值不会被掩码 → 不可能被回填
			}
			out[s.name] = append(out[s.name], stored[i])
		case argOptInline:
			raw := stored[i][s.valStart:s.valEnd]
			if isMaskedValue(raw) {
				continue
			}
			if !valueWouldBeMasked(s, raw) {
				continue
			}
			out[s.name] = append(out[s.name], raw)
		}
	}
	return out
}

// storedStandaloneValues 收集已存 ExtraArgs 里**会被掩码的独立元素**
// (不是任何选项的值,例:extra_args 里直接放了一个 token)。
//
// 只收"会被掩码"的:独立元素没有名字可对齐,只能按次序对齐,混进
// 普通元素会让回填串位。
func storedStandaloneValues(stored []string) []string {
	var out []string
	if stored == nil {
		return out
	}
	slots := scanExtraArgs(stored)
	for i, s := range slots {
		if s.kind != argPlain {
			continue
		}
		if isMaskedValue(stored[i]) || !valueWouldBeMasked(s, stored[i]) {
			continue
		}
		out = append(out, stored[i])
	}
	return out
}

// takeStoredValue 按名称取出下一个未使用的已存值。
func takeStoredValue(byName map[string][]string, used map[string]int, name string) (string, bool) {
	vals := byName[name]
	idx := used[name]
	if idx >= len(vals) {
		return "", false
	}
	used[name] = idx + 1
	return vals[idx], true
}

// takeStandaloneValue 按次序取出下一个未使用的已存独立元素。
func takeStandaloneValue(vals []string, used *int) (string, bool) {
	if *used >= len(vals) {
		return "", false
	}
	v := vals[*used]
	*used++
	return v, true
}

// isMaskedValue 判断一个值是否就是掩码哨兵(允许带引号)。
func isMaskedValue(v string) bool {
	return unquote(strings.TrimSpace(v)) == MaskedValue
}

// unquote 去掉首尾的一对引号(值在参数里常被引起来)。
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// argKind 描述 ExtraArgs 里一个元素的角色。
type argKind int

const (
	argPlain     argKind = iota // 独立元素(不是选项名,也不是任何选项的值)
	argOptName                  // 选项名(值在下一个元素)
	argOptValue                 // 选项的值(上一个元素是选项名)
	argOptInline                // 选项名与值在同一元素内(--api-key=xxx)
)

// argSlot 是单个元素的解析结果。
type argSlot struct {
	kind argKind
	// name 是选项的**归一化键**(小写、去前导 -、去掉 - 与 _),
	// 合并时按它对齐:--api-key / --api_key / --API-Key → "apikey"。
	name string
	// cred 表示该选项名本身指向凭据(--api-key / --token / --auth):
	// 它的值一律按"会被掩码"处理(名字就是线索,与值长什么样无关)。
	cred bool
	// valStart/valEnd 是值在该元素内的字节区间(仅 argOptInline)。
	valStart, valEnd int
}

// scanExtraArgs 解析 ExtraArgs:标出每个元素的角色。
//
// 掩码(MaskExtraArgs)、合并(MergeExtraArgs)、剔除(SanitizeExtraArgs)
// 三处共用这一份解析 —— 三份实现各自演化就会出现"掩码认得、合并不认"
// 的分裂(约定 13 的同源精神)。
//
// 解析规则:
//
//   - "--name=value" → 一个元素,名与值同体(argOptInline);
//   - "--name" 后面跟着一个**不像选项名**的元素 → 名与值分成两个元素
//     (argOptName + argOptValue);
//   - "--name" 后面是另一个选项名(或没有下一个元素) → 它没有值
//     (argOptName 单独一个元素)。
//
// 最后一条是关键:"--verbose --api-key sk-x" 里 --verbose 是**布尔开关**
// (不带值),--api-key 才是带值的选项。早期实现把凭据选项名之后的元素
// 无条件当成"凭据值",于是 "--api-key --verbose" 会把 "--verbose" 当成
// 凭据值掩掉 —— 参数被改坏(而不是被掩码)。
//
// 认得**所有**选项(不只凭据选项)是因为掩码现在也看"值的形态":
// "--header Authorization: Bearer ghp_xxx" 的名字不指向凭据,但值是凭据 ——
// 名字不认识它,值位就无从判定(迭代 19 / P1-2)。
func scanExtraArgs(args []string) []argSlot {
	slots := make([]argSlot, len(args))
	for i := range slots {
		slots[i] = argSlot{kind: argPlain}
	}
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		if !looksLikeOptionName(name) {
			continue
		}
		key := normalizeOptKey(name)
		cred := isCredentialArgName(name)
		if hasValue {
			slots[i] = argSlot{
				kind:     argOptInline,
				name:     key,
				cred:     cred,
				valStart: len(name) + 1,
				valEnd:   len(args[i]),
			}
			continue
		}
		slots[i] = argSlot{kind: argOptName, name: key, cred: cred}
		// 值在下一个元素:前提是下一个元素**不像选项名**(像的话它是
		// 另一个选项,--verbose 这类布尔开关就不带值)。
		if i+1 < len(args) && !looksLikeOption(args[i+1]) {
			slots[i+1] = argSlot{kind: argOptValue, name: key, cred: cred}
			i++
		}
	}
	return slots
}

// looksLikeOption 判断一个元素是不是选项(含 "--name=value" 写法)。
func looksLikeOption(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	return looksLikeOptionName(name)
}

// looksLikeOptionName 判断一个元素是不是"选项名"(--verbose / -v / --api-key)。
func looksLikeOptionName(name string) bool {
	if len(name) < 2 || name[0] != '-' {
		return false
	}
	if name == "--" {
		return false // "--" 是选项终止符,不是选项名
	}
	if isNegativeNumber(name) {
		return false // "-5" 是负数(值),不是选项名
	}
	return true
}

// isNegativeNumber 判断一个元素是不是负数(-5 / -1.5 / -1e3)。
//
// 负数常作为选项的值("--offset -5"):把它当选项名会让前一个选项
// 变成"没有值",值的归属就错了。
func isNegativeNumber(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	digits := false
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-':
		default:
			return false
		}
	}
	return digits
}

// normalizeOptKey 归一化选项名(合并时按名字对齐的唯一依据)。
func normalizeOptKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimLeft(name, "-")) {
		if r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isCredentialArgName 判断 CLI 参数名是否指向凭据。
func isCredentialArgName(name string) bool {
	n := strings.ToLower(strings.TrimLeft(name, "-"))
	if n == "" {
		return false
	}
	return IsSensitiveEnvName(n)
}

// MaskedValue 是环境变量值的掩码哨兵:
// GET 返回 ***;保存时值为 *** 表示"保留原值",为空串表示"删除该项"。
const MaskedValue = "***"

// MergeEnv 生成子进程环境:父环境 + 配置 Env + 请求 Env(后者优先)。
func (c AgentConfig) MergeEnv(reqEnv map[string]string) []string {
	merged := map[string]string{}
	// 以 os.Environ 为基准由调用方提供;这里只合并配置层。
	for k, v := range c.Env {
		merged[k] = v
	}
	for k, v := range reqEnv {
		merged[k] = v
	}
	if c.BaseURL != "" {
		if _, ok := merged["ANTHROPIC_BASE_URL"]; !ok {
			merged["ANTHROPIC_BASE_URL"] = c.BaseURL
		}
		if _, ok := merged["PI_BASE_URL"]; !ok {
			merged["PI_BASE_URL"] = c.BaseURL
		}
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// ResolveModel 决定本次调用使用的模型:请求级 > 配置级 > 默认(空)。
func (c AgentConfig) ResolveModel(reqModel string) string {
	if reqModel != "" {
		return reqModel
	}
	return c.Model
}
