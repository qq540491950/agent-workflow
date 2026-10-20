package builtin

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/skill"
)

// TestRunTestSkillRejectsUnrecognizedBoolArg 无法识别的布尔取值必须报错。
//
// 此前 truthy 对不认识的字符串一律返回 false:`fail: "treu"`(拼错)会让
// "注入失败"静默失效,测试照常"通过" —— 用户以为自己在跑失败路径,实际
// 跑的是成功路径(约定 14:该失败却成功)。
func TestRunTestSkillRejectsUnrecognizedBoolArg(t *testing.T) {
	s := &RunTestSkill{}
	_, err := s.Execute(context.Background(), skill.SkillRequest{
		Args: map[string]any{"fail": "treu"},
	})
	if err == nil {
		t.Fatal("无法识别的布尔取值应报错,实际静默当成 false")
	}
	if !strings.Contains(err.Error(), "fail") {
		t.Errorf("错误消息 = %q, want 提到参数名 fail", err.Error())
	}
}

// TestRunTestSkillNumericBoolArg 数值形态的开关必须生效。
//
// YAML 里的整数被 goccy/go-yaml 解成 uint64,而 json.Unmarshal 解成
// float64:此前 truthy 只认 int/int64/float64,uint64 会落到 default 分支
// 被当成 false —— `fail: 1` 静默不注入失败。
func TestRunTestSkillNumericBoolArg(t *testing.T) {
	s := &RunTestSkill{}
	for _, v := range []any{1, int64(1), uint64(1), float64(1), uint(1)} {
		resp, err := s.Execute(context.Background(), skill.SkillRequest{
			Args: map[string]any{"fail": v},
		})
		if err != nil {
			t.Fatalf("fail=%v(%T): %v", v, v, err)
		}
		if resp.Status != skill.StatusFailed {
			t.Errorf("fail=%v(%T) → status = %s, want FAILED", v, v, resp.Status)
		}
	}
}

// TestRunTestSkillRecognizedBoolArgs 既有语义不得改变:真值字符串/数字注入
// 失败,假值不注入。
func TestRunTestSkillRecognizedBoolArgs(t *testing.T) {
	s := &RunTestSkill{}
	truthyVals := []any{"true", "TRUE", " True ", "1", "yes", "on", true, 1}
	for _, v := range truthyVals {
		resp, err := s.Execute(context.Background(), skill.SkillRequest{
			Args: map[string]any{"fail": v},
		})
		if err != nil {
			t.Fatalf("fail=%v(%T): %v", v, v, err)
		}
		if resp.Status != skill.StatusFailed {
			t.Errorf("fail=%v(%T) → status = %s, want FAILED", v, v, resp.Status)
		}
	}
	falsyVals := []any{nil, false, "false", "0", "", "no", "off", 0, 0.0}
	for _, v := range falsyVals {
		resp, err := s.Execute(context.Background(), skill.SkillRequest{
			Args: map[string]any{"fail": v},
		})
		if err != nil {
			t.Fatalf("fail=%v(%T): %v", v, v, err)
		}
		if resp.Status != skill.StatusSuccess {
			t.Errorf("fail=%v(%T) → status = %s, want SUCCESS", v, v, resp.Status)
		}
	}
}
