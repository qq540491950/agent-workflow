package validator

import (
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁定"permissions 段写进节点 config"必须**硬失败**(fail closed),
// 与 DSL 解析层(workflow:/settings: 段错放)同一个错误码。
//
// 背景:权限覆盖(需求.md 第十八章)是文档根级段,节点级不受支持。用户把
// 它写进节点 config,意图是"收紧该节点的权限",但运行期会整体忽略 →
// 权限收紧无声失效。泛化的 UNKNOWN_NODE_CONFIG 只是警告,压不住这种
// 安全语义的静默丢失,因此这里升级为硬错误。
//
// 与"未知配置键只警告"策略的边界:permissions(及 JSON 拼写 permission)
// 是**登记过的文档根级段名**,不是"拼错的键" —— 报错不会误伤存量工作流
// (存量里权限段只会在根级,根级写法不受本检查影响)。

// misplacedPermWf 构造"permissions 写进 agent 节点 config"的工作流,
// spelling 是节点 config 里的键名(permission / permissions 两种拼写)。
func misplacedPermWf(t *testing.T, spelling string) *model.Workflow {
	t.Helper()
	return mustWf(t, `
workflow:
  id: misplaced-perm
  name: Misplaced Perm
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
    `+spelling+`:
      claude-code:
        filesystem:
          write: false
`)
}

// TestMisplacedPermissionsInNodeConfigFailsClosed 两种拼写都必须硬失败,
// 错误码与 DSL 解析层同一个(model.CodeMisplacedSection)。
func TestMisplacedPermissionsInNodeConfigFailsClosed(t *testing.T) {
	for _, spelling := range []string{"permissions", "permission"} {
		res := Validate(misplacedPermWf(t, spelling), agentResolver([]string{"mock-claude"}), skillResolver(nil))
		if res.Valid {
			t.Fatalf("节点 config 里的 %q 段不得静默通过(权限收紧无声失效): %+v", spelling, res.Warnings)
		}
		found := false
		for _, e := range res.Errors {
			if e.Code == model.CodeMisplacedSection && e.Node == "a" {
				found = true
				if !strings.Contains(e.Message, "文档根级") {
					t.Errorf("错误信息应指出正确写法(文档根级): %+v", e)
				}
			}
		}
		if !found {
			t.Errorf("%q 拼写未给出 %s 错误: %+v", spelling, model.CodeMisplacedSection, res.Errors)
		}
	}
}

// TestMisplacedPermissionsIsNotAlsoUnknownWarning 硬失败不得同时再给一条
// 泛化的 UNKNOWN_NODE_CONFIG 警告 —— 同一个键报两遍,用户会以为有两个问题。
func TestMisplacedPermissionsIsNotAlsoUnknownWarning(t *testing.T) {
	res := Validate(misplacedPermWf(t, "permissions"), agentResolver([]string{"mock-claude"}), skillResolver(nil))
	if w := warnFor(res, "a", "UNKNOWN_NODE_CONFIG"); w != nil {
		t.Errorf("permissions 段错放只应报硬错误,不得重复报 UNKNOWN_NODE_CONFIG: %+v", w)
	}
}

// TestRootLevelPermissionsStillValid 根级 permissions 段(正确写法)不得
// 被本检查误伤;顺带锁住"根级 + 节点级同时出现"时节点级仍要报。
func TestRootLevelPermissionsStillValid(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: root-perm
  name: Root Perm
permissions:
  claude-code:
    filesystem:
      write: false
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
`)
	res := Validate(wf, agentResolver([]string{"mock-claude"}), skillResolver(nil))
	if !res.Valid {
		t.Fatalf("根级 permissions 段是正确写法,不得报错: %+v", res.Errors)
	}

	wf.Nodes[0].Config = map[string]any{
		"permissions": map[string]any{"claude-code": map[string]any{}},
	}
	res = Validate(wf, agentResolver([]string{"mock-claude"}), skillResolver(nil))
	if res.Valid {
		t.Fatal("根级正确 + 节点级错放,节点级仍必须报错")
	}
}

// TestPermissionSectionKeysMatchSpellings 键集合的唯一来源断言:两种拼写
// 必须都在 model.PermissionSectionKeys 里 —— 若权限段将来增加/改名拼写,
// 这里会先红,提示同步校验层与测试。
func TestPermissionSectionKeysMatchSpellings(t *testing.T) {
	keys := model.PermissionSectionKeys()
	for _, want := range []string{"permission", "permissions"} {
		if !keys[want] {
			t.Errorf("PermissionSectionKeys 缺少 %q(两种序列化格式的正式拼写都必须在列)", want)
		}
	}
}
