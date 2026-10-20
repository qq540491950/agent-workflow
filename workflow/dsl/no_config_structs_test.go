package dsl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// 本文件是"dsl 包不得再声明节点配置结构体"的守卫。
//
// 背景(AGENTS.md 硬性约定 #13):校验期与运行期必须同源 —— 节点类型的
// 必填配置结构只有**一份**来源:workflow/compiler/nodeconfig.go。
//
// 此前 workflow/dsl 里还留着 9 个零引用的配置结构体(PromptDef /
// AgentNodeConfig / RetryPolicy / SkillNodeConfig / ConditionNodeConfig /
// HumanNodeConfig / ScriptNodeConfig / GitNodeConfig / SubWorkflowNodeConfig),
// 它们是"第二份实现"的化石:内容已过期(缺 model 与 enabled,多出运行期
// 从不读取的 Env),却仍然看起来像一份可用的契约 —— 还会把"把凭据写进
// DSL 结构体"的诱惑变成现实。
//
// 零引用的结构体不会自己消失:下一个人照着旧结构体加字段,就会再次造出
// 第二份契约,于是"校验期要求但运行期不读"的老问题重新长出来。
// 本测试让"再写一份"立刻失败,并指出该去哪里改。

// forbiddenConfigTypes 是 dsl 包里不允许出现的类型名 → 原因。
var forbiddenConfigTypes = map[string]string{
	"PromptDef":               "prompt 契约由 workflow/compiler/nodeconfig.go 定义(运行期直接读 prompt / prompt.template)",
	"AgentNodeConfig":         "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"RetryPolicy":             "重试策略由 workflow/compiler/nodeconfig.go 的 SubKeys 定义",
	"SkillNodeConfig":         "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"ConditionNodeConfig":     "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"HumanNodeConfig":         "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"ScriptNodeConfig":        "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"GitNodeConfig":           "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"SubWorkflowNodeConfig":   "节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"NodeConfigSpec":          "配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"NodeConfigSpecFor":       "配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"RequiredConfigKeys":      "配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"OptionalConfigKeys":      "配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"KnownConfigKeys":         "配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
	"ValidatorOnlyConfigKeys": "配置契约的唯一来源是 workflow/compiler/nodeconfig.go",
}

// TestDSLPackageDeclaresNoConfigStructs dsl 包里不得出现节点配置结构体。
//
// 判据:类型名在 forbiddenConfigTypes 里,或以 "NodeConfig" 结尾
// (后者拦住"换个名字再写一份")。
func TestDSLPackageDeclaresNoConfigStructs(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 dsl 包目录: %v", err)
	}
	found := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				typeName := ts.Name.Name
				reason, banned := forbiddenConfigTypes[typeName]
				if !banned && strings.HasSuffix(typeName, "NodeConfig") {
					reason = "dsl 里不得再声明节点配置结构体(名字后缀 NodeConfig)"
					banned = true
				}
				if !banned {
					continue
				}
				found++
				t.Errorf("workflow/dsl/%s 声明了配置结构体 %q:%s", name, typeName, reason)
			}
		}
	}
	if found == 0 {
		t.Log("dsl 包只保留文档/节点/边等解析结构,配置契约已收敛到 workflow/compiler/nodeconfig.go")
	}
}
