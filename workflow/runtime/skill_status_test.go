package runtime

import (
	"context"
	"testing"

	"agentworkflow/event"
	"agentworkflow/skill"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"Skill 返回状态的归一化"(约定 #14:该失败/该等待却成功等同 P0)。
//
// Skill 接口承诺的状态只有三个:SUCCESS / FAILED / WAIT_USER
// (见 skill/skill.go 的常量与 NormalizeStatus)。但状态是**字符串**,
// 外部实现(自建 Skill、内置 Skill、第三方脚本桥接)很可能返回
// "wait_user" / "Failed" / " wait_user " 这类形态。
//
// 运行期此前只精确匹配 "WAIT_USER" 与 {"FAILED","failed"}:
//   - 小写 "wait_user" → 不匹配 → 节点按 **成功** 继续往下跑。
//     该等待却成功:人工确认这一步被静默跳过,工作流带着未确认的状态完成。
//   - "Failed" / " failed " → 不匹配 → 同样按成功处理。该失败却成功。
//
// 归一化的唯一来源是 skill.NormalizeStatus(去空白 + 转大写),
// 与 Agent 侧 coreagent.StatusXxx 的判定口径一致。

// statusSkill 返回指定状态字符串的 Skill(用于覆盖各种大小写/空白形态)。
type statusSkill struct {
	id     string
	status string
}

func (s *statusSkill) ID() string          { return s.id }
func (s *statusSkill) Name() string        { return "status skill" }
func (s *statusSkill) Description() string { return "test skill with fixed status" }

func (s *statusSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	return &skill.SkillResponse{Status: s.status, Output: "out", Error: "boom"}, nil
}

// TestSkillStatusNormalized 各种大小写/空白形态的状态必须得到同一个结论。
func TestSkillStatusNormalized(t *testing.T) {
	cases := []struct {
		status string
		want   model.NodeState
	}{
		// WAIT_USER:必须暂停等待人工输入
		{"WAIT_USER", model.NodeWaiting},
		{"wait_user", model.NodeWaiting},
		{"Wait_User", model.NodeWaiting},
		{" wait_user ", model.NodeWaiting},
		// FAILED:必须失败
		{"FAILED", model.NodeFailed},
		{"failed", model.NodeFailed},
		{"Failed", model.NodeFailed},
		{" failed ", model.NodeFailed},
		// SUCCESS:正常成功
		{"SUCCESS", model.NodeSuccess},
		{"success", model.NodeSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			eng := newMinimalEngine(t, &statusSkill{id: "st", status: tc.status})
			node := &model.Node{ID: "n1", Type: model.NodeTypeSkill, Config: map[string]any{
				"skill": "st",
			}}
			env := &compiler.RunEnv{
				Exec: &model.Execution{ID: "exec-status"}, WF: &model.Workflow{},
				Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
			}
			out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
			if out.State != tc.want {
				t.Errorf("Skill 状态 %q → 节点状态 %s,want %s", tc.status, out.State, tc.want)
			}
			// FAILED 必须带上失败原因(否则用户只看到"失败"没有线索)
			if tc.want == model.NodeFailed && out.Error == "" {
				t.Errorf("Skill 状态 %q:失败节点必须带失败原因", tc.status)
			}
			// WAIT_USER 必须给出可答复的选项(前端据此渲染 HITL 按钮)
			if tc.want == model.NodeWaiting {
				if _, ok := out.Data["responses"]; !ok {
					t.Errorf("Skill 状态 %q:等待节点必须带 responses 选项,实际 Data=%v", tc.status, out.Data)
				}
			}
		})
	}
}

// TestSkillStatusUnknownIsSuccess 未知状态按成功处理(不猜)。
//
// 契约只承诺三个状态;未知取值既不是失败也不是等待,保持"按成功继续"
// 是既有行为(不引入新的静默失败面)。本用例把该口径钉住,避免以后
// 有人把未知状态改成 FAILED 造成行为漂移。
func TestSkillStatusUnknownIsSuccess(t *testing.T) {
	eng := newMinimalEngine(t, &statusSkill{id: "st", status: "weird"})
	node := &model.Node{ID: "n1", Type: model.NodeTypeSkill, Config: map[string]any{
		"skill": "st",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-status"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeSuccess {
		t.Errorf("未知状态 → 节点状态 %s,want %s", out.State, model.NodeSuccess)
	}
}
