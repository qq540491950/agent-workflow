package runtime

// 声明式入参(inputs)的启动校验与兜底(迭代 36)。
//
// 锁住四条行为:
//  1. 未提供的入参兜 default,落到 exec.Variables(模板 {{variables.xxx}} 与
//     条件 `variables.xxx == ...` 直接可用 —— 变量宇宙不变);
//  2. 请求传入值覆盖 default(显式传入的优先级最高);
//  3. 必填未提供 → INPUT_REQUIRED(执行不启动,无副作用);
//  4. 传入值类型不符 → INPUT_TYPE_MISMATCH。
//
// 类型判定的同源点在 model.InputValueMatchesType(校验期查 default 与
// 这里查传入值必须走同一个函数,约定 13)。

import (
	"context"
	"encoding/json"
	"testing"

	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

const inputsRunYAML = `
version: "1"
workflow:
  id: inputs-run
  name: Inputs Run
inputs:
  - name: repo
    type: string
    required: true
  - name: limit
    type: number
    default: 3
  - name: dry_run
    type: boolean
    default: false
nodes:
  - id: a
    type: agent
    agent: mock-a
    mode: plan
edges: []
`

// parseInputsWorkflow 解析并转模型(引擎侧校验由 Start 自带)。
func parseInputsWorkflow(t *testing.T, yml string) *model.Workflow {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc.ToModel()
}

// assertModelErrorCode 断言错误是 model.Error 且 Code 匹配。
func assertModelErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	me, ok := err.(*model.Error)
	if !ok {
		t.Fatalf("错误应为 model.Error,实际 %T: %v", err, err)
	}
	if me.Code != code {
		t.Fatalf("错误码必须是 %s(前端按此码给出人话提示),实际 %s(%s)", code, me.Code, me.Message)
	}
}

// assertNumEquals 数值断言:int/float64 都接受 —— Start 返回的执行是
// cloneExec 快照(JSON 深拷贝),整数按约定 #2 变 float64(与既有
// variables 的出口口径一致);引擎内存里的值仍是 int。
func assertNumEquals(t *testing.T, name string, got any, want float64) {
	t.Helper()
	switch n := got.(type) {
	case int:
		if float64(n) != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	case int64:
		if float64(n) != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	case float64:
		if n != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	default:
		t.Fatalf("%s 应是数值,实际 %T(%v)", name, got, got)
	}
}

// TestInputsDefaultsAppliedAndOverrides 未提供的兜 default,传入的覆盖。
func TestInputsDefaultsAppliedAndOverrides(t *testing.T) {
	eng := newMinimalEngine(t)
	wf := parseInputsWorkflow(t, inputsRunYAML)

	// 只传必填项:limit/dry_run 兜 default
	exec, err := eng.Start(context.Background(), wf, "t", map[string]any{"repo": "/tmp/x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if exec.Variables["repo"] != "/tmp/x" {
		t.Fatalf("必填入参应取传入值,实际 %v", exec.Variables["repo"])
	}
	assertNumEquals(t, "limit", exec.Variables["limit"], 3)
	if exec.Variables["dry_run"] != false {
		t.Fatalf("dry_run 应兜 default false,实际 %v", exec.Variables["dry_run"])
	}

	// 显式传入覆盖 default
	exec2, err := eng.Start(context.Background(), wf, "t", map[string]any{"repo": "/tmp/y", "limit": 7})
	if err != nil {
		t.Fatalf("start2: %v", err)
	}
	assertNumEquals(t, "limit", exec2.Variables["limit"], 7)
}

// TestInputsRequiredMissingRejected 必填未提供 → INPUT_REQUIRED。
func TestInputsRequiredMissingRejected(t *testing.T) {
	eng := newMinimalEngine(t)
	wf := parseInputsWorkflow(t, inputsRunYAML)
	_, err := eng.Start(context.Background(), wf, "t", nil)
	if err == nil {
		t.Fatal("必填入参缺失必须拒绝启动")
	}
	assertModelErrorCode(t, err, "INPUT_REQUIRED")
}

// TestInputsTypeMismatchRejected 传入值类型不符 → INPUT_TYPE_MISMATCH。
func TestInputsTypeMismatchRejected(t *testing.T) {
	eng := newMinimalEngine(t)
	wf := parseInputsWorkflow(t, inputsRunYAML)
	for name, v := range map[string]any{"repo": 42, "limit": "3", "dry_run": "yes"} {
		_, err := eng.Start(context.Background(), wf, "t", map[string]any{"repo": "x", name: v})
		if err == nil {
			t.Fatalf("入参 %s = %#v 必须被拒", name, v)
		}
		assertModelErrorCode(t, err, "INPUT_TYPE_MISMATCH")
	}
}

// TestInputsUndeclaredKeysStillFlow 未声明的传入键照常可用(向后兼容)。
func TestInputsUndeclaredKeysStillFlow(t *testing.T) {
	eng := newMinimalEngine(t)
	wf := parseInputsWorkflow(t, `
version: "1"
workflow: {id: inputs-free, name: Inputs Free}
nodes:
  - id: a
    type: agent
    agent: mock-a
    mode: plan
`)
	exec, err := eng.Start(context.Background(), wf, "t", map[string]any{"anything": "goes"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if exec.Variables["anything"] != "goes" {
		t.Fatalf("未声明的传入键应照常进入变量表,实际 %v", exec.Variables["anything"])
	}
}

// TestInputsModelJSONRoundtrip 模型的 JSON 往返不丢声明(快照 / API 出口的口径)。
func TestInputsModelJSONRoundtrip(t *testing.T) {
	wf := parseInputsWorkflow(t, inputsRunYAML)
	raw, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back model.Workflow
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Inputs) != 3 {
		t.Fatalf("JSON 往返后应保留 3 条声明,实际 %d", len(back.Inputs))
	}
	if back.Inputs[0].Name != "repo" || !back.Inputs[0].Required {
		t.Fatalf("往返后声明不符: %+v", back.Inputs[0])
	}
}
