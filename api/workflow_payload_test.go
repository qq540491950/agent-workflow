package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 本文件锁定"工作流保存/校验的顶层键契约"与"导入响应里被忽略的键可见"。
//
// 1) 未知顶层键此前被 json.Unmarshal **静默丢弃**:用户提交了一份含多余
//    键的 JSON,接口返回 200,用户以为那份设置生效了,实际根本没进模型。
//    同一份 DSL 的**顶层**未知键在 workflow/dsl 里已经报 DSL_UNKNOWN_KEY
//    (迭代 15),JSON 保存路径此前却没有对应行为 —— 两条路径语义不一致。
//    这里改成:未知顶层键 → 400 + 结构化 code(UNKNOWN_FIELD)。
//
// 2) DSL 解析器把闭合结构里的未知键丢弃了(记入 Document.IgnoredKeys +
//    WARN 日志),但 IgnoredKeys 不参与序列化,用户**看不到**"这份 DSL 有
//    N 个键被忽略"。导入接口的响应现在带上 ignored_keys。

// designerSavePayload 是前端设计器保存工作流时发出的 payload 形状
// (frontend/src/pages/Designer.tsx 的 toWorkflowModel)。
//
// 这条用例是"严格化不得打破前端保存路径"的回归证据:前端发什么,后端
// 必须收什么。
const designerSavePayload = `{
  "id": "wf-designer",
  "name": "设计器保存",
  "description": "",
  "version": 3,
  "enabled": true,
  "variables": {},
  "nodes": [
    {"id": "a", "name": "A", "type": "agent", "position": {"x": 0, "y": 0},
     "config": {"agent": "mock-claude", "mode": "plan"}},
    {"id": "b", "name": "B", "type": "skill", "position": {"x": 200, "y": 0},
     "config": {"skill": "log"}}
  ],
  "edges": [{"from": "a", "to": "b"}],
  "settings": {"max_iterations": 5, "on_loop_limit": "wait_user"}
}`

