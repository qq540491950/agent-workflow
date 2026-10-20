package compiler

// 本文件是"条件表达式可用变量集合"与**运行期实际供给**之间的探针式一致性
// 测试 —— AGENTS.md 硬性约定 #13(校验期与运行期必须同源)的机械化守卫。
//
// 背景:conditionenv.go 的 conditionEnvVars 是条件表达式可用变量的唯一定义。
// 此前只有"静态环境形状 == 运行期环境形状"的自洽测试
// (conditionenv_test.go 的 TestConditionEnvStaticAndRuntimeShapesMatch):
// 两侧都从同一张表派生,天生一致 —— 运行期真的少提供了一个变量、或多注入了
// 一个变量,没有任何测试会红。
//
// 本探针把"运行期实际提供的来源"独立地钉住,并与契约表**双向**比对:
//
//	契约侧:每个条件变量声明它从哪取值(state:<状态键> 或 vars);
//	运行期侧:状态键必须真的被运行期写入;变量表必须真的被运行期交给表达式
//	          环境;表达式环境必须只经契约构造器产出(不允许手写 env)。
//
// 双向含义:
//
//	契约有、运行期没有 → 契约声明了运行期不提供的变量,用户写了永远取不到;
//	运行期有、契约没有 → 运行期注入了契约未声明的变量(两侧对不上)。
//
// 技术路线沿用 nodeconfig_contract_test.go:go/ast 扫描运行期源码,与契约表
// 比对;识别注入点靠**类型/字段/函数名**,不靠变量名(变量名随时会改)。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---- 契约侧:条件变量的"取值来源" ----

// condEnvBacking 表示一个条件变量的取值来源。
//
//	"state:<键>" —— 从会话状态的某个键取值(运行期把该键写进状态);
//	"vars"      —— 从运行期变量表取值(任意键值 map)。
type condEnvBacking string

// condEnvBackingVars 是变量表来源的标识。
const condEnvBackingVars condEnvBacking = "vars"

// conditionEnvBackings 把契约表投影成"取值来源"集合。
//
// 投影只依赖 kind 与 stateKey 两个**字段**(结构性的),不依赖变量名 ——
// 变量名是用户可见的 API,由 conditionenv_test.go 的稳定性测试钉死。
func conditionEnvBackings(t *testing.T) []condEnvBacking {
	t.Helper()
	out := make([]condEnvBacking, 0, len(conditionEnvVars))
	for _, v := range conditionEnvVars {
		switch v.kind {
		case condEnvString, condEnvInt:
			out = append(out, condEnvBacking("state:"+v.stateKey))
		case condEnvVars:
			out = append(out, condEnvBackingVars)
		default:
			t.Errorf("条件变量 %q 的 kind=%d 无法识别(新增 kind 必须在这里投影成取值来源)", v.name, v.kind)
		}
	}
	return out
}

// runtimeConditionBackings 是"运行期实际提供的条件变量来源"清单(运行期事实表)。
//
// 为什么需要这张表,而不是纯 AST 推导:
// 运行期**从不按变量名**提供条件变量 —— 它只做两件事:把状态写进会话状态、
// 把变量表交给表达式环境。表达式环境里有哪些键,完全由 conditionenv.go 的
// 契约表决定。也就是说,"哪些状态键是为条件表达式准备的"这件事在运行期
// 源码里**没有对应的名字**(状态读取是 `v.stateKey` 这种动态下标,纯 AST
// 推不出"这个键是给条件用的")。
//
// 因此这里显式登记运行期的供给清单,并让下面的 AST 探针逐项核实:
//
//	state:<键> → 该状态键必须真的被运行期写入(写入点由 AST 扫描得到);
//	vars      → 运行期必须真的把变量表交给表达式环境(RunEnv.Vars)。
//
// 登记项与运行期源码对不上 → 红灯;与契约表对不上 → 红灯。两侧都靠机械
// 核对,不会"靠印象维护"。
var runtimeConditionBackings = []condEnvBacking{
	condEnvBacking("state:" + KeyDecision),
	condEnvBacking("state:" + KeyStatus),
	condEnvBacking("state:" + KeyOutput),
	condEnvBacking("state:" + KeyLoopCount),
	condEnvBackingVars,
}

