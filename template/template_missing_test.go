package template

import (
	"strings"
	"testing"
)

// 本文件锁住模板渲染的"缺失变量"口径(迭代 15)。
//
// 口径:**键在数据里 = 可解析**(值为 nil 时渲染成空串,不算"缺失");
// 键不在数据里 = 缺失(进 missing 列表,调用方可告警)。
//
// 为什么按"键是否存在"而不是"值是否为 nil"判定:runtime 的 contextx
// 总是把标准槽位(task / plan / iteration …)放进模板数据,上游还没产出
// 时值是 nil。此时若把 nil 当"缺失",每个引用了未产出槽位的 prompt 都会
// 刷告警(噪音),真正拼错的变量名反而被淹没 —— 告警方向反了。
//
// 这条口径由 workflow/runtime 的既有契约决定
// (TestNoWarningWhenTemplateComplete:变量齐全时不得产生噪音告警),
// template/ 与之保持一致。

// TestRenderNilValueIsResolvableButNotMissing nil 值能解析(渲染成空串),
// 但**不**算缺失(否则未产出的槽位会刷噪音告警)。
func TestRenderNilValueIsResolvableButNotMissing(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render("任务:{{task}} 拼错:{{revwie}}", map[string]any{
		"task": nil, // 上游没产出(contextx.Build 的形状)
	})
	if out != "任务: 拼错:" {
		t.Errorf("out = %q, want %q", out, "任务: 拼错:")
	}
	// 只有"键不存在"的 revwie 算缺失。
	if len(missing) != 1 || missing[0] != "revwie" {
		t.Errorf("missing = %v, want [revwie]", missing)
	}
}

// TestRenderEmptyStringIsNotMissing 空串是"有值",不算缺失。
func TestRenderEmptyStringIsNotMissing(t *testing.T) {
	e := NewEngine()
	_, missing := e.Render("x:{{v}}", map[string]any{"v": ""})
	if len(missing) != 0 {
		t.Errorf("missing = %v, want 空(空串是有值,不是缺失)", missing)
	}
}

// TestRenderZeroValueIsNotMissing 0 / false 是有值,不算缺失。
func TestRenderZeroValueIsNotMissing(t *testing.T) {
	e := NewEngine()
	_, missing := e.Render("a:{{n}} b:{{f}}", map[string]any{"n": 0, "f": false})
	if len(missing) != 0 {
		t.Errorf("missing = %v, want 空(0/false 是有值)", missing)
	}
}

// TestRenderNilVariableValueRendersEmpty nil 值渲染成空串(不 panic)。
func TestRenderNilVariableValueRendersEmpty(t *testing.T) {
	e := NewEngine()
	out, _ := e.Render("值:[{{v}}]", map[string]any{"v": nil})
	if out != "值:[]" {
		t.Errorf("out = %q, want %q", out, "值:[]")
	}
}

// TestRenderMissingVariableInVariableTable 变量表里没有的名字算缺失。
func TestRenderMissingVariableInVariableTable(t *testing.T) {
	e := NewEngine()
	_, missing := e.Render("{{variable.x}}", map[string]any{
		"variables": map[string]any{"y": 1},
	})
	if len(missing) != 1 || missing[0] != "variable.x" {
		t.Errorf("missing = %v, want [variable.x]", missing)
	}
}

// TestRenderSelfReferentialValueDoesNotRecurse 变量值里含占位符时**不**再展开
// (单遍替换):否则 a="{{b}}"、b="{{a}}" 这类环会让渲染无限递归。
func TestRenderSelfReferentialValueDoesNotRecurse(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render("{{a}}", map[string]any{
		"a": "{{b}}",
		"b": "{{a}}",
	})
	if out != "{{b}}" {
		t.Errorf("out = %q, want %q(值里的占位符不应再展开)", out, "{{b}}")
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want 空", missing)
	}
}

// TestRenderTemplateCannotReachOutsideData 模板只能读到传入的数据,
// 读不到环境变量 / 宿主文件系统。
func TestRenderTemplateCannotReachOutsideData(t *testing.T) {
	t.Setenv("SOME_SECRET_TOKEN", "sk-should-not-leak")
	e := NewEngine()
	out, missing := e.Render("v={{env.SOME_SECRET_TOKEN}} f={{.Env.SOME_SECRET_TOKEN}}", map[string]any{})
	if strings.Contains(out, "sk-should-not-leak") {
		t.Errorf("模板读到了环境变量: %q", out)
	}
	if len(missing) != 2 {
		t.Errorf("missing = %v, want 2 项", missing)
	}
}
