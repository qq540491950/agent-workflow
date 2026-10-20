package agent

import (
	"strings"
	"testing"
)

// 迭代 7 审计新增:决策取值的归一化与形态校验。

// TestNormalizeDecision 决策取值不枚举白名单(工作流允许自定义决策,
// 如 REFACTOR),只做归一化(去空格 + 大写)与形态校验。
func TestNormalizeDecision(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want AgentDecision
		ok   bool
	}{
		{"小写归一", "approved", DecisionApproved, true},
		{"去空格", "  rejected \n", DecisionRejected, true},
		{"自定义决策", "REFACTOR", AgentDecision("REFACTOR"), true},
		{"自定义决策小写归一", "refactor", AgentDecision("REFACTOR"), true},
		{"空串非法", "", "", false},
		{"模板回显非法", "APPROVED|REJECTED|CONTINUE|DONE|WAIT_USER", "", false},
		{"带标点非法", "APPROVED!", "", false},
		{"数字开头非法", "2ND", "", false},
		{"超长非法", strings.Repeat("A", 65), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := NormalizeDecision(c.in)
			if got != c.want || ok != c.ok {
				t.Errorf("NormalizeDecision(%q) = (%q, %v), want (%q, %v)",
					c.in, got, ok, c.want, c.ok)
			}
		})
	}
}
