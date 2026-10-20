package validator

import (
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"工作流级 permissions 段"的结构校验。
//
// 背景:DSL 的 permissions 段(需求 §18)形如
//
//	permissions:
//	  pi-agent:
//	    filesystem: {read: true, write: false}
//	    git:        {read: true, commit: false, push: false}
//
// 结构非法时(段名拼错、字段值不是布尔、值不是映射),运行期会把该 Agent
// 的权限**全拒**(fail closed)—— 用户看到的失败原因是"agent 无权执行
// fs:write",真正的原因"permissions 写错了"被掩盖,排查方向被带偏。
//
// 校验期必须用**同一份**解析逻辑把问题报出来(约定 #13:校验期与运行期
// 同源):permission.ValidateOverrides 与运行期的 ApplyChecked 共用
// applyAgentOverrides,两边对同一份 DSL 的结论必然一致。

// permWorkflow 构造一个只含 permissions 段的工作流(节点类型无必填配置,
// 避免与本用例要测的错误码混在一起)。
func permWorkflow(perm map[string]any) *model.Workflow {
	return &model.Workflow{
		ID:         "wf-1",
		Name:       "Perm",
		Permission: perm,
		Nodes: []model.Node{
			{ID: "n1", Type: model.NodeTypeParallel, Config: map[string]any{}},
		},
	}
}

// TestInvalidPermissionOverrideRejected 非法 permissions 结构必须被校验期拦下。
func TestInvalidPermissionOverrideRejected(t *testing.T) {
	cases := []struct {
		name string
		perm map[string]any
	}{
		{"字段值不是布尔", map[string]any{
			"pi-agent": map[string]any{"filesystem": map[string]any{"write": "no"}},
		}},
		{"段名拼错", map[string]any{
			"pi-agent": map[string]any{"filesystemz": map[string]any{"write": false}},
		}},
		{"字段名拼错", map[string]any{
			"pi-agent": map[string]any{"filesystem": map[string]any{"writ": false}},
		}},
		{"值不是映射", map[string]any{
			"pi-agent": "read-only",
		}},
		{"git 段不是映射", map[string]any{
			"pi-agent": map[string]any{"git": []string{"read"}},
		}},
		{"git 字段名拼错", map[string]any{
			"pi-agent": map[string]any{"git": map[string]any{"comit": false}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(permWorkflow(tc.perm), nil, nil)
			if res.Valid {
				t.Fatalf("非法 permissions 结构应校验失败,实际通过: %+v", res.Errors)
			}
			found := false
			for _, e := range res.Errors {
				if e.Code == "INVALID_PERMISSION_OVERRIDE" {
					found = true
				}
			}
			if !found {
				t.Errorf("应报 INVALID_PERMISSION_OVERRIDE,实际错误: %+v", res.Errors)
			}
		})
	}
}

// TestValidPermissionOverrideAccepted 合法 permissions 结构必须放行
// (不能因为新增校验把正常路径也堵死)。
func TestValidPermissionOverrideAccepted(t *testing.T) {
	res := Validate(permWorkflow(map[string]any{
		"pi-agent": map[string]any{
			"filesystem": map[string]any{"read": true, "write": false},
			"git":        map[string]any{"read": true, "commit": false, "push": false},
		},
		"claude-code": map[string]any{
			"filesystem": map[string]any{"read": true},
		},
	}), nil, nil)
	if !res.Valid {
		t.Fatalf("合法 permissions 结构应校验通过,实际错误: %+v", res.Errors)
	}
}

// TestEmptyPermissionOverrideAccepted 未配置 permissions 时不得报错。
func TestEmptyPermissionOverrideAccepted(t *testing.T) {
	for _, perm := range []map[string]any{nil, {}} {
		res := Validate(permWorkflow(perm), nil, nil)
		if !res.Valid {
			t.Errorf("未配置 permissions 时不应报错,实际: %+v", res.Errors)
		}
	}
}