// ---- AST 扫描 ----

// probeRuntimeRoot 是运行期引擎包的扫描根(相对本包目录)。
var probeRuntimeRoot = filepath.Join("..", "runtime")

// probeRoots 是本探针扫描的源码根目录(相对 workflow/compiler 包目录)。
//
// 耦合提示(路径变了必须同步改这里):
//
//   - "." 是本包 —— **运行期的条件求值循环在这里**(lower.go 的 runCondition
//     与 buildConditionEnv),条件环境也在这里构造;
//   - "../runtime" 是运行期引擎(engine/executor):它写状态、提供变量表;
//   - "../validator" 是校验期:它用同一份契约表构造静态环境。
//
// 若运行期循环迁到 workflow/runtime,或校验期拆包,这里必须同步改 ——
// 改完若探针报"扫描不到任何…",多半就是这里漏了(而不是扫描器坏了)。
var probeRoots = []string{".", probeRuntimeRoot, filepath.Join("..", "validator")}

// probeCompilerPkg 是 compiler 包的包名(跨包引用 compiler.X 时用来解析)。
const probeCompilerPkg = "compiler"

// probeExprImportPath 是表达式引擎的导入路径。
const probeExprImportPath = "github.com/expr-lang/expr"

// probeStateMapFields 是"承载会话状态的 map 字段名"。
//
// 字段名是结构性的:model.Execution.StateData 与 ADK EventActions.StateDelta
// 是仓库里仅有的两个会话状态 map。
var probeStateMapFields = map[string]bool{
	"StateData":  true,
	"StateDelta": true,
}

// probeFile 是被扫描的一个源文件。
type probeFile struct {
	root string
	path string
	pkg  string
	file *ast.File
}

// probeWrite 是一处会话状态写入。
type probeWrite struct {
	pkg  string
	file string
	line int
	key  string
	expr string
}

// probeEnvOp 是表达式环境的一处构造/改写。
type probeEnvOp struct {
	pkg   string
	file  string
	line  int
	fn    string
	key   string // 改写用的键(字面量键才有值)
	isLit bool   // 键是否是字面量
	expr  string
}

// probeSink 是表达式环境的一处消费点(环境被交给表达式引擎的地方)。
type probeSink struct {
	pkg  string
	file string
	line int
	fn   string
	arg  string
	// argIsCtor 环境参数是契约构造器的调用。
	argIsCtor bool
	// argIsEnvVar 环境参数是表达式环境类型的变量(形如 env)。
	argIsEnvVar bool
}

// probeScan 是本探针的全部扫描结果。
type probeScan struct {
	fset   *token.FileSet
	files  []probeFile
	consts map[string]map[string]string

	// writes 扫到的会话状态写入点。
	writes []probeWrite
	// writtenKeys 写入点涉及的状态键集合。
	writtenKeys map[string]bool

	// envType 表达式环境的类型别名名(如 expressionEnv)。
	envType string
	// builders 契约构造器:结果类型是表达式环境。
	builders map[string]bool
	// forwarders 转发构造器:结果是契约构造器的返回值。
	forwarders map[string]bool
	// envParamFuncs 函数名 → 其表达式环境参数的位置。
	envParamFuncs map[string]map[int]bool

	creations []probeEnvOp
	mutations []probeEnvOp
	sinks     []probeSink

	// runtimeExprImport 运行期包是否直接导入了表达式引擎。
	runtimeExprImport string
	// varsSupplied 运行期是否把变量表交给表达式环境。
	varsSupplied bool
}

// probeGoFiles 收集目录(或单个文件)下的全部非测试 .go 文件。
func probeGoFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if strings.HasSuffix(root, ".go") && !strings.HasSuffix(root, "_test.go") {
			return []string{root}, nil
		}
		return nil, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// loadProbeFiles 解析扫描根目录下的全部源文件。
func loadProbeFiles(t *testing.T, roots []string) ([]probeFile, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	var out []probeFile
	for _, root := range roots {
		paths, err := probeGoFiles(root)
		if err != nil {
			t.Fatalf("扫描源码目录 %s: %v", root, err)
		}
		for _, p := range paths {
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				t.Fatalf("解析源码 %s: %v", p, err)
			}
			out = append(out, probeFile{root: root, path: p, pkg: f.Name.Name, file: f})
		}
	}
	return out, fset
}

