package api

// 本文件锁住"服务器模式与桌面模式同源"的契约(迭代 22 · 双模式一致性)。
//
// 桌面模式走 Wails 绑定(app.Workflows.SavePayload / ImportYAML /
// Executions.Run),服务器模式走本包的 REST 端点。两者必须给出**同一个**
// 判定与同一份附加信息,否则同一个语义在两种模式下行为不同 —— 桌面应用
// 特有的缺陷类型,冒烟脚本(只测服务器模式)抓不到。
//
// 本文件同时做"同源交叉验证":同一份载荷分别喂给应用层与 REST 端点,
// 断言两边的错误码一致(应用层 = 桌面模式的判定来源)。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"agentworkflow/app/application"
	"agentworkflow/workflow/model"
)

// unknownKeyPayload 是一份含未知顶层键的载荷。
const unknownKeyPayload = `{
  "id": "wf-unknown", "name": "Unknown", "version": 1, "enabled": true,
  "timeout_seconds": 30,
  "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
  "edges": [], "settings": {}
}`

// deprecatedConfigPayload 是一份"能保存但带警告"的载荷。
const deprecatedConfigPayload = `{
  "id": "wf-warn", "name": "Warn", "version": 1, "enabled": true,
  "nodes": [{"id": "c", "name": "C", "type": "condition", "position": {"x":0,"y":0},
             "config": {"expression": "1 == 1"}}],
  "edges": [], "settings": {}
}`

// TestSaveAndValidateAgreeOnUnknownTopLevelKey 保存与校验必须给出同一个
// 判定(否则"校验通过"却保存不了,用户无从下手)。
func TestSaveAndValidateAgreeOnUnknownTopLevelKey(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/api/workflows", "/api/workflows/validate"} {
		code, body, raw := doReq(t, s, http.MethodPost, path, unknownKeyPayload)
		if code != http.StatusBadRequest {
			t.Errorf("%s 未知顶层键 → %d, want 400 (%s)", path, code, raw)
			continue
		}
		if body["code"] != "UNKNOWN_FIELD" {
			t.Errorf("%s code = %v, want UNKNOWN_FIELD", path, body["code"])
		}
	}
}

// TestServerAndDesktopAgreeOnUnknownTopLevelKey 同一个非法载荷,
// 服务器模式(REST)与桌面模式(应用层)必须给出同一个错误码。
//
// 这是"双模式一致性"的核心断言:桌面模式此前走模型级 Save,未知顶层键
// 被 json.Unmarshal 静默丢弃 —— 同一个载荷,两种模式一个报错一个成功。
func TestServerAndDesktopAgreeOnUnknownTopLevelKey(t *testing.T) {
	s := newTestServer(t)

	// 桌面模式路径(应用层 = Wails 绑定背后的同一段代码)
	_, appErr := s.app.Workflows.SavePayload(unknownKeyPayload)
	if appErr == nil {
		t.Fatal("桌面模式路径必须拒绝未知顶层键")
	}
	appCode := errorCodeOf(t, appErr)

	// 服务器模式路径(REST)
	status, body, raw := doReq(t, s, http.MethodPost, "/api/workflows", unknownKeyPayload)
	if status != http.StatusBadRequest {
		t.Fatalf("服务器模式 → %d, want 400 (%s)", status, raw)
	}
	restCode, _ := body["code"].(string)
	if restCode != appCode {
		t.Errorf("两种模式的错误码不一致: 桌面=%s 服务器=%s", appCode, restCode)
	}
}

// TestSaveResponseCarriesValidationWarnings 保存响应必须带回校验警告 ——
// 此前警告只进日志,直连 API 的调用方永远看不到,用户以为设置生效了。
//
// 响应形状:工作流字段仍在顶层(既有客户端契约不变),warnings 是附加字段。
func TestSaveResponseCarriesValidationWarnings(t *testing.T) {
	s := newTestServer(t)
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows", deprecatedConfigPayload)
	if code != http.StatusOK {
		t.Fatalf("保存 → %d, want 200 (%s)", code, raw)
	}
	if body["id"] != "wf-warn" {
		t.Errorf("工作流字段必须在顶层(既有响应契约不变): %s", raw)
	}
	warnings, _ := body["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("保存响应必须带 warnings(此前只进日志): %s", raw)
	}
	found := false
	for _, w := range warnings {
		m, _ := w.(map[string]any)
		if m["code"] == "DEPRECATED_NODE_CONFIG" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings 应含 DEPRECATED_NODE_CONFIG: %s", raw)
	}
}

