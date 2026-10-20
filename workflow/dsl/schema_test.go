package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"DSL 解析不再静默丢弃键"的行为(迭代 15)。
//
// 背景:解析器此前只对 YAML 的**顶层**键做白名单检查,其余位置一律
// 非严格解析 —— 键被**静默丢弃**,用户以为设置生效了,实际没有:
//
//	workflow:
//	  id: x
//	  permissions:      ← 权限收紧写错层级 → 整段被丢弃 → 权限没收紧
//	    claude-code: ...
//
// 这一类失败没有任何提示(runtime 拿到的是空权限表),正是约定 14
// 说的"该失败却成功"。本文件把策略钉死:
//
//   - 顶层未知键 → 报错(YAML 与 JSON **同源**,此前 JSON 完全不检查);
//   - permissions 写错层级 → 报错(权限收紧无声失效 = 安全语义被悄悄放开);
//   - 其余未知/错层级键 → 忽略但**可观测**(记入 Document.IgnoredKeys + WARN 日志),
//     不阻断导入(存量 DSL 里这类写法存在,直接报错会让它们导不进来)。
//
// 节点 config 的键**不在**本文件管辖范围:节点配置契约的唯一来源是
// workflow/compiler/nodeconfig.go(AGENTS.md 硬性约定 #13),由校验期报告。

func parseErrCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("期望解析报错,实际无错误")
	}
	e, ok := err.(*model.Error)
	if !ok {
		t.Fatalf("错误类型 = %T, want *model.Error", err)
	}
	if e.Kind != model.KindValidationError {
		t.Errorf("kind = %q, want ValidationError", e.Kind)
	}
	return e.Code
}

// TestParseRejectsMisplacedPermissions 错层级的 permissions 必须报错。
//
// 这是迭代 10 记录下来的遗留项:permissions 写进 workflow: 段会被解析器
// 丢掉,用户的权限收紧**无声失效**(约定 14:该失败却成功)。
func TestParseRejectsMisplacedPermissions(t *testing.T) {
	const yml = `
version: "1"
workflow:
  id: x
  name: X
  permissions:
    claude-code:
      filesystem:
        write: false
nodes: []
`
	_, err := Parse([]byte(yml))
	code := parseErrCode(t, err)
	if code != "DSL_MISPLACED_KEY" {
		t.Errorf("错误码 = %q, want DSL_MISPLACED_KEY", code)
	}
}

// TestParseRejectsMisplacedPermissionsJSON JSON 路径必须与 YAML 同源。
//
// 此前 JSON 路径**完全不检查**顶层键(YAML 会报错,JSON 静默接受),
// 同一份 DSL 换种格式导入行为就不同 —— 违反约定 13 的"同源"精神。
func TestParseRejectsMisplacedPermissionsJSON(t *testing.T) {
	const src = `{
  "version": "1",
  "workflow": {"id": "x", "name": "X", "permissions": {"claude-code": {"filesystem": {"write": false}}}},
  "nodes": []
}`
	_, err := Parse([]byte(src))
	code := parseErrCode(t, err)
	if code != "DSL_MISPLACED_KEY" {
		t.Errorf("错误码 = %q, want DSL_MISPLACED_KEY", code)
	}
}

// TestParseRejectsUnknownTopLevelKeyJSON JSON 路径的顶层白名单。
//
// YAML 路径早就有 DSL_UNKNOWN_KEY 检查,JSON 路径此前没有 —— 拼错的顶层键
// (如 "nodez") 在 JSON 里被静默丢弃,导入出一个空工作流。
func TestParseRejectsUnknownTopLevelKeyJSON(t *testing.T) {
	const src = `{"version":"1","workflow":{"id":"x","name":"X"},"nodes":[],"nodez":[]}`
	_, err := Parse([]byte(src))
	code := parseErrCode(t, err)
	if code != "DSL_UNKNOWN_KEY" {
		t.Errorf("错误码 = %q, want DSL_UNKNOWN_KEY", code)
	}
}

// TestParseTopLevelStrictnessSameForYAMLAndJSON 两种格式的顶层严格度必须一致。
func TestParseTopLevelStrictnessSameForYAMLAndJSON(t *testing.T) {
	yml := "version: \"1\"\nworkflow:\n  id: x\n  name: X\nnodes: []\nnodez: []\n"
	js := `{"version":"1","workflow":{"id":"x","name":"X"},"nodes":[],"nodez":[]}`

	_, errY := Parse([]byte(yml))
	_, errJ := Parse([]byte(js))
	if errY == nil || errJ == nil {
		t.Fatalf("两种格式都应报错,yaml=%v json=%v", errY, errJ)
	}
	if parseErrCode(t, errY) != parseErrCode(t, errJ) {
		t.Errorf("两种格式错误码不一致: yaml=%v json=%v", errY, errJ)
	}
}

