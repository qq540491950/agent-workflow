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

// matchBranch:先匹配有条件分支,无条件分支仅作兜底;全不匹配返回 false。
func TestMatchBranch(t *testing.T) {
	l := &lowerer{env: &RunEnv{Vars: map[string]any{}}}
	branches := []RouteBranch{
		{Condition: `decision == "APPROVED"`},
		{Condition: `decision == "REJECTED"`},
		{},
	}

	st := &fakeState{data: map[string]any{KeyDecision: "REJECTED"}}
	idx, ok := l.matchBranch(branches, st)
	if !ok || idx != 1 {
		t.Fatalf("REJECTED → idx=%d ok=%v, want 1 true", idx, ok)
	}

	st = &fakeState{data: map[string]any{KeyDecision: "APPROVED"}}
	idx, ok = l.matchBranch(branches, st)
	if !ok || idx != 0 {
		t.Fatalf("APPROVED → idx=%d ok=%v, want 0 true", idx, ok)
	}

	// 未知决策:条件全不成立 → 兜底无条件分支
	st = &fakeState{data: map[string]any{KeyDecision: "WHATEVER"}}
	idx, ok = l.matchBranch(branches, st)
	if !ok || idx != 2 {
		t.Fatalf("fallback → idx=%d ok=%v, want 2 true", idx, ok)
	}
}

// EvalCondition 支持 decision/status/output 字符串比较与 iteration 数值比较。
func TestEvalConditionVariants(t *testing.T) {
	st := &fakeState{data: map[string]any{
		KeyDecision:  "APPROVED",
		KeyStatus:    "success",
		KeyOutput:    "done",
		KeyLoopCount: float64(3),
	}}
	cases := []struct {
		expr string
		want bool
	}{
		{`decision == "APPROVED"`, true},
		{`decision != "REJECTED"`, true},
		{`status == "success" && iteration >= 2`, true},
		{`output contains "on"`, true},
		{`iteration > 5`, false},
	}
	for _, c := range cases {
		got, err := EvalCondition(c.expr, st, nil)
		if err != nil {
			t.Fatalf("eval %q: %v", c.expr, err)
		}
		if got != c.want {
			t.Errorf("eval %q = %v, want %v", c.expr, got, c.want)
		}
	}
	// 变量注入
	st2 := &fakeState{data: map[string]any{}}
	got, err := EvalCondition(`variables.level == "high"`, st2, map[string]any{"level": "high"})
	if err != nil || !got {
		t.Errorf("variables condition = %v, %v", got, err)
	}
}
