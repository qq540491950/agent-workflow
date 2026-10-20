package builtin

import (
	"context"
	"testing"

	"agentworkflow/skill"
)

// 迭代 7 审计新增:args 里的布尔值以字符串形态到达时也必须被识别。
// YAML 里写 fail: "true"(带引号)会解析成字符串,类型断言失败会让
// "注入失败"静默失效 —— 该失败却成功(约定 14)。

// TestRunTestSkillStringFailArg 字符串形态的失败开关必须生效。
func TestRunTestSkillStringFailArg(t *testing.T) {
	s := &RunTestSkill{}
	for _, v := range []any{"true", "TRUE", "True", "1"} {
		resp, err := s.Execute(context.Background(), skill.SkillRequest{
			Args: map[string]any{"fail": v},
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if resp.Status != skill.StatusFailed {
			t.Errorf("fail=%v: status = %s, want FAILED", v, resp.Status)
		}
		if resp.Error == "" {
			t.Errorf("fail=%v: 失败必须有可读原因", v)
		}
	}
}

// TestRunTestSkillFalseLikeArgs 非真值不得被当成失败。
func TestRunTestSkillFalseLikeArgs(t *testing.T) {
	s := &RunTestSkill{}
	for _, v := range []any{nil, false, "false", "0", "", "no", 0, 0.0} {
		resp, err := s.Execute(context.Background(), skill.SkillRequest{
			Args: map[string]any{"fail": v},
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if resp.Status != skill.StatusSuccess {
			t.Errorf("fail=%v: status = %s, want SUCCESS", v, resp.Status)
		}
	}
}