// TestParseIgnoresButReportsUnknownNestedKey 其余未知键:不阻断导入,但必须可观测。
//
// 存量 DSL 里确实存在把 settings 写在 workflow: 段下的写法(仓库自己的
// 测试夹具里就有),直接报错会让这些工作流导不进来 —— 因此对**非
// permissions** 的错层级键采取"忽略 + 记 WARN + 记入 IgnoredKeys"。
func TestParseIgnoresButReportsUnknownNestedKey(t *testing.T) {
	const yml = `
version: "1"
workflow:
  id: x
  name: X
  settings:
    max_iterations: 5
nodes: []
`
	doc, err := Parse([]byte(yml))
	if err != nil {
		t.Fatalf("不应阻断导入: %v", err)
	}
	if !containsString(doc.IgnoredKeys, "workflow.settings") {
		t.Errorf("IgnoredKeys = %v, want 包含 workflow.settings", doc.IgnoredKeys)
	}
}

// TestParseIgnoresButReportsUnknownSettingsKey settings 段内的拼写错误同样可观测。
func TestParseIgnoresButReportsUnknownSettingsKey(t *testing.T) {
	const yml = `
version: "1"
workflow:
  id: x
  name: X
settings:
  max_iteration: 10
nodes: []
`
	doc, err := Parse([]byte(yml))
	if err != nil {
		t.Fatalf("不应阻断导入: %v", err)
	}
	if !containsString(doc.IgnoredKeys, "settings.max_iteration") {
		t.Errorf("IgnoredKeys = %v, want 包含 settings.max_iteration", doc.IgnoredKeys)
	}
	// 被忽略的键确实没有生效(语义仍是"丢弃"),但用户现在能看见它。
	if doc.Settings.MaxIterations != 0 {
		t.Errorf("MaxIterations = %d, want 0(拼错的键不生效)", doc.Settings.MaxIterations)
	}
}

// TestParseAcceptsRepoExamples 仓库示例必须仍能导入(向后兼容的硬约束)。
func TestParseAcceptsRepoExamples(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "workflows", "examples", "*.y*ml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("未找到示例工作流")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s: %v", f, err)
		}
		doc, err := Parse(raw)
		if err != nil {
			t.Errorf("示例 %s 导入失败: %v", f, err)
			continue
		}
		if len(doc.IgnoredKeys) != 0 {
			t.Errorf("示例 %s 出现被忽略的键: %v", f, doc.IgnoredKeys)
		}
	}
}

// TestParseAcceptsPermissionsAtRoot permissions 写在**根级**是合法用法。
func TestParseAcceptsPermissionsAtRoot(t *testing.T) {
	const yml = `
version: "1"
permissions:
  claude-code:
    filesystem:
      write: false
workflow:
  id: x
  name: X
nodes: []
`
	doc, err := Parse([]byte(yml))
	if err != nil {
		t.Fatalf("根级 permissions 应被接受: %v", err)
	}
	if len(doc.Permission) != 1 {
		t.Errorf("Permission = %v, want 1 个 agent", doc.Permission)
	}
	if len(doc.IgnoredKeys) != 0 {
		t.Errorf("IgnoredKeys = %v, want 空", doc.IgnoredKeys)
	}
}

// TestRoundTripExportImportHasNoIgnoredKeys 导出的 DSL 再导入不应产生"被忽略的键"。
//
// 导出文本来自 dsl.FromModel(它按结构体字段生成),因此必须能被自己的
// 解析器完整接受 —— 否则"导出→导入"会凭空多出一批 WARN。
func TestRoundTripExportImportHasNoIgnoredKeys(t *testing.T) {
	wf := &model.Workflow{
		ID:   "x",
		Name: "X",
		Permission: map[string]any{
			"claude-code": map[string]any{"filesystem": map[string]any{"write": false}},
		},
		Nodes: []model.Node{{ID: "a", Name: "A", Type: model.NodeTypeAgent, Config: map[string]any{
			"agent": "claude-code", "mode": "plan",
		}}},
		Edges: []model.Edge{},
	}
	raw, err := FromModel(wf).EncodeYAML()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	doc, err := Parse(raw)
	if err != nil {
		t.Fatalf("导出文本再导入失败: %v\n%s", err, raw)
	}
	if len(doc.IgnoredKeys) != 0 {
		t.Errorf("往返后出现被忽略的键: %v\n%s", doc.IgnoredKeys, raw)
	}
	if !strings.Contains(string(raw), "permissions:") {
		t.Errorf("导出文本缺少 permissions 段:\n%s", raw)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
