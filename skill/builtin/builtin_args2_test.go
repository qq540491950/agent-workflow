package builtin

import (
	"context"
	"testing"

	"agentworkflow/skill"
)

// 本文件锁住 builtin Skill 的 args 解析语义(迭代 15)。
//
// 背景:args 里的值来自 DSL(YAML/JSON),类型由用户书写决定。此前
// builtin 用 `msg, _ := req.Args["message"].(string)` 取值 —— **类型
// 断言失败就静默变成空串**:
//
//	args:
//	  message: 123        → 断言失败 → msg = "" → 用户的提交信息被丢弃
//
// 对 LogSkill 更糟:输出变成空串 + Status=SUCCESS —— 节点"成功"了,
// 但日志里什么都没有(约定 14:该失败却成功 / 静默丢弃)。
//
// 现在的口径:
//   - 标量(int/float/bool 等)→ 转成字符串,用户的意图被保留;
//   - 结构化值(map/slice)→ **报错**,不静默变成空串;
//   - 缺省 / null → 空串(与"没传"一致)。

func TestSubmitSkillCoercesScalarMessage(t *testing.T) {
	s := &SubmitSkill{}
	cases := []struct {
		in   any
		want string
	}{
		{123, "123"},
		{1.5, "1.5"},
		{true, "true"},
	}
	for _, c := range cases {
		resp, err := s.Execute(context.Background(), skill.SkillRequest{
			NodeID: "n",
			Args:   map[string]any{"message": c.in},
		})
		if err != nil {
			t.Fatalf("execute(%v): %v", c.in, err)
		}
		if got := resp.Data["commit_message"]; got != c.want {
			t.Errorf("message=%v(%T) → commit_message = %v, want %q", c.in, c.in, got, c.want)
		}
	}
}

func TestSubmitSkillRejectsStructuredMessage(t *testing.T) {
	s := &SubmitSkill{}
	_, err := s.Execute(context.Background(), skill.SkillRequest{
		NodeID: "n",
		Args:   map[string]any{"message": map[string]any{"a": 1}},
	})
	if err == nil {
		t.Fatal("结构化的 message 应报错,而不是静默变成空串")
	}
}

func TestLogSkillCoercesScalarMessage(t *testing.T) {
	s := &LogSkill{}
	resp, err := s.Execute(context.Background(), skill.SkillRequest{
		Args: map[string]any{"message": 42},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Output != "42" {
		t.Errorf("Output = %q, want 42", resp.Output)
	}
}

func TestLogSkillRejectsStructuredMessage(t *testing.T) {
	s := &LogSkill{}
	_, err := s.Execute(context.Background(), skill.SkillRequest{
		Args: map[string]any{"message": []any{"a"}},
	})
	if err == nil {
		t.Fatal("结构化的 message 应报错,而不是静默输出空串")
	}
}

func TestLogSkillMissingMessageStaysEmpty(t *testing.T) {
	s := &LogSkill{}
	resp, err := s.Execute(context.Background(), skill.SkillRequest{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != skill.StatusSuccess || resp.Output != "" {
		t.Errorf("未传 message 应为空输出且成功: %+v", resp)
	}
}
