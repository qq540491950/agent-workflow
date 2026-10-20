// 本文件是 model 包的**外部测试包**(package model_test):
// 它需要引用 workflow/compiler 的 IntOfOk(约定的唯一状态数值读取入口),
// 而 compiler 依赖 model —— 同包测试会造成导入环,故用外部测试包。
package model_test

import (
	"encoding/json"
	"testing"

	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// TestStateDataIntRoundTripReadPath 锁住约定 2 的正确读法:
//
//	state_json 往返后整数是 float64(不是 int),读取必须用
//	compiler.IntOfOk(双类型兼容),或先在解码边界 model.NormalizeStateJSON
//	归一。直接 v.(int) 断言会 panic —— 这是历史上"循环计数回落"的根因。
func TestStateDataIntRoundTripReadPath(t *testing.T) {
	exec := &model.Execution{
		ID:    "exec-1",
		State: model.ExecutionRunning,
		StateData: map[string]any{
			compiler.KeyLoopCount: 3,
			"score":               1.5,
		},
	}

	raw, err := json.Marshal(exec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := &model.Execution{}
	if err := json.Unmarshal(raw, got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 陷阱:整数已变成 float64
	if _, ok := got.StateData[compiler.KeyLoopCount].(float64); !ok {
		t.Fatalf("往返后应为 float64,实际 %T", got.StateData[compiler.KeyLoopCount])
	}
	// 正确读法一:compiler.IntOfOk
	n, ok := compiler.IntOfOk(got.StateData[compiler.KeyLoopCount])
	if !ok || n != 3 {
		t.Fatalf("IntOfOk 读取失败: n=%d ok=%v", n, ok)
	}
	// 正确读法二:解码边界归一
	model.NormalizeStateJSON(got.StateData)
	if v, ok := got.StateData[compiler.KeyLoopCount].(int); !ok || v != 3 {
		t.Fatalf("归一后应为 int 3,实际 %v(%T)", v, got.StateData[compiler.KeyLoopCount])
	}
	// 带小数的值保持浮点语义
	if v, ok := got.StateData["score"].(float64); !ok || v != 1.5 {
		t.Fatalf("score 应保持 float64 1.5,实际 %v(%T)", v, got.StateData["score"])
	}
}
