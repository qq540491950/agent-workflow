package skill_test

import (
	"testing"

	"agentworkflow/skill"
)

// 迭代 7 审计新增:Skill 状态是接口契约,必须有唯一来源与归一化规则。
// 运行时此前只精确匹配 "WAIT_USER",小写形态会被漏判成成功。

// TestNormalizeStatus 状态归一化:大小写与首尾空白都不应影响判定。
func TestNormalizeStatus(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"SUCCESS", skill.StatusSuccess},
		{"success", skill.StatusSuccess},
		{" FAILED ", skill.StatusFailed},
		{"failed", skill.StatusFailed},
		{"WAIT_USER", skill.StatusWaitUser},
		{"wait_user", skill.StatusWaitUser},
		{"", ""},
	}
	for _, c := range cases {
		if got := skill.NormalizeStatus(c.in); got != c.want {
			t.Errorf("NormalizeStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
