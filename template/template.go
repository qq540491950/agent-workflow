// Package template 实现 Agent Prompt 的模板引擎。
// Agent Node 的 Prompt 不写死在 Go 代码中,而是通过 DSL 配置模板,
// 执行时注入最小化的上下文变量。
package template

import (
	"fmt"
	"sort"
	"strings"

	"agentworkflow/logx"
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
//
// 渲染过程不会 panic:模板引擎自己兜底(约定 12 —— 模板渲染是明确的
// panic 风险路径,不能依赖上层的 recover)。变量里的 Stringer 即使
// 自身 panic,也只是渲染成占位文本。
func (e *Engine) Render(tpl string, data map[string]any) (string, []string) {
	out, missing, err := e.RenderErr(tpl, data)
	if err != nil {
		// 兜底失败:回退为原始模板(让节点拿到可读的提示而不是空串),
		// 并留下 WARN 记录(不静默)。
		logx.Warn("模板渲染失败,已回退为原始模板", "error", err)
		return tpl, missing
	}
	return out, missing
}

// RenderErr 是带错误返回的渲染入口:渲染期的 panic 被兜底成 error,
// 而不是向上抛(调用方想知道"渲染失败"时应使用它)。
func (e *Engine) RenderErr(tpl string, data map[string]any) (out string, missing []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = tpl
			missing = nil
			err = fmt.Errorf("模板渲染 panic: %v", r)
		}
	}()
	var miss []string
	seen := map[string]bool{}

	out = renderAll(tpl, func(key string) string {
		v, ok := lookup(key, data)
		if !ok {
			if !seen[key] {
				seen[key] = true
				miss = append(miss, key)
			}
			return ""
		}
		return toString(v)
	})

	sort.Strings(miss)
	return out, miss, nil
}

// MustRender 渲染并忽略缺失变量(用于日志等非关键场景)。
func (e *Engine) MustRender(tpl string, data map[string]any) string {
	out, _ := e.Render(tpl, data)
	return out
}

// lookup 按 key 查找变量,支持 variable.xxx 前缀与直接变量名。
//
// 口径:**键在数据里 = 可解析**(值为 nil 时渲染成空串,不算"缺失");
// 键不在数据里 = 缺失(进 missing 列表,调用方可告警)。
//
// 之所以按"键是否存在"而不是"值是否为 nil"判定:runtime 的 contextx
// 总是把标准槽位(task / plan / iteration …)放进模板数据,上游还没产出时
// 值是 nil。此时"缺失"应当只表示"这个键根本不存在"(拼错的名字),
// 否则每个引用了未产出槽位的 prompt 都会刷告警(噪音),真正拼错的变量
// 反而被淹没。
func lookup(key string, data map[string]any) (any, bool) {
	if strings.HasPrefix(key, "variable.") {
		name := strings.TrimPrefix(key, "variable.")
		if vars, ok := data["variables"].(map[string]any); ok {
			v, ok := vars[name]
			return v, ok
		}
		// 变量表也可能是 map[string]string(不同来源的变量表形态不同),
		// 否则这个变量会被静默当成"缺失"。
		if vars, ok := data["variables"].(map[string]string); ok {
			v, ok := vars[name]
			return v, ok
		}
		return nil, false
	}
	// 扁平查找(点路径,如 result.decision)
	if v, ok := data[key]; ok {
		return v, true
	}
	parts := strings.Split(key, ".")
	cur := any(data)
	for _, p := range parts {
		switch m := cur.(type) {
		case map[string]any:
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		case map[string]string:
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		default:
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
		return safeString(t)
	default:
		return fmt.Sprint(t)
	}
}

// safeString 调用 Stringer 的 String(),并兜住它内部的 panic。
// 变量来自工作流 DSL/Agent 输出,第三方类型的 String() 崩溃不应该
// 带崩执行协程(约定 12:不得依赖上层保护)。
func safeString(s fmt.Stringer) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = "<unrenderable>"
		}
	}()
	return s.String()
}
