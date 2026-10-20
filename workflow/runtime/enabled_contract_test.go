package runtime

import (
	"testing"

	"agentworkflow/workflow/model"
	"agentworkflow/workflow/validator"
)

// 本文件锁住"节点级开关 enabled"的**校验期与运行期结论必须一致**
// (AGENTS.md 硬性约定 #13:校验期与运行期必须同源)。
//
// 为什么值得单独锁:enabled 的解析一旦两边各写一份,漂移方向决定后果 ——
//
//   - 校验器放行、运行期 fail closed → 用户保存成功、运行时节点失败,
//     失败原因藏在执行详情里,用户完全想不到是 enabled 写错了;
//   - 校验器拦下、运行期放行 → 明明能跑的工作流保存不了(用户被自己的
//     校验器挡住)。
//
// 字符串形态("false" / "true")是最容易漂移的取值:JSON/YAML 里手写引号
// 是常事,而字符串 "false" 与布尔 false 完全是两回事 —— 若运行期按字符串
// 解析、校验器只认布尔,就会出现"用户写了 enabled: "false" 以为关掉了
// 节点,校验器报错说不是布尔,运行期却按 false 跳过"的分裂语义。
//
// 本测试**真实调用** validator.Validate 与运行期的 nodeEnabled,
// 断言两边的结论逐项相同 —— 不是复述规则,而是对照两个实现。

// TestEnabledConclusionMatchesValidator enabled 的校验期与运行期结论逐项一致。
func TestEnabledConclusionMatchesValidator(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		// wantEnabled:合法布尔时的运行期结论(true=执行,false=跳过)。
		wantEnabled bool
		// wantInvalid:取值非法时的结论(运行期报错 / 校验期报 INVALID_NODE_ENABLED)。
		wantInvalid bool
	}{
		{"缺省", map[string]any{}, true, false},
		{"布尔 true", map[string]any{"enabled": true}, true, false},
		{"布尔 false", map[string]any{"enabled": false}, false, false},
		{"字符串 false", map[string]any{"enabled": "false"}, false, true},
		{"字符串 true", map[string]any{"enabled": "true"}, false, true},
		{"大写字符串 FALSE", map[string]any{"enabled": "FALSE"}, false, true},
		{"数字 0", map[string]any{"enabled": 0}, false, true},
		{"数字 1", map[string]any{"enabled": 1}, false, true},
		{"nil", map[string]any{"enabled": nil}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 运行期结论
			node := &model.Node{ID: "n1", Type: model.NodeTypeParallel, Config: tc.cfg}
			enabled, err := nodeEnabled(node)
			rtInvalid := err != nil
			if rtInvalid != tc.wantInvalid {
				t.Errorf("运行期结论:非法=%v,want %v(err=%v)", rtInvalid, tc.wantInvalid, err)
			}
			if !rtInvalid && enabled != tc.wantEnabled {
				t.Errorf("运行期结论:enabled=%v,want %v", enabled, tc.wantEnabled)
			}

			// 校验期结论(真实调用校验器)
			wf := &model.Workflow{
				ID:   "wf-1",
				Name: "Demo",
				Nodes: []model.Node{
					{ID: "n1", Type: model.NodeTypeParallel, Config: tc.cfg},
				},
			}
			res := validator.Validate(wf, nil, nil)
			valInvalid := false
			for _, e := range res.Errors {
				if e.Code == "INVALID_NODE_ENABLED" {
					valInvalid = true
				}
			}
			if valInvalid != tc.wantInvalid {
				t.Errorf("校验期结论:INVALID_NODE_ENABLED=%v,want %v(错误: %+v)", valInvalid, tc.wantInvalid, res.Errors)
			}

			// 核心断言:两边的结论必须一致。
			if rtInvalid != valInvalid {
				t.Errorf("校验期与运行期结论不一致:运行期判非法=%v,校验期报 INVALID_NODE_ENABLED=%v", rtInvalid, valInvalid)
			}
		})
	}
}

// TestEnabledInvalidFailsClosed 非法取值必须 fail closed(节点失败),
// 而不是"当作启用照常执行" —— 用户以为关掉了节点,实际却执行了副作用
// (写文件/跑测试/提交代码),这属于约定 14 的"该失败却成功"。
//
// 这条同时锁住 compiler 侧注释与实现的语义一致:注释若再写"非布尔值会被
// 忽略,节点仍然执行",下一个维护者会照着它把 fail closed 改回 fail open。
func TestEnabledInvalidFailsClosed(t *testing.T) {
	node := &model.Node{ID: "n1", Type: model.NodeTypeParallel, Config: map[string]any{
		"enabled": "false",
	}}
	enabled, err := nodeEnabled(node)
	if err == nil {
		t.Fatalf("enabled 为字符串时必须报错(fail closed),实际 enabled=%v 无错", enabled)
	}
	if enabled {
		t.Error("enabled 为非法值时不得放行节点")
	}
}
