package agent

import "sort"

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

// MaskEnv 返回掩码后的环境变量副本(值替换为 ***)。
func (c AgentConfig) Masked() AgentConfig {
	out := c
	if c.Env != nil {
		out.Env = make(map[string]string, len(c.Env))
		for k := range c.Env {
			out.Env[k] = MaskedValue
		}
	}
	return out
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
