package application

// 本文件锁住"两种模式同源"的应用层契约(迭代 22 · 双模式一致性)。
//
// 背景:前几轮的很多修复只落在 REST 侧或只落在 app 层,导致同一个语义在
// 两种模式下行为不同 —— 桌面应用特有的缺陷类型,冒烟脚本(只测服务器模式)
// 抓不到。本文件把"两种模式共用的后端"的行为钉死:
//
//  1. 顶层键严格校验必须下沉到应用层:桌面模式走 Wails 绑定
//     (app.Workflows.Save),绑定把载荷反序列化成 model.Workflow 时未知
//     顶层键被**静默丢弃** —— 与服务器模式(400 UNKNOWN_FIELD)行为不同。
//  2. 校验警告必须随结果回到调用方:此前只进日志,直连 API 的调用方
//     永远看不到,用户以为设置生效了。
//  3. 导入时被解析器丢弃的键必须随结果回到调用方:服务器模式的 import
//     端点自己又解析了一遍 DSL 才补上这个字段,桌面模式完全没有。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// validWorkflowPayload 是一份最小可保存的工作流载荷(前端"新建工作流"
// 按钮发出的形状,见 frontend/src/pages/Workflows.tsx 的 create)。
const validWorkflowPayload = `{
  "id": "wf-payload",
  "name": "载荷测试",
  "description": "",
  "version": 0,
  "enabled": true,
  "nodes": [
    {"id": "start", "name": "开始", "type": "human", "position": {"x": 80, "y": 160},
     "config": {"prompt": "任务开始前确认", "responses": ["approve", "continue"]}}
  ],
  "edges": [],
  "settings": {"max_iterations": 5, "on_loop_limit": "wait_user"}
}`

// TestSavePayloadRejectsUnknownTopLevelKey 桌面模式的保存入口必须拒掉
// 未知顶层键 —— 与服务器模式同一个判定(UNKNOWN_FIELD)。
//
// 此前桌面模式走 app.Workflows.Save(模型级):未知键在反序列化时就被
// json.Unmarshal 静默丢弃,接口返回成功,用户以为那份设置生效了。
func TestSavePayloadRejectsUnknownTopLevelKey(t *testing.T) {
	a := newTestApp(t)

	payload := `{
      "id": "wf-unknown", "name": "Unknown", "version": 1, "enabled": true,
      "timeout_seconds": 30,
      "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
      "edges": [], "settings": {}
    }`
	_, err := a.Workflows.SavePayload(payload)
	if err == nil {
		t.Fatal("未知顶层键必须被拒,桌面模式此前静默接受(与服务器模式行为不同)")
	}
	var se *model.Error
	if !errors.As(err, &se) {
		t.Fatalf("错误必须是结构化的 *model.Error,实际: %T (%v)", err, err)
	}
	if se.Code != "UNKNOWN_FIELD" {
		t.Errorf("code = %q, want UNKNOWN_FIELD", se.Code)
	}
	if !strings.Contains(se.Message, "timeout_seconds") {
		t.Errorf("错误信息应点名未知键: %s", se.Message)
	}
	// 不得落库
	if list, _ := a.Workflows.List(); len(list) != 0 {
		t.Errorf("非法载荷不应落库,现有 %d 个工作流", len(list))
	}
}

// TestSavePayloadAcceptsFrontendPayload 前端的保存载荷必须照旧成功
// (严格化不得打破前端保存路径 —— 向后兼容硬要求)。
func TestSavePayloadAcceptsFrontendPayload(t *testing.T) {
	a := newTestApp(t)

	res, err := a.Workflows.SavePayload(validWorkflowPayload)
	if err != nil {
		t.Fatalf("前端保存载荷应当被接受: %v", err)
	}
	if res == nil || res.Workflow == nil {
		t.Fatal("保存结果必须带回工作流")
	}
	if res.Workflow.ID != "wf-payload" {
		t.Errorf("工作流 ID = %q, want wf-payload", res.Workflow.ID)
	}
	if len(res.Workflow.Nodes) != 1 {
		t.Errorf("节点数 = %d, want 1", len(res.Workflow.Nodes))
	}
}

// TestSavePayloadAcceptsEveryKnownTopLevelKey 模型里的每一个顶层字段
// (含 created_at/updated_at 这类回写字段)都必须被接受 —— 否则
// "GET 一份再 POST 回去"这种最常见的往返会失败。
func TestSavePayloadAcceptsEveryKnownTopLevelKey(t *testing.T) {
	a := newTestApp(t)

	payload := `{
      "id": "wf-known", "name": "Known", "description": "d", "version": 2,
      "enabled": true, "variables": {"k": "v"},
      "permission": {"claude-code": {"filesystem": {"write": false}}},
      "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
      "edges": [],
      "settings": {"max_iterations": 5, "on_loop_limit": "wait_user"},
      "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"
    }`
	res, err := a.Workflows.SavePayload(payload)
	if err != nil {
		t.Fatalf("全部已知键应当被接受: %v", err)
	}
	if len(res.Workflow.Permission) == 0 {
		t.Error("权限覆盖必须真的存下来(不是被静默丢弃)")
	}
}