// createWorkflowPayload 是前端"新建工作流"按钮发出的 payload 形状
// (frontend/src/pages/Workflows.tsx 的 create)。
const createWorkflowPayload = `{
  "id": "",
  "name": "未命名工作流",
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

// TestFrontendSavePayloadsAreAccepted 前端的两条保存路径必须照旧成功。
func TestFrontendSavePayloadsAreAccepted(t *testing.T) {
	s := newTestServer(t)
	for name, payload := range map[string]string{
		"designer-save": designerSavePayload,
		"create":        createWorkflowPayload,
	} {
		code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows", payload)
		if code != http.StatusOK {
			t.Errorf("%s → %d, want 200 (%s)", name, code, raw)
			continue
		}
		if body["id"] == nil || body["id"] == "" {
			t.Errorf("%s 未返回工作流: %s", name, raw)
		}
	}
}

// TestPostWorkflowAcceptsEveryKnownTopLevelKey 模型里的每一个顶层字段
// (含 created_at/updated_at 这类回写字段)都必须被接受 —— 否则"GET 一份
// 再 POST 回去"这种最常见的往返会失败。
func TestPostWorkflowAcceptsEveryKnownTopLevelKey(t *testing.T) {
	s := newTestServer(t)
	payload := `{
      "id": "wf-known", "name": "Known", "description": "d", "version": 2,
      "enabled": true, "variables": {"k": "v"},
      "permission": {"claude-code": {"filesystem": {"write": false}}},
      "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
      "edges": [],
      "settings": {"max_iterations": 5, "on_loop_limit": "wait_user"},
      "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"
    }`
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows", payload)
	if code != http.StatusOK {
		t.Fatalf("全部已知键 → %d, want 200 (%s)", code, raw)
	}
	// 权限覆盖必须真的存下来(不是被静默丢弃)
	perm, _ := body["permission"].(map[string]any)
	if len(perm) == 0 {
		t.Errorf("permission 未被保存: %s", raw)
	}
}

// TestPostWorkflowAcceptsPermissionsAlias DSL 根级段写作 permissions
// (复数),模型读它时映射到 Permission 字段 —— 不得被当成未知键拒掉。
func TestPostWorkflowAcceptsPermissionsAlias(t *testing.T) {
	s := newTestServer(t)
	payload := `{
      "id": "wf-alias", "name": "Alias", "version": 1, "enabled": true,
      "permissions": {"claude-code": {"git": {"commit": false}}},
      "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
      "edges": [], "settings": {}
    }`
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows", payload)
	if code != http.StatusOK {
		t.Fatalf("permissions 别名 → %d, want 200 (%s)", code, raw)
	}
	perm, _ := body["permission"].(map[string]any)
	if len(perm) == 0 {
		t.Errorf("permissions 别名未映射到 permission: %s", raw)
	}
}

// TestPostWorkflowRejectsUnknownTopLevelKey 未知顶层键必须被拒(400 +
// 结构化 code),不得静默丢弃 —— 用户以为那份设置生效了,实际没有。
func TestPostWorkflowRejectsUnknownTopLevelKey(t *testing.T) {
	s := newTestServer(t)
	payload := `{
      "id": "wf-unknown", "name": "Unknown", "version": 1, "enabled": true,
      "timeout_seconds": 30,
      "nodes": [{"id": "a", "name": "A", "type": "human", "position": {"x":0,"y":0}, "config": {}}],
      "edges": [], "settings": {}
    }`
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows", payload)
	if code != http.StatusBadRequest {
		t.Fatalf("未知顶层键 → %d, want 400 (%s)", code, raw)
	}
	if body["code"] != "UNKNOWN_FIELD" {
		t.Errorf("code = %v, want UNKNOWN_FIELD", body["code"])
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "timeout_seconds") {
		t.Errorf("错误信息应点名未知键: %s", raw)
	}
	// 不得落库
	if list, _ := s.app.Workflows.List(); len(list) != 0 {
		t.Errorf("非法请求不应落库,现有 %d 个工作流", len(list))
	}
}

// TestValidateWorkflowRejectsUnknownTopLevelKey 校验端点与保存端点同源:
// 未知顶层键同样 400(否则"校验通过"却保存不了,用户无从下手)。
func TestValidateWorkflowRejectsUnknownTopLevelKey(t *testing.T) {
	s := newTestServer(t)
	payload := `{"id":"wf-v","name":"V","version":1,"enabled":true,"oops":1,
      "nodes": [{"id":"a","name":"A","type":"human","position":{"x":0,"y":0},"config":{}}],
      "edges": [], "settings": {}}`
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/validate", payload)
	if code != http.StatusBadRequest {
		t.Fatalf("validate 未知顶层键 → %d, want 400 (%s)", code, raw)
	}
	if body["code"] != "UNKNOWN_FIELD" {
		t.Errorf("code = %v, want UNKNOWN_FIELD", body["code"])
	}
}

// TestImportResponseCarriesIgnoredKeys 导入响应必须带上"被忽略的键"清单。
//
// 此前 IgnoredKeys 只落在内存 + WARN 日志里,用户看不到"这份 DSL 有 1 个
// 键被忽略"(设置没生效,却显示导入成功)。
//
// 响应形状:工作流字段仍在**顶层**(前端 api.importYAML 直接把响应当
// Workflow 用),额外附加一个 ignored_keys 数组。
func TestImportResponseCarriesIgnoredKeys(t *testing.T) {
	s := newTestServer(t)
	// settings 写在 workflow 段下 → 解析器丢弃它并记入 IgnoredKeys
	yaml := "version: \"1\"\nworkflow:\n  id: imp-ignored\n  name: Ignored\n  settings:\n    max_iterations: 3\nnodes:\n  - id: a\n    type: human\n    prompt: hi\nedges: []\n"
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/import", yaml)
	if code != http.StatusOK {
		t.Fatalf("import → %d, want 200 (%s)", code, raw)
	}
	// 工作流字段仍在顶层(前端契约不变)
	if body["id"] != "imp-ignored" {
		t.Errorf("import 响应顶层应含 id: %s", raw)
	}
	ignored, _ := body["ignored_keys"].([]any)
	if len(ignored) == 0 {
		t.Fatalf("import 响应应带 ignored_keys: %s", raw)
	}
	found := false
	for _, k := range ignored {
		if s, _ := k.(string); strings.Contains(s, "settings") {
			found = true
		}
	}
	if !found {
		t.Errorf("ignored_keys 应点名被丢弃的键: %s", raw)
	}
}

// TestImportResponseWithoutIgnoredKeysHasNoField 没有键被忽略时,响应里
// 不出现这个字段(避免前端把"没有 ignored_keys"误当成"没解析到")。
func TestImportResponseWithoutIgnoredKeysHasNoField(t *testing.T) {
	s := newTestServer(t)
	yaml := "version: \"1\"\nworkflow:\n  id: imp-clean\n  name: Clean\nnodes:\n  - id: a\n    type: human\n    prompt: hi\nedges: []\n"
	code, _, raw := doReq(t, s, http.MethodPost, "/api/workflows/import", yaml)
	if code != http.StatusOK {
		t.Fatalf("import → %d, want 200 (%s)", code, raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if _, ok := m["ignored_keys"]; ok {
		t.Errorf("没有键被忽略时不应出现 ignored_keys 字段: %s", raw)
	}
}
