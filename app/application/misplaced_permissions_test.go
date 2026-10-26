package application

// 本文件锁定开放清单第 7 条的收口:permissions 段写进节点 config 时,
// 两条保存入口(YAML 导入 / JSON 保存载荷)必须给出**同一个错误码**
// (约定 13:同一份定义换个格式导入行为不得不同)。
//
// 此前的形态:DSL 解析层对 workflow:/settings: 段错放报 DSL_MISPLACED_KEY,
// 而节点 config 里的 permissions 段只得到一条泛化的 UNKNOWN_NODE_CONFIG
// **警告**(保存照样成功)—— 用户以为权限收紧了,实际整段被忽略。

import (
	"errors"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// nodeLevelPermissionsYAML / nodeLevelPermissionsJSON 是同一份"权限段
// 写进节点 config"的定义的两种格式(节点 a 的 config 里放 permissions)。
const nodeLevelPermissionsYAML = `
workflow:
  id: wf-node-perm
  name: Node Perm
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
    permissions:
      claude-code:
        filesystem:
          write: false
`

const nodeLevelPermissionsJSON = `{
  "id": "wf-node-perm",
  "name": "Node Perm",
  "nodes": [
    {"id": "a", "type": "agent",
     "config": {"agent": "mock-claude", "mode": "plan",
                "permissions": {"claude-code": {"filesystem": {"write": false}}}}}
  ],
  "edges": [],
  "settings": {}
}`

// assertMisplacedPermissionError 断言错误是 WORKFLOW_INVALID 且错误体里
// 点名 DSL_MISPLACED_KEY,返回其中的错放码供两条入口比对。
func assertMisplacedPermissionError(t *testing.T, entry string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s 入口:节点 config 里的 permissions 段不得静默通过(权限收紧无声失效)", entry)
	}
	var merr *model.Error
	if !errors.As(err, &merr) {
		t.Fatalf("%s 入口:错误应是结构化 model.Error,got %v", entry, err)
	}
	if merr.Code != "WORKFLOW_INVALID" {
		t.Fatalf("%s 入口:错误码 = %q,期望 WORKFLOW_INVALID(%v)", entry, merr.Code, err)
	}
	if !strings.Contains(merr.Message, model.CodeMisplacedSection) {
		t.Fatalf("%s 入口:错误体未点名 %s(两条入口必须同码): %s", entry, model.CodeMisplacedSection, merr.Message)
	}
}

// TestMisplacedPermissionsSameCodeOnBothEntries 两条入口同一个 code。
func TestMisplacedPermissionsSameCodeOnBothEntries(t *testing.T) {
	a := newTestApp(t)

	if _, err := a.Workflows.ImportYAML(nodeLevelPermissionsYAML); err != nil {
		assertMisplacedPermissionError(t, "ImportYAML", err)
	} else {
		t.Fatal("ImportYAML:节点 config 里的 permissions 段不得静默通过")
	}

	if _, err := a.Workflows.SavePayload(nodeLevelPermissionsJSON); err != nil {
		assertMisplacedPermissionError(t, "SavePayload", err)
	} else {
		t.Fatal("SavePayload:节点 config 里的 permissions 段不得静默通过")
	}
}

// TestRootLevelPermissionsStillSaves 根级 permissions(正确写法)在两条
// 入口上都仍然可用 —— 本修复不得误伤既有契约。
func TestRootLevelPermissionsStillSaves(t *testing.T) {
	a := newTestApp(t)

	if _, err := a.Workflows.ImportYAML(`
workflow:
  id: wf-root-perm
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
`); err != nil {
		t.Fatalf("根级 permissions 不得被误伤: %v", err)
	}

	rootJSON := `{
  "id": "wf-root-perm-json",
  "name": "Root Perm JSON",
  "permission": {"claude-code": {"filesystem": {"write": false}}},
  "nodes": [
    {"id": "a", "type": "agent",
     "config": {"agent": "mock-claude", "mode": "plan"}}
  ],
  "edges": [],
  "settings": {}
}`
	if _, err := a.Workflows.SavePayload(rootJSON); err != nil {
		t.Fatalf("顶层 permission 字段(JSON 拼写)不得被误伤: %v", err)
	}
}
