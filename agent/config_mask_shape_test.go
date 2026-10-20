package agent_test

import (
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 本文件钉住迭代 19 / P1-2:**掩码判定认"值的形态"**,而不只认"选项名"。
//
// 背景:此前 extra_args 只实现了"按名"那一半 —— --api-key 的值被掩了,
// 但 --header "Authorization: Bearer ghp_xxx" 里的凭据**明文**出了 GET
// 响应(同一个请求里 --api-key 与 env 都是 ***,只有 --header 漏了)。
//
// 更要命的是"掩码认得、合并不认":按名合并只认凭据选项名,于是
// 名字不敏感的选项(--header)出现 "***" 时,合并逻辑认不出它是掩码,
// 会把它当**用户新填的值**入库 → 库里存进字面量 "***" → 下发给 CLI 的
// 参数是 "--header ***" → 鉴权失败,而用户只看到"已保存"。
//
// 所以本文件同时钉死三件事:
//  1. 掩码:名字不敏感但值呈凭据形态的,也要掩;
//  2. 误伤边界:普通参数值(--description "some text")不掩;
//  3. 自洽:掩过 → 回传 → 合并,拿回的是**原值**(不是哨兵)。

// ghpToken / skToken / highEntropyToken 是测试用的凭据样本。
//
// 长度都取真实形态:logx 的"已知前缀"规则要求前缀之后至少 12 个
// 凭据字符,高熵裸值要求 ≥24 字符且大小写混合 + 含数字 —— 用短样本
// 会让用例"因为门槛不够而绿",钉不住任何东西。
const (
	ghpToken          = "ghp_ABCdef1234567890ABCdef1234567890ABCD"
	skToken           = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890"
	highEntropyToken  = "aB3dE5fG7hI9jK1lM2nO4pQ6rS8tU0"
	plainDescription  = "some text"
	plainModelName    = "claude-sonnet-4-5"
	plainOutputPath   = "/tmp/report.pdf"
	nonCredentialName = "header"
)

// TestMaskExtraArgsMasksCredentialShapedValues 名字不敏感、但**值本身
// 呈凭据形态**的选项,值必须被掩码。
//
// 修复前(只认"按名"):这三个用例全红 —— 值原样出现在 GET 响应里。
func TestMaskExtraArgsMasksCredentialShapedValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// secret 是不得出现在掩码结果里的明文。
		secret string
	}{
		{
			name:   "认证头形态",
			args:   []string{"--header", "Authorization: Bearer " + ghpToken},
			secret: ghpToken,
		},
		{
			name:   "已知前缀形态",
			args:   []string{"--foo", skToken},
			secret: skToken,
		},
		{
			name:   "高熵裸值形态",
			args:   []string{"--flag", highEntropyToken},
			secret: highEntropyToken,
		},
		{
			name:   "认证头形态(= 写法)",
			args:   []string{"--header=Authorization: Bearer " + ghpToken},
			secret: ghpToken,
		},
		{
			name:   "独立元素里的裸凭据",
			args:   []string{ghpToken},
			secret: ghpToken,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			masked := agent.MaskExtraArgs(c.args)
			joined := strings.Join(masked, " ")
			if strings.Contains(joined, c.secret) {
				t.Errorf("凭据明文未掩码: %q", joined)
			}
			if !strings.Contains(joined, agent.MaskedValue) {
				t.Errorf("掩码后应保留 %s 标记: %q", agent.MaskedValue, joined)
			}
		})
	}
}

// TestMaskExtraArgsKeepsPlainValues 误伤边界:普通参数值**不得**被掩码。
//
// 掩码过宽同样是 Bug:用户看到 "--description ***" 会以为自己写的
// 内容被系统改了,存回去就把参数改坏了(约定 14 的同族问题)。
func TestMaskExtraArgsKeepsPlainValues(t *testing.T) {
	args := []string{
		"--verbose",
		"--description", plainDescription,
		"--model", plainModelName,
		"--output", plainOutputPath,
		"--prompt", "Please review PR #123 and fix the bug",
	}
	masked := agent.MaskExtraArgs(args)
	if len(masked) != len(args) {
		t.Fatalf("掩码后元素数变了: %v → %v", args, masked)
	}
	for i := range args {
		if masked[i] != args[i] {
			t.Errorf("普通参数被误伤: [%d] %q → %q", i, args[i], masked[i])
		}
	}
}

