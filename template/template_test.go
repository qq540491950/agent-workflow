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
		"task":      "实现登录",
		"plan":      "1. 改代码 2. 测试",
		"iteration": 2,
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

// panicStringer 的 String() 必然 panic(模拟第三方类型的崩溃)。
type panicStringer struct{}

func (panicStringer) String() string { panic("stringer boom") }

// nilStringer 是 nil 指针接收者:调用 String() 会解引用 nil 而 panic。
type nilStringer struct{ value string }

func (n *nilStringer) String() string { return n.value }

// 回归:模板渲染是明确的 panic 风险路径(约定 12),不能依赖上层 recover。
// 此前 toString 直接调用 t.String(),变量里的 Stringer 一崩就把整个
// 执行协程带下去。
func TestRenderSurvivesPanickingStringer(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render("v={{value}}", map[string]any{"value": panicStringer{}})
	if strings.Contains(out, "boom") {
		t.Errorf("panic 信息不应进入渲染结果: %q", out)
	}
	if !strings.Contains(out, "<unrenderable>") {
		t.Errorf("期望占位文本: %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("变量存在,不应报缺失: %v", missing)
	}
}

func TestRenderSurvivesNilStringer(t *testing.T) {
	e := NewEngine()
	out, _ := e.Render("v={{value}}", map[string]any{"value": (*nilStringer)(nil)})
	if !strings.Contains(out, "<unrenderable>") {
		t.Errorf("nil Stringer 期望占位文本: %q", out)
	}
}

// RenderErr 是带错误返回的入口:渲染期 panic 被兜底成 error。
func TestRenderErrReportsPanic(t *testing.T) {
	e := NewEngine()
	out, _, err := e.RenderErr("v={{value}}", map[string]any{"value": panicStringer{}})
	if err != nil {
		t.Fatalf("Stringer panic 不应导致整体失败(已就地兜底): %v", err)
	}
	if !strings.Contains(out, "<unrenderable>") {
		t.Errorf("期望占位文本: %q", out)
	}
}

// 畸形模板不得 panic(未闭合、嵌套、纯符号、深度路径)。
func TestRenderMalformedTemplateNoPanic(t *testing.T) {
	e := NewEngine()
	cases := []string{
		"{{",
		"{{}}",
		"{{{{task}}}}",
		"{{ task ",
		"{{a.b.c.d.e.f.g.h}}",
		"{{变量}}",
		strings.Repeat("{{x}}", 100),
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			out, _ := e.Render(c, map[string]any{"task": "t"})
			_ = out
		})
	}
}

// 变量表是 map[string]string 时不能被静默当成"缺失"。
func TestRenderVariablePrefixWithStringMap(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render("max={{variable.max_review_iterations}}", map[string]any{
		"variables": map[string]string{"max_review_iterations": "5"},
	})
	if out != "max=5" {
		t.Errorf("map[string]string 变量表应能取到值: %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("不应报缺失: %v", missing)
	}
}

// 点路径穿过 map[string]string 也要能取到值。
func TestRenderDotPathThroughStringMap(t *testing.T) {
	e := NewEngine()
	out, missing := e.Render("d={{result.decision}}", map[string]any{
		"result": map[string]string{"decision": "APPROVED"},
	})
	if out != "d=APPROVED" {
		t.Errorf("点路径应能穿过 map[string]string: %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("不应报缺失: %v", missing)
	}
}

// 模板不得读到环境变量(注入面)。
func TestRenderCannotReadEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env-secret")
	e := NewEngine()
	out, missing := e.Render("k={{env.ANTHROPIC_API_KEY}}", map[string]any{})
	if strings.Contains(out, "sk-env-secret") {
		t.Errorf("模板读到了环境变量: %q", out)
	}
	if len(missing) != 1 || missing[0] != "env.ANTHROPIC_API_KEY" {
		t.Errorf("应报告缺失变量: %v", missing)
	}
}

// 变量值里的占位符不再展开(避免二次注入)。
func TestRenderDoesNotExpandValuePlaceholders(t *testing.T) {
	e := NewEngine()
	out, _ := e.Render("{{a}}", map[string]any{"a": "{{task}}", "task": "SHOULD-NOT-APPEAR"})
	if strings.Contains(out, "SHOULD-NOT-APPEAR") {
		t.Errorf("变量值里的占位符被二次展开: %q", out)
	}
	if out != "{{task}}" {
		t.Errorf("变量值应原样渲染: %q", out)
	}
}