// probeConsts 收集包级字符串常量(按包名分组)。
func probeConsts(files []probeFile) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, pf := range files {
		if out[pf.pkg] == nil {
			out[pf.pkg] = map[string]string{}
		}
		for _, decl := range pf.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
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
					bl, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || bl.Kind != token.STRING {
						continue
					}
					sv, err := strconv.Unquote(bl.Value)
					if err != nil {
						continue
					}
					out[pf.pkg][name.Name] = sv
				}
			}
		}
	}
	return out
}

// probeResolveKey 把状态键表达式解析成字面量键。
//
// 解析依据是**常量表**(结构性的),不是变量名:`KeyDecision` 与
// `compiler.KeyDecision` 都解析成 "decision"。
func (s *probeScan) probeResolveKey(e ast.Expr, pkg string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		sv, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return sv, true
	case *ast.Ident:
		if c, ok := s.consts[pkg][v.Name]; ok {
			return c, true
		}
		if c, ok := s.consts[probeCompilerPkg][v.Name]; ok {
			return c, true
		}
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if !ok || x.Name != probeCompilerPkg {
			return "", false
		}
		if c, ok := s.consts[probeCompilerPkg][v.Sel.Name]; ok {
			return c, true
		}
	}
	return "", false
}

// probeEnvTypeName 找出表达式环境的类型别名。
//
// 判定依据:一个 **类型别名**(type X = map[string]any)且底层是
// map[string]any —— 表达式环境必须是 map 形状(expr 的虚拟机对 env 做类型
// 断言,具名 map 会 panic)。不按名字猜。
func probeEnvTypeName(files []probeFile) string {
	for _, pf := range files {
		if pf.pkg != probeCompilerPkg {
			continue
		}
		for _, decl := range pf.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Assign == 0 {
					continue
				}
				if !probeIsMapStringAny(ts.Type) {
					continue
				}
				return ts.Name.Name
			}
		}
	}
	return ""
}

// probeIsMapStringAny 判断类型表达式是不是 map[string]any。
func probeIsMapStringAny(e ast.Expr) bool {
	mt, ok := e.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mt.Key.(*ast.Ident)
	if !ok || key.Name != "string" {
		return false
	}
	val, ok := mt.Value.(*ast.Ident)
	return ok && val.Name == "any"
}

// probeCalleeName 取调用表达式的被调函数名(去掉包限定)。
func probeCalleeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// probeImportAlias 取某导入路径在文件里的本地别名。
func probeImportAlias(f *ast.File, path string) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return path[strings.LastIndex(path, "/")+1:], true
	}
	return "", false
}

// newProbeScan 扫描全部源码,得出本探针需要的事实。
func newProbeScan(t *testing.T) *probeScan {
	t.Helper()
	s := &probeScan{
		consts:        map[string]map[string]string{},
		writtenKeys:   map[string]bool{},
		builders:      map[string]bool{},
		forwarders:    map[string]bool{},
		envParamFuncs: map[string]map[int]bool{},
	}
	files, fset := loadProbeFiles(t, probeRoots)
	s.files = files
	s.fset = fset
	s.consts = probeConsts(files)
	s.envType = probeEnvTypeName(files)

	for _, pf := range s.files {
		if _, ok := probeImportAlias(pf.file, probeExprImportPath); ok && pf.root == probeRuntimeRoot {
			s.runtimeExprImport = pf.path
		}
		s.probeScanFile(pf)
		s.probeScanRunEnv(pf)
	}
	s.probeScanEnv()
	return s
}

// probeScanRunEnv 识别运行期是否把变量表交给表达式环境。
//
// 识别依据是**类型名 + 字段名**:构造 compiler.RunEnv 时必须给 Vars 赋值
// (Vars 就是表达式环境里 `variables` 变量的来源)。
func (s *probeScan) probeScanRunEnv(pf probeFile) {
	if pf.pkg == probeCompilerPkg {
		return // 契约表自身不算"运行期供给"
	}
	ast.Inspect(pf.file, func(n ast.Node) bool {
		var cl *ast.CompositeLit
		switch v := n.(type) {
		case *ast.CompositeLit:
			cl = v
		case *ast.UnaryExpr:
			// &compiler.RunEnv{…}
			inner, ok := v.X.(*ast.CompositeLit)
			if !ok {
				return true
			}
			cl = inner
		default:
			return true
		}
		sel, ok := cl.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RunEnv" {
			return true
		}
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Vars" {
				s.varsSupplied = true
			}
		}
		return true
	})
}