// TestMaskExtraArgsIsIdempotent 幂等:掩一次 vs 掩两次必须逐字节相同。
//
// 读出口(GET 掩码)与落库侧(各过一次)结果必须一致 —— 否则同一份配置
// 在两次处理后形态不同,往返比对会出现"幻影差异"。
func TestMaskExtraArgsIsIdempotent(t *testing.T) {
	args := []string{
		"--verbose",
		"--api-key", skToken,
		"--header", "Authorization: Bearer " + ghpToken,
		"--foo=sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890",
		"--flag", highEntropyToken,
		"--description", plainDescription,
	}
	once := agent.MaskExtraArgs(args)
	twice := agent.MaskExtraArgs(once)
	if len(once) != len(twice) {
		t.Fatalf("二次掩码后元素数变了: %v → %v", once, twice)
	}
	for i := range once {
		if once[i] != twice[i] {
			t.Errorf("掩码不幂等: [%d] %q → %q", i, once[i], twice[i])
		}
	}
}

// TestMaskExtraArgsKeepsBooleanFlags 布尔开关(没有值的选项)不得被当成
// 凭据值掩掉。
//
// 早期实现把凭据选项名之后的元素无条件当成"凭据值",于是
// "--api-key --verbose" 会把 "--verbose" 掩成 "***" —— 参数被改坏。
func TestMaskExtraArgsKeepsBooleanFlags(t *testing.T) {
	masked := agent.MaskExtraArgs([]string{"--api-key", "--verbose"})
	if len(masked) != 2 || masked[1] != "--verbose" {
		t.Errorf("布尔开关被误当成凭据值: %v", masked)
	}
}

// TestMergeExtraArgsRestoresShapeMaskedValue **自洽性**(最容易出错的一处):
// 名字不敏感的选项(--header)的值被掩成 "***" 后回传,合并必须拿回
// **原值**,而不是把 "***" 当成用户新填的值存进库。
//
// 修复前:合并只认凭据选项名,--header 的 "***" 认不出来 → 入库成字面量
// "***" → 下发给 CLI 的是 "--header ***" → 鉴权失败(用户只看到"已保存")。
func TestMergeExtraArgsRestoresShapeMaskedValue(t *testing.T) {
	stored := []string{"--header", "Authorization: Bearer " + ghpToken}
	masked := agent.MaskExtraArgs(stored)
	joined := strings.Join(masked, " ")
	if !strings.Contains(joined, agent.MaskedValue) {
		t.Fatalf("前置条件不成立:值未被掩码: %q", joined)
	}

	got := agent.MergeExtraArgs(masked, stored)
	out := strings.Join(got, " ")
	if !strings.Contains(out, ghpToken) {
		t.Errorf("回传后未拿回原值(凭据丢失): %q", out)
	}
	if strings.Contains(out, agent.MaskedValue) {
		t.Errorf("合并结果残留字面量 %s: %q", agent.MaskedValue, out)
	}
}

// TestMergeExtraArgsDoesNotCrossRenamedShapeOption 换名后**不回填**旧值:
// 用户把 --header 改名成 --x-header,旧凭据不能被写到新名字下面
// (那是另一个参数的凭据)。没有同名原值 → 整个选项丢弃。
func TestMergeExtraArgsDoesNotCrossRenamedShapeOption(t *testing.T) {
	stored := []string{"--header", "Authorization: Bearer " + ghpToken}
	submitted := []string{"--x-header", agent.MaskedValue}

	got := agent.MergeExtraArgs(submitted, stored)
	out := strings.Join(got, " ")
	if strings.Contains(out, ghpToken) {
		t.Errorf("改名后旧凭据被回填(串位): %q", out)
	}
	if strings.Contains(out, agent.MaskedValue) {
		t.Errorf("合并结果残留字面量 %s: %q", agent.MaskedValue, out)
	}
	if len(got) != 0 {
		t.Errorf("无同名原值时应丢弃整个选项: %v", got)
	}
}

