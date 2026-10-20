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

// ---------------------------------------------------------------------------
// 迭代 10(P0-1):ExtraArgs 的 *** 回写语义
//
// GET 返回的 ExtraArgs 里凭据位是 "***",前端把整份配置回传时会把
// "***" 一起带回来。若后端不合并,存库的真实凭据就被字面量 "***"
// 覆盖 —— 之后每次 CLI 调用都带 --api-key *** → 鉴权失败。
// ---------------------------------------------------------------------------

// TestMergeExtraArgsKeepsStoredValueByName "***" 必须沿用**已存同名选项**的值。
func TestMergeExtraArgsKeepsStoredValueByName(t *testing.T) {
	stored := []string{"--verbose", "--api-key", "sk-real-secret"}
	submitted := []string{"--verbose", "--api-key", agent.MaskedValue}
	got := agent.MergeExtraArgs(submitted, stored)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "sk-real-secret") {
		t.Errorf("*** 未沿用已存凭据: %q", joined)
	}
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("合并结果不得残留字面量 %s: %q", agent.MaskedValue, joined)
	}
}

// TestMergeExtraArgsInlineForm "= 写法"(--api-key=xxx)同样要能合并。
func TestMergeExtraArgsInlineForm(t *testing.T) {
	stored := []string{"--api-key=sk-real-secret"}
	submitted := []string{"--api-key=" + agent.MaskedValue}
	got := agent.MergeExtraArgs(submitted, stored)
	if len(got) != 1 || got[0] != "--api-key=sk-real-secret" {
		t.Errorf("= 写法未沿用已存凭据: %v", got)
	}
}

// TestMergeExtraArgsDropsMaskWithoutStoredValue 无原值的 *** 必须丢弃
// (与 Env 一致:约定 5),且不能留下悬空的选项名。
func TestMergeExtraArgsDropsMaskWithoutStoredValue(t *testing.T) {
	got := agent.MergeExtraArgs([]string{"--api-key", agent.MaskedValue}, nil)
	if len(got) != 0 {
		t.Errorf("无原值的 *** 应丢弃整个选项,实际: %v", got)
	}
	gotInline := agent.MergeExtraArgs([]string{"--api-key=" + agent.MaskedValue}, nil)
	if len(gotInline) != 0 {
		t.Errorf("无原值的 *** 应丢弃整个选项(= 写法),实际: %v", gotInline)
	}
}

// TestMergeExtraArgsNeverCrossesOptionNames 关键不变式:
// 凭据值只在**同名**选项之间流动,绝不能出现"把 A 的凭据写到 B 的
// 参数位"这种比原缺陷更糟的情况。
func TestMergeExtraArgsNeverCrossesOptionNames(t *testing.T) {
	cases := []struct {
		name      string
		stored    []string
		submitted []string
		forbid    string // 绝不允许出现在结果里的凭据
	}{
		{
			name:      "不同名选项不串位",
			stored:    []string{"--api-key", "sk-secret-A"},
			submitted: []string{"--token", agent.MaskedValue},
			forbid:    "sk-secret-A",
		},
		{
			name:      "删除 A 选项后 B 拿不到 A 的值",
			stored:    []string{"--api-key", "sk-secret-A", "--token", "sk-secret-B"},
			submitted: []string{"--token", agent.MaskedValue},
			forbid:    "sk-secret-A",
		},
		{
			name:      "增删参数造成下标错位也不串位",
			stored:    []string{"--api-key", "sk-secret-A"},
			submitted: []string{"--verbose", "--api-key", agent.MaskedValue},
			forbid:    "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agent.MergeExtraArgs(c.submitted, c.stored)
			joined := strings.Join(got, " ")
			if c.forbid != "" && strings.Contains(joined, c.forbid) {
				t.Errorf("凭据串到了错误的参数位: %q(禁止出现 %q)", joined, c.forbid)
			}
			if strings.Contains(joined, agent.MaskedValue) {
				t.Errorf("合并结果不得残留字面量 %s: %q", agent.MaskedValue, joined)
			}
		})
	}
}

// TestMergeExtraArgsReorderKeepsCredential 用户调整参数顺序后,凭据仍
// 跟着自己的选项走(按名字对齐,不是按下标)。
func TestMergeExtraArgsReorderKeepsCredential(t *testing.T) {
	stored := []string{"--api-key", "sk-secret-A", "--verbose"}
	submitted := []string{"--verbose", "--api-key", agent.MaskedValue}
	got := agent.MergeExtraArgs(submitted, stored)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "sk-secret-A") {
		t.Errorf("重排后凭据丢失: %q", joined)
	}
	if !strings.Contains(joined, "--verbose") {
		t.Errorf("重排后普通参数丢失: %q", joined)
	}
}

// TestMergeExtraArgsKeepsUserSuppliedValue 用户新填的值必须原样采用
// (不能被"合并"逻辑吃掉)。
func TestMergeExtraArgsKeepsUserSuppliedValue(t *testing.T) {
	stored := []string{"--api-key", "sk-old"}
	submitted := []string{"--api-key", "sk-new"}
	got := agent.MergeExtraArgs(submitted, stored)
	if len(got) != 2 || got[1] != "sk-new" {
		t.Errorf("新填的值应原样保留: %v", got)
	}
}

// TestMergeExtraArgsIgnoresStoredMask 已存值本身就是 "***"(旧版本往返
// bug 的产物)时不得当成凭据继续下发。
func TestMergeExtraArgsIgnoresStoredMask(t *testing.T) {
	stored := []string{"--api-key", agent.MaskedValue}
	submitted := []string{"--api-key", agent.MaskedValue}
	got := agent.MergeExtraArgs(submitted, stored)
	if len(got) != 0 {
		t.Errorf("已存值是掩码时不应保留任何选项: %v", got)
	}
}

// TestSanitizeExtraArgsDropsLiteralMask 库里存着 "--api-key ***" 时,
// 下发给 CLI 的参数里不能出现字面量 ***(否则鉴权失败)。
func TestSanitizeExtraArgsDropsLiteralMask(t *testing.T) {
	got := agent.SanitizeExtraArgs([]string{"--verbose", "--api-key", agent.MaskedValue})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("下发参数不得含字面量 %s: %q", agent.MaskedValue, joined)
	}
	if !strings.Contains(joined, "--verbose") {
		t.Errorf("普通参数应保留: %q", joined)
	}
	inline := agent.SanitizeExtraArgs([]string{"--api-key=" + agent.MaskedValue})
	if len(inline) != 0 {
		t.Errorf("= 写法的字面量掩码应整体剔除: %v", inline)
	}
}

// TestMaskExtraArgsRoundTrip 掩码 → 合并 之后必须拿回原值,且中途
// 不出现凭据明文(前端只见过 ***)。
func TestMaskExtraArgsRoundTrip(t *testing.T) {
	original := []string{"--verbose", "--api-key", "sk-round-trip", "--token=tok-round-trip"}
	masked := agent.MaskExtraArgs(original)
	joined := strings.Join(masked, " ")
	if strings.Contains(joined, "sk-round-trip") || strings.Contains(joined, "tok-round-trip") {
		t.Fatalf("掩码后仍含凭据明文: %q", joined)
	}
	merged := agent.MergeExtraArgs(masked, original)
	got := strings.Join(merged, " ")
	for _, want := range []string{"sk-round-trip", "tok-round-trip", "--verbose"} {
		if !strings.Contains(got, want) {
			t.Errorf("往返后丢失 %q: %q", want, got)
		}
	}
	if strings.Contains(got, agent.MaskedValue) {
		t.Errorf("往返后残留字面量掩码: %q", got)
	}
}
