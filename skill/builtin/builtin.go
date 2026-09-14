// Package builtin 提供内置 Skill 实现。
package builtin

import (
	"context"
	"fmt"

	"agentworkflow/skill"
)

// SubmitSkill 模拟/执行提交动作。
// 第一阶段仅记录提交信息(不做真实 git push);git 操作由 git 节点完成。
type SubmitSkill struct{}

// ID 实现 skill.Skill。
func (s *SubmitSkill) ID() string { return "submit" }

// Name 实现 skill.Skill。
func (s *SubmitSkill) Name() string { return "Submit" }

// Description 实现 skill.Skill。
func (s *SubmitSkill) Description() string {
	return "提交本次任务的产出(记录 commit message 并标记任务完成)"
}

// Execute 实现 skill.Skill。
func (s *SubmitSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	msg, _ := req.Args["message"].(string)
	if msg == "" {
		msg = fmt.Sprintf("workflow submit: %s", req.NodeID)
	}
	summary, _ := req.Context["summary"].(string)
	return &skill.SkillResponse{
		Status: "SUCCESS",
		Output: fmt.Sprintf("submitted: %s\nsummary: %s", msg, summary),
		Data: map[string]any{
			"commit_message": msg,
			"submitted":      true,
		},
	}, nil
}

// LogSkill 输出日志信息,常用于调试工作流。
type LogSkill struct{}

// ID 实现 skill.Skill。
func (s *LogSkill) ID() string { return "log" }

// Name 实现 skill.Skill。
func (s *LogSkill) Name() string { return "Log" }

// Description 实现 skill.Skill。
func (s *LogSkill) Description() string { return "输出一条日志(消息来自 args.message)" }

// Execute 实现 skill.Skill。
func (s *LogSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	msg, _ := req.Args["message"].(string)
	return &skill.SkillResponse{Status: "SUCCESS", Output: msg}, nil
}

// RunTestSkill 模拟运行测试并返回结构化结果。
type RunTestSkill struct{}

// ID 实现 skill.Skill。
func (s *RunTestSkill) ID() string { return "run-test" }

// Name 实现 skill.Skill。
func (s *RunTestSkill) Name() string { return "Run Test" }

// Description 实现 skill.Skill。
func (s *RunTestSkill) Description() string { return "运行测试(内置模拟实现,可通过 args.fail 注入失败)" }

// Execute 实现 skill.Skill。
func (s *RunTestSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	fail, _ := req.Args["fail"].(bool)
	if fail {
		return &skill.SkillResponse{
			Status: "FAILED",
			Error:  "test failure: 2 tests failed (injected)",
			Data:   map[string]any{"passed": false, "failed_count": 2},
		}, nil
	}
	return &skill.SkillResponse{
		Status: "SUCCESS",
		Output: "all tests passed",
		Data:   map[string]any{"passed": true, "passed_count": 12},
	}, nil
}

// All 返回全部内置 Skill 实例。
func All() []skill.Skill {
	return []skill.Skill{&SubmitSkill{}, &LogSkill{}, &RunTestSkill{}}
}
