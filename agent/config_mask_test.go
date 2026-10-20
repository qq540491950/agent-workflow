package agent_test

import (
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 迭代 7 审计新增:Agent 配置经 API 返回时必须整体掩码(约定 8)。
// 此前只掩码 Env,ExtraArgs 里的凭据(--api-key sk-xxx)原样返回。

// TestMaskedExtraArgs 凭据形态的 CLI 参数值必须被掩码。
func TestMaskedExtraArgs(t *testing.T) {
	cfg := agent.AgentConfig{
		ExtraArgs: []string{
			"--verbose",
			"--api-key", "sk-ant-supersecret",
			"--token=sk-token-secret",
			"--model", "claude-sonnet-4-5",
		},
	}
	masked := cfg.Masked()
	joined := strings.Join(masked.ExtraArgs, " ")
	for _, secret := range []string{"sk-ant-supersecret", "sk-token-secret"} {
		if strings.Contains(joined, secret) {
			t.Errorf("ExtraArgs 里的凭据未掩码: %q", joined)
		}
	}
	if !strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("掩码后应保留 %s 标记: %q", agent.MaskedValue, joined)
	}
	// 非凭据参数必须原样保留
	for _, keep := range []string{"--verbose", "--model", "claude-sonnet-4-5"} {
		if !strings.Contains(joined, keep) {
			t.Errorf("非凭据参数被改动: %q", joined)
		}
	}
}

// TestMaskedExtraArgsDoesNotMutate 掩码必须返回副本,不能改调用方的原值。
func TestMaskedExtraArgsDoesNotMutate(t *testing.T) {
	cfg := agent.AgentConfig{ExtraArgs: []string{"--api-key", "sk-ant-supersecret"}}
	_ = cfg.Masked()
	if cfg.ExtraArgs[1] != "sk-ant-supersecret" {
		t.Errorf("原配置被就地修改: %v", cfg.ExtraArgs)
	}
}
