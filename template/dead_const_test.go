package template

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// deadVarConsts 是本包**曾经**导出、但零引用的 6 个常量。
//
// 为什么它们是死代码:它们只是"上下文槽位键名"的**第三份抄写**
// (另两份是 contextx.StateKey* 与 model.ContextSlot*):
//
//	task / plan / git_diff / review / test_result / iteration
//
// 覆盖 12 个槽位里的 6 个,本身就说明它不是清单而是历史残留。
// 危险的不是"没人用",而是**看起来像清单**:读代码的人会以为
// "模板支持的标准变量就是这 6 个",于是新增槽位时照着它加一个 ——
// 于是又多一份会漂移的来源(约定 13:同一份语义只有一处定义)。
//
// 本测试用**源码扫描**锁住"不得再出现":常量被删掉之后,Go 编译器
// 不再报错(导出常量没人引用是合法的),只有源码扫描能把它钉住。
func TestNoDeadVarConstants(t *testing.T) {
	const src = "template.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s: %v", src, err)
	}

	dead := map[string]bool{
		"VarTask": true, "VarPlan": true, "VarGitDiff": true,
		"VarReview": true, "VarTestResult": true, "VarIteration": true,
	}
	found := map[string]bool{}
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
			for _, name := range vs.Names {
				if dead[name.Name] {
					found[name.Name] = true
				}
			}
		}
	}
	for name := range dead {
		if found[name] {
			t.Errorf("%s 仍在本包声明:它是零引用的死常量,且是上下文槽位键名的"+
				"第三份抄写(另两份是 contextx.StateKey* 与 model.ContextSlot*)—— "+
				"看着像清单会诱导后来者照着它加常量,多出一份会漂移的来源(约定 13)",
				name)
		}
	}
}