// TestParseWorkflowPayloadRejectsInvalidJSON 非法 JSON 必须是结构化错误
// (INVALID_JSON),不能是裸的解析错误 —— 两种模式给调用方同一个码。
func TestParseWorkflowPayloadRejectsInvalidJSON(t *testing.T) {
	_, err := ParseWorkflowPayload([]byte("{not json"))
	if err == nil {
		t.Fatal("非法 JSON 必须报错")
	}
	var se *model.Error
	if !errors.As(err, &se) {
		t.Fatalf("错误必须是结构化的 *model.Error,实际: %T", err)
	}
	if se.Code != "INVALID_JSON" {
		t.Errorf("code = %q, want INVALID_JSON", se.Code)
	}
}

// TestSavePayloadReturnsValidationWarnings 保存结果必须带回校验警告 ——
// 此前警告只进日志,直连 API 的调用方永远看不到。
func TestSavePayloadReturnsValidationWarnings(t *testing.T) {
	a := newTestApp(t)

	// condition 节点的 expression 配置键已废弃(编译器不再读取它):
	// 能通过校验,但带 DEPRECATED_NODE_CONFIG 警告。
	payload := `{
      "id": "wf-warn", "name": "Warn", "version": 1, "enabled": true,
      "nodes": [{"id": "c", "name": "C", "type": "condition", "position": {"x":0,"y":0},
                 "config": {"expression": "1 == 1"}}],
      "edges": [], "settings": {}
    }`
	res, err := a.Workflows.SavePayload(payload)
	if err != nil {
		t.Fatalf("带警告的工作流应当保存成功: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Code == "DEPRECATED_NODE_CONFIG" {
			found = true
		}
	}
	if !found {
		t.Errorf("保存结果必须带回 DEPRECATED_NODE_CONFIG 警告,实际: %+v", res.Warnings)
	}
}

// TestImportYAMLReturnsIgnoredKeys 导入结果必须带回"被解析器丢弃的键"。
//
// 此前 IgnoredKeys 只落在内存 + WARN 日志里:服务器模式的 import 端点
// 自己又解析了一遍 DSL 才把这个字段补到响应上,桌面模式(走绑定)则完全
// 没有 —— 同一个语义,两种模式行为不同。
func TestImportYAMLReturnsIgnoredKeys(t *testing.T) {
	a := newTestApp(t)

	// settings 写在 workflow 段下 → 解析器丢弃它并记入 IgnoredKeys
	yaml := "version: \"1\"\nworkflow:\n  id: imp-ignored\n  name: Ignored\n  settings:\n    max_iterations: 3\nnodes:\n  - id: a\n    type: human\n    prompt: hi\nedges: []\n"
	res, err := a.Workflows.ImportYAML(yaml)
	if err != nil {
		t.Fatalf("导入工作流: %v", err)
	}
	if len(res.IgnoredKeys) == 0 {
		t.Fatal("导入结果必须带回被忽略的键(桌面模式此前完全没有这个信息)")
	}
	found := false
	for _, k := range res.IgnoredKeys {
		if strings.Contains(k, "settings") {
			found = true
		}
	}
	if !found {
		t.Errorf("ignored_keys 应点名被丢弃的键: %v", res.IgnoredKeys)
	}
}

// TestImportYAMLWithoutIgnoredKeys 没有任何键被忽略时,ignored_keys 为空
// (前端用 ?? [] 兜底)。
func TestImportYAMLWithoutIgnoredKeys(t *testing.T) {
	a := newTestApp(t)

	yaml := "version: \"1\"\nworkflow:\n  id: imp-clean\n  name: Clean\nnodes:\n  - id: a\n    type: human\n    prompt: hi\nedges: []\n"
	res, err := a.Workflows.ImportYAML(yaml)
	if err != nil {
		t.Fatalf("导入工作流: %v", err)
	}
	if len(res.IgnoredKeys) != 0 {
		t.Errorf("没有键被忽略时 ignored_keys 应为空,实际: %v", res.IgnoredKeys)
	}
}

// TestRunReturnsValidationWarnings 运行结果必须带回校验警告(此前只进日志)。
func TestRunReturnsValidationWarnings(t *testing.T) {
	a := newTestApp(t)

	// 带 DEPRECATED_NODE_CONFIG 警告的工作流
	yaml := "version: \"1\"\nworkflow:\n  id: wf-run-warn\n  name: Warn\nnodes:\n  - id: c\n    type: condition\n    expression: \"1 == 1\"\nedges: []\n"
	res, err := a.Workflows.ImportYAML(yaml)
	if err != nil {
		t.Fatalf("导入工作流: %v", err)
	}
	run, err := a.Executions.Run(context.Background(), res.Workflow.ID, "t", nil)
	if err != nil {
		t.Fatalf("运行工作流: %v", err)
	}
	found := false
	for _, w := range run.Warnings {
		if w.Code == "DEPRECATED_NODE_CONFIG" {
			found = true
		}
	}
	if !found {
		t.Errorf("运行结果必须带回 DEPRECATED_NODE_CONFIG 警告,实际: %+v", run.Warnings)
	}
}

