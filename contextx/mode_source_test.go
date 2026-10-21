package contextx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"contextx 消费共享的 mode 定义"(约定 13 的同源精神)。
//
// 背景:mode 的取值域与语义(是否需要写权限 / 产出进哪个上下文槽位)现在
// 只有一份 —— workflow/model.ModeSpec。此前 contextx 自带一份 mode 清单
// (switch 的 case "plan" / "execute" / "review" / "fix" / "test" / "skill"),
// 两份清单各自演化:model 认了某个 mode 而 contextx 没认,或反之 ——
// 同一个 mode 在校验期合法、在上下文层静默丢产物(用户看不出任何异常)。
//
// 守卫方式:**源码级**扫描(而不是只比值)。只比值挡不住"抄一份字面量":
// 两边都写 "plan" 时值相等,但仍是两份来源,下次改一边就漂移。

// contextxSource 是被扫描的源文件(同包)。
const contextxSource = "context.go"

// parseContextx 解析 contextx 的源文件。
func parseContextx(t *testing.T) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, contextxSource, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s: %v", contextxSource, err)
	}
	return f
}

// stringLiterals 返回源文件里所有字符串字面量的**值**。
//
// 用 AST 而不是文本扫描:注释里的 "plan" 不是代码,文本扫描会误报。
//
// **排除状态键常量块**:StateKey* 的字面量("plan" / "review" / …)与
// mode 名同名(槽位名恰好等于产出它的 mode 名),它们是**槽位**不是
// mode 清单 —— 把它们算进来会误报。
func stringLiterals(t *testing.T, f *ast.File) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, decl := range f.Decls {
		if isStateKeyConstDecl(decl) {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			out[v] = true
			return true
		})
	}
	return out
}

// isStateKeyConstDecl 判断这是不是声明 StateKey* 的那个 const 块。
func isStateKeyConstDecl(decl ast.Decl) bool {
	gd, ok := decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.CONST {
		return false
	}
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, name := range vs.Names {
			if strings.HasPrefix(name.Name, "StateKey") {
				return true
			}
		}
	}
	return false
}

// TestContextxHasNoHardcodedModeLiterals contextx 的源文件里不得出现
// mode 名字面量 —— mode 的取值域与语义只有一份(workflow/model),
// contextx 只能引用它。
//
// 修前(红):context.go 里有 case "plan" / case "review" /
// case "execute", "fix", "skill" / case "test" 四组字面量。
// 修后(绿):全部改为 model.Mode* / model.ModeSpecFor 引用。
func TestContextxHasNoHardcodedModeLiterals(t *testing.T) {
	f := parseContextx(t)
	lits := stringLiterals(t, f)

	// 伪 mode "skill" 也算:它是**节点类型**(model.NodeTypeSkill),
	// 同样不该在 contextx 里另抄一份字面量。
	forbidden := map[string]string{}
	for _, m := range model.AllModes() {
		forbidden[m] = "mode"
	}
	forbidden[string(model.NodeTypeSkill)] = "节点类型(伪 mode)"

	for lit, kind := range forbidden {
		if lits[lit] {
			t.Errorf("contextx/%s 里出现 %s 的字面量 %q:mode 的取值域与语义只有一份"+
				"(workflow/model),contextx 必须引用它而不是各抄一份(约定 13)",
				contextxSource, kind, lit)
		}
	}
}

// slotRecorder 记录 SaveNodeOutput 写进了哪些键(测试用)。
type slotRecorder struct{ keys map[string]any }

func (r *slotRecorder) Set(k string, v any) error {
	if r.keys == nil {
		r.keys = map[string]any{}
	}
	r.keys[k] = v
	return nil
}

// TestSaveNodeOutputFollowsModelModeSpec 每个 mode 的产出必须写进
// model.ModeSpec 声明的那个槽位;声明"不进上下文链"的 mode 必须什么都不写。
//
// 这是**行为级**断言(上面的源码扫描只保证"不自带字面量清单",
// 这里保证"真的写对了地方"):contextx 一旦又自己长出一份映射表,
// 这里的判定立刻变红。
func TestSaveNodeOutputFollowsModelModeSpec(t *testing.T) {
	m := NewManager()
	for _, mode := range model.AllModes() {
		spec, ok := model.ModeSpecFor(mode)
		if !ok {
			t.Fatalf("mode %q 在 AllModes 里却没有 ModeSpec", mode)
		}
		rec := &slotRecorder{}
		result := map[string]any{"summary": "结论", "output": "产物"}
		if err := m.SaveNodeOutput(mode, "n1", result, rec); err != nil {
			t.Fatalf("SaveNodeOutput(%q): %v", mode, err)
		}
		if spec.ContextSlot == "" {
			if len(rec.keys) != 0 {
				t.Errorf("mode=%q 声明为不进上下文链,实际写入了: %v", mode, rec.keys)
			}
			continue
		}
		if _, ok := rec.keys[spec.ContextSlot]; !ok {
			t.Errorf("mode=%q 的产出应写进槽位 %q,实际写入: %v",
				mode, spec.ContextSlot, rec.keys)
		}
	}
}