// TestSaveResponseWithoutWarningsHasNoField 没有警告时响应里不出现
// warnings 字段(前端用 ?? [] 兜底,不必区分"没有"与"空")。
func TestSaveResponseWithoutWarningsHasNoField(t *testing.T) {
	s := newTestServer(t)
	payload := `{
      "id": "wf-clean", "name": "Clean", "version": 1, "enabled": true,
      "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
      "edges": [], "settings": {}
    }`
	code, _, raw := doReq(t, s, http.MethodPost, "/api/workflows", payload)
	if code != http.StatusOK {
		t.Fatalf("保存 → %d, want 200 (%s)", code, raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if _, ok := m["warnings"]; ok {
		t.Errorf("没有警告时不应出现 warnings 字段: %s", raw)
	}
}

// TestRunResponseCarriesValidationWarnings 运行响应必须带回校验警告。
func TestRunResponseCarriesValidationWarnings(t *testing.T) {
	s := newTestServer(t)
	// 先导入一份带警告的工作流(导入路径本身也带 ignored_keys 契约)
	yaml := "version: \"1\"\nworkflow:\n  id: wf-run-warn\n  name: Warn\nnodes:\n  - id: c\n    type: condition\n    expression: \"1 == 1\"\nedges: []\n"
	if code, _, raw := doReq(t, s, http.MethodPost, "/api/workflows/import", yaml); code != http.StatusOK {
		t.Fatalf("导入 → %d, want 200 (%s)", code, raw)
	}
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/wf-run-warn/run", `{"task":"t"}`)
	if code != http.StatusOK {
		t.Fatalf("运行 → %d, want 200 (%s)", code, raw)
	}
	// 执行字段仍在顶层(既有响应契约不变)
	if body["id"] == nil || body["workflow_id"] != "wf-run-warn" {
		t.Errorf("执行字段必须在顶层: %s", raw)
	}
	warnings, _ := body["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("运行响应必须带 warnings(此前只进日志): %s", raw)
	}
}

// TestImportResponseMatchesAppLayerIgnoredKeys import 端点返回的
// ignored_keys 必须与应用层产出的一致(此前端点自己又解析了一遍 DSL)。
func TestImportResponseMatchesAppLayerIgnoredKeys(t *testing.T) {
	s := newTestServer(t)
	yaml := "version: \"1\"\nworkflow:\n  id: imp-x\n  name: X\n  settings:\n    max_iterations: 3\nnodes:\n  - id: a\n    type: human\n    prompt: hi\nedges: []\n"

	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/import", yaml)
	if code != http.StatusOK {
		t.Fatalf("import → %d, want 200 (%s)", code, raw)
	}
	rest, _ := body["ignored_keys"].([]any)

	appRes, err := s.app.Workflows.ImportYAML(yaml)
	if err != nil {
		t.Fatalf("应用层导入: %v", err)
	}
	if len(rest) != len(appRes.IgnoredKeys) {
		t.Errorf("两种模式的 ignored_keys 数量不一致: 服务器=%d 应用层=%d",
			len(rest), len(appRes.IgnoredKeys))
	}
}

// TestParseWorkflowPayloadIsSharedByBothModes 应用层的载荷解析是两种模式
// 的唯一来源(顶层键集合由 model.Workflow 的 json tag 反射派生)。
func TestParseWorkflowPayloadIsSharedByBothModes(t *testing.T) {
	if _, err := application.ParseWorkflowPayload([]byte(unknownKeyPayload)); err == nil {
		t.Error("应用层必须拒绝未知顶层键")
	}
	if _, err := application.ParseWorkflowPayload([]byte(deprecatedConfigPayload)); err != nil {
		t.Errorf("带警告的合法载荷必须被接受: %v", err)
	}
	keys := application.AllowedWorkflowTopLevelKeys()
	if len(keys) == 0 {
		t.Fatal("允许键集合不得为空")
	}
	joined := strings.Join(keys, ",")
	for _, want := range []string{"nodes", "settings", "permissions", "ignored_keys", "warnings"} {
		if !strings.Contains(joined, want) {
			t.Errorf("允许键集合缺少 %q: %v", want, keys)
		}
	}
}

// errorCodeOf 取出结构化错误的 code(测试用)。
func errorCodeOf(t *testing.T, err error) string {
	t.Helper()
	var se *model.Error
	if errors.As(err, &se) {
		return se.Code
	}
	t.Fatalf("错误不是结构化的 *model.Error: %T (%v)", err, err)
	return ""
}