// TestWorkflowResultJSONKeepsFlatShape 结果结构序列化后,工作流字段仍在
// **顶层**(既有 REST 响应契约不变),warnings/ignored_keys 是附加字段。
//
// 这一点是向后兼容的关键:第三方客户端直接读 body.id / body.nodes,
// 若改成 {workflow: {...}} 包一层,全部客户端都会崩。
func TestWorkflowResultJSONKeepsFlatShape(t *testing.T) {
	a := newTestApp(t)

	res, err := a.Workflows.SavePayload(validWorkflowPayload)
	if err != nil {
		t.Fatalf("保存: %v", err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("序列化结果: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("结果不是 JSON 对象: %v", err)
	}
	if m["id"] != "wf-payload" {
		t.Errorf("工作流字段必须在顶层(既有响应契约): %s", raw)
	}
	if m["nodes"] == nil {
		t.Errorf("nodes 必须在顶层: %s", raw)
	}
	if _, ok := m["workflow"]; ok {
		t.Errorf("不得把工作流包进 workflow 字段(会破坏既有客户端): %s", raw)
	}
}

// TestAllowedWorkflowTopLevelKeysDerivedFromModel 允许键集合必须由
// model.Workflow 的 json tag 反射派生(单一来源,模型加字段自动生效)。
func TestAllowedWorkflowTopLevelKeysDerivedFromModel(t *testing.T) {
	keys := AllowedWorkflowTopLevelKeys()
	has := func(k string) bool {
		for _, x := range keys {
			if x == k {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"id", "name", "description", "version", "enabled", "variables",
		"permission", "nodes", "edges", "settings", "created_at", "updated_at",
		// DSL 根级段的正式写法(模型读它时映射到 Permission 字段)
		"permissions",
		// 响应专用字段:导入结果附加 ignored_keys,保存/运行结果附加 warnings
		"ignored_keys", "warnings",
	} {
		if !has(want) {
			t.Errorf("允许键集合缺少 %q(实际: %v)", want, keys)
		}
	}
	if has("timeout_seconds") {
		t.Error("timeout_seconds 不是模型字段,不应被允许")
	}
}

// TestWorkflowResultJSONRoundTrip 结果结构反序列化后不得丢字段。
//
// model.Workflow 自带 UnmarshalJSON(权限段别名兼容),嵌入 WorkflowResult
// 后该方法会被**提升**到本类型 —— 若不显式屏蔽,反序列化时 warnings /
// ignored_keys 会被整个丢掉(静默丢数据,约定 #14 同族)。
func TestWorkflowResultJSONRoundTrip(t *testing.T) {
	raw := []byte(`{
      "id": "wf-rt", "name": "RT", "version": 1, "enabled": true,
      "nodes": [], "edges": [], "settings": {},
      "warnings": [{"code": "DEPRECATED_NODE_CONFIG", "node": "c", "message": "已废弃"}],
      "ignored_keys": ["workflow.settings"]
    }`)
	var res WorkflowResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("反序列化结果: %v", err)
	}
	if res.Workflow == nil || res.Workflow.ID != "wf-rt" {
		t.Fatalf("工作流本体丢失: %+v", res.Workflow)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "DEPRECATED_NODE_CONFIG" {
		t.Errorf("warnings 丢失: %+v", res.Warnings)
	}
	if len(res.IgnoredKeys) != 1 || res.IgnoredKeys[0] != "workflow.settings" {
		t.Errorf("ignored_keys 丢失: %+v", res.IgnoredKeys)
	}
}

// TestExecutionResultJSONRoundTrip 执行结果的反序列化同样不得丢字段,
// 也不得 panic(嵌入的是 *model.Execution)。
func TestExecutionResultJSONRoundTrip(t *testing.T) {
	raw := []byte(`{
      "id": "exec-rt", "workflow_id": "wf-rt", "state": "RUNNING", "task": "t",
      "created_at": "2026-01-01T00:00:00Z",
      "warnings": [{"code": "DEPRECATED_NODE_CONFIG", "message": "已废弃"}]
    }`)
	var res ExecutionResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("反序列化结果: %v", err)
	}
	if res.Execution == nil || res.Execution.ID != "exec-rt" {
		t.Fatalf("执行记录丢失: %+v", res.Execution)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "DEPRECATED_NODE_CONFIG" {
		t.Errorf("warnings 丢失: %+v", res.Warnings)
	}
}
