package runtime

// 本文件锁住"会话状态快照(state_json)的脱敏规则"(约定 #8)。
//
// 关键不变式有两条,缺一不可:
//
//  1. **自由文本键必须脱敏** —— output / node:<id>:result / _error 等
//     直接来自 Agent 的 stdout/stderr,是凭据的载体。
//  2. **结构化键必须原样保留** —— decision / node:<id>:status / _abort 等
//     被运行期读取,用于路由与决策;脱敏它们 = 改写执行语义。
//
// 第 2 条比看上去微妙:键名判定规则(logx)把"token 结尾"的键判为强敏感,
// 于是节点 ID 里带 token 的状态键(node:my-token:status)在无差别脱敏下
// 会被整值替换成 "***" —— 节点状态直接丢失,恢复时该节点会被重跑。
// 这就是"按键分类"而不是"整个 map 无差别脱敏"的原因。

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMaskStateDataKeepsStructuredKeys 结构化键必须原样保留。
func TestMaskStateDataKeepsStructuredKeys(t *testing.T) {
	data := map[string]any{
		"decision":          "APPROVED",
		"status":            "success",
		"_abort":            "WAIT_USER",
		"_resume_node":      "work",
		"_current_node":     "work",
		"_loop_count":       3,
		"_loop_exited":      true,
		"_loop_stuck":       false,
		"_loop_decision":    "APPROVED",
		"node:work:status":  "SUCCESS",
		"node:work:attempt": 2,
		// 节点 ID 里带敏感词:键名判定规则会把它整值替换 —— 必须原样保留
		"node:my-token:status": "SUCCESS",
	}
	out := MaskStateData(data)
	for k, want := range data {
		if got := fmt.Sprint(out[k]); got != fmt.Sprint(want) {
			t.Errorf("结构化键 %q 被改动了: got %q want %q", k, got, fmt.Sprint(want))
		}
	}
}

// TestMaskStateDataMasksFreeTextKeys 自由文本键必须脱敏。
func TestMaskStateDataMasksFreeTextKeys(t *testing.T) {
	data := map[string]any{
		"output":              "cli stderr: ANTHROPIC_API_KEY=" + leakSecret,
		"_error":              "boom ANTHROPIC_API_KEY=" + leakSecret,
		"task":                "部署时带上 ANTHROPIC_API_KEY=" + leakSecret,
		"node:work:result":    `{"State":"SUCCESS","Output":"ANTHROPIC_API_KEY=` + leakSecret + `"}`,
		"node:work:answered":  `{"State":"WAITING","Output":"ANTHROPIC_API_KEY=` + leakSecret + `"}`,
		"user_instruction":    "带上 ANTHROPIC_API_KEY=" + leakSecret,
		"git_diff":            "+ANTHROPIC_API_KEY=" + leakSecret,
		"variables":           map[string]any{"api_key": leakSecret},
		"_user_input":         map[string]any{"response": "带上 ANTHROPIC_API_KEY=" + leakSecret},
		"revisit:rev:history": []any{"ANTHROPIC_API_KEY=" + leakSecret},
	}
	out := MaskStateData(data)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("序列化脱敏结果: %v", err)
	}
	if strings.Contains(string(raw), leakSecret) {
		t.Errorf("脱敏后仍含凭据明文: %s", raw)
	}
	// 脱敏不能把内容整体抹掉:键名保留、值替换为 ***(可读性不退化)
	if got := fmt.Sprint(out["output"]); !strings.Contains(got, "ANTHROPIC_API_KEY=***") {
		t.Errorf("output 应保留键名并把值替换为 ***: %q", got)
	}
	// 内层按键名判定:"api_key" 是强敏感键 → 整值替换
	vars, _ := out["variables"].(map[string]any)
	if got := fmt.Sprint(vars["api_key"]); got != "***" {
		t.Errorf("variables.api_key 应整值替换为 ***: %q", got)
	}
}

// TestMaskStateDataKeepsOutcomeJSONValid outcome JSON 脱敏后必须仍是合法 JSON。
//
// 恢复路径要 json.Unmarshal 这个值(executor 的 answeredOutcome):
// 解析失败就按"未答复"处理 → 静默重跑节点,用户白等一次。
func TestMaskStateDataKeepsOutcomeJSONValid(t *testing.T) {
	raw := `{"State":"SUCCESS","Decision":"APPROVED","Summary":"ok",` +
		`"Output":"cli stderr: ANTHROPIC_API_KEY=` + leakSecret + `",` +
		`"Data":{"count":3},"Error":""}`
	out := MaskStateData(map[string]any{"node:work:result": raw})
	got := fmt.Sprint(out["node:work:result"])

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("脱敏后不再是合法 JSON(恢复路径会解析失败): %v (%s)", err, got)
	}
	if parsed["State"] != "SUCCESS" {
		t.Errorf("结构化字段 State 被改动: %v", parsed["State"])
	}
	if parsed["Decision"] != "APPROVED" {
		t.Errorf("结构化字段 Decision 被改动: %v", parsed["Decision"])
	}
	if strings.Contains(got, leakSecret) {
		t.Errorf("脱敏后仍含凭据明文: %s", got)
	}
	// 数字字面量不能被 float64 往返改写(脱敏不该顺手改数据的数值)
	if !strings.Contains(got, `"count":3`) {
		t.Errorf("Data 里的数字被改写了: %s", got)
	}
}

