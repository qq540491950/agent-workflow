package runtime

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/permission"
	"agentworkflow/workflow/dsl"
)

// 本文件锁住"运行期也拒绝**想放宽**全局禁用的工作流"。
//
// 现状(迭代 13 之前):应用层保存工作流时已经用
// Perms.ValidateTightenOnly 拦下"想放宽"的 permissions 段
// (app/application/application.go),但**引擎层**没有这道检查:
//   - 历史工作流(规则之前保存的)、备份恢复出来的工作流,
//     走 Engine.Start 时可以带着"想放宽"的 permissions 段跑起来。
//
// 后果:permissions 段里写的 `write: true`(全局已禁用写)在运行期被
// **静默忽略**(有效权限 = 全局 ∧ 覆盖段,只能收紧),而用户以为它生效了。
// 更糟的是:该节点若恰好不需要写权限(如 mode: plan),全程**没有任何**
// 报错 —— 用户永远不知道自己写的收紧/放宽规则没起作用(约定 14)。
//
// 现在:引擎的 Validate 也做同一道检查(与保存期同一份实现),
// "想放宽"在**跑起来之前**就被明确拒绝。

// loosenPermYAML 的 permissions 段试图打开 mock-a 的写权限,
// 而全局策略里写权限是禁用的。
const loosenPermYAML = `
version: "1"
permissions:
  mock-a:
    filesystem:
      write: true
workflow:
  id: perm-loosen
  name: Perm Loosen
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`

// TestEngineValidateRejectsLooseningOverride 引擎校验必须报出"想放宽"。
func TestEngineValidateRejectsLooseningOverride(t *testing.T) {
	eng := newMinimalEngine(t)
	// 全局禁用写权限
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: false})
	doc, err := dsl.Parse([]byte(loosenPermYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if len(wf.Permission) == 0 {
		t.Fatal("permissions 段未被解析(检查 YAML 层级:它是文档根级段)")
	}
	res := eng.Validate(wf)
	if res.Valid {
		t.Fatal("permissions 段试图放宽全局禁用时,引擎校验必须判非法")
	}
	found := false
	for _, e := range res.Errors {
		if e.Code == "PERMISSION_OVERRIDE_LOOSENS" {
			found = true
			if !strings.Contains(e.Message, "只能收紧") {
				t.Errorf("错误消息必须说明 permissions 段只能收紧,实际: %q", e.Message)
			}
		}
	}
	if !found {
		t.Errorf("错误码必须是 PERMISSION_OVERRIDE_LOOSENS(前端按此码给出人话提示),实际: %+v", res.Errors)
	}
}

// TestStartRejectsLooseningOverride 想放宽的工作流不得被跑起来。
//
// 这是"历史工作流"路径:保存期已经拦过,引擎层是第二道门。
// 节点用 mode: plan —— 它**不需要**写权限,旧行为下全程无任何报错,
// 用户以为 permissions 段生效了(约定 14)。
func TestStartRejectsLooseningOverride(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: false})
	doc, err := dsl.Parse([]byte(loosenPermYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if _, err := eng.Start(context.Background(), wf, "t", nil); err == nil {
		t.Fatal("permissions 段试图放宽全局禁用的工作流不得被运行")
	} else if !strings.Contains(err.Error(), "permissions 段") {
		t.Errorf("错误消息必须指明是 permissions 段的问题,实际: %q", err.Error())
	}
}

// TestEngineValidateAllowsTighteningOverride 对照组:收紧(而不是放宽)
// 的 permissions 段必须照常通过校验 —— 不能把正常路径堵死。
func TestEngineValidateAllowsTighteningOverride(t *testing.T) {
	yml := `
version: "1"
permissions:
  mock-a:
    filesystem:
      write: false
workflow:
  id: perm-tighten
  name: Perm Tighten
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`
	eng := newMinimalEngine(t)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("收紧的 permissions 段必须通过校验: %+v", res.Errors)
	}
}

// TestEngineValidateAllowsNoPermissionSection 没有 permissions 段的工作流
// 不受影响(不能因为新增检查把普通工作流判非法)。
func TestEngineValidateAllowsNoPermissionSection(t *testing.T) {
	yml := `
version: "1"
workflow:
  id: perm-none
  name: Perm None
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`
	eng := newMinimalEngine(t)
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("无 permissions 段的工作流必须通过校验: %+v", res.Errors)
	}
}
