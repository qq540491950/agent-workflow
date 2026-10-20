package validator

import (
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住 settings.on_loop_limit 的取值白名单。
//
// 背景:on_loop_limit 只有两个合法取值(fail / wait_user),运行期靠
// `p.Loop.OnLoopLimit == "fail"` 区分两条完全不同的路径:
//   - fail     → 执行显式失败(LOOP_LIMIT)
//   - wait_user → 暂停等待人工介入
//
// 此前任何非 "fail" 的值(包括拼错的 "fl"、"abort")都落回 wait_user:
// 用户以为自己配了 fail,实际执行却在等人;拼错的取值既不报错也不告警
// (约定 14:静默降级与"该失败却成功"同族)。
//
// 现在:取值白名单在**校验期**就报出来(约定 13:校验期与运行期同源),
// 运行期对非法值 fail closed(按 fail 处理,不静默改语义)。

// loopLimitWorkflow 构造一个带 settings.on_loop_limit 的最小工作流。
func loopLimitWorkflow(onLoopLimit string) *model.Workflow {
	return &model.Workflow{
		ID:   "wf-loop",
		Name: "Loop",
		Settings: model.WorkflowSettings{
			OnLoopLimit: onLoopLimit,
		},
		Nodes: []model.Node{
			{ID: "n1", Type: model.NodeTypeParallel, Config: map[string]any{}},
		},
	}
}

// TestInvalidOnLoopLimitRejected 非法取值必须在校验期被拦下。
func TestInvalidOnLoopLimitRejected(t *testing.T) {
	for _, v := range []string{"fl", "FAIL", "wait", "abort", "true", "wait-user"} {
		res := Validate(loopLimitWorkflow(v), nil, nil)
		if res.Valid {
			t.Errorf("on_loop_limit=%q 非法,应校验失败,实际通过", v)
			continue
		}
		found := false
		for _, e := range res.Errors {
			if e.Code == "INVALID_LOOP_LIMIT" {
				found = true
			}
		}
		if !found {
			t.Errorf("on_loop_limit=%q 应报 INVALID_LOOP_LIMIT,实际错误: %+v", v, res.Errors)
		}
	}
}

// TestValidOnLoopLimitAccepted 合法取值(含缺省)必须放行。
func TestValidOnLoopLimitAccepted(t *testing.T) {
	for _, v := range []string{"", "fail", "wait_user"} {
		res := Validate(loopLimitWorkflow(v), nil, nil)
		if !res.Valid {
			t.Errorf("on_loop_limit=%q 应校验通过,实际错误: %+v", v, res.Errors)
		}
	}
}
