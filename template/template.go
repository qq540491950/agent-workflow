// Package template 实现 Agent Prompt 的模板引擎。
// Agent Node 的 Prompt 不写死在 Go 代码中,而是通过 DSL 配置模板,
// 执行时注入最小化的上下文变量。
package template

import (
	"fmt"
	"sort"
	"strings"
)

// Engine 渲染 {{var}} 风格的模板。
type Engine struct{}

// NewEngine 创建模板引擎。
func NewEngine() *Engine { return &Engine{} }

// 支持的标准变量。
const (
	VarTask       = "task"
	VarPlan       = "plan"
	VarGitDiff    = "git_diff"
	VarReview     = "review"
	VarTestResult = "test_result"
	VarIteration  = "iteration"
)

// Render 将 data 中的变量替换进模板。
// 支持 {{task}} {{plan}} {{git_diff}} {{review}} {{test_result}}
// {{iteration}} 以及 {{variable.xxx}} 形式。
// 未定义的变量渲染为空串(并在返回值中报告缺失变量名)。
func (e *Engine) Render(tpl string, data map[string]any) (string, []string) {
	var missing []string
	seen := map[string]bool{}

	out := renderAll(tpl, func(key string) string {
		v, ok := lookup(key, data)
		if !ok {
			if !seen[key] {
				seen[key] = true
				missing = append(missing, key)
			}
			return ""
		}
		return toString(v)
	})

	sort.Strings(missing)
	return out, missing
}

// MustRender 渲染并忽略缺失变量(用于日志等非关键场景)。
func (e *Engine) MustRender(tpl string, data map[string]any) string {
	out, _ := e.Render(tpl, data)
	return out
}

// lookup 按 key 查找变量,支持 variable.xxx 前缀与直接变量名。
func lookup(key string, data map[string]any) (any, bool) {
	if strings.HasPrefix(key, "variable.") {
		vars, ok := data["variables"].(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := vars[strings.TrimPrefix(key, "variable.")]
		return v, ok
	}
	// 扁平查找(点路径,如 result.decision)
	if v, ok := data[key]; ok {
		return v, true
	}
	parts := strings.Split(key, ".")
	cur := any(data)
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// renderAll 扫描模板中的 {{key}} 占位符并调用替换函数。
func renderAll(tpl string, repl func(key string) string) string {
	var b strings.Builder
	for {
		start := strings.Index(tpl, "{{")
		if start < 0 {
			b.WriteString(tpl)
			return b.String()
		}
		end := strings.Index(tpl[start:], "}}")
		if end < 0 {
			b.WriteString(tpl)
			return b.String()
		}
		end += start
		b.WriteString(tpl[:start])
		key := strings.TrimSpace(tpl[start+2 : end])
		b.WriteString(repl(key))
		tpl = tpl[end+2:]
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}
