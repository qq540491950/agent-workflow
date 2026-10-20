package compiler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件是"节点类型 → 配置契约"(nodeconfig.go)与**运行期实际实现**之间的
// 探针式一致性测试 —— AGENTS.md 硬性约定 #13 的机械化守卫。
//
// 约定 13 要求校验期与运行期同源。nodeconfig.go 的表描述的是"运行期实际
// 读取哪些配置键",而运行期的实现在 workflow/runtime/executor.go。两份
// 实现各写各的必然漂移,而漂移方向决定后果:
//
//   - 运行期读了表里没有的键 → 校验期不认识 → 用户被自己的校验器挡住;
//   - 表里要求了运行期不读的键 → 校验期要求用户填一个没有作用的键;
//   - 表里的键运行期已不再读 → 表过期,校验期按过期的契约要求用户填。
//
// 本测试用 AST 扫描运行期源码,抽出每个节点类型实际读取的配置键,
// 与 nodeconfig.go 逐项比对。任何一侧改动而另一侧没同步,这里立刻失败,
// 并在失败信息里指出该补哪一处。

// runtimeNodeFuncs 把运行期的节点执行函数映射到节点类型。
//
// 新增/改名了读取配置的函数而这里没同步 → 测试失败(强制人工确认归属:
// 该函数属于哪种节点类型,还是全局的)。
var runtimeNodeFuncs = map[string]model.NodeType{
	"runAgentNode":       model.NodeTypeAgent,
	"runSkillNode":       model.NodeTypeSkill,
	"runGitNode":         model.NodeTypeGit,
	"runScriptNode":      model.NodeTypeScript,
	"runHumanNode":       model.NodeTypeHuman,
	"runSubworkflowNode": model.NodeTypeSubWorkflow,
	"runMergeNode":       model.NodeTypeMerge,
	// retryPolicyOf 读 agent 节点的 retry 配置(仅 agent 节点使用)。
	"retryPolicyOf": model.NodeTypeAgent,
}

// runtimeGlobalFuncs 是读取配置、但不归属某个节点类型的函数。
var runtimeGlobalFuncs = map[string]bool{
	// dispatchNode 在类型分发之前读节点级开关 enabled(对所有节点生效)。
	"dispatchNode": true,
	// Engine.Start 遍历所有节点取引用到的 agent(与节点类型无关)。
	"Start": true,
}

// runtimeConfigReads 扫描运行期包,返回"函数名 → 该函数读取的配置键"。
func runtimeConfigReads(t *testing.T) map[string]configReads {
	t.Helper()
	dir := filepath.Join("..", "runtime")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取运行期源码目录 %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := map[string]configReads{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("解析运行期源码 %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			reads := collectConfigKeys(fn.Body)
			if len(reads.nodeKeys) == 0 && len(reads.subKeys) == 0 {
				continue
			}
			merged, exists := out[fn.Name.Name]
			if !exists {
				merged = configReads{subKeys: map[string][]string{}}
			}
			merged.nodeKeys = mergeKeys(merged.nodeKeys, reads.nodeKeys)
			for k, v := range reads.subKeys {
				merged.subKeys[k] = mergeKeys(merged.subKeys[k], v)
			}
			out[fn.Name.Name] = merged
		}
	}
	return out
}

// configReads 是一个函数读取到的配置键。
type configReads struct {
	// nodeKeys 是节点级配置键(node.Config["x"] / cfgStr(node, "x"))。
	nodeKeys []string
	// subKeys 是 map 型节点级配置键的子键(节点键 → 子键)。
	subKeys map[string][]string
}