// probeScanFile 扫描一个文件里的会话状态写入点。
func (s *probeScan) probeScanFile(pf probeFile) {
	for _, decl := range pf.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Set" || len(v.Args) != 2 {
					return true
				}
				if key, ok := s.probeResolveKey(v.Args[0], pf.pkg); ok {
					s.addWrite(pf, v.Args[0], key)
				}
			case *ast.AssignStmt:
				if len(v.Lhs) != 1 || len(v.Rhs) != 1 {
					return true
				}
				idx, ok := v.Lhs[0].(*ast.IndexExpr)
				if !ok {
					return true
				}
				sel, ok := idx.X.(*ast.SelectorExpr)
				if !ok || !probeStateMapFields[sel.Sel.Name] {
					return true
				}
				if key, ok := s.probeResolveKey(idx.Index, pf.pkg); ok {
					s.addWrite(pf, idx.Index, key)
				}
			}
			return true
		})
	}
}

// addWrite 记录一处状态写入。
func (s *probeScan) addWrite(pf probeFile, keyExpr ast.Expr, key string) {
	s.writtenKeys[key] = true
	s.writes = append(s.writes, probeWrite{
		pkg:  pf.pkg,
		file: pf.path,
		line: s.fset.Position(keyExpr.Pos()).Line,
		key:  key,
		expr: exprText(keyExpr),
	})
}

// probeEnvTypeOf 判断类型表达式是不是表达式环境类型。
func (s *probeScan) probeEnvTypeOf(e ast.Expr) bool {
	if s.envType == "" {
		return false
	}
	id, ok := e.(*ast.Ident)
	return ok && id.Name == s.envType
}

// probeIsEnvMake 判断调用是不是 make(表达式环境类型, …)。
func (s *probeScan) probeIsEnvMake(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != "make" || len(call.Args) == 0 {
		return false
	}
	return s.probeEnvTypeOf(call.Args[0])
}

// probeFuncs 枚举被扫描的全部函数。
func (s *probeScan) probeFuncs() []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, pf := range s.files {
		for _, decl := range pf.file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				out = append(out, fn)
			}
		}
	}
	return out
}

// probeResultIsEnv 判断函数的结果类型是不是表达式环境。
func (s *probeScan) probeResultIsEnv(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		if s.probeEnvTypeOf(r.Type) {
			return true
		}
	}
	return false
}

// probeResultIsMapStringAny 判断函数的结果类型是不是 map[string]any。
func (s *probeScan) probeResultIsMapStringAny(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		if probeIsMapStringAny(r.Type) {
			return true
		}
	}
	return false
}

// probeCallsAny 判断函数体里是否调用了给定名字的函数。
func probeCallsAny(body *ast.BlockStmt, names map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if names[probeCalleeName(call.Fun)] {
			found = true
			return false
		}
		return true
	})
	return found
}

