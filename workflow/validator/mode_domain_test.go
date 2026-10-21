package validator

import (
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"agent 节点的 mode 取值域只有一份来源,并且校验期真的会拦"。
//
// 缺口:校验期此前只查"mode 必填",**不查取值**。于是
//
//	mode: exectue   (execute 拼错)
//	mode: banana
//
// 都能通过校验 → 工作流能保存、能运行;运行期又把它当成"不是
// execute/fix/submit"的只读 mode,写权限检查被跳过,上下文链里也没有
// 对应槽位 —— 用户拼错一个 mode,既不会被拦,也不会被正确对待(约定 14)。
//
// 取值域的唯一来源:model.AllModes()(与运行期、agent.ModeRequiresWrite
// 同源,约定 13)。

// agentNodeWf 造一个"只含一个 agent 节点"的工作流(mode 拼写错误是唯一的变量)。
func agentNodeWf(mode string) *model.Workflow {
	return &model.Workflow{
		ID:   "wf-mode",
		Name: "wf-mode",
		Nodes: []model.Node{{
			ID:   "n1",
			Name: "n1",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent": "a1",
				"mode":  mode,
			},
		}},
	}
}

// TestIllegalAgentModeIsRejected 非法 mode 必须被校验期拒绝(INVALID_NODE)。
func TestIllegalAgentModeIsRejected(t *testing.T) {
	cases := []string{
		"exectue",  // execute 拼错
		"banana",   // 完全不存在的 mode
		"PLAN",     // 大小写不同
		"execute ", // 尾部多一个空格
		"skill",    // skill 是**节点类型**,不是 agent 节点的 mode
	}
	for _, mode := range cases {
		res := Validate(agentNodeWf(mode), func(string) bool { return true }, nil)
		if res.Valid {
			t.Errorf("mode=%q 应当判非法,实际 valid=true(无任何错误)", mode)
			continue
		}
		found := false
		for _, e := range res.Errors {
			if e.Code == "INVALID_NODE" && e.Node == "n1" {
				found = true
			}
		}
		if !found {
			t.Errorf("mode=%q 应报 INVALID_NODE,实际错误列表: %+v", mode, res.Errors)
		}
	}
}

// TestMissingAgentModeStillReportsMissingMode 空 mode 报 MISSING_MODE
// (而不是 INVALID_NODE):两种问题要分开,用户才知道是"没填"还是"填错了"。
func TestMissingAgentModeStillReportsMissingMode(t *testing.T) {
	res := Validate(agentNodeWf(""), func(string) bool { return true }, nil)
	if res.Valid {
		t.Fatal("空 mode 应当判非法")
	}
	var gotMissing, gotInvalid bool
	for _, e := range res.Errors {
		if e.Code == "MISSING_MODE" {
			gotMissing = true
		}
		if e.Code == "INVALID_NODE" {
			gotInvalid = true
		}
	}
	if !gotMissing {
		t.Errorf("空 mode 应报 MISSING_MODE,实际: %+v", res.Errors)
	}
	if gotInvalid {
		t.Errorf("空 mode 不应再报 INVALID_NODE(否则同一个问题报两个码),实际: %+v", res.Errors)
	}
}

// TestLegalAgentModesAreAccepted 取值域内的 mode 一律放行(约定 13 的反例:
// 校验期不得把用户挡在自己的合法工作流外面)。
func TestLegalAgentModesAreAccepted(t *testing.T) {
	for _, mode := range model.AllModes() {
		res := Validate(agentNodeWf(mode), func(string) bool { return true }, nil)
		if !res.Valid {
			t.Errorf("合法 mode=%q 被判非法: %+v", mode, res.Errors)
		}
	}
}