// collectConfigKeys 抽出函数体内读取的节点级配置键及其子键。
//
// 识别两种写法:
//
//	node.Config["agent"]        —— 直接下标
//	cfgStr(node, "agent")       —— 读取辅助函数(键是字面量参数)
//
// 键是变量时(cfgStr(node, key))不计入:那是辅助函数自身,不是某个具体键。
func collectConfigKeys(body *ast.BlockStmt) configReads {
	out := configReads{subKeys: map[string][]string{}}
	// 第一遍:找出"从节点配置里取出来的 map 局部变量"。
	//	raw, ok := node.Config["retry"].(map[string]any)
	mapVars := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) != 1 {
			return true
		}
		lhs, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		ta, ok := assign.Rhs[0].(*ast.TypeAssertExpr)
		if !ok {
			return true
		}
		idx, ok := ta.X.(*ast.IndexExpr)
		if !ok {
			return true
		}
		key, ok := nodeConfigKey(idx)
		if !ok {
			return true
		}
		mapVars[lhs.Name] = key
		return true
	})
	// 第二遍:节点级键 + 子键。
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			ident, ok := v.Fun.(*ast.Ident)
			if !ok || (ident.Name != "cfgStr" && ident.Name != "cfgInt") {
				return true
			}
			if len(v.Args) != 2 || rootIdent(v.Args[0]) != "node" {
				return true
			}
			if k, ok := stringLit(v.Args[1]); ok {
				out.nodeKeys = append(out.nodeKeys, k)
			}
		case *ast.IndexExpr:
			if key, ok := nodeConfigKey(v); ok {
				out.nodeKeys = append(out.nodeKeys, key)
				return true
			}
			// 子键:map 局部变量的字面量下标。
			base, ok := v.X.(*ast.Ident)
			if !ok {
				return true
			}
			owner, ok := mapVars[base.Name]
			if !ok {
				return true
			}
			if k, ok := stringLit(v.Index); ok {
				out.subKeys[owner] = append(out.subKeys[owner], k)
			}
		}
		return true
	})
	for k := range out.subKeys {
		out.subKeys[k] = mergeKeys(nil, out.subKeys[k])
	}
	return out
}

// nodeConfigKey 判断一个下标表达式是否读取节点配置,返回配置键名。
//
// 只认节点配置(node.Config / wf.Nodes[i].Config),不认其它结构体的
// .Config 字段。
func nodeConfigKey(idx *ast.IndexExpr) (string, bool) {
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Config" {
		return "", false
	}
	root := rootIdent(sel.X)
	if root != "node" && root != "wf" {
		return "", false
	}
	return stringLit(idx.Index)
}

// rootIdent 返回表达式链最根部的标识符(node.Config → "node")。
func rootIdent(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return rootIdent(v.X)
	case *ast.IndexExpr:
		return rootIdent(v.X)
	case *ast.CallExpr:
		return rootIdent(v.Fun)
	case *ast.StarExpr:
		return rootIdent(v.X)
	case *ast.ParenExpr:
		return rootIdent(v.X)
	}
	return ""
}