// probeScanEnv 扫描表达式环境的构造/改写/消费点,并算出契约构造器集合。
func (s *probeScan) probeScanEnv() {
	fns := s.probeFuncs()

	// 第一轮:结果类型是表达式环境的函数 = 契约构造器。
	for _, fn := range fns {
		if s.probeResultIsEnv(fn) {
			s.builders[fn.Name.Name] = true
		}
	}
	// 第二轮:结果类型是 map[string]any 且返回值来自构造器的函数 = 转发构造器
	// (校验期的 newExpressionEnv 就是这种)。迭代到不动点。
	for i := 0; i < 8; i++ {
		changed := false
		for _, fn := range fns {
			if s.builders[fn.Name.Name] || s.forwarders[fn.Name.Name] {
				continue
			}
			if !s.probeResultIsMapStringAny(fn) {
				continue
			}
			if probeCallsAny(fn.Body, s.probeCtorNames()) {
				s.forwarders[fn.Name.Name] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// 第三轮:记下"哪些函数的哪个参数是表达式环境"(用于核对调用点)。
	for _, fn := range fns {
		idx := 0
		for _, p := range fn.Type.Params.List {
			if s.probeEnvTypeOf(p.Type) {
				if s.envParamFuncs[fn.Name.Name] == nil {
					s.envParamFuncs[fn.Name.Name] = map[int]bool{}
				}
				for range p.Names {
					s.envParamFuncs[fn.Name.Name][idx] = true
				}
			}
			idx += len(p.Names)
		}
	}
	// 第四轮:逐函数扫描构造/改写/消费点。
	for _, pf := range s.files {
		exprAlias, hasExpr := probeImportAlias(pf.file, probeExprImportPath)
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			s.probeScanFunc(pf, fn, exprAlias, hasExpr)
		}
	}
}

// probeCtorNames 返回契约构造器与转发构造器的名字集合。
func (s *probeScan) probeCtorNames() map[string]bool {
	out := map[string]bool{}
	for k := range s.builders {
		out[k] = true
	}
	for k := range s.forwarders {
		out[k] = true
	}
	return out
}

// probeIsCtorCall 判断调用是不是契约构造器/转发构造器的调用。
func (s *probeScan) probeIsCtorCall(call *ast.CallExpr) bool {
	return s.probeCtorNames()[probeCalleeName(call.Fun)]
}

// probeScanFunc 扫描单个函数里的表达式环境操作。
func (s *probeScan) probeScanFunc(pf probeFile, fn *ast.FuncDecl, exprAlias string, hasExpr bool) {
	// 表达式环境类型的变量(参数 + 由构造器赋值 + 显式声明)。
	envVars := map[string]bool{}
	for _, p := range fn.Type.Params.List {
		if s.probeEnvTypeOf(p.Type) {
			for _, n := range p.Names {
				envVars[n.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if len(v.Lhs) != 1 || len(v.Rhs) != 1 {
				return true
			}
			id, ok := v.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			call, isCall := v.Rhs[0].(*ast.CallExpr)
			if !isCall {
				return true
			}
			if s.probeIsEnvMake(call) {
				envVars[id.Name] = true
				s.creations = append(s.creations, probeEnvOp{
					pkg: pf.pkg, file: pf.path, line: s.fset.Position(call.Pos()).Line,
					fn: fn.Name.Name, expr: "make(" + s.envType + ", …)",
				})
				return true
			}
			if s.probeIsCtorCall(call) {
				envVars[id.Name] = true
			}
		case *ast.DeclStmt:
			gd, ok := v.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if s.probeEnvTypeOf(vs.Type) {
					for _, n := range vs.Names {
						envVars[n.Name] = true
					}
				}
			}
		}
		return true
	})

	// 改写点:环境变量下标赋值。
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 {
			return true
		}
		idxExpr, ok := assign.Lhs[0].(*ast.IndexExpr)
		if !ok {
			return true
		}
		base, ok := idxExpr.X.(*ast.Ident)
		if !ok || !envVars[base.Name] {
			return true
		}
		key, isLit := "", false
		if bl, ok := idxExpr.Index.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if sv, err := strconv.Unquote(bl.Value); err == nil {
				key, isLit = sv, true
			}
		}
		s.mutations = append(s.mutations, probeEnvOp{
			pkg: pf.pkg, file: pf.path, line: s.fset.Position(idxExpr.Pos()).Line,
			fn: fn.Name.Name, key: key, isLit: isLit, expr: idxExprText(idxExpr),
		})
		return true
	})

	// 消费点:交给表达式引擎,或交给以环境为参数的函数。
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		// expr.Env(x) / expr.Run(program, x)
		if hasExpr {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == exprAlias {
					switch sel.Sel.Name {
					case "Env":
						if len(call.Args) == 1 {
							s.addSink(pf, fn, call.Args[0], envVars)
						}
					case "Run":
						if len(call.Args) == 2 {
							s.addSink(pf, fn, call.Args[1], envVars)
						}
					}
				}
			}
		}
		// 调用以环境为参数的函数(例如 runCondition)。
		if idxs, ok := s.envParamFuncs[probeCalleeName(call.Fun)]; ok {
			for i, a := range call.Args {
				if idxs[i] {
					s.addSink(pf, fn, a, envVars)
				}
			}
		}
		return true
	})
}

