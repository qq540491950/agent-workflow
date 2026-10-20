package agent

import (
	"sort"
	"strings"
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
	Behavior map[string]any `json:"behavior,omitempty"`
}

// Masked 返回掩码后的配置副本(GET 一律 ***)。
//
// Env 的值整体替换为 ***;ExtraArgs 里**凭据形态**的参数值同样要替换 ——
// 配置项本身就允许放 "--api-key sk-xxx",此前只有 Env 被掩码,
// 这条路径让凭据原样出现在 API 响应里(约定 8)。
func (c AgentConfig) Masked() AgentConfig {
	out := c
	if c.Env != nil {
		out.Env = make(map[string]string, len(c.Env))
		for k := range c.Env {
			out.Env[k] = MaskedValue
		}
	}
	out.ExtraArgs = MaskExtraArgs(c.ExtraArgs)
	return out
}

// MaskExtraArgs 掩码 CLI 参数里的凭据值。
//
// 覆盖两种写法:--api-key sk-xxx(值是下一个参数)与
// --api-key=sk-xxx(值在同一参数内)。判定依据是**参数名**是否指向
// 凭据,不按值的形态猜测。
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
		case argCredValue:
			out[i] = MaskedValue
		case argCredInline:
			out[i] = args[i][:s.valStart] + MaskedValue + args[i][s.valEnd:]
		}
	}
	return out
}

// MergeExtraArgs 把前端回传的 ExtraArgs 与**已存** ExtraArgs 合并。
//
// 语义与 Env 同源(约定 5:掩码语义):
//
//   - 凭据选项的值恰为 MaskedValue("***")→ 沿用**已存同名选项**的值;
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
// 结果里不会残留字面量 "***":掩码哨兵不是真值,下发到 CLI 只会导致
// 鉴权失败。
func MergeExtraArgs(submitted, stored []string) []string {
	if submitted == nil {
		return nil
	}
	byName := storedCredentialValues(stored)
	used := map[string]int{}
	slots := scanExtraArgs(submitted)
	out := make([]string, 0, len(submitted))
	for i := 0; i < len(submitted); i++ {
		s := slots[i]
		switch s.kind {
		case argCredName:
			if i+1 >= len(submitted) {
				out = append(out, submitted[i]) // 选项没有值:原样保留
				continue
			}
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
		case argCredInline:
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
		default:
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
func SanitizeExtraArgs(args []string) []string {
	if args == nil {
		return nil
	}
	slots := scanExtraArgs(args)
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch slots[i].kind {
		case argCredName:
			if i+1 >= len(args) {
				out = append(out, args[i])
				continue
			}
			if isMaskedValue(args[i+1]) {
				i++ // 丢弃名与值
				continue
			}
			out = append(out, args[i], args[i+1])
			i++
		case argCredInline:
			if isMaskedValue(args[i][slots[i].valStart:slots[i].valEnd]) {
				continue // 丢弃整个选项
			}
			out = append(out, args[i])
		default:
			out = append(out, args[i])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// storedCredentialValues 收集已存 ExtraArgs 里的凭据值:键是归一化后的
// 选项名,值按出现顺序排列(同名选项多次出现时依次取用)。
//
// 已存值本身就是 "***" 的条目视为"没有真值"(旧版本往返 bug 的产物),
// 不参与合并 —— 否则会把掩码哨兵当成凭据继续下发。
func storedCredentialValues(stored []string) map[string][]string {
	out := map[string][]string{}
	if stored == nil {
		return out
	}
	slots := scanExtraArgs(stored)
	for i, s := range slots {
		switch s.kind {
		case argCredName:
			if i+1 >= len(stored) {
				continue
			}
			if v := unquote(stored[i+1]); v != MaskedValue {
				out[s.name] = append(out[s.name], v)
			}
		case argCredInline:
			if v := unquote(stored[i][s.valStart:s.valEnd]); v != MaskedValue {
				out[s.name] = append(out[s.name], v)
			}
		}
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
	argPlain      argKind = iota // 普通参数(不参与掩码与合并)
	argCredName                  // 凭据选项名(值在下一个元素)
	argCredValue                 // 凭据选项的值(上一个元素是选项名)
	argCredInline                // 凭据选项名与值在同一元素内(--api-key=xxx)
)

// argSlot 是单个元素的解析结果。
type argSlot struct {
	kind argKind
	// name 是凭据选项的**归一化键**(小写、去前导 -、去掉 - 与 _),
	// 合并时按它对齐:--api-key / --api_key / --API-Key → "apikey"。
	name string
	// valStart/valEnd 是值在该元素内的字节区间(仅 argCredInline)。
	valStart, valEnd int
}

// scanExtraArgs 解析 ExtraArgs:标出每个元素的角色。
//
// 掩码(MaskExtraArgs)、合并(MergeExtraArgs)、剔除(SanitizeExtraArgs)
// 三处共用这一份解析 —— 三份实现各自演化就会出现"掩码认得、合并不认"
// 的分裂(约定 13 的同源精神)。
func scanExtraArgs(args []string) []argSlot {
	slots := make([]argSlot, len(args))
	for i := range slots {
		slots[i] = argSlot{kind: argPlain}
	}
	maskNext := false
	pending := ""
	for i, arg := range args {
		if maskNext {
			slots[i] = argSlot{kind: argCredValue, name: pending}
			maskNext = false
			continue
		}
		name, _, hasValue := strings.Cut(arg, "=")
		if !isCredentialArgName(name) {
			continue
		}
		key := normalizeCredKey(name)
		if hasValue {
			slots[i] = argSlot{
				kind:     argCredInline,
				name:     key,
				valStart: len(name) + 1,
				valEnd:   len(arg),
			}
			continue
		}
		// 值在下一个参数里:标记下来(若已是最后一个,无可掩码)
		slots[i] = argSlot{kind: argCredName, name: key}
		pending = key
		maskNext = true
	}
	return slots
}

// normalizeCredKey 归一化凭据选项名(合并时按名字对齐的唯一依据)。
func normalizeCredKey(name string) string {
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
