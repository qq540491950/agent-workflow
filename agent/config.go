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
	// DefaultWorkingDir 默认工作目录(节点未指定时使用)。
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
func MaskExtraArgs(args []string) []string {
	if args == nil {
		return nil
	}
	out := make([]string, len(args))
	copy(out, args)
	maskNext := false
	for i, arg := range out {
		if maskNext {
			out[i] = MaskedValue
			maskNext = false
			continue
		}
		name, _, hasValue := strings.Cut(arg, "=")
		if !isCredentialArgName(name) {
			continue
		}
		if hasValue {
			out[i] = name + "=" + MaskedValue
			continue
		}
		// 值在下一个参数里:标记下来(若已是最后一个,无可掩码)
		maskNext = true
	}
	return out
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