// addSink 记录一处表达式环境消费点,并判定环境参数的来源。
func (s *probeScan) addSink(pf probeFile, fn *ast.FuncDecl, arg ast.Expr, envVars map[string]bool) {
	sk := probeSink{
		pkg: pf.pkg, file: pf.path, line: s.fset.Position(arg.Pos()).Line,
		fn: fn.Name.Name, arg: exprText(arg),
	}
	if call, ok := arg.(*ast.CallExpr); ok && s.probeIsCtorCall(call) {
		sk.argIsCtor = true
	}
	if id, ok := arg.(*ast.Ident); ok && envVars[id.Name] {
		sk.argIsEnvVar = true
	}
	s.sinks = append(s.sinks, sk)
}

// exprText 取表达式的源码文本(简版:标识符/字面量/选择器)。
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return probeCalleeName(v.Fun) + "(…)"
	case *ast.IndexExpr:
		return exprText(v.X) + "[…]"
	}
	return "…"
}

// idxExprText 取下标表达式的源码文本。
func idxExprText(e *ast.IndexExpr) string {
	return exprText(e.X) + "[" + exprText(e.Index) + "]"
}

// ---- 测试 ----

// TestConditionEnvContractMatchesRuntimeBackings 核心探针:契约表声明的
// 取值来源必须与运行期实际提供的一致(双向)。
//
//	契约有、运行期没有 → 契约声明了运行期不提供的变量,用户写了永远取不到;
//	运行期有、契约没有 → 运行期注入了契约未声明的变量。
func TestConditionEnvContractMatchesRuntimeBackings(t *testing.T) {
	contract := conditionEnvBackings(t)
	runtime := runtimeConditionBackings

	contractSet := map[condEnvBacking]bool{}
	for _, b := range contract {
		contractSet[b] = true
	}
	runtimeSet := map[condEnvBacking]bool{}
	for _, b := range runtime {
		runtimeSet[b] = true
	}

	for b := range contractSet {
		if !runtimeSet[b] {
			t.Errorf("契约表声明了取值来源 %q,但运行期不提供它(契约声明了运行期不提供的变量,用户写了永远取不到)", b)
		}
	}
	for b := range runtimeSet {
		if !contractSet[b] {
			t.Errorf("运行期提供了取值来源 %q,但契约表没有声明(运行期注入了契约未声明的变量)", b)
		}
	}
}

// TestConditionEnvRuntimeBackingsAreReal 运行期事实表里的每一项都必须
// 在运行期源码里真的存在 —— 否则事实表本身会腐烂成恒真的假保护。
//
//	state:<键> → 该状态键必须真的被写入会话状态(AST 扫描写入点);
//	vars      → 运行期必须真的把变量表交给表达式环境(RunEnv.Vars)。
func TestConditionEnvRuntimeBackingsAreReal(t *testing.T) {
	s := newProbeScan(t)

	for _, b := range runtimeConditionBackings {
		if b == condEnvBackingVars {
			if !s.varsSupplied {
				t.Errorf("运行期没有把变量表交给表达式环境(compiler.RunEnv.Vars 未被赋值):契约声明了 %q 取值来源", b)
			}
			continue
		}
		key := strings.TrimPrefix(string(b), "state:")
		if !s.writtenKeys[key] {
			t.Errorf("运行期事实表登记了状态键 %q,但运行期源码里没有它的写入点(该键不会被提供,契约里的变量永远取不到值)", key)
		}
	}
}

