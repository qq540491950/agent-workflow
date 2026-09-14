package template

import (
	"strings"
	"testing"
)

func TestRenderBasic(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render(`
Task:
{{task}}

Plan:
{{plan}}

Iteration: {{iteration}}
`, map[string]any{
		"task":       "实现登录",
		"plan":       "1. 改代码 2. 测试",
		"iteration":  2,
	})
	if strings.Contains(out, "{{task}}") {
		t.Errorf("task not replaced: %q", out)
	}
	if !strings.Contains(out, "实现登录") || !strings.Contains(out, "Iteration: 2") {
		t.Errorf("unexpected output: %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("unexpected missing: %v", missing)
	}
}

func TestRenderMissingVariables(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render("{{task}} {{git_diff}} {{review}}", map[string]any{"task": "t"})
	if out != "t  " {
		t.Errorf("missing vars should render empty: %q", out)
	}
	if len(missing) != 2 {
		t.Fatalf("missing = %v", missing)
	}
	// 排序后的缺失列表
	if missing[0] != "git_diff" || missing[1] != "review" {
		t.Errorf("missing order = %v", missing)
	}
}

func TestRenderVariablePrefix(t *testing.T) {
	e := NewEngine()
	out, _ := e.Render("max={{variable.max_review_iterations}}", map[string]any{
		"variables": map[string]any{"max_review_iterations": 5},
	})
	if out != "max=5" {
		t.Errorf("variable.xxx failed: %q", out)
	}
}

func TestRenderNestedDotPath(t *testing.T) {
	e := NewEngine()
	out, _ := e.Render("decision={{result.decision}}", map[string]any{
		"result": map[string]any{"decision": "APPROVED"},
	})
	if out != "decision=APPROVED" {
		t.Errorf("dot path failed: %q", out)
	}
}

func TestUnclosedPlaceholder(t *testing.T) {
	e := NewEngine()
	out, _ := e.Render("hello {{task", nil)
	if out != "hello {{task" {
		t.Errorf("unclosed placeholder should pass through: %q", out)
	}
}
