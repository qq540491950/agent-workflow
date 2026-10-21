package validator

import (
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"timeout_seconds 的取值范围"这条校验期规则(第二轮 P2-2)。
//
// 实测缺陷:timeout_seconds **没有上界** → 运行期把秒数换算成
// time.Duration(raw)*time.Second 时**溢出**:
//
//   - raw=10000000000      → dur=-2346317h47m53.7s(负数)→ 立即超时;
//   - raw=4611686018427387904 → dur=0s               → 立即超时;
//   - raw=1e20(float)     → int(1e20) 为负 → req.Timeout>0 假 →
//     静默改用适配器默认 600s(用户配的值**根本没生效**,且无任何告警)。
//
// 三条路径的共同点:用户配的超时**没有按自己配的值生效**,而界面与日志
// 里看不出任何异常(约定 14:静默改写用户配置与崩溃同样危险)。
//
// 校验期加上界(唯一来源:workflow/compiler/nodeconfig.go 的契约表 ——
// 约定 13 要求校验期与运行期同源);运行期的钳制由 executor 侧配合
// (见交付报告里的跨文件改法)。
//
// 错误码复用 INVALID_NODE(前端 CODE_HINTS 已有该码)。

// timeoutWorkflow 造一个带 agent 节点的工作流。
func timeoutWorkflow() *model.Workflow {
	return &model.Workflow{
		ID:   "wf-timeout",
		Name: "超时校验",
		Nodes: []model.Node{{
			ID:   "n1",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent": "claude",
				"mode":  "plan",
			},
		}},
	}
}

// TestValidateRejectsOutOfRangeTimeoutSeconds 越界的 timeout_seconds 必须
// 被拒绝(否则运行期溢出成负数/0/静默默认值)。
func TestValidateRejectsOutOfRangeTimeoutSeconds(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"溢出成负数", 10000000000},
		{"溢出成 0", int64(4611686018427387904)},
		{"float 溢出", 1e20},
		{"负数", -1},
		{"超出上界", 86401},
		{"字符串", "30"},
		{"小数", 1.5},
		{"布尔", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wf := timeoutWorkflow()
			wf.Nodes[0].Config["timeout_seconds"] = c.value
			res := Validate(wf, nil, nil)
			if res.Valid {
				t.Fatalf("timeout_seconds=%v(%s)必须校验失败(运行期会溢出)", c.value, c.name)
			}
			if !hasCode(res.Errors, "INVALID_NODE") {
				t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
			}
		})
	}
}

// TestValidateAcceptsTimeoutSecondsInRange 合法值必须**不被误报**。
//
// 特别地 **0 必须合法**:运行期用 `req.Timeout > 0` 判断"没配超时",
// 0 = 用适配器默认超时。把它判成非法就是"用户被自己的校验器挡住"
// (约定 13 的反例),也会让存量工作流存不下去。
func TestValidateAcceptsTimeoutSecondsInRange(t *testing.T) {
	for _, v := range []any{0, 1, 30, 3600, 86400} {
		wf := timeoutWorkflow()
		wf.Nodes[0].Config["timeout_seconds"] = v
		res := Validate(wf, nil, nil)
		if !res.Valid {
			t.Errorf("timeout_seconds=%v 是合法值,不得被判非法: %+v", v, res.Errors)
		}
	}
}

// TestValidateAppliesTimeoutRangeToScriptNodes script 节点同样读
// timeout_seconds(运行期 runScriptNode),上界对它一样成立。
func TestValidateAppliesTimeoutRangeToScriptNodes(t *testing.T) {
	wf := &model.Workflow{
		ID:   "wf-script-timeout",
		Name: "脚本超时校验",
		Nodes: []model.Node{{
			ID:   "s1",
			Type: model.NodeTypeScript,
			Config: map[string]any{
				"command":         "echo hi",
				"timeout_seconds": 10000000000,
			},
		}},
	}
	res := Validate(wf, nil, nil)
	if res.Valid {
		t.Fatal("script 节点的 timeout_seconds 越界也必须校验失败")
	}
}

// TestValidateAcceptsTimeoutSecondsOnScriptNodes script 节点的合法值不被误报。
func TestValidateAcceptsTimeoutSecondsOnScriptNodes(t *testing.T) {
	wf := &model.Workflow{
		ID:   "wf-script-timeout-ok",
		Name: "脚本超时校验",
		Nodes: []model.Node{{
			ID:   "s1",
			Type: model.NodeTypeScript,
			Config: map[string]any{
				"command":         "echo hi",
				"timeout_seconds": 120,
			},
		}},
	}
	res := Validate(wf, nil, nil)
	if !res.Valid {
		t.Fatalf("script 节点的合法 timeout_seconds 不得被判非法: %+v", res.Errors)
	}
}