// TestConditionEnvNoUndeclaredRuntimeInjection 运行期不得在表达式求值环境里
// 注入契约未声明的变量。
//
// 四条规则:
//
//  1. 表达式环境只能由契约构造器产出(builder/forwarder);
//  2. 契约构造器之外不得构造或改写表达式环境;
//  3. 契约构造器内部改写环境时,键必须来自契约表(不得写字面量键)——
//     写字面量键就是注入了契约未声明的变量;
//  4. 运行期包不得直接导入表达式引擎(否则可以绕过契约构造器)。
func TestConditionEnvNoUndeclaredRuntimeInjection(t *testing.T) {
	s := newProbeScan(t)

	ctorNames := s.probeCtorNames()
	for _, op := range s.creations {
		if !ctorNames[op.fn] {
			t.Errorf("%s:%d 函数 %s 在契约构造器之外构造了表达式环境(%s):环境只能由契约构造器产出,否则会注入契约未声明的变量",
				op.file, op.line, op.fn, op.expr)
		}
	}
	for _, op := range s.mutations {
		if !ctorNames[op.fn] {
			t.Errorf("%s:%d 函数 %s 在契约构造器之外改写了表达式环境(%s):环境只能由契约构造器产出,否则会注入契约未声明的变量",
				op.file, op.line, op.fn, op.expr)
			continue
		}
		if op.isLit {
			t.Errorf("%s:%d 函数 %s 在契约构造器内部用字面量键改写表达式环境(%s = %q):键必须来自契约表,否则会注入契约未声明的变量",
				op.file, op.line, op.fn, op.expr, op.key)
		}
	}
	// 消费点:交给表达式引擎的环境必须由契约构造器产出。
	for _, sk := range s.sinks {
		if sk.argIsCtor || sk.argIsEnvVar {
			continue
		}
		t.Errorf("%s:%d 函数 %s 把非契约构造器产出的环境 %q 交给表达式引擎:环境只能由契约构造器产出,否则会注入契约未声明的变量",
			sk.file, sk.line, sk.fn, sk.arg)
	}
	// 运行期包不得直接导入表达式引擎。
	if s.runtimeExprImport != "" {
		t.Errorf("%s: 运行期包直接导入了表达式引擎(%s):表达式求值必须经 compiler 的契约构造器,否则运行期可以绕过契约注入未声明的变量",
			s.runtimeExprImport, probeExprImportPath)
	}
}

// TestConditionEnvProbeScansEnough 防空转:扫描器的识别逻辑一旦退化
// (变量改名、访问器改名、扫错目录),扫描结果会变成空集或极小集,而
// "没有漂移"这条断言仍然成立 → 测试恒真通过(虚假安全感)。
//
// 下限取自当前实现的实际扫描结果,留了一点余量。
func TestConditionEnvProbeScansEnough(t *testing.T) {
	s := newProbeScan(t)

	// 契约侧:变量数下限(约定 13 的变量集合不允许悄悄缩水)。
	if len(conditionEnvVars) < 5 {
		t.Fatalf("契约表里只有 %d 个条件变量(下限 5):扫描范围或识别逻辑退化了,本测试会恒真通过", len(conditionEnvVars))
	}
	if len(runtimeConditionBackings) < 5 {
		t.Fatalf("运行期事实表里只有 %d 项(下限 5):扫描范围或识别逻辑退化了,本测试会恒真通过", len(runtimeConditionBackings))
	}
	// 运行期侧:状态写入点下限。
	if len(s.writtenKeys) < 4 {
		t.Fatalf("只扫到 %d 个状态键(下限 4):扫描范围或识别逻辑退化了,本测试会恒真通过", len(s.writtenKeys))
	}
	t.Logf("扫到 %d 处状态写入点,涉及 %d 个状态键", len(s.writes), len(s.writtenKeys))
	// 运行期侧:表达式环境的消费点下限(运行期循环 + 校验期各至少一处)。
	if len(s.sinks) < 2 {
		t.Fatalf("只扫到 %d 个表达式环境消费点(下限 2):扫描范围或识别逻辑退化了,本测试会恒真通过", len(s.sinks))
	}
	// 契约构造器下限(运行期构造器 + 校验期构造器)。
	if len(s.builders) < 2 {
		t.Fatalf("只识别到 %d 个契约构造器(下限 2):扫描范围或识别逻辑退化了,本测试会恒真通过", len(s.builders))
	}
	if len(s.forwarders) < 1 {
		t.Fatalf("只识别到 %d 个转发构造器(下限 1):扫描范围或识别逻辑退化了,本测试会恒真通过", len(s.forwarders))
	}
	// 运行期侧:变量表供给点必须扫到。
	if !s.varsSupplied {
		t.Fatalf("没扫到运行期把变量表交给表达式环境的构造点(compiler.RunEnv{Vars: …}):扫描范围或识别逻辑退化了,本测试会恒真通过")
	}
}
