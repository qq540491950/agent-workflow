// Package builtin 提供内置 Skill 实现。
package builtin

import (
	"context"
	"fmt"
	"strings"

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
	msg, err := argMessage(req.Args, "message")
	if err != nil {
		return nil, err
	}
	if msg == "" {
		msg = fmt.Sprintf("workflow submit: %s", req.NodeID)
	}
	summary, err := argMessage(req.Context, "summary")
	if err != nil {
		return nil, err
	}
	return &skill.SkillResponse{
		Status: skill.StatusSuccess,
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
	msg, err := argMessage(req.Args, "message")
	if err != nil {
		return nil, err
	}
	return &skill.SkillResponse{Status: skill.StatusSuccess, Output: msg}, nil
}

// argMessage 从 args/context 里取一个"消息"类参数。
//
// args 的值来自 DSL(YAML/JSON),类型由用户书写决定。此前 builtin 用
// `msg, _ := req.Args["message"].(string)` 取值 —— **类型断言失败就静默
// 变成空串**:用户写的 message 被丢弃,LogSkill 还以 SUCCESS + 空输出
// 结束(约定 14:该失败却成功 / 静默丢弃)。
//
// 现在的口径:
//   - 字符串 → 原样;
//   - 其它标量(int/float/bool)→ 转成字符串(保留用户意图);
//   - 结构化值(map/slice)→ 报错,不静默变成空串;
//   - 缺省 / null → 空串(与"没传"一致)。
func argMessage(src map[string]any, key string) (string, error) {
	v, ok := src[key]
	if !ok || v == nil {
		return "", nil
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprint(t), nil
	default:
		return "", fmt.Errorf("builtin skill: 参数 %q 必须是字符串(收到 %T)", key, v)
	}
}

// RunTestSkill 模拟运行测试并返回结构化结果。
type RunTestSkill struct{}

// ID 实现 skill.Skill。
func (s *RunTestSkill) ID() string { return "run-test" }

// Name 实现 skill.Skill。
func (s *RunTestSkill) Name() string { return "Run Test" }

// Description 实现 skill.Skill。
func (s *RunTestSkill) Description() string {
	return "运行测试(内置模拟实现,可通过 args.fail 注入失败)"
}

// Execute 实现 skill.Skill。
func (s *RunTestSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	fail, err := truthy("fail", req.Args["fail"])
	if err != nil {
		return nil, err
	}
	if fail {
		return &skill.SkillResponse{
			Status: skill.StatusFailed,
			Error:  "test failure: 2 tests failed (injected)",
			Data:   map[string]any{"passed": false, "failed_count": 2},
		}, nil
	}
	return &skill.SkillResponse{
		Status: skill.StatusSuccess,
		Output: "all tests passed",
		Data:   map[string]any{"passed": true, "passed_count": 12},
	}, nil
}

// truthy 解析 args 里的布尔开关。
//
// 不能直接用 v.(bool):DSL 是 YAML 解析的,`fail: true` 是布尔,
// 但 `fail: "true"`(带引号)是字符串 —— 类型断言失败会让"注入失败"
// 静默失效,测试照常"通过"(约定 14:该失败却成功)。
//
// 数值形态必须全认:YAML 的整数是 uint64(goccy/go-yaml),JSON 的是
// float64,此前只认 int/int64/float64,uint64 落到 default 分支被当成
// false —— `fail: 1` 静默不注入失败。
//
// 无法识别的取值**报错**而不是猜成 false:`fail: "treu"`(拼错)静默当成
// false,等于让用户以为自己在跑失败路径、实际跑的是成功路径。
//
// key 参与错误消息:报错必须指出是哪个参数出的问题,否则用户在节点配置里
// 找不着(排查方向被带偏)。
func truthy(key string, v any) (bool, error) {
	switch t := v.(type) {
	case nil:
		return false, nil
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "":
			return false, nil
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		}
		return false, fmt.Errorf("builtin skill: 参数 %q 无法识别为布尔值 %q(可用 true/false/1/0/yes/no/on/off)", key, t)
	case int:
		return t != 0, nil
	case int8:
		return t != 0, nil
	case int16:
		return t != 0, nil
	case int32:
		return t != 0, nil
	case int64:
		return t != 0, nil
	case uint:
		return t != 0, nil
	case uint8:
		return t != 0, nil
	case uint16:
		return t != 0, nil
	case uint32:
		return t != 0, nil
	case uint64:
		return t != 0, nil
	case float32:
		return t != 0, nil
	case float64:
		return t != 0, nil
	default:
		return false, fmt.Errorf("builtin skill: 参数 %q 类型非法(%T),请写 true/false", key, v)
	}
}

// All 返回全部内置 Skill 实例。
func All() []skill.Skill {
	return []skill.Skill{&SubmitSkill{}, &LogSkill{}, &RunTestSkill{}}
}
