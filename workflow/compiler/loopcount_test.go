package compiler

import (
	"encoding/json"
	"testing"
)

// 回归:状态经 SQLite JSON 持久化往返后,int 变 float64。
// 此前 loopResetter/syncNodeStates 只断言 int,恢复后循环计数回落,
// 触发过早的循环保护(报告:ADK 用法审计,2026-09)。
func TestIntOfOkJSONRoundTrip(t *testing.T) {
	// 模拟持久化往返
	var roundTripped map[string]any
	raw, err := json.Marshal(map[string]any{"loop": 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}

	v := roundTripped["loop"]
	if _, isInt := v.(int); isInt {
		t.Fatal("expected int to become float64 after JSON round trip")
	}
	got, ok := IntOfOk(v)
	if !ok || got != 3 {
		t.Fatalf("IntOfOk(float64(3)) = %d, %v; want 3, true", got, ok)
	}
}

func TestIntOfOkTypes(t *testing.T) {
	cases := []struct {
		v    any
		want int
		ok   bool
	}{
		{7, 7, true},
		{float64(7), 7, true},
		{int64(7), 7, true},
		{"7", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := IntOfOk(c.v)
		if got != c.want || ok != c.ok {
			t.Errorf("IntOfOk(%#v) = %d, %v; want %d, %v", c.v, got, ok, c.want, c.ok)
		}
	}
}

// fakeState 是 StateAccess 的内存实现(测试专用)。
type fakeState struct{ data map[string]any }

func (f *fakeState) Get(key string) (any, bool) {
	v, ok := f.data[key]
	return v, ok
}

func (f *fakeState) Set(key string, val any) error {
	f.data[key] = val
	return nil
}

// iteration 变量用于条件表达式({{iteration}}/iteration > 2),
// 恢复后必须继续读到正确轮数。
func TestEvalConditionIterationAfterRoundTrip(t *testing.T) {
	var roundTripped map[string]any
	raw, _ := json.Marshal(map[string]any{KeyLoopCount: 2})
	_ = json.Unmarshal(raw, &roundTripped)

	st := &fakeState{data: roundTripped}
	ok, err := EvalCondition("iteration >= 2", st, nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !ok {
		t.Error("iteration >= 2 should be true after JSON round trip (loop count preserved)")
	}
}
