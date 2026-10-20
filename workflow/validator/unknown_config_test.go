package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁定"节点配置里的未知键"必须被**看见**(约定 14:该失败却成功 /
// 静默丢弃与崩溃同样危险)。
//
// 背景:配置键名写错(例如 timeout_seconds 少写一个 s)时,运行期**照常
// 执行节点**,只是那个设置**根本没生效** —— 用户以为超时是 30 秒,实际
// 用的是默认值。此前校验期只查"必填键有没有",拼错的键既不报错也不警告,
// 拼错与没写完全无法区分。
//
// 策略:**警告,不是错误**。
//
//   - 报错的风险:存量工作流里确实存在拼错或已废弃的键,一旦报错,用户
//     **打不开自己的老工作流** —— 那是约定 13 的反例("用户被自己的校验器
//     挡住")。硬要求:任何让存量工作流打不开的改动都不可接受。
//   - 警告的风险:拼错仍然"不生效",但用户看得见提示(前端会把 warnings
//     显示在画布错误面板与 toast 里),且**不阻断保存**。
//
// 已废弃的键(condition.expression)只报"已废弃",**不报"未知"** ——
// 它是契约表里登记过的键(唯一来源:workflow/compiler/nodeconfig.go)。

// warnHas 判断是否给出了指定错误码的警告。
func warnHas(res *Result, code string) bool {
	for _, w := range res.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// warnFor 返回指定节点上的指定错误码警告。
func warnFor(res *Result, node, code string) *ValidationError {
	for i, w := range res.Warnings {
		if w.Code == code && w.Node == node {
			return &res.Warnings[i]
		}
	}
	return nil
}

// TestUnknownNodeConfigKeyIsReportedAsWarning 拼错的配置键必须被报出来。
func TestUnknownNodeConfigKeyIsReportedAsWarning(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: typo
  name: Typo
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
    timeout_second: 30
`)
	res := Validate(wf, agentResolver([]string{"mock-claude"}), skillResolver(nil))
	w := warnFor(res, "a", "UNKNOWN_NODE_CONFIG")
	if w == nil {
		t.Fatalf("拼错的配置键 timeout_second 应给出 UNKNOWN_NODE_CONFIG 警告: %+v", res.Warnings)
	}
	if !strings.Contains(w.Message, "timeout_second") {
		t.Errorf("警告应点名未知键: %+v", w)
	}
	if !strings.Contains(w.Message, "timeout_seconds") {
		t.Errorf("警告应给出可用键清单(或相近键建议): %+v", w)
	}
	t.Logf("警告文案: %s", w.Message)
}

// TestUnknownNodeConfigKeyDoesNotBlockSave 未知键只是警告,**不得**阻断
// 保存 —— 存量工作流里可能有拼错的键,报错会让用户打不开自己的老工作流。
func TestUnknownNodeConfigKeyDoesNotBlockSave(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: typo-save
  name: Typo Save
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
    timeout_second: 30
`)
	res := Validate(wf, agentResolver([]string{"mock-claude"}), skillResolver(nil))
	if !res.Valid {
		t.Errorf("未知配置键不得阻断保存: %+v", res.Errors)
	}
	for _, e := range res.Errors {
		if e.Code == "UNKNOWN_NODE_CONFIG" {
			t.Errorf("未知配置键不得作为错误报出: %+v", res.Errors)
		}
	}
}

// TestDeprecatedConfigKeyIsNotReportedAsUnknown 已废弃的键是契约表里
// 登记过的键,只报"已废弃",不得再报"未知"。
func TestDeprecatedConfigKeyIsNotReportedAsUnknown(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: deprecated
  name: Deprecated
nodes:
  - id: a
    type: human
  - id: gate
    type: condition
    expression: 'decision == "APPROVED"'
edges:
  - from: a
    to: gate
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if warnFor(res, "gate", "UNKNOWN_NODE_CONFIG") != nil {
		t.Errorf("废弃键不得报未知: %+v", res.Warnings)
	}
	if !warnHas(res, "DEPRECATED_NODE_CONFIG") {
		t.Errorf("废弃键应给出 DEPRECATED_NODE_CONFIG 警告: %+v", res.Warnings)
	}
}

// TestDeclaredConfigKeysAreAllKnown 契约表里登记的每一个键(含只有校验期
// 使用的、已废弃的、全局的)都不得被报成未知键。
//
// 这条同时锁住"校验期与运行期同源"(约定 13):键的清单只有 compiler
// 契约表一份来源,校验期不得自己另抄一份。
func TestDeclaredConfigKeysAreAllKnown(t *testing.T) {
	for _, nt := range model.AllNodeTypes() {
		spec, ok := compiler.NodeConfigSpecFor(nt)
		if !ok {
			t.Fatalf("%s 节点没有配置契约", nt)
		}
		if len(spec.Required) == 0 && len(spec.Optional) == 0 &&
			len(spec.ValidatorOnly) == 0 && len(spec.Deprecated) == 0 {
			// parallel / merge 本来就没有任何配置键,只校验全局键 enabled。
			t.Logf("%s 节点没有专属配置键,只校验全局键", nt)
		}
		cfg := map[string]any{}
		add := func(k string) {
			switch k {
			case compiler.NodeEnabledKey:
				cfg[k] = true // 只认布尔,否则会先被判 INVALID_NODE_ENABLED
			case "expression":
				cfg[k] = `decision == "APPROVED"` // 废弃键:语法合法
			case "retry":
				cfg[k] = map[string]any{"max_attempts": 1, "backoff": "fixed"}
			default:
				cfg[k] = "x"
			}
		}
		for _, k := range compiler.RequiredConfigKeys(nt) {
			add(k)
		}
		for _, k := range compiler.OptionalConfigKeys(nt) {
			add(k)
		}
		for _, k := range compiler.ValidatorOnlyConfigKeys(nt) {
			add(k)
		}
		for _, k := range compiler.DeprecatedConfigKeys(nt) {
			add(k)
		}
		for _, k := range compiler.GlobalConfigKeys() {
			add(k)
		}
		wf := &model.Workflow{
			ID:   "wf",
			Name: "wf",
			Nodes: []model.Node{
				{ID: "n1", Type: nt, Config: cfg},
			},
		}
		res := Validate(wf, nil, nil)
		if w := warnFor(res, "n1", "UNKNOWN_NODE_CONFIG"); w != nil {
			t.Errorf("%s 节点的契约键被报成未知: %+v", nt, w)
		}
	}
}

// TestRepoExampleWorkflowsStayValid 仓库里的示例 DSL(应用启动时的种子
// 工作流)必须**全部**仍然通过校验,且不得出现未知键告警。
//
// 这是"加了未知键报告之后,存量工作流仍然打得开"的回归证据。
func TestRepoExampleWorkflowsStayValid(t *testing.T) {
	dir := filepath.Join("..", "..", "workflows", "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取示例目录 %s: %v", dir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("示例目录 %s 是空的(夹具丢失?)", dir)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("读取 %s: %v", e.Name(), err)
		}
		wf := mustWf(t, string(raw))
		res := Validate(wf, nil, nil)
		if !res.Valid {
			t.Errorf("示例 %s 校验失败(存量工作流必须仍能打开): %+v", e.Name(), res.Errors)
		}
		if warnHas(res, "UNKNOWN_NODE_CONFIG") {
			t.Errorf("示例 %s 出现未知配置键告警: %+v", e.Name(), res.Warnings)
		}
	}
}