// TestMaskExtraArgsRoundTripShapeMasked 往返:掩码 → 合并 之后必须拿回
// **逐字节相同**的原值,且全程不出现明文。
//
// 这是"GET 拿掩码 → PUT 回传 → 库里仍是真实凭据"的最小单元。
func TestMaskExtraArgsRoundTripShapeMasked(t *testing.T) {
	original := []string{
		"--verbose",
		"--header", "Authorization: Bearer " + ghpToken,
		"--foo", skToken,
		"--flag", highEntropyToken,
		"--description", plainDescription,
	}
	masked := agent.MaskExtraArgs(original)
	joined := strings.Join(masked, " ")
	for _, secret := range []string{ghpToken, skToken, highEntropyToken} {
		if strings.Contains(joined, secret) {
			t.Fatalf("掩码后仍含凭据明文: %q", joined)
		}
	}
	merged := agent.MergeExtraArgs(masked, original)
	if len(merged) != len(original) {
		t.Fatalf("往返后元素数变了: %v → %v", original, merged)
	}
	for i := range original {
		if merged[i] != original[i] {
			t.Errorf("往返后逐字节不一致: [%d] %q → %q", i, original[i], merged[i])
		}
	}
}

// TestMaskExtraArgsRoundTripKeepsQuotes 引号是**参数值的一部分**
// (参数不经 shell):"--api-key \"sk-x\"" 回来必须还是带引号的原文,
// 去掉引号就换了值。
func TestMaskExtraArgsRoundTripKeepsQuotes(t *testing.T) {
	original := []string{"--api-key", `"` + skToken + `"`}
	masked := agent.MaskExtraArgs(original)
	if strings.Contains(strings.Join(masked, " "), skToken) {
		t.Fatalf("带引号的凭据未掩码: %v", masked)
	}
	merged := agent.MergeExtraArgs(masked, original)
	if len(merged) != 2 || merged[1] != original[1] {
		t.Errorf("往返后引号丢失(值被改了): %v → %v", original, merged)
	}
}

// TestSanitizeExtraArgsDropsShapeMaskedValue 库里若存着 "--header ***"
// (旧版本往返 bug 的产物),下发时必须剔除 —— 否则 CLI 每次调用都带
// 一个废值,而鉴权失败的原因完全看不出来。
func TestSanitizeExtraArgsDropsShapeMaskedValue(t *testing.T) {
	got := agent.SanitizeExtraArgs([]string{"--verbose", "--header", agent.MaskedValue})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, agent.MaskedValue) {
		t.Errorf("下发参数不得含字面量 %s: %q", agent.MaskedValue, joined)
	}
	if !strings.Contains(joined, "--verbose") {
		t.Errorf("普通参数应保留: %q", joined)
	}
	if strings.Contains(joined, "--header") {
		t.Errorf("悬空的选项名同样会让 CLI 报错: %q", joined)
	}
}

// TestMergeExtraArgsRestoresInlineShapeMaskedValue "= 写法"同样要能
// 还原(前端回传的就是 "--header=***")。
func TestMergeExtraArgsRestoresInlineShapeMaskedValue(t *testing.T) {
	stored := []string{"--header=Authorization: Bearer " + ghpToken}
	masked := agent.MaskExtraArgs(stored)
	if strings.Contains(strings.Join(masked, " "), ghpToken) {
		t.Fatalf("= 写法的凭据未掩码: %v", masked)
	}
	got := agent.MergeExtraArgs(masked, stored)
	if len(got) != 1 || got[0] != stored[0] {
		t.Errorf("= 写法未拿回原值: %v → %v", stored, got)
	}
}

// TestMergeExtraArgsKeepsPlainValueAfterShapeRule 普通值(不被掩码的值)
// 在回传时必须原样保留 —— 掩码规则变宽不改变"用户填的值原样采用"。
func TestMergeExtraArgsKeepsPlainValueAfterShapeRule(t *testing.T) {
	stored := []string{"--header", plainDescription}
	submitted := agent.MaskExtraArgs(stored)
	got := agent.MergeExtraArgs(submitted, stored)
	if len(got) != 2 || got[1] != plainDescription {
		t.Errorf("普通值未原样保留: %v → %v", stored, got)
	}
}
