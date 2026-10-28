package validator

import (
	"encoding/json"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住 P2-6 的校验期一半:retry 子键的类型必须被校验。
//
// 背景:运行期此前用 toInt 读 retry.max_attempts,字符串 "3" 被当成 0 再
// 钳到 1 → **重试被静默关闭**(用户以为重试 3 次,实际只跑 1 次,节点
// 照常成功)。运行期已改成 fail closed(节点失败),校验期必须**同口径**
// 报错(约定 13:校验期与运行期同源)——
//
//   - max_attempts:必须是非负整数(YAML 整数 / JSON 整数 / json.Number);
//   - backoff:必须是字符串;
//
// 错误码复用 INVALID_NODE(前端 CODE_HINTS 已有该码 → 不用动前端,
// 约定 10 的双模式契约)。

// retryWorkflow 造一个只带 retry 配置的 agent 节点工作流。
func retryWorkflow(retry any) *model.Workflow {
	return &model.Workflow{
		ID:   "wf",
		Name: "wf",
		Nodes: []model.Node{{
			ID:   "n1",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent": "mock-a",
				"mode":  "plan",
				"retry": retry,
			},
		}},
	}
}

// errHas 判断是否给出了指定错误码的错误。
func errHas(res *Result, code string) bool {
	for _, e := range res.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

// TestRetryMaxAttemptsMustBeNonNegativeInteger max_attempts 非法必须报错。
func TestRetryMaxAttemptsMustBeNonNegativeInteger(t *testing.T) {
	bad := []struct {
		name string
		val  any
	}{
		{"字符串", "3"},
		{"小数", 3.5},
		{"负数", -1},
		{"布尔", true},
		{"null", nil},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(retryWorkflow(map[string]any{"max_attempts": tc.val}), nil, nil)
			if res.Valid {
				t.Fatalf("max_attempts = %#v: 校验必须不通过(运行期会 fail closed)", tc.val)
			}
			if !errHas(res, "INVALID_NODE") {
				t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
			}
		})
	}
}

// TestRetryMaxAttemptsAcceptsIntegerForms 合法数值形态必须放行。
func TestRetryMaxAttemptsAcceptsIntegerForms(t *testing.T) {
	good := []struct {
		name string
		val  any
	}{
		{"int", 3},
		{"int64", int64(3)},
		{"uint64(YAML 整数)", uint64(3)},
		{"float64(JSON 往返)", float64(3)},
		{"json.Number", json.Number("3")},
		{"0(与缺省同义)", 0},
	}
	for _, tc := range good {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(retryWorkflow(map[string]any{"max_attempts": tc.val}), nil, nil)
			if !res.Valid {
				t.Errorf("max_attempts = %#v: 校验必须通过,实际错误: %+v", tc.val, res.Errors)
			}
		})
	}
}

// TestRetryMustBeObject retry 不是对象时必须报错。
func TestRetryMustBeObject(t *testing.T) {
	res := Validate(retryWorkflow("3"), nil, nil)
	if res.Valid {
		t.Fatal("retry 不是对象时校验必须不通过")
	}
	if !errHas(res, "INVALID_NODE") {
		t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
	}
}

// TestRetryBackoffMustBeString backoff 类型错必须报错。
func TestRetryBackoffMustBeString(t *testing.T) {
	res := Validate(retryWorkflow(map[string]any{"backoff": 3}), nil, nil)
	if res.Valid {
		t.Fatal("backoff 不是字符串时校验必须不通过")
	}
	if !errHas(res, "INVALID_NODE") {
		t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
	}
}

// TestRetryConfigValidFormsStayValid 合法写法不得被误伤(向后兼容)。
func TestRetryConfigValidFormsStayValid(t *testing.T) {
	for _, retry := range []any{
		map[string]any{},
		map[string]any{"max_attempts": 3, "backoff": "fixed"},
		map[string]any{"max_attempts": 3, "backoff": "exponential"},
	} {
		res := Validate(retryWorkflow(retry), nil, nil)
		if !res.Valid {
			t.Errorf("retry = %#v: 校验必须通过,实际错误: %+v", retry, res.Errors)
		}
	}
}

// scriptRetryWorkflow 造一个只带 retry 配置的 script 节点工作流。
//
// script 节点自迭代 36 起与 agent 节点共用同一份 retry 契约
// (compiler.nodeConfigSpecs 的 SubKeys["retry"]),子键类型校验必须
// 同口径 —— 两类节点是同一个校验函数(validateRetrySubKeys),
// 这里用例的价值是**钉住契约表**:哪天有人把 script 的 SubKeys 拆掉,
// 这些用例立刻变红(约定 13:校验期与运行期同源)。
func scriptRetryWorkflow(retry any) *model.Workflow {
	return &model.Workflow{
		ID:   "wf-script",
		Name: "wf-script",
		Nodes: []model.Node{{
			ID:   "s1",
			Type: model.NodeTypeScript,
			Config: map[string]any{
				"command": "echo hi",
				"retry":   retry,
			},
		}},
	}
}

// TestScriptRetrySubKeysSameAsAgent script 节点的 retry 子键口径与 agent 一致。
func TestScriptRetrySubKeysSameAsAgent(t *testing.T) {
	cases := []struct {
		name   string
		retry  any
		wantOK bool
	}{
		{"字符串 max_attempts", map[string]any{"max_attempts": "3"}, false},
		{"负数 max_attempts", map[string]any{"max_attempts": -1}, false},
		{"数值 backoff", map[string]any{"backoff": 3}, false},
		{"retry 非对象", "3", false},
		{"合法 fixed", map[string]any{"max_attempts": 3, "backoff": "fixed"}, true},
		{"合法 exponential", map[string]any{"max_attempts": 3, "backoff": "exponential"}, true},
		{"空对象(全缺省)", map[string]any{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(scriptRetryWorkflow(tc.retry), nil, nil)
			if res.Valid != tc.wantOK {
				t.Fatalf("retry = %#v: 期望 valid=%v,实际 %v(错误: %+v)",
					tc.retry, tc.wantOK, res.Valid, res.Errors)
			}
			if !tc.wantOK && !errHas(res, "INVALID_NODE") {
				t.Errorf("错误码必须是 INVALID_NODE,实际: %+v", res.Errors)
			}
		})
	}
}