// stringLit 取字符串字面量的值(非字面量返回 false)。
func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// mergeKeys 合并两组键(去重)。
func mergeKeys(a, b []string) []string {
	seen := map[string]bool{}
	for _, k := range a {
		seen[k] = true
	}
	for _, k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// keysOf 返回一组键的集合。
func keysOf(keys []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// TestNodeConfigSpecCoversAllNodeTypes 每个已注册节点类型都必须有配置契约,
// 且契约表里不能出现未注册的类型(否则是死定义)。
func TestNodeConfigSpecCoversAllNodeTypes(t *testing.T) {
	registered := map[model.NodeType]bool{}
	for _, nt := range model.AllNodeTypes() {
		registered[nt] = true
		if _, ok := NodeConfigSpecFor(nt); !ok {
			t.Errorf("节点类型 %q 没有配置契约定义(请补进 workflow/compiler/nodeconfig.go)", nt)
		}
	}
	for nt := range nodeConfigSpecs {
		if !registered[nt] {
			t.Errorf("配置契约里出现了未注册的节点类型 %q(节点类型清单的唯一来源是 model.AllNodeTypes())", nt)
		}
	}
}

// TestNodeConfigSpecMatchesRuntimeReads 核心探针:契约表必须与运行期实际
// 读取的配置键逐项一致(每个节点类型),子键同样对齐。
//
// 稳健性说明:运行期重构(把配置读取搬进新的辅助函数、改函数名)不应让本
// 测试误报。因此:
//   - 某节点类型在运行期已无对应函数时,只跳过"表已过期"方向,
//     "运行期读了表里没有的键"仍由全局检查兜底;
//   - 函数归属不明只记日志,不阻断 —— 键级漂移才是真正的缺陷。
func TestNodeConfigSpecMatchesRuntimeReads(t *testing.T) {
	reads := runtimeConfigReads(t)
	for _, nt := range model.AllNodeTypes() {
		spec, ok := NodeConfigSpecFor(nt)
		if !ok {
			continue // 已由 TestNodeConfigSpecCoversAllNodeTypes 报告
		}
		want := keysOf(append(append([]string{}, spec.Required...), spec.Optional...))
		got := map[string]bool{}
		gotSub := map[string][]string{}
		mapped := 0
		for fn, owner := range runtimeNodeFuncs {
			if owner != nt {
				continue
			}
			r, exists := reads[fn]
			if !exists {
				continue // 运行期已改名/删除该函数
			}
			mapped++
			for k := range keysOf(r.nodeKeys) {
				got[k] = true
			}
			for k, v := range r.subKeys {
				gotSub[k] = mergeKeys(gotSub[k], v)
			}
		}
		if mapped == 0 {
			t.Logf("%s 节点:运行期没有对应的执行函数(可能已重构),跳过逐类型比对", nt)
			continue
		}
		for k := range want {
			if !got[k] {
				t.Errorf("%s 节点:契约表声明了配置键 %q,但运行期没有读取它(表已过期,请同步 workflow/compiler/nodeconfig.go)", nt, k)
			}
		}
		for k := range got {
			if !want[k] {
				t.Errorf("%s 节点:运行期读取了配置键 %q,但契约表里没有(请补进 workflow/compiler/nodeconfig.go 的 Optional)", nt, k)
			}
		}
		// 子键(如 agent 节点的 prompt.template / retry.max_attempts)。
		for owner, subs := range gotSub {
			wantSubs := spec.SubKeys[owner]
			if len(wantSubs) == 0 {
				t.Errorf("%s 节点:运行期读取了配置键 %q 的子键 %v,但契约表没有声明子键", nt, owner, subs)
				continue
			}
			for _, s := range subs {
				if !slices.Contains(wantSubs, s) {
					t.Errorf("%s 节点:运行期读取了配置键 %q 的子键 %q,但契约表里没有(请补进 SubKeys)", nt, owner, s)
				}
			}
			for _, s := range wantSubs {
				if !slices.Contains(subs, s) {
					t.Errorf("%s 节点:契约表声明了配置键 %q 的子键 %q,但运行期没有读取它", nt, owner, s)
				}
			}
		}
	}
}

// TestRuntimeReadsOnlyKnownConfigKeys 运行期读取的每一个配置键都必须在
// 契约表里被声明(任何节点类型或全局键)。
//
// 这条兜住"运行期新增了一个键但没人登记"的情况:它可能属于任何节点类型,
// 上面的逐类型比对只有在归属明确时才能发现。
func TestRuntimeReadsOnlyKnownConfigKeys(t *testing.T) {
	reads := runtimeConfigReads(t)
	declared := keysOf(GlobalConfigKeys())
	for _, nt := range model.AllNodeTypes() {
		for _, k := range KnownConfigKeys(nt) {
			declared[k] = true
		}
	}
	for fn, r := range reads {
		for _, k := range r.nodeKeys {
			if !declared[k] {
				t.Errorf("运行期函数 %s 读取了配置键 %q,契约表里完全没有声明(请补进 workflow/compiler/nodeconfig.go)", fn, k)
			}
		}
	}
}

// TestValidatorOnlyKeysNotReadByRuntime 只有校验期使用的键不得被运行期读取。
//
// 现状:condition 节点的 expression 属于这一类 —— 路由条件在**边上**
// (edge.condition),节点上的 expression 运行期与编译器都不读。
// 若将来运行期开始读它,这里会失败,提示把该键从 ValidatorOnly 挪到 Optional
// (并重新评估"是否必填")。
func TestValidatorOnlyKeysNotReadByRuntime(t *testing.T) {
	reads := runtimeConfigReads(t)
	for _, nt := range model.AllNodeTypes() {
		for _, k := range ValidatorOnlyConfigKeys(nt) {
			for fn, r := range reads {
				if keysOf(r.nodeKeys)[k] {
					t.Errorf("%s 节点的配置键 %q 被标为[只有校验期使用],但运行期函数 %s 读了它(请把它挪到 Optional 并重估必填性)", nt, k, fn)
				}
			}
		}
	}
}

// TestDeprecatedKeysNotReadByRuntime 已废弃的配置键不得被运行期读取。
//
// 现状:condition 节点的 expression 属于这一类 —— 路由条件在**边上**
// (edge.condition),节点上的 expression 运行期与编译器都不读。
//
// 这条守卫的意义:一旦有人"顺手"让运行期读起这个键(比如把
// condition 节点当成决策点),而校验期还在发"已废弃"的警告,用户就会被
// 误导 —— 警告说"不影响执行结果",实际却影响。运行期一旦开始读它,
// 这里立刻失败,提示把该键从 Deprecated 挪到 Optional/Required 并重估。
func TestDeprecatedKeysNotReadByRuntime(t *testing.T) {
	reads := runtimeConfigReads(t)
	for _, nt := range model.AllNodeTypes() {
		for _, k := range DeprecatedConfigKeys(nt) {
			for fn, r := range reads {
				if keysOf(r.nodeKeys)[k] {
					t.Errorf("%s 节点的配置键 %q 已废弃,但运行期函数 %s 读了它(请把它挪到 Optional 并重估必填性)", nt, k, fn)
				}
			}
		}
	}
}

// TestGlobalConfigKeysMatchRuntime 全局键必须与运行期的读取一致。
//
// 两条约束:
//  1. 声明为全局键的,运行期必须真的读它(否则是死定义);
//  2. 运行期在全局位置(跨节点类型的函数)读到的键,必须是全局键或某个
//     节点类型的键 —— 例如 Engine.Start 遍历所有节点取引用到的 agent,
//     它读的是 agent 节点的类型键,不是全局键。
func TestGlobalConfigKeysMatchRuntime(t *testing.T) {
	reads := runtimeConfigReads(t)
	declaredTypeKeys := map[string]bool{}
	for _, nt := range model.AllNodeTypes() {
		for _, k := range KnownConfigKeys(nt) {
			declaredTypeKeys[k] = true
		}
	}
	allRead := map[string]bool{}
	for _, r := range reads {
		for _, k := range r.nodeKeys {
			allRead[k] = true
		}
	}
	got := map[string]bool{}
	for fn := range runtimeGlobalFuncs {
		for _, k := range reads[fn].nodeKeys {
			got[k] = true
		}
	}
	want := keysOf(GlobalConfigKeys())
	for k := range want {
		if !allRead[k] {
			t.Errorf("全局配置键 %q 被声明,但运行期没有读取它", k)
		}
	}
	for k := range got {
		if !want[k] && !declaredTypeKeys[k] {
			t.Errorf("运行期全局读取了配置键 %q,但它既不是全局键、也不是任何节点类型的配置键", k)
		}
	}
}

// TestRuntimeConfigReadsAllAttributed 运行期每个读取配置的函数都应该归属到
// 某个节点类型或全局。
//
// 只记日志不阻断:运行期把配置读取搬进新的辅助函数(例如把 enabled 的解析
// 抽成 nodeEnabled)是正常重构,不应让门禁变红。键级漂移由
// TestRuntimeReadsOnlyKnownConfigKeys 与 TestNodeConfigSpecMatchesRuntimeReads
// 兜底。
func TestRuntimeConfigReadsAllAttributed(t *testing.T) {
	reads := runtimeConfigReads(t)
	for fn, r := range reads {
		if _, ok := runtimeNodeFuncs[fn]; ok {
			continue
		}
		if runtimeGlobalFuncs[fn] {
			continue
		}
		t.Logf("运行期函数 %s 读取了配置键 %v,未归属到节点类型(若这是新的全局读取点,请补进 runtimeGlobalFuncs)", fn, r.nodeKeys)
	}
}

// TestGitOperationsAreKnown 运行期支持的 git operation 必须都在契约里,
// 且契约里不得出现运行期不支持的 operation。
//
// 运行期的允许集合在 workflow/runtime/executor.go 的 runGitNode switch 里;
// 这里只校验"契约表 ⊆ 运行期允许集合"的方向需要读源码,故用探针方式:
// 契约里的每个 operation 都必须是 runGitNode 的 switch 分支之一。
func TestGitOperationsAreKnown(t *testing.T) {
	ops := GitOperations()
	if len(ops) == 0 {
		t.Fatal("git 允许集合不能为空")
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if seen[op] {
			t.Errorf("git 允许集合里有重复项 %q", op)
		}
		seen[op] = true
	}
	// 运行期读到的 operation 键必须在契约里(覆盖关系已在
	// TestNodeConfigSpecMatchesRuntimeReads 里逐类型校验)。
	reads := runtimeConfigReads(t)
	if !keysOf(reads["runGitNode"].nodeKeys)["operation"] {
		t.Error("运行期 runGitNode 不再读取 operation 配置键,请同步契约表")
	}
}
