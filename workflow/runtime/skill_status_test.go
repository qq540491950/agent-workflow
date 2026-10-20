package runtime

import (
	"context"
	"strings"
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

// TestSkillStatusUnknownFailsNode 契约外的状态必须**显式失败**。
//
// 旧口径("未知状态按成功处理(不猜)")是错的:"不猜"应当是"不猜某个具体
// 状态值",而不是"猜它是成功"。Skill 接口只承诺 SUCCESS / FAILED /
// WAIT_USER 三个状态(见 skill/skill.go 的常量与 NormalizeStatus);
// 返回契约外的值说明这个 Skill 出了问题(实现有 bug,或第三方实现用了
// 别的口径)。此时工作流**无法知道它到底成功没有** —— 按成功继续就是
// "该失败却成功"(约定 14),而用户看到的是"执行成功"。
//
// 因此:契约外状态 → 节点 FAILED,原因里点名状态值与契约,让用户能改。
func TestSkillStatusUnknownFailsNode(t *testing.T) {
	for _, status := range []string{"weird", "BANANA", "success!"} {
		t.Run(status, func(t *testing.T) {
			eng := newMinimalEngine(t, &statusSkill{id: "st", status: status})
			node := &model.Node{ID: "n1", Type: model.NodeTypeSkill, Config: map[string]any{
				"skill": "st",
			}}
			env := &compiler.RunEnv{
				Exec: &model.Execution{ID: "exec-status"}, WF: &model.Workflow{},
				Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
			}
			out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
			if out.State != model.NodeFailed {
				t.Fatalf("未知状态 %q → 节点状态 %s,want %s", status, out.State, model.NodeFailed)
			}
			if !strings.Contains(out.Error, "未知状态") {
				t.Errorf("失败原因应写明是未知状态,实际 %q", out.Error)
			}
			if !strings.Contains(out.Error, status) {
				t.Errorf("失败原因应点名状态值 %q,实际 %q", status, out.Error)
			}
			if !strings.Contains(out.Error, "st") {
				t.Errorf("失败原因应点名是哪个 Skill,实际 %q", out.Error)
			}
		})
	}
}

// TestSkillStatusEmptyFailsNode 空状态同样是契约外取值:Skill 没有报告
// 任何状态,工作流无从判断成败。按成功继续 = 静默假装成功(约定 14),
// 所以显式失败并说明"没有返回状态"。
//
// 判断依据:skill.SkillResponse.Status 的契约取值只有三个,内置 Skill
// 全部显式赋值(见 skill/builtin/builtin.go);空串只可能来自"没写"的
// 实现,不是"成功"的另一种写法。
func TestSkillStatusEmptyFailsNode(t *testing.T) {
	eng := newMinimalEngine(t, &statusSkill{id: "st", status: ""})
	node := &model.Node{ID: "n1", Type: model.NodeTypeSkill, Config: map[string]any{
		"skill": "st",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-status"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("空状态 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if !strings.Contains(out.Error, "未返回状态") {
		t.Errorf("失败原因应写明 Skill 没有返回状态,实际 %q", out.Error)
	}
}

// TestSkillStatusFailedKeepsReason Skill 报 FAILED 时必须带上原因。
func TestSkillStatusFailedKeepsReason(t *testing.T) {
	eng := newMinimalEngine(t, &statusSkill{id: "st", status: skill.StatusFailed})
	node := &model.Node{ID: "n1", Type: model.NodeTypeSkill, Config: map[string]any{
		"skill": "st",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-status"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("FAILED → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if !strings.Contains(out.Error, "boom") {
		t.Errorf("失败原因应保留 Skill 返回的错误,实际 %q", out.Error)
	}
}