// TestSaveNodeOutputSkillPseudoMode skill 是**节点类型**(不在 mode 取值域
// 里),但它的产物仍要进上下文链 —— 不能因为"不在取值域里"就丢掉。
func TestSaveNodeOutputSkillPseudoMode(t *testing.T) {
	if model.IsValidMode(string(model.NodeTypeSkill)) {
		t.Fatal("skill 是节点类型,不应出现在 agent 节点的 mode 取值域里")
	}
	m := NewManager()
	rec := &slotRecorder{}
	if err := m.SaveNodeOutput(string(model.NodeTypeSkill), "sk",
		map[string]any{"summary": "S", "output": "O"}, rec); err != nil {
		t.Fatalf("SaveNodeOutput(skill): %v", err)
	}
	if _, ok := rec.keys[model.ContextSlotLastOutput]; !ok {
		t.Errorf("skill 伪 mode 的产出应写进 %q,实际写入: %v",
			model.ContextSlotLastOutput, rec.keys)
	}
}

// TestOutputSlotFollowsModel 槽位解析必须完全由 model 决定:
//
//   - 合法的 agent mode → model.ModeSpecFor(mode).ContextSlot;
//   - 伪 mode(skill 节点类型)→ last_output;
//   - 提交型 mode(submit:声明"不进上下文链")→ 空串;
//   - 不认识的 mode → 不写(fail closed)。
func TestOutputSlotFollowsModel(t *testing.T) {
	for _, mode := range model.AllModes() {
		spec, ok := model.ModeSpecFor(mode)
		if !ok {
			t.Fatalf("mode %q 在 AllModes 里却没有 ModeSpec", mode)
		}
		got, ok := outputSlot(mode)
		if !ok {
			t.Errorf("mode=%q 应被识别,实际 outputSlot 说不认识", mode)
			continue
		}
		if got != spec.ContextSlot {
			t.Errorf("mode=%q 的槽位 = %q,期望 model 声明的 %q",
				mode, got, spec.ContextSlot)
		}
	}
	// 伪 mode:skill 是节点类型,不在取值域里,但产物要进上下文链。
	if got, ok := outputSlot(string(model.NodeTypeSkill)); !ok || got != model.ContextSlotLastOutput {
		t.Errorf("skill 伪 mode 的槽位 = %q(ok=%v),期望 %q",
			got, ok, model.ContextSlotLastOutput)
	}
	// 不认识的 mode:不写(fail closed),而不是乱写一个槽位。
	if got, ok := outputSlot("no-such-mode"); ok {
		t.Errorf("不认识的 mode 不该被解析出槽位,实际 %q", got)
	}
}

// TestStateKeysMatchModelContextSlots StateKey* 必须与 model.ContextSlot*
// **逐项一致**。
//
// 键名是写进 state_json 的持久化契约:漂移会让存量会话状态读不出来
// (下游读到 nil → 模板渲染成空串 → Agent 收到没有方案的提示词照常执行)。
//
// **为什么是"值一致"而不是"别名"**:运行期的状态脱敏守卫
// (workflow/runtime/state_mask_test.go 的 TestStateWritesAreClassified)
// 会**源码扫描**所有状态写入点,把键名解析成字符串后逐个登记分类;
// 它的常量表只收**字符串字面量**常量(constStringValues 要求
// *ast.BasicLit),别名(选择器表达式)解析不出来 —— 守卫会报"状态键
// 写法无法解析"并连带判定"登记与实现漂移"。那份守卫在 workflow/**
// 下(本轮只读),所以本包的常量保持字面量,由本用例 + 运行期的跨包
// 守卫(mode_contract_test.go)共同保证两边一致。
func TestStateKeysMatchModelContextSlots(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		ctxKey string
	}{
		{"task", model.ContextSlotTask, StateKeyTask},
		{"plan", model.ContextSlotPlan, StateKeyPlan},
		{"review", model.ContextSlotReview, StateKeyReview},
		{"issues", model.ContextSlotIssues, StateKeyIssues},
		{"test_result", model.ContextSlotTestResult, StateKeyTestResult},
		{"last_output", model.ContextSlotLastOutput, StateKeyLastOutput},
		{"git_diff", model.ContextSlotGitDiff, StateKeyGitDiff},
		{"iteration", model.ContextSlotIteration, StateKeyIteration},
		{"relevant_files", model.ContextSlotRelevantFiles, StateKeyRelevantFiles},
		{"user_instruction", model.ContextSlotUserInstruction, StateKeyUserInstruction},
	}
	for _, c := range cases {
		if c.model != c.ctxKey {
			t.Errorf("槽位 %s:model=%q,contextx=%q(键名漂移会让存量 state_json 读不出来)",
				c.name, c.model, c.ctxKey)
		}
	}
}
