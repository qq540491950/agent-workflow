package runtime

import (
	"context"
	"fmt"
	"testing"

	"agentworkflow/event"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 回归(#256):HITL 的 responses 只认 []any 一种形态。
//
// 此前 workflow/runtime/executor.go 是:
//
//	responses, _ := node.Config["responses"].([]any)
//
// 值是 []string 时断言失败、被 `_` 吞掉 → 用户配置的选项列表
// **静默丢弃**、回落到默认四项(approve/reject/continue/instruction),
// 全程无报错(约定 14 同族:该用却没用 = 静默降级)。
//
// 与 cfgFloat 同一族(数值要认全所有形态):配置值的类型随来源而变 ——
// YAML/JSON 解析出 []any,程序化构造(导入器/嵌入方/测试)是 []string。
//
// 现状说明(实测):引擎跑的是 exec.Snapshot(JSON 往返后的副本),
// 所以经 Engine.Start 的路径上 responses 总是 []any —— 断言单态
// 在**引擎路径上不可达**。但 runHumanNode 是导出的执行单元,配置读取
// 口径应当与 cfgFloat 一致(认全所有形态),否则换一个调用方就静默降级。
func TestHumanNodeResponsesAcceptsBothSliceShapes(t *testing.T) {
	eng := newMinimalEngine(t)
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-1"},
		Bus:  event.NewBus(),
		Vars: map[string]any{},
	}
	st := &fakeState{m: map[string]any{}}

	cases := []struct {
		name string
		val  any
		want []string
	}{
		{"[]any(解析产物)", []any{"approve", "reject"}, []string{"approve", "reject"}},
		{"[]string(程序化构造)", []string{"approve", "reject"}, []string{"approve", "reject"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &model.Node{
				ID: "gate", Name: "Gate", Type: model.NodeTypeHuman,
				Config: map[string]any{"prompt": "请审批", "responses": tc.val},
			}
			out := eng.runHumanNode(context.Background(), env, node, st)
			if out.State != model.NodeWaiting {
				t.Fatalf("人工闸门应暂停,实际状态 %s", out.State)
			}
			raw, ok := out.Data["responses"]
			if !ok {
				t.Fatalf("outcome 缺少 responses: %v", out.Data)
			}
			got := toStringSlice(raw)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("responses = %v, 期望 %v(用户配置的选项被静默丢弃了)", got, tc.want)
			}
		})
	}
}

// toStringSlice 把任意切片形态转成 []string(测试断言用)。
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}