// TestMaskStateDataDoesNotMutateInput 脱敏不得就地改运行期状态。
//
// 入参是运行期正在用的 StateData(还要读它做路由与决策)。
func TestMaskStateDataDoesNotMutateInput(t *testing.T) {
	data := map[string]any{"output": "ANTHROPIC_API_KEY=" + leakSecret}
	out := MaskStateData(data)
	if !strings.Contains(fmt.Sprint(data["output"]), leakSecret) {
		t.Errorf("脱敏就地改了入参(运行期会读到被遮蔽的值): %v", data["output"])
	}
	if strings.Contains(fmt.Sprint(out["output"]), leakSecret) {
		t.Errorf("返回的副本仍含凭据明文: %v", out["output"])
	}
}

// ---- 键分类的同源守卫 ----

// stateKeyFuncs 把"生成状态键的函数"映射到键形态。
var stateKeyFuncs = map[string]string{
	"StatusKey":   "node:*:status",
	"ResultKey":   "node:*:result",
	"AnsweredKey": "node:*:answered",
	"attemptKey":  "node:*:attempt",
}

// stateKeyLocals 把"先在局部变量里算好再写入"的键映射到键形态。
var stateKeyLocals = map[string]string{
	"histKey":  "revisit:*:history",
	"answered": "node:*:answered",
}

// expectedStateKeyClass 是每个状态键的期望分类。
//
// 与实现对照:分类错了要么改写执行语义(结构化键被脱敏),
// 要么漏凭据(自由文本键没脱敏)。新增状态键必须在这里登记 ——
// 凭印象维护清单必然漏项(约定 #13 的同源精神)。
var expectedStateKeyClass = map[string]string{
	// 结构化:路由/决策依据,必须原样保留
	"decision":       "structured",
	"status":         "structured",
	"_abort":         "structured",
	"_resume_node":   "structured",
	"_current_node":  "structured",
	"_loop_count":    "structured",
	"_loop_exited":   "structured",
	"_loop_stuck":    "structured",
	"_loop_decision": "structured",
	"node:*:status":  "structured",
	"node:*:attempt": "structured",
	// 自由文本:凭据的载体,必须脱敏
	"output":            "free",
	"task":              "free",
	"_user_input":       "free",
	"_error":            "free",
	"user_instruction":  "free",
	"git_diff":          "free",
	"node:*:result":     "free",
	"node:*:answered":   "free",
	"revisit:*:history": "free",
}

// TestStateWritesAreClassified 扫描状态写入点,确保每个键都已分类。
//
// 这条守卫的作用是"新增状态键必须被显式分类":编译器或运行期新增一个
// 状态键时,若不登记,落库脱敏会按默认规则把它当自由文本处理 ——
// 结构化键(路由依据)会被改写,执行语义悄悄变掉。
func TestStateWritesAreClassified(t *testing.T) {
	files := []string{
		filepath.Join("..", "compiler", "lower.go"),
		"executor.go",
	}
	for _, f := range files {
		keys, unknown := scanStateWriteKeys(t, f)
		for _, u := range unknown {
			t.Errorf("%s: 状态键写法 %q 无法解析 —— 必须在 expectedStateKeyClass 里登记分类", f, u)
		}
		for _, k := range keys {
			want, ok := expectedStateKeyClass[k]
			if !ok {
				t.Errorf("%s: 新增的状态键 %q 未登记分类(漏登记 = 落库明文或执行语义被改写)", f, k)
				continue
			}
			got := "free"
			if structuredStateKey(k) {
				got = "structured"
			}
			if got != want {
				t.Errorf("状态键 %q 分类错误: 实现=%s 期望=%s", k, got, want)
			}
		}
	}
}

// scanStateWriteKeys 扫描源码里的状态写入点,返回解析出的键与无法解析的写法。
func scanStateWriteKeys(t *testing.T, path string) (keys []string, unknown []string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	consts := constStringValues(f)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Set" || len(call.Args) == 0 {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || (recv.Name != "st" && recv.Name != "state") {
			return true
		}
		key, ok := resolveStateKey(call.Args[0], consts)
		if !ok {
			unknown = append(unknown, exprString(fset, call.Args[0]))
			return true
		}
		keys = append(keys, key)
		return true
	})
	return keys, unknown
}

// resolveStateKey 把状态键表达式解析成具体的键(或键形态)。
func resolveStateKey(expr ast.Expr, consts map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	case *ast.Ident:
		if v, ok := consts[e.Name]; ok {
			return v, true
		}
		if v, ok := stateKeyLocals[e.Name]; ok {
			return v, true
		}
		return "", false
	case *ast.CallExpr:
		// 包级函数(StatusKey(node.ID))与方法调用(st.Set 之外的形态)都要认
		if ident, ok := e.Fun.(*ast.Ident); ok {
			if v, ok := stateKeyFuncs[ident.Name]; ok {
				return v, true
			}
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if v, ok := stateKeyFuncs[sel.Sel.Name]; ok {
				return v, true
			}
		}
		return "", false
	}
	return "", false
}

// constStringValues 收集文件里 const 块的字符串常量(name → 值)。
func constStringValues(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if v, err := strconv.Unquote(lit.Value); err == nil {
					out[name.Name] = v
				}
			}
		}
	}
	return out
}

// exprString 把表达式还原成源码文本(报错信息用)。
func exprString(fset *token.FileSet, expr ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, expr)
	return b.String()
}
