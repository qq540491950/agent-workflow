package compiler

import (
	"testing"
)

// 本文件锁定条件表达式"可用变量集合"的唯一定义(AGENTS.md 硬性约定 #13:
// 校验期与运行期必须同源)。
//
// 此前运行期(lower.go)与校验期(workflow/validator)各写了一份 env 字面量,
// 靠人工保持一致:一侧新增变量,另一侧就会把用户合法的表达式判成非法
// (用户被自己的校验器挡住),或反过来放行一个运行期没有的变量(条件恒假)。

// TestConditionEnvKeysAreStable 钉死可用变量集合。
//
// 新增变量时必须显式改这里 —— 目的是"新增要 conscious",而不是让两侧
// 悄悄漂移。
func TestConditionEnvKeysAreStable(t *testing.T) {
	want := []string{"decision", "status", "output", "iteration", "variables"}
	got := ConditionEnvKeys()
	if len(got) != len(want) {
		t.Fatalf("条件表达式可用变量 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("条件表达式可用变量[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestConditionEnvStaticAndRuntimeShapesMatch 静态环境(校验期编译表达式用)
// 与运行期环境(从状态取值)的键集合必须完全一致。
//
// 形状不一致的后果:校验期放行/拒绝的表达式与运行期实际能求值的不是一回事
// —— 前者让用户存下永远为假的条件,后者把合法条件判成非法。
func TestConditionEnvStaticAndRuntimeShapesMatch(t *testing.T) {
	st := &fakeState{data: map[string]any{
		KeyDecision:  "APPROVED",
		KeyStatus:    "success",
		KeyOutput:    "done",
		KeyLoopCount: float64(2),
	}}
	runtimeEnv := buildConditionEnv(st, map[string]any{"env": "prod"}, "")
	staticEnv := NewConditionEnv(nil)

	keys := ConditionEnvKeys()
	if len(keys) != len(runtimeEnv) {
		t.Errorf("运行期环境键数 = %d, want %d(%v)", len(runtimeEnv), len(keys), keys)
	}
	if len(keys) != len(staticEnv) {
		t.Errorf("静态环境键数 = %d, want %d(%v)", len(staticEnv), len(keys), keys)
	}
	for _, k := range keys {
		if _, ok := runtimeEnv[k]; !ok {
			t.Errorf("运行期环境缺少键 %q", k)
		}
		if _, ok := staticEnv[k]; !ok {
			t.Errorf("静态环境缺少键 %q", k)
		}
	}
	// 运行期环境必须真的从状态取值(否则 conditionEnvVars 的 stateKey 写错了)。
	if runtimeEnv["decision"] != "APPROVED" {
		t.Errorf("decision = %v, want APPROVED", runtimeEnv["decision"])
	}
	if runtimeEnv["iteration"] != 2 {
		t.Errorf("iteration = %v, want 2", runtimeEnv["iteration"])
	}
	// variables 归一成 map(两侧形状一致:nil 与空 map 不混用)。
	vars, ok := runtimeEnv["variables"].(map[string]any)
	if !ok {
		t.Fatalf("运行期 variables = %T, want map[string]any", runtimeEnv["variables"])
	}
	if vars["env"] != "prod" {
		t.Errorf("variables.env = %v, want prod", vars["env"])
	}
	empty, ok := staticEnv["variables"].(map[string]any)
	if !ok {
		t.Fatalf("静态 variables = %T, want map[string]any", staticEnv["variables"])
	}
	if len(empty) != 0 {
		t.Errorf("静态 variables 应为空 map, got %v", empty)
	}
}

// TestConditionEnvNilVarsNormalized nil 变量表必须归一成空 map,
// 不能一侧是 nil 另一侧是空 map(expr 对两者的处理相同,但形状必须一致)。
func TestConditionEnvNilVarsNormalized(t *testing.T) {
	env := NewConditionEnv(nil)
	vars, ok := env["variables"].(map[string]any)
	if !ok {
		t.Fatalf("variables = %T, want map[string]any", env["variables"])
	}
	if vars == nil {
		t.Error("variables 不应为 nil map")
	}
	st := &fakeState{data: map[string]any{}}
	rt := buildConditionEnv(st, nil, "")
	rtVars, ok := rt["variables"].(map[string]any)
	if !ok || rtVars == nil {
		t.Errorf("运行期 variables = %v(%T), want 非 nil map[string]any", rt["variables"], rt["variables"])
	}
}

// TestEvalConditionUsesSharedEnv 运行期求值用的就是那份共享定义。
func TestEvalConditionUsesSharedEnv(t *testing.T) {
	st := &fakeState{data: map[string]any{KeyDecision: "APPROVED", KeyLoopCount: 2}}
	for _, k := range ConditionEnvKeys() {
		expr := "variables.missing == nil"
		switch k {
		case "decision":
			expr = `decision == "APPROVED"`
		case "status":
			expr = `status == ""`
		case "output":
			expr = `output == ""`
		case "iteration":
			expr = `iteration == 2`
		case "variables":
			expr = `variables.missing == nil`
		}
		ok, err := EvalCondition(expr, st, map[string]any{})
		if err != nil {
			t.Errorf("求值 %q 失败: %v", expr, err)
			continue
		}
		if !ok {
			t.Errorf("求值 %q = false, want true", expr)
		}
	}
}
