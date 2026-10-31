package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "agentworkflow/app/application"
	"agentworkflow/workflow/model"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	return NewServer(a)
}

func doReq(t *testing.T, s *Server, method, path, body string) (int, map[string]any, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	raw := w.Body.Bytes()
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return w.Code, m, raw
}

// 资源缺失必须 404 + 结构化 code(此前一律 400,仅 body 里带 NOT_FOUND 字样)。
// 注:DELETE 是幂等语义,删除已不存在的资源返回 200(对 UI 更友好,
// 避免"已在别处删除"场景误报错),不在此列。
func TestNotFoundSemantics(t *testing.T) {
	s := newTestServer(t)

	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/workflows/ghost", ""},
		{http.MethodPost, "/api/workflows/ghost/duplicate", ""},
		{http.MethodGet, "/api/executions/ghost", ""},
		{http.MethodPost, "/api/executions/ghost/retry", "{}"},
		{http.MethodPost, "/api/executions/ghost/input", "{}"},
		{http.MethodPost, "/api/executions/ghost/cancel", ""},
		{http.MethodPost, "/api/workflows/ghost/run", `{"task":"t"}`},
	}
	for _, c := range cases {
		code, body, _ := doReq(t, s, c.method, c.path, c.body)
		if code != http.StatusNotFound {
			t.Errorf("%s %s → %d, want 404 (body=%v)", c.method, c.path, code, body)
		}
		if body["code"] != "NOT_FOUND" {
			t.Errorf("%s %s → code=%v, want NOT_FOUND", c.method, c.path, body["code"])
		}
	}

	// 幂等删除:不存在的资源同样返回 200
	for _, path := range []string{"/api/workflows/ghost", "/api/executions/ghost"} {
		code, _, _ := doReq(t, s, http.MethodDelete, path, "")
		if code != http.StatusOK {
			t.Errorf("DELETE %s → %d, want 200(幂等)", path, code)
		}
	}
}

// 校验错误 400 + WORKFLOW_INVALID 结构化码。
func TestValidationSemantics(t *testing.T) {
	s := newTestServer(t)
	bad := `{"id":"wf-bad","name":"Bad","nodes":[{"id":"a","type":"agent","agent":"no-such","config":{}}],"edges":[]}`
	code, body, _ := doReq(t, s, http.MethodPost, "/api/workflows", bad)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid workflow → %d, want 400", code)
	}
	if body["code"] != "WORKFLOW_INVALID" {
		t.Errorf("code = %v, want WORKFLOW_INVALID", body["code"])
	}
}

// 列表契约:必须返回 JSON 数组(前端 asArray 防御的源头契约)。
func TestListContractsReturnArrays(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/api/workflows", "/api/executions", "/api/stats", "/api/agents", "/api/skills"} {
		_, _, raw := doReq(t, s, http.MethodGet, path, "")
		if len(raw) == 0 || raw[0] != '[' {
			t.Errorf("GET %s → 非 JSON 数组: %.40s", path, raw)
		}
	}
}

// Agent 配置:GET 返回掩码值,PUT *** 合并,PUT 空串删除。
func TestAgentConfigEndpointMasking(t *testing.T) {
	s := newTestServer(t)

	code, _, _ := doReq(t, s, http.MethodPut, "/api/agents/claude-code/config",
		`{"model":"m1","env":{"TOK":"sk-live"}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT config → %d", code)
	}

	_, body, _ := doReq(t, s, http.MethodGet, "/api/agents/claude-code/config", "")
	env, _ := body["env"].(map[string]any)
	if env["TOK"] != "***" {
		t.Errorf("GET config env.TOK = %v, want 掩码", env["TOK"])
	}

	// *** 保留原值 + 空串删除
	code, _, _ = doReq(t, s, http.MethodPut, "/api/agents/claude-code/config",
		`{"model":"m2","env":{"TOK":"***","NEW":"n1"}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT config 2 → %d", code)
	}
	_, body, _ = doReq(t, s, http.MethodGet, "/api/agents/claude-code/config", "")
	env, _ = body["env"].(map[string]any)
	if env["TOK"] != "***" || env["NEW"] != "***" || body["model"] != "m2" {
		t.Errorf("config after merge = %v", body)
	}

	// 空串 = 删除该项(前端传空串即删除,不得把空值落库)
	code, _, _ = doReq(t, s, http.MethodPut, "/api/agents/claude-code/config",
		`{"model":"m3","env":{"TOK":"","NEW":"***"}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT config 3 → %d", code)
	}
	_, body, _ = doReq(t, s, http.MethodGet, "/api/agents/claude-code/config", "")
	env, _ = body["env"].(map[string]any)
	if _, exists := env["TOK"]; exists {
		t.Errorf("空串未删除 env 项: %v", env)
	}
	if env["NEW"] != "***" {
		t.Errorf("*** 未保留 env 项: %v", env)
	}
	if body["model"] != "m3" {
		t.Errorf("model = %v,期望 m3", body["model"])
	}
}

// Skill 启停端点真实生效。
func TestSkillEnableEndpoint(t *testing.T) {
	s := newTestServer(t)
	code, _, _ := doReq(t, s, http.MethodPost, "/api/skills/log/enable", `{"enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("disable → %d", code)
	}
	_, _, raw := doReq(t, s, http.MethodGet, "/api/skills", "")
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("skills not an array: %v (%.40s)", err, raw)
	}
	for _, m := range arr {
		if m["id"] == "log" {
			if m["enabled"] != false {
				t.Errorf("log skill = %v, want disabled", m)
			}
		}
	}
}

// 健康与版本端点。
func TestHealthAndVersion(t *testing.T) {
	s := newTestServer(t)
	code, body, _ := doReq(t, s, http.MethodGet, "/api/health", "")
	if code != http.StatusOK || body["status"] != "ok" {
		t.Errorf("health = %d %v", code, body)
	}
	code, body, _ = doReq(t, s, http.MethodGet, "/api/version", "")
	if code != http.StatusOK || body["version"] == "" {
		t.Errorf("version = %d %v", code, body)
	}
}

// Git 路由:真实临时仓库上的 status/diff/log/commit 契约。
func TestGitRoutes(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })

	// 初始化临时仓库并指定为工作目录
	repoDir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.GitSvc.SetWorkingDir(repoDir)

	s := NewServer(a)

	// status:未提交文件出现在 untracked
	code, body, _ := doReq(t, s, http.MethodGet, "/api/git/status", "")
	if code != http.StatusOK {
		t.Fatalf("git status → %d", code)
	}
	untracked, _ := body["untracked"].([]any)
	found := false
	for _, u := range untracked {
		if u == "f.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("untracked = %v, want f.txt", untracked)
	}

	// commit:空 message 拒绝
	code, _, _ = doReq(t, s, http.MethodPost, "/api/git/commit", `{"message":"  "}`)
	if code != http.StatusBadRequest {
		t.Errorf("empty commit → %d, want 400", code)
	}

	// commit:成功
	code, body, _ = doReq(t, s, http.MethodPost, "/api/git/commit", `{"message":"test commit"}`)
	if code != http.StatusOK {
		t.Fatalf("commit → %d (%v)", code, body)
	}

	// log:至少一条
	code, _, raw := doReq(t, s, http.MethodGet, "/api/git/log?limit=5", "")
	if code != http.StatusOK || len(raw) < 2 {
		t.Errorf("git log → %d %.40s", code, raw)
	}
}

// 回归:HTTP 恢复(HITL input)必须在请求返回后继续执行。
// 若有人把 r.Context() 直接传进 Engine.Resume,请求结束会取消
// 整个恢复执行 —— 本测试锁定"脱离请求生命周期"的行为。
func TestResumeInputSurvivesRequestEnd(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	_, _, _ = doReq(t, s, http.MethodPost, "/api/workflows/import", `
version: "1"
workflow:
  id: hitl-http
  name: HITL HTTP
nodes:
  - id: gate
    type: human
    prompt: "确认?"
    responses: ["approve"]
  - id: done
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: gate
    to: done
`)

	_, execResp, _ := doReq(t, s, http.MethodPost, "/api/workflows/hitl-http/run", `{"task":"t"}`)
	execID, _ := execResp["id"].(string)
	if execID == "" {
		t.Fatal("run 未返回执行 ID")
	}

	// 等待进入 WAITING_USER
	deadline := time.Now().Add(10 * time.Second)
	state := ""
	for time.Now().Before(deadline) {
		_, body, _ := doReq(t, s, http.MethodGet, "/api/executions/"+execID, "")
		state, _ = body["state"].(string)
		if state == "WAITING_USER" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != "WAITING_USER" {
		t.Fatalf("state = %s, want WAITING_USER", state)
	}

	// HTTP 恢复:请求立即返回
	code, _, _ := doReq(t, s, http.MethodPost, "/api/executions/"+execID+"/input", `{"response":"approve"}`)
	if code != http.StatusOK {
		t.Fatalf("input → %d", code)
	}

	// 请求已结束后,执行必须继续到 COMPLETED
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, body, _ := doReq(t, s, http.MethodGet, "/api/executions/"+execID, "")
		state, _ = body["state"].(string)
		if state == "COMPLETED" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("请求结束后执行未完成,最终 state=%s", state)
}

// 回归:POST /api/workflows 的请求体若字段类型不符(合法 JSON、非法结构),
// 此前 parseWorkflow 丢弃 Unmarshal 错误,会静默落库一个空白/残缺工作流。
// 现必须 400 且不落库。
func TestPostWorkflowRejectsWrongTypedFields(t *testing.T) {
	s := newTestServer(t)
	for _, body := range []string{
		`{"id":"wf-bad-1","name":123}`,           // name 应为 string
		`{"id":"wf-bad-2","nodes":"not-a-list"}`, // nodes 应为数组
		`{"id":"wf-bad-3","version":"abc"}`,      // version 应为整数
	} {
		code, resp, raw := doReq(t, s, http.MethodPost, "/api/workflows", body)
		if code != http.StatusBadRequest {
			t.Errorf("POST %s → %d, want 400 (resp=%v)", body, code, resp)
		}
		if !strings.Contains(string(raw), "工作流结构非法") {
			t.Errorf("错误信息不可读: %s", raw)
		}
		// 不得落库
		if list, _ := s.app.Workflows.List(); len(list) != 0 {
			t.Errorf("非法请求不应落库,现有 %d 个工作流", len(list))
		}
	}
}

// 回归:不存在的版本快照此前走 wrap() → DB_ERROR → 400,与其他"资源缺失"
// 端点的 404 语义不一致(wrapNotFound 此前是无人调用的死代码)。
func TestGetWorkflowVersionMissingIs404(t *testing.T) {
	s := newTestServer(t)
	code, resp, _ := doReq(t, s, http.MethodGet, "/api/workflows/ghost/versions/3", "")
	if code != http.StatusNotFound {
		t.Fatalf("→ %d, want 404 (resp=%v)", code, resp)
	}
	if resp["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", resp["code"])
	}
}

// ---- REST 契约健壮性(轮次 27)----

// TestRetryRejectsMalformedBody:retry 的 skip 语义不得因解析失败而静默
// 变 false(那会把「跳过失败节点」变成「重跑失败节点」——可能重复写文件、
// 重复发外部调用),且必须返回 4xx 而不是 200 假装成功。
func TestRetryRejectsMalformedBody(t *testing.T) {
	s := newTestServer(t)

	for _, body := range []string{`{"skip": "yes"}`, `{"skip": 1}`, `not-json`} {
		code, m, _ := doReq(t, s, http.MethodPost, "/api/executions/none/retry", body)
		if code < 400 {
			t.Errorf("body=%q 期望 4xx,实际 %d (%v)", body, code, m)
		}
	}
	// 合法 body 与空 body 都要走到业务层(404 证明解析没被拦)
	for _, body := range []string{`{"skip":true}`, `{}`, ``} {
		code, m, _ := doReq(t, s, http.MethodPost, "/api/executions/none/retry", body)
		if code != http.StatusNotFound {
			t.Errorf("body=%q 期望解析通过后返回 404,实际 %d (%v)", body, code, m)
		}
	}
}

// TestEnableRequiresExplicitValue:禁用开关缺参数必须 fail closed。
// 此前 `value != "false"` 让缺参/?value=0/?value=no 全部判为「启用」。
func TestEnableRequiresExplicitValue(t *testing.T) {
	s := newTestServer(t)
	wf := seedWorkflow(t, s, "wf-enable")

	for _, q := range []string{"", "?value=", "?value=0", "?value=no", "?value=False", "?value=TRUE"} {
		code, _, _ := doReq(t, s, http.MethodPost, "/api/workflows/"+wf+"/enable"+q, "")
		if code < 400 {
			t.Errorf("query=%q 期望 4xx(fail closed),实际 %d", q, code)
		}
	}
	// 合法取值照旧工作,且真正改变状态
	code, m, _ := doReq(t, s, http.MethodPost, "/api/workflows/"+wf+"/enable?value=false", "")
	if code != 200 {
		t.Fatalf("value=false 应成功,实际 %d (%v)", code, m)
	}
	if en, _ := m["enabled"].(bool); en {
		t.Error("value=false 后工作流应为禁用")
	}
	code, m, _ = doReq(t, s, http.MethodPost, "/api/workflows/"+wf+"/enable?value=true", "")
	if code != 200 {
		t.Fatalf("value=true 应成功,实际 %d (%v)", code, m)
	}
	if en, _ := m["enabled"].(bool); !en {
		t.Error("value=true 后工作流应为启用")
	}
}

// TestJSONBodySizeLimit:所有 JSON 端点都有 1MB 上限。此前只有 import 端点
// 有,backup/restore 等直接 json.Decode 全量读进内存,可被一个大 body 打爆。
func TestJSONBodySizeLimit(t *testing.T) {
	s := newTestServer(t)
	huge := `{"task":"` + strings.Repeat("x", 2<<20) + `"}`

	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/workflows"},
		{http.MethodPost, "/api/backup/restore"},
		{http.MethodPost, "/api/workflows/wf-x/run"},
		{http.MethodPost, "/api/workflows/validate"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(huge))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("%s %s 超大 body 应被拒绝,实际 200", c.method, c.path)
		}
	}
}

// seedWorkflow 通过 REST 创建一个工作流并返回其 ID。
func seedWorkflow(t *testing.T, s *Server, id string) string {
	t.Helper()
	body := `{"id":"` + id + `","name":"Seed","nodes":[
		{"id":"a","name":"A","type":"agent","config":{"agent":"mock-claude","mode":"plan"}},
		{"id":"b","name":"B","type":"agent","config":{"agent":"mock-claude","mode":"review"}}],
		"edges":[{"from":"a","to":"b"}],"enabled":true}`
	code, m, _ := doReq(t, s, http.MethodPost, "/api/workflows", body)
	if code != 200 {
		t.Fatalf("seed workflow: %d %v", code, m)
	}
	return id
}

// ---- 逐端点 REST 契约测试(C2)----
// 约定:
//  1) 4xx 必须断言错误体形状 —— 至少含 error 文本;结构化错误另断言 code。
//     code:"NOT_FOUND" → 404;kind:"permission_error" → 403;其余 → 400。
//  2) 资源缺失的端点期望 404 + code=="NOT_FOUND"。
//  3) 少数端点后端当前未按 (2) 实现(见 TestContract_*Deviation 用例内的注释),
//     这些用例只断言"实际观察到的行为",不改后端,缺陷清单见交付说明。

// assertErrorBody 断言错误体形状:error 必填且非空;wantCode 非空时还要求
// code 精确匹配(用于 NOT_FOUND / WORKFLOW_DISABLED 等结构化码)。
func assertErrorBody(t *testing.T, label string, body map[string]any, raw []byte, wantCode string) {
	t.Helper()
	msg, _ := body["error"].(string)
	if msg == "" {
		t.Errorf("%s: 错误体缺 error 字段: %s", label, raw)
	}
	if wantCode == "" {
		return
	}
	if body["code"] != wantCode {
		t.Errorf("%s: code = %v, want %q (body=%s)", label, body["code"], wantCode, raw)
	}
}

// doServe 直接走 handler 并保留响应头(doReq 只返回 body,拿不到
// Content-Type / Content-Disposition 之类的响应头契约)。
func doServe(t *testing.T, s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// waitExecState 轮询直到执行进入终态或超时。
func waitExecState(t *testing.T, s *Server, execID string, want ...string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		_, body, _ := doReq(t, s, http.MethodGet, "/api/executions/"+execID, "")
		last, _ = body["state"].(string)
		for _, w := range want {
			if last == w {
				return body
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("执行 %s 未进入 %v,最后状态 %q", execID, want, last)
	return nil
}

// TestContract_Health:健康检查体必须含 status/version/mode/adk/wails。
func TestContract_Health(t *testing.T) {
	s := newTestServer(t)
	code, body, raw := doReq(t, s, http.MethodGet, "/api/health", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/health → %d, want 200 (%s)", code, raw)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if body["mode"] != "server" {
		t.Errorf("mode = %v, want server", body["mode"])
	}
	// adk/wails 在无构建信息的二进制(test)中可能是空串,但键必须存在 ——
	// 前端按存在性渲染依赖信息。
	for _, k := range []string{"status", "version", "mode", "adk", "wails"} {
		if _, ok := body[k]; !ok {
			t.Errorf("health 缺字段 %q: %s", k, raw)
		}
	}
}

// TestContract_Version:版本端点必须返回 app/version 两个字段。
func TestContract_Version(t *testing.T) {
	s := newTestServer(t)
	code, body, raw := doReq(t, s, http.MethodGet, "/api/version", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/version → %d, want 200 (%s)", code, raw)
	}
	if s, _ := body["app"].(string); s == "" {
		t.Errorf("app = %v, want 非空: %s", body["app"], raw)
	}
	if s, _ := body["version"].(string); s == "" {
		t.Errorf("version = %v, want 非空: %s", body["version"], raw)
	}
}

// TestContract_WorkflowCRUD:创建 → 列表 → 详情 → 复制 → 启停的完整链路。
func TestContract_WorkflowCRUD(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-crud")

	// 详情
	code, body, raw := doReq(t, s, http.MethodGet, "/api/workflows/"+id, "")
	if code != http.StatusOK || body["id"] != id {
		t.Fatalf("GET /api/workflows/%s → %d (%s)", id, code, raw)
	}

	// 列表:必须是 JSON 数组且含新建工作流
	code, _, raw = doReq(t, s, http.MethodGet, "/api/workflows", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/workflows → %d", code)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("列表非 JSON 数组: %v (%s)", err, raw)
	}
	found := false
	for _, m := range list {
		if m["id"] == id {
			found = true
		}
	}
	if !found {
		t.Errorf("列表未含 %s: %s", id, raw)
	}

	// 复制:新 ID + 名称带"(副本)",原工作流不受影响
	code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/duplicate", "")
	if code != http.StatusOK {
		t.Fatalf("duplicate → %d (%s)", code, raw)
	}
	dupID, _ := body["id"].(string)
	if dupID == "" || dupID == id {
		t.Fatalf("duplicate 未分配新 ID: %s", raw)
	}
	if name, _ := body["name"].(string); !strings.Contains(name, "副本") {
		t.Errorf("副本名 = %v, want 含「副本」", body["name"])
	}
	if _, m, _ := doReq(t, s, http.MethodGet, "/api/workflows/"+id, ""); m["name"] != "Seed" {
		t.Errorf("复制改动了原工作流名: %v", m["name"])
	}

	// 启停:响应体即更新后的工作流,enabled 字段真实翻转
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"?value=false", false}, {"?value=true", true},
	} {
		code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/enable"+tc.query, "")
		if code != http.StatusOK {
			t.Fatalf("enable%s → %d (%s)", tc.query, code, raw)
		}
		if got, _ := body["enabled"].(bool); got != tc.want {
			t.Errorf("enable%s → enabled=%v, want %v", tc.query, got, tc.want)
		}
		// 状态必须已落库(GET 回读),不是只改了响应体
		_, back, _ := doReq(t, s, http.MethodGet, "/api/workflows/"+id, "")
		if got, _ := back["enabled"].(bool); got != tc.want {
			t.Errorf("enable%s 未落库,回读 enabled=%v", tc.query, got)
		}
	}
}

// TestContract_WorkflowExport:导出端点返回 application/yaml 原文。
func TestContract_WorkflowExport(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-export")

	w := doServe(t, s, http.MethodGet, "/api/workflows/"+id+"/export", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("export → %d (%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("export Content-Type = %q, want application/yaml", ct)
	}
	out := w.Body.String()
	if !strings.Contains(out, "workflow:") || !strings.Contains(out, id) {
		t.Errorf("export 响应不是该工作流的 DSL: %s", out)
	}
	// YAML 可被 import 原样导入(往返契约)
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/import", out)
	if code != http.StatusOK {
		t.Fatalf("导出内容无法再导入 → %d (%s)", code, raw)
	}
	if body["id"] == nil || body["id"] == "" {
		t.Errorf("import 未返回工作流: %s", raw)
	}
}

// TestContract_WorkflowImport:import 接受 application/yaml;解析失败与
// 校验失败分别是 DSL_PARSE_ERROR / WORKFLOW_INVALID(均为 400)。
func TestContract_WorkflowImport(t *testing.T) {
	s := newTestServer(t)
	yaml := "version: \"1\"\nworkflow:\n  id: imp-ok\n  name: Imported\nnodes:\n  - id: a\n    type: agent\n    agent: mock-claude\n    mode: plan\nedges: []\n"

	w := doServe(t, s, http.MethodPost, "/api/workflows/import", yaml,
		map[string]string{"Content-Type": "application/yaml"})
	if w.Code != http.StatusOK {
		t.Fatalf("import → %d (%s)", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("import 响应非 JSON: %v", err)
	}
	if created["id"] == nil || !strings.Contains(w.Body.String(), "imp-ok") {
		t.Errorf("import 未创建 imp-ok: %s", w.Body.String())
	}

	for _, tc := range []struct {
		label, body, wantCode string
	}{
		{"非法 YAML", "not: [yaml", "DSL_PARSE_ERROR"},
		{"引用不存在的 Agent", "version: \"1\"\nworkflow:\n  id: imp-bad\n  name: Bad\nnodes:\n  - id: a\n    type: agent\n    agent: nope\n    mode: plan\nedges: []\n", "WORKFLOW_INVALID"},
	} {
		code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/import", tc.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s → %d, want 400 (%s)", tc.label, code, raw)
		}
		assertErrorBody(t, tc.label, body, raw, tc.wantCode)
	}
}

// TestContract_ImportRejectsNonFiniteVariables 导入含 `.nan` 的 DSL 必须
// **失败**(400 WORKFLOW_INVALID),且工作流**根本没被创建**。
//
// 这条锁住的是"**导入路径走校验器**"这一事实(#314 的判断依据):
// api 的 import 端点 → 应用层 ImportYAML → saveModel → validateWorkflow →
// Engine.Validate → validator.Validate → findNonFinite。校验期已经拦住
// 非有限值,所以**不必**在 DSL 解析期再加一层预检 —— 那只是"更早一点失败",
// 不是"修好了什么";而 dsl 包是所有路径的入口,多一处判断就多一处可能误伤。
//
// 反面(没有校验期这一层时):导入返回 200,但 variables_json 整列被写成空串
// → 一个坏值**连坐清空**整张变量表 → 条件变量恒假 → 静默走错分支,
// 执行仍 COMPLETED(约定 14)。
func TestContract_ImportRejectsNonFiniteVariables(t *testing.T) {
	s := newTestServer(t)
	yaml := "version: \"1\"\nworkflow:\n  id: wf-nan-import\n  name: 含 NaN 的导入\nvariables:\n  env: .nan\n  limit: 10\nnodes:\n  - id: a\n    type: agent\n    agent: mock-claude\n    mode: plan\nedges: []\n"
	w := doServe(t, s, http.MethodPost, "/api/workflows/import", yaml,
		map[string]string{"Content-Type": "application/yaml"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("导入含 .nan 的 DSL → %d,want 400(%s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if body["code"] != "WORKFLOW_INVALID" {
		t.Errorf("错误码 = %v,want WORKFLOW_INVALID", body["code"])
	}
	// 用户必须看得到**为什么**(而不是一句"导入失败")。
	if !strings.Contains(w.Body.String(), "有限数值") {
		t.Errorf("错误响应必须指出是非有限值,实际: %s", w.Body.String())
	}
	// 工作流根本没被创建(不得留下"导入成功但变量全没了"的记录)。
	code, _, raw := doReq(t, s, http.MethodGet, "/api/workflows/wf-nan-import", "")
	if code != http.StatusNotFound {
		t.Errorf("导入被拒后工作流不得存在 → %d,want 404(%s)", code, raw)
	}
}

// TestContract_WorkflowValidate:合法 → 200 valid:true;非法 → 200
// valid:false + 结构化 errors(不是 4xx);请求体本身畸形才是 400。
func TestContract_WorkflowValidate(t *testing.T) {
	s := newTestServer(t)

	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/validate",
		`{"id":"v-ok","name":"V","nodes":[{"id":"a","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],"edges":[]}`)
	if code != http.StatusOK || body["valid"] != true {
		t.Fatalf("合法工作流 → %d valid=%v (%s)", code, body["valid"], raw)
	}

	code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/validate",
		`{"id":"v-bad","name":"V","nodes":[{"id":"a","type":"agent","config":{"agent":"nope","mode":"plan"}}],"edges":[]}`)
	if code != http.StatusOK {
		t.Fatalf("非法工作流应 200 + valid:false,实际 %d (%s)", code, raw)
	}
	if body["valid"] != false {
		t.Fatalf("valid = %v, want false (%s)", body["valid"], raw)
	}
	errs, _ := body["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("valid:false 但 errors 为空: %s", raw)
	}
	first, _ := errs[0].(map[string]any)
	if first["code"] == nil || first["message"] == nil {
		t.Errorf("errors[0] 形状不对(需 code/message): %s", raw)
	}

	code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/validate", `{`)
	if code != http.StatusBadRequest {
		t.Errorf("畸形 body → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "validate 畸形 body", body, raw, "")
}

// TestContract_WorkflowVersions:版本列表(数组)、指定版本(YAML 原文)、
// 非数字版本号(400)。404 语义由 TestGetWorkflowVersionMissingIs404 覆盖。
func TestContract_WorkflowVersions(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-ver")
	// 再次保存 → 版本号递增(产生 v1 快照与 v2 快照)
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows",
		`{"id":"`+id+`","name":"Seed v2","nodes":[{"id":"a","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],"edges":[],"enabled":true}`)
	if code != http.StatusOK {
		t.Fatalf("二次保存 → %d (%s)", code, raw)
	}

	code, _, raw = doReq(t, s, http.MethodGet, "/api/workflows/"+id+"/versions", "")
	if code != http.StatusOK {
		t.Fatalf("versions → %d (%s)", code, raw)
	}
	var vers []int
	if err := json.Unmarshal(raw, &vers); err != nil {
		t.Fatalf("versions 非数字数组: %v (%s)", err, raw)
	}
	if len(vers) < 2 {
		t.Errorf("versions = %v, want 至少 2 个版本", vers)
	}

	// 指定版本:返回 YAML 原文
	w := doServe(t, s, http.MethodGet, "/api/workflows/"+id+"/versions/1", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("versions/1 → %d (%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("versions/1 Content-Type = %q, want application/yaml", ct)
	}
	if !strings.Contains(w.Body.String(), "workflow:") {
		t.Errorf("versions/1 非 DSL 原文: %s", w.Body.String())
	}

	// 非数字版本号 → 400
	code, body, raw = doReq(t, s, http.MethodGet, "/api/workflows/"+id+"/versions/abc", "")
	if code != http.StatusBadRequest {
		t.Errorf("versions/abc → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "versions/abc", body, raw, "")
}

// TestContract_Run:空 body 合法;畸形 body 400;禁用工作流 400 +
// code=WORKFLOW_DISABLED。404 语义由 TestNotFoundSemantics 覆盖。
func TestContract_Run(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-run")

	// 空 body(等价空任务 + 无变量)
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	if code != http.StatusOK {
		t.Fatalf("run 空 body → %d (%s)", code, raw)
	}
	execID, _ := body["id"].(string)
	if execID == "" || body["workflow_id"] != id {
		t.Fatalf("run 响应缺 id/workflow_id: %s", raw)
	}
	if state, _ := body["state"].(string); state == "" {
		t.Errorf("run 响应缺 state: %s", raw)
	}
	waitExecState(t, s, execID, "COMPLETED", "FAILED")

	// 畸形 body → 400,且不得启动执行
	code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", `{bad`)
	if code != http.StatusBadRequest {
		t.Errorf("run 畸形 body → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "run 畸形 body", body, raw, "")

	// 禁用后拒绝执行
	if code, _, _ = doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/enable?value=false", ""); code != http.StatusOK {
		t.Fatalf("禁用工作流失败: %d", code)
	}
	code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	if code != http.StatusBadRequest {
		t.Fatalf("禁用后 run → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "禁用工作流 run", body, raw, "WORKFLOW_DISABLED")
}

// TestContract_ExecutionReads:一次真实执行后,执行本体/节点/事件/制品/
// 导出五个读取端点的形状契约。
func TestContract_ExecutionReads(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-exec")
	code, body, raw := doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	if code != http.StatusOK {
		t.Fatalf("run → %d (%s)", code, raw)
	}
	execID, _ := body["id"].(string)
	waitExecState(t, s, execID, "COMPLETED", "FAILED")

	// 执行本体
	code, body, raw = doReq(t, s, http.MethodGet, "/api/executions/"+execID, "")
	if code != http.StatusOK || body["id"] != execID {
		t.Fatalf("GET /api/executions/%s → %d (%s)", execID, code, raw)
	}
	if body["state"] != "COMPLETED" {
		t.Errorf("state = %v, want COMPLETED", body["state"])
	}

	// 列表(带 workflow_id 过滤)
	code, _, raw = doReq(t, s, http.MethodGet, "/api/executions?workflow_id="+id+"&limit=5", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/executions → %d (%s)", code, raw)
	}
	var execs []map[string]any
	if err := json.Unmarshal(raw, &execs); err != nil {
		t.Fatalf("executions 非数组: %v (%s)", err, raw)
	}
	if len(execs) == 0 {
		t.Fatalf("过滤后应至少 1 条: %s", raw)
	}
	for _, e := range execs {
		if e["workflow_id"] != id {
			t.Errorf("workflow_id 过滤失效: %v", e["workflow_id"])
		}
	}

	// 节点 / 事件:必须非空数组(本次执行确实跑过节点)
	for _, p := range []string{"/api/executions/" + execID + "/nodes", "/api/executions/" + execID + "/events"} {
		code, _, raw = doReq(t, s, http.MethodGet, p, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s → %d (%s)", p, code, raw)
		}
		var arr []map[string]any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("GET %s 非数组: %v (%s)", p, err, raw)
		}
		if len(arr) == 0 {
			t.Errorf("GET %s 返回空数组,执行 %s 应有记录", p, execID)
		}
	}

	// 制品:mock 工作流不产出制品,但契约是数组
	code, _, raw = doReq(t, s, http.MethodGet, "/api/executions/"+execID+"/artifacts", "")
	if code != http.StatusOK {
		t.Fatalf("artifacts → %d (%s)", code, raw)
	}
	var arts []map[string]any
	if err := json.Unmarshal(raw, &arts); err != nil {
		t.Fatalf("artifacts 非数组: %v (%s)", err, raw)
	}

	// 导出:JSON + attachment 头 + 四段齐全
	w := doServe(t, s, http.MethodGet, "/api/executions/"+execID+"/export", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("exec export → %d (%s)", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") ||
		!strings.Contains(cd, execID) {
		t.Errorf("exec export Content-Disposition = %q, want attachment 且含执行 ID", cd)
	}
	var dump map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &dump); err != nil {
		t.Fatalf("exec export 非 JSON: %v", err)
	}
	for _, k := range []string{"execution", "nodes", "events", "artifacts"} {
		if _, ok := dump[k]; !ok {
			t.Errorf("exec export 缺 %q 段: %s", k, w.Body.String())
		}
	}
}

// TestContract_ExecutionCancel:取消不存在的执行 404;取消终态执行 400 +
// code=INVALID_TRANSITION(而非静默 200)。
func TestContract_ExecutionCancel(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-cancel")

	// 资源缺失 → 404
	code, body, raw := doReq(t, s, http.MethodPost, "/api/executions/ghost/cancel", "")
	if code != http.StatusNotFound {
		t.Fatalf("cancel 不存在执行 → %d, want 404 (%s)", code, raw)
	}
	assertErrorBody(t, "cancel 不存在执行", body, raw, "NOT_FOUND")

	// 已完成执行不可再取消
	code, body, raw = doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	if code != http.StatusOK {
		t.Fatalf("run → %d (%s)", code, raw)
	}
	execID, _ := body["id"].(string)
	waitExecState(t, s, execID, "COMPLETED", "FAILED")

	code, body, raw = doReq(t, s, http.MethodPost, "/api/executions/"+execID+"/cancel", "")
	if code != http.StatusBadRequest {
		t.Fatalf("cancel 终态执行 → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "cancel 终态执行", body, raw, "INVALID_TRANSITION")
}

// TestContract_ExecutionDelete:删除存在的执行返回 200,再删一次仍 200
// (幂等),删除后详情 404。
func TestContract_ExecutionDelete(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-del")
	_, body, _ := doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	execID, _ := body["id"].(string)
	if execID == "" {
		t.Fatal("run 未返回执行 ID")
	}
	waitExecState(t, s, execID, "COMPLETED", "FAILED")

	if code, _, raw := doReq(t, s, http.MethodDelete, "/api/executions/"+execID, ""); code != http.StatusOK {
		t.Fatalf("DELETE → %d, want 200 (%s)", code, raw)
	}
	if code, _, _ := doReq(t, s, http.MethodDelete, "/api/executions/"+execID, ""); code != http.StatusOK {
		t.Errorf("重复 DELETE → 期望 200(幂等)")
	}
	code, body, raw := doReq(t, s, http.MethodGet, "/api/executions/"+execID, "")
	if code != http.StatusNotFound {
		t.Errorf("删除后详情 → %d, want 404 (%s)", code, raw)
	}
	assertErrorBody(t, "删除后详情", body, raw, "NOT_FOUND")
}

// TestContract_StatsAndEvents:统计与全局审计流都是数组,过滤参数可用。
func TestContract_StatsAndEvents(t *testing.T) {
	s := newTestServer(t)
	id := seedWorkflow(t, s, "wf-stats")
	_, body, _ := doReq(t, s, http.MethodPost, "/api/workflows/"+id+"/run", "")
	execID, _ := body["id"].(string)
	waitExecState(t, s, execID, "COMPLETED", "FAILED")

	code, _, raw := doReq(t, s, http.MethodGet, "/api/stats", "")
	if code != http.StatusOK {
		t.Fatalf("stats → %d (%s)", code, raw)
	}
	var stats []map[string]any
	if err := json.Unmarshal(raw, &stats); err != nil {
		t.Fatalf("stats 非数组: %v (%s)", err, raw)
	}
	found := false
	for _, st := range stats {
		if st["workflow_id"] == id {
			found = true
			if n, _ := st["total"].(float64); n < 1 {
				t.Errorf("stats.total = %v, want ≥1", st["total"])
			}
		}
	}
	if !found {
		t.Errorf("stats 未含 %s: %s", id, raw)
	}

	// 审计流:默认 + type 过滤
	code, _, raw = doReq(t, s, http.MethodGet, "/api/events/all", "")
	if code != http.StatusOK {
		t.Fatalf("events/all → %d (%s)", code, raw)
	}
	var evs []map[string]any
	if err := json.Unmarshal(raw, &evs); err != nil {
		t.Fatalf("events/all 非数组: %v (%s)", err, raw)
	}
	if len(evs) == 0 {
		t.Errorf("events/all 为空,执行 %s 应留痕", execID)
	}
	for _, e := range evs {
		if e["type"] == nil || e["execution_id"] == nil {
			t.Errorf("事件形状缺 type/execution_id: %v", e)
		}
	}
	code, _, raw = doReq(t, s, http.MethodGet, "/api/events/all?type=workflow_started&limit=5", "")
	if code != http.StatusOK {
		t.Errorf("events/all?type=... → %d (%s)", code, raw)
	}
	if err := json.Unmarshal(raw, &evs); err != nil {
		t.Errorf("type 过滤结果非数组: %v (%s)", err, raw)
	}
}

// TestContract_AgentConfigLifecycle:GET/PUT/reset 三个配置端点的契约。
// 掩码与合并语义由 TestAgentConfigEndpointMasking 覆盖,此处只锁形状。
func TestContract_AgentConfigLifecycle(t *testing.T) {
	s := newTestServer(t)
	const path = "/api/agents/mock-claude/config"

	// GET:即使未配置也返回完整 AgentConfig 结构(键齐全)
	code, body, raw := doReq(t, s, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("GET config → %d (%s)", code, raw)
	}
	for _, k := range []string{"model", "base_url", "timeout_seconds"} {
		if _, ok := body[k]; !ok {
			t.Errorf("GET config 缺字段 %q: %s", k, raw)
		}
	}

	// PUT:返回掩码后的配置
	code, body, raw = doReq(t, s, http.MethodPut, path, `{"model":"m-1","env":{"TOK":"sk-live"}}`)
	if code != http.StatusOK || body["model"] != "m-1" {
		t.Fatalf("PUT config → %d (%s)", code, raw)
	}
	if env, _ := body["env"].(map[string]any); env["TOK"] != "***" {
		t.Errorf("PUT 响应未掩码: %s", raw)
	}

	// reset:清空后回到零值
	code, body, raw = doReq(t, s, http.MethodPost, "/api/agents/mock-claude/reset", "")
	if code != http.StatusOK {
		t.Fatalf("reset → %d (%s)", code, raw)
	}
	if m, _ := body["model"].(string); m != "" {
		t.Errorf("reset 后 model = %v, want 空", body["model"])
	}

	// 畸形 body → 400 + error
	code, body, raw = doReq(t, s, http.MethodPut, path, `{"model":123}`)
	if code != http.StatusBadRequest {
		t.Errorf("PUT 错类型 → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "PUT config 错类型", body, raw, "")
}

// TestContract_AgentPermissionAndTest:权限写入后可在列表回读;test 返回
// output 文本;未知 Agent 的 test 是 4xx(现状为 400 + 无 code,见交付说明)。
func TestContract_AgentPermissionAndTest(t *testing.T) {
	s := newTestServer(t)

	code, body, raw := doReq(t, s, http.MethodPost, "/api/agents/mock-claude/permission",
		`{"filesystem_read":true,"git_read":true}`)
	if code != http.StatusOK {
		t.Fatalf("permission → %d (%s)", code, raw)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("permission ok = %v, want true (%s)", body["ok"], raw)
	}
	_, _, raw = doReq(t, s, http.MethodGet, "/api/agents", "")
	var agents []map[string]any
	if err := json.Unmarshal(raw, &agents); err != nil {
		t.Fatalf("agents 非数组: %v (%s)", err, raw)
	}
	applied := false
	for _, a := range agents {
		if a["id"] != "mock-claude" {
			continue
		}
		applied = true
		perms, _ := a["permissions"].(map[string]any)
		if perms["filesystem_read"] != true || perms["git_read"] != true {
			t.Errorf("权限未生效: %v", perms)
		}
		if a["name"] == nil {
			t.Errorf("agents 列表缺 name: %v", a)
		}
	}
	if !applied {
		t.Fatalf("agents 列表未含 mock-claude: %s", raw)
	}

	code, body, raw = doReq(t, s, http.MethodPost, "/api/agents/mock-claude/test", "")
	if code != http.StatusOK {
		t.Fatalf("agent test → %d (%s)", code, raw)
	}
	if out, _ := body["output"].(string); out == "" {
		t.Errorf("agent test 无 output: %s", raw)
	}

	// 畸形 body → 400
	code, body, raw = doReq(t, s, http.MethodPost, "/api/agents/mock-claude/permission", `not-json`)
	if code != http.StatusBadRequest {
		t.Errorf("permission 畸形 body → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "permission 畸形 body", body, raw, "")

	// 未知 Agent:期望 404 + NOT_FOUND;现状 400(注册表返回普通 error),
	// 这里只要求 4xx + error 文本,不改后端。
	code, body, raw = doReq(t, s, http.MethodPost, "/api/agents/ghost/test", "")
	if code < 400 || code >= 500 {
		t.Errorf("未知 Agent test → %d, 期望 4xx(%s)", code, raw)
	}
	assertErrorBody(t, "未知 Agent test", body, raw, "")
}

// TestContract_AgentConfigUnknownID:未知 Agent 的配置读写必须 404
// NOT_FOUND。此前直接查配置行,不存在的 ID 返回 200 + 空配置(前端分不清
// "Agent 不存在"与"它没配过"),PUT/reset 更糟 —— 会为不存在的 ID 凭空
// 落库一行配置,applyAgentConfig 对未知 ID 静默返回,配置永远不生效。
func TestContract_AgentConfigUnknownID(t *testing.T) {
	s := newTestServer(t)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/agents/ghost/config", ""},
		{http.MethodPut, "/api/agents/ghost/config", `{"model":"m"}`},
		{http.MethodPost, "/api/agents/ghost/reset", ""},
		{http.MethodPost, "/api/agents/ghost/permission", `{"git_read":true}`},
	} {
		code, body, raw := doReq(t, s, c.method, c.path, c.body)
		if code != http.StatusNotFound {
			t.Errorf("%s %s → %d,应 404 (%s)", c.method, c.path, code, raw)
			continue
		}
		assertErrorBody(t, c.path, body, raw, "NOT_FOUND")
	}
	// 不得为不存在的 Agent 落库配置
	_, backup, _ := doReq(t, s, http.MethodGet, "/api/backup", "")
	cfgs, _ := backup["agent_configs"].(map[string]any)
	if cfgs != nil && cfgs["ghost"] != nil {
		t.Errorf("未知 Agent 不应落库配置: %v", cfgs["ghost"])
	}
	// 已知 Agent 仍正常
	code, _, raw := doReq(t, s, http.MethodGet, "/api/agents/mock-claude/config", "")
	if code != http.StatusOK {
		t.Errorf("已知 Agent config → %d (%s)", code, raw)
	}
}

// TestContract_SkillEnable:列表形状 + enabled 字段缺失/畸形 → 400。
// 真实启停效果由 TestSkillEnableEndpoint 覆盖。
func TestContract_SkillEnable(t *testing.T) {
	s := newTestServer(t)

	code, _, raw := doReq(t, s, http.MethodGet, "/api/skills", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/skills → %d (%s)", code, raw)
	}
	var skills []map[string]any
	if err := json.Unmarshal(raw, &skills); err != nil {
		t.Fatalf("skills 非数组: %v (%s)", err, raw)
	}
	hasLog := false
	for _, sk := range skills {
		if _, ok := sk["enabled"]; !ok {
			t.Errorf("skill 缺 enabled 字段: %v", sk)
		}
		if sk["id"] == "log" {
			hasLog = true
		}
	}
	if !hasLog {
		t.Errorf("skills 未含内置 log: %s", raw)
	}

	// 缺 enabled / 畸形 body / 未知 skill → 4xx + error
	cases := []struct{ label, path, body string }{
		{"缺 enabled 字段", "/api/skills/log/enable", `{}`},
		{"enabled 非布尔", "/api/skills/log/enable", `{"enabled":"yes"}`},
		{"畸形 JSON", "/api/skills/log/enable", `not-json`},
		{"未知 skill", "/api/skills/ghost/enable", `{"enabled":true}`},
	}
	for _, c := range cases {
		code, body, raw := doReq(t, s, http.MethodPost, c.path, c.body)
		if code < 400 || code >= 500 {
			t.Errorf("%s → %d, 期望 4xx(%s)", c.label, code, raw)
		}
		assertErrorBody(t, c.label, body, raw, "")
	}

	// 合法请求 → 200 + ok
	code, body, raw := doReq(t, s, http.MethodPost, "/api/skills/log/enable", `{"enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("enable → %d (%s)", code, raw)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("enable ok = %v, want true (%s)", body["ok"], raw)
	}
}

// TestContract_Git:status/diff/log 形状 + commit 空 message 400。
// 真实仓库的提交行为由 TestGitRoutes 覆盖,这里用独立临时仓库。
func TestContract_Git(t *testing.T) {
	s := newGitTestServer(t)

	code, body, raw := doReq(t, s, http.MethodGet, "/api/git/status", "")
	if code != http.StatusOK {
		t.Fatalf("git status → %d (%s)", code, raw)
	}
	for _, k := range []string{"branch", "modified", "untracked"} {
		if _, ok := body[k]; !ok {
			t.Errorf("git status 缺字段 %q: %s", k, raw)
		}
	}

	code, body, raw = doReq(t, s, http.MethodGet, "/api/git/diff", "")
	if code != http.StatusOK {
		t.Fatalf("git diff → %d (%s)", code, raw)
	}
	if _, ok := body["diff"].(string); !ok {
		t.Errorf("git diff 缺 diff 字符串: %s", raw)
	}

	code, _, raw = doReq(t, s, http.MethodGet, "/api/git/log?limit=5", "")
	if code != http.StatusOK {
		t.Fatalf("git log → %d (%s)", code, raw)
	}
	var logs []map[string]any
	if err := json.Unmarshal(raw, &logs); err != nil {
		t.Fatalf("git log 非数组: %v (%s)", err, raw)
	}
	if len(logs) == 0 {
		t.Errorf("git log 为空,临时仓库已有一个提交: %s", raw)
	}
	for _, l := range logs {
		if l["hash"] == nil || l["subject"] == nil {
			t.Errorf("log 条目缺 hash/subject: %v", l)
		}
	}

	// 空 message(及全空白)→ 400 + error
	for _, msg := range []string{`{"message":""}`, `{"message":"   "}`, `{}`} {
		code, body, raw := doReq(t, s, http.MethodPost, "/api/git/commit", msg)
		if code != http.StatusBadRequest {
			t.Errorf("commit %s → %d, want 400 (%s)", msg, code, raw)
		}
		assertErrorBody(t, "commit "+msg, body, raw, "")
	}
}

// newGitTestServer 建一个指向临时 git 仓库的 Server(避免读写开发仓库)。
func newGitTestServer(t *testing.T) *Server {
	t.Helper()
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })

	repoDir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.GitSvc.SetWorkingDir(repoDir)
	if code, _, raw := doReq(t, NewServer(a), http.MethodPost, "/api/git/commit", `{"message":"init"}`); code != http.StatusOK {
		t.Fatalf("初始提交失败: %d (%s)", code, raw)
	}
	return NewServer(a)
}

// TestContract_Settings:读取形状 + 三个可改字段真实生效 + 畸形 body 400。
func TestContract_Settings(t *testing.T) {
	s := newTestServer(t)

	code, body, raw := doReq(t, s, http.MethodGet, "/api/settings", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/settings → %d (%s)", code, raw)
	}
	for _, k := range []string{"data_dir", "git_working_dir", "log_level", "allow_script_nodes"} {
		if _, ok := body[k]; !ok {
			t.Errorf("settings 缺字段 %q: %s", k, raw)
		}
	}
	if body["allow_script_nodes"] != false {
		t.Errorf("allow_script_nodes = %v, want 默认 false", body["allow_script_nodes"])
	}

	// log_level 即时生效并回读一致
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings", `{"log_level":"debug"}`)
	if code != http.StatusOK || body["log_level"] != "debug" {
		t.Fatalf("POST settings log_level → %d (%s)", code, raw)
	}
	_, back, _ := doReq(t, s, http.MethodGet, "/api/settings", "")
	if back["log_level"] != "debug" {
		t.Errorf("log_level 未落库: %v", back["log_level"])
	}

	// git_working_dir
	dir := t.TempDir()
	dirBody, _ := json.Marshal(map[string]string{"git_working_dir": dir})
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings", string(dirBody))
	if code != http.StatusOK || body["git_working_dir"] != dir {
		t.Fatalf("POST settings git_working_dir → %d (%s)", code, raw)
	}

	// allow_script_nodes 显式布尔开关(fail closed:缺省不改)
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings", `{"allow_script_nodes":true}`)
	if code != http.StatusOK || body["allow_script_nodes"] != true {
		t.Fatalf("POST settings allow_script_nodes → %d (%s)", code, raw)
	}
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings", `{}`)
	if code != http.StatusOK || body["allow_script_nodes"] != true {
		t.Errorf("缺省 body 不应改动 allow_script_nodes,实际 %v", body["allow_script_nodes"])
	}
	// 恢复默认,避免污染后续用例的全局 logx 状态
	if code, _, _ = doReq(t, s, http.MethodPost, "/api/settings",
		`{"allow_script_nodes":false,"log_level":"info"}`); code != http.StatusOK {
		t.Errorf("恢复默认设置失败: %d", code)
	}

	// 畸形 body → 400 + error
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings", `not-json`)
	if code != http.StatusBadRequest {
		t.Errorf("settings 畸形 body → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "settings 畸形 body", body, raw, "")
}

// TestContract_Backup:导出结构 + attachment 头;恢复回导计数。
func TestContract_Backup(t *testing.T) {
	s := newTestServer(t)
	seedWorkflow(t, s, "wf-bk")

	w := doServe(t, s, http.MethodGet, "/api/backup", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/backup → %d (%s)", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("backup Content-Disposition = %q, want attachment", cd)
	}
	var backup map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &backup); err != nil {
		t.Fatalf("backup 非 JSON: %v", err)
	}
	for _, k := range []string{"exported_at", "version", "workflows", "agent_configs"} {
		if _, ok := backup[k]; !ok {
			t.Errorf("backup 缺字段 %q: %s", k, w.Body.String())
		}
	}
	if wfs, _ := backup["workflows"].([]any); len(wfs) != 1 {
		t.Errorf("backup.workflows = %v, want 1 条", backup["workflows"])
	}

	// 回导:已存在的工作流跳过,新工作流恢复,非法条目计入 invalid
	payload := `{"version":1,"workflows":[
	  {"id":"wf-bk","name":"Seed","nodes":[{"id":"a","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],"edges":[]},
	  {"id":"wf-bk-new","name":"New","nodes":[{"id":"a","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],"edges":[]},
	  {"id":"","name":"Broken"}
	],"agent_configs":{}}`
	code, body, raw := doReq(t, s, http.MethodPost, "/api/backup/restore", payload)
	if code != http.StatusOK {
		t.Fatalf("restore → %d (%s)", code, raw)
	}
	if n, _ := body["workflows_skipped"].(float64); n != 1 {
		t.Errorf("workflows_skipped = %v, want 1 (%s)", body["workflows_skipped"], raw)
	}
	if n, _ := body["workflows_restored"].(float64); n != 1 {
		t.Errorf("workflows_restored = %v, want 1 (%s)", body["workflows_restored"], raw)
	}
	if n, _ := body["workflows_invalid"].(float64); n != 1 {
		t.Errorf("workflows_invalid = %v, want 1 (%s)", body["workflows_invalid"], raw)
	}
	if _, ok := body["agent_configs_restored"]; !ok {
		t.Errorf("restore 响应缺 agent_configs_restored: %s", raw)
	}
	// 恢复出来的工作流确实在库里
	if code, _, raw = doReq(t, s, http.MethodGet, "/api/workflows/wf-bk-new", ""); code != http.StatusOK {
		t.Errorf("恢复的工作流不可读: %d (%s)", code, raw)
	}

	// 畸形 body → 400 + error
	code, body, raw = doReq(t, s, http.MethodPost, "/api/backup/restore", `{`)
	if code != http.StatusBadRequest {
		t.Errorf("restore 畸形 body → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "restore 畸形 body", body, raw, "")
}

// TestContract_SSEHeaders:SSE 建连后立刻下发响应头与 `: connected` 注释行
// (否则 EventSource 一直 CONNECTING)。只读首行,不消费事件流。
func TestContract_SSEHeaders(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE 连接: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Errorf("SSE → %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("读首行: %v", err)
	}
	if line != ": connected\n" {
		t.Errorf("首行 = %q, want \": connected\\n\"", line)
	}
}

// TestContract_SubresourceNotFound:执行子资源(nodes/events/artifacts)对
// 不存在的父执行必须 404 NOT_FOUND。此前直接查子表,返回 200 + 空数组,
// 前端无法区分"这个执行没有节点明细"与"执行 ID 打错了"。
func TestContract_SubresourceNotFound(t *testing.T) {
	s := newTestServer(t)

	for _, p := range []string{
		"/api/executions/ghost/nodes",
		"/api/executions/ghost/events",
		"/api/executions/ghost/artifacts",
	} {
		code, body, raw := doReq(t, s, http.MethodGet, p, "")
		if code != http.StatusNotFound {
			t.Errorf("GET %s → %d,应 404 (%s)", p, code, raw)
			continue
		}
		assertErrorBody(t, p, body, raw, "NOT_FOUND")
	}
}

// TestContract_EmptyListIsArrayNotNull:列表端点的空结果必须是 `[]` 而非
// `null`。Go 把 nil slice 编码成 null,前端 asArray 能兜住,但 REST 契约
// 要求一律返回数组(AGENTS.md 约定 #7),否则任何非前端消费者都要各自兜一遍。
func TestContract_EmptyListIsArrayNotNull(t *testing.T) {
	s := newTestServer(t)

	for _, p := range []string{
		"/api/workflows/ghost/versions",
		"/api/executions",
		"/api/workflows",
		"/api/agents",
		"/api/skills",
		"/api/events/all",
		"/api/stats",
	} {
		_, _, raw := doReq(t, s, http.MethodGet, p, "")
		if string(raw) == "null\n" || string(raw) == "null" {
			t.Errorf("GET %s 返回 null,应为 [] (%s)", p, raw)
		}
	}
}

// TestContract_RegistryErrorsAreStructured:Agent/Skill 注册表查不到时
// 必须返回 404 + code=NOT_FOUND。此前 registry 返回裸 fmt.Errorf,
// wrap() 只能映射成 400 且 body 里没有 code —— 前端分不清"资源不存在"
// 与"参数错误"(与 AGENTS.md 404 语义约定不一致)。
func TestContract_RegistryErrorsAreStructured(t *testing.T) {
	s := newTestServer(t)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/agents/ghost/test", ""},
		{http.MethodPost, "/api/skills/ghost/enable", `{"enabled":true}`},
	} {
		code, body, raw := doReq(t, s, c.method, c.path, c.body)
		if code != http.StatusNotFound {
			t.Errorf("%s %s → %d,应 404 (%s)", c.method, c.path, code, raw)
			continue
		}
		assertErrorBody(t, c.path, body, raw, "NOT_FOUND")
	}
	// 已知 ID 不受影响
	if code, _, raw := doReq(t, s, http.MethodPost, "/api/agents/mock-claude/test", ""); code != http.StatusOK {
		t.Errorf("已知 Agent test → %d (%s)", code, raw)
	}
}

// TestErrorBodyAlwaysHasCodeAndKind:错误体形状必须统一。
// 此前只有结构化错误(model.Error)带 code/kind,而 badRequest 这类
// 普通错误只有 error 字段 —— 同一套 API 出现两种错误体形状,
// 前端 ApiError.code 时而 undefined,无法按 code 分支处理
// (约定 8 的"API 返回掩码/结构化"同理:错误也要结构化)。
func TestErrorBodyAlwaysHasCodeAndKind(t *testing.T) {
	s := newTestServer(t)

	cases := []struct{ label, method, path, body string }{
		{"结构化校验错误", http.MethodPost, "/api/workflows",
			`{"id":"wf-bad","name":"Bad","nodes":[{"id":"a","type":"agent","config":{"agent":"no-such"}}],"edges":[]}`},
		{"普通错误(非法 JSON)", http.MethodPost, "/api/workflows/validate", `not-json`},
		{"普通错误(空 commit message)", http.MethodPost, "/api/git/commit", `{"message":""}`},
		{"普通错误(缺 enabled)", http.MethodPost, "/api/skills/log/enable", `{}`},
		{"资源缺失", http.MethodGet, "/api/workflows/ghost", ""},
		{"方法不允许", http.MethodPut, "/api/workflows", `{}`},
		{"未知路由", http.MethodGet, "/api/no-such-endpoint", ""},
	}
	for _, c := range cases {
		code, body, raw := doReq(t, s, c.method, c.path, c.body)
		// **断言口径从"必须 4xx"放宽为"4xx 或 5xx"(迭代 30,P1-2)**。
		//
		// 原因:此前这条断言把"错误必须是 4xx"**钉成了契约** ——
		// 而那正是缺陷本身:数据库锁死、Agent 崩溃、超时这些**内部**
		// 错误此前一律报 400,用户与监控无法区分"用户填错了"与
		// "后端炸了"(监控侧的 4xx 告警会被这类噪声淹没)。
		// 内部错误改为 5xx 之后,这条断言若还要求 4xx,就会把
		// 正确的 5xx 判成失败 —— 即"断言反过来钉住了错误行为"。
		//
		// 各 kind → 状态码的**精确**映射由
		// error_contract_test.go 的 TestErrorStatusMappingByKind
		// 逐条断言(那张表才是不放松的地方)。
		if code < 400 || code >= 600 {
			t.Errorf("%s: %s %s → %d,期望 4xx 或 5xx(%s)", c.label, c.method, c.path, code, raw)
			continue
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Errorf("%s: 错误体缺 error: %s", c.label, raw)
		}
		if cd, _ := body["code"].(string); cd == "" {
			t.Errorf("%s: 错误体缺 code: %s", c.label, raw)
		}
		if kd, _ := body["kind"].(string); kd == "" {
			t.Errorf("%s: 错误体缺 kind: %s", c.label, raw)
		}
	}
}

// TestMethodNotAllowedAndUnknownRouteReturnJSONError:方法不匹配(405)与
// 未知路由(404)此前由 net/http ServeMux 直接写纯文本("Method Not
// Allowed" / "404 page not found"),与本 API 的 JSON 错误体契约不一致:
// 前端 http() 解析不出 error 字段,只能回退成 "HTTP 405"。
func TestMethodNotAllowedAndUnknownRouteReturnJSONError(t *testing.T) {
	s := newTestServer(t)

	code, body, raw := doReq(t, s, http.MethodPut, "/api/workflows", `{}`)
	if code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/workflows → %d,期望 405(%s)", code, raw)
	}
	assertErrorBody(t, "PUT /api/workflows", body, raw, "METHOD_NOT_ALLOWED")

	code, body, raw = doReq(t, s, http.MethodGet, "/api/no-such-endpoint", "")
	if code != http.StatusNotFound {
		t.Errorf("GET /api/no-such-endpoint → %d,期望 404(%s)", code, raw)
	}
	assertErrorBody(t, "GET /api/no-such-endpoint", body, raw, "NOT_FOUND")
}

// TestSettingsRejectsInvalidLogLevel:非法日志级别此前被静默接受 ——
// logx.SetLevel 对未知值回落 info,而 Settings 落库/回显的却是那个
// 非法字符串,界面显示"debug"实际却是 info(静默丢弃校验错误)。
func TestSettingsRejectsInvalidLogLevel(t *testing.T) {
	s := newTestServer(t)

	code, body, raw := doReq(t, s, http.MethodPost, "/api/settings", `{"log_level":"bogus"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("非法 log_level → %d,期望 400(%s)", code, raw)
	}
	assertErrorBody(t, "非法 log_level", body, raw, "")

	// 非法值不得落库、不得改变当前级别
	_, body, _ = doReq(t, s, http.MethodGet, "/api/settings", "")
	if lv, _ := body["log_level"].(string); lv != "info" {
		t.Errorf("log_level = %q,期望保持 info", lv)
	}
	// 合法值仍然可用
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings", `{"log_level":"warn"}`)
	if code != http.StatusOK {
		t.Fatalf("合法 log_level → %d(%s)", code, raw)
	}
	if body["log_level"] != "warn" {
		t.Errorf("log_level = %v,期望 warn", body["log_level"])
	}
}

// TestQueryLimitOverflowIsClamped:查询参数里的 limit 此前用 int 累加解析,
// 超长数字会整数溢出成负数,而仓储层把 limit<=0 解释为"不限制" ——
// 一个 limit=99999999999999999999 的请求变成"返回全表"。
// 截断必须同时带标志:静默截断会让消费方把"返回了 1000 条"误读成
// "总共只有 1000 条"(开放清单第 4 条)。
func TestQueryLimitOverflowIsClamped(t *testing.T) {
	mk := func(q string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/"+q, nil)
	}
	if got, tr := queryLimit(mk("?limit=25"), "limit", 7); got != 25 || tr {
		t.Errorf("limit=25 → (%d,%v),期望 (25,false)", got, tr)
	}
	if got, tr := queryLimit(mk("?limit="), "limit", 7); got != 7 || tr {
		t.Errorf("空 limit → (%d,%v),期望默认值 (7,false)", got, tr)
	}
	if got, tr := queryLimit(mk("?limit=abc"), "limit", 7); got != 7 || tr {
		t.Errorf("非数字 limit → (%d,%v),期望默认值 (7,false)", got, tr)
	}
	if got, tr := queryLimit(mk("?limit=99999999999999999999"), "limit", 7); got != maxQueryLimit || !tr {
		t.Errorf("溢出 limit → (%d,%v),期望 (%d,true)(不得为负数或全表)", got, tr, maxQueryLimit)
	}
	if got, tr := queryLimit(mk("?limit=999999"), "limit", 7); got != maxQueryLimit || !tr {
		t.Errorf("超上限 limit → (%d,%v),期望 (%d,true)", got, tr, maxQueryLimit)
	}
	if got, tr := queryLimit(mk("?limit=1000"), "limit", 7); got != maxQueryLimit || tr {
		t.Errorf("恰等于上限 → (%d,%v),期望 (%d,false) —— 等于上限不是截断", got, tr, maxQueryLimit)
	}
}

// TestLimitTruncatedHeader:截断标记必须真正出现在响应头里,
// 且未截断的请求不得带这两个头(消费方按"存在即截断"判定)。
func TestLimitTruncatedHeader(t *testing.T) {
	s := newTestServer(t)
	do := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	w := do("/api/executions?limit=999999")
	if w.Code != http.StatusOK {
		t.Fatalf("截断请求 → %d,期望 200(%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Limit-Truncated"); got != "true" {
		t.Errorf("截断请求缺 X-Limit-Truncated: true,got %q", got)
	}
	// 执行列表的上限是 500(摘要 DTO + 游标),与 events/git 的 1000 分开。
	if got := w.Header().Get("X-Limit-Applied"); got != "500" {
		t.Errorf("截断请求缺 X-Limit-Applied: 500,got %q", got)
	}

	w = do("/api/executions?limit=50")
	if w.Code != http.StatusOK {
		t.Fatalf("常规请求 → %d,期望 200(%s)", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Limit-Truncated") != "" || w.Header().Get("X-Limit-Applied") != "" {
		t.Errorf("未截断的请求不应带截断头, got %v", w.Header())
	}

	// 三个列表端点同口径:events/all 与 git/log 也要打标。
	for _, path := range []string{"/api/events/all?limit=424242", "/api/git/log?limit=424242"} {
		w = do(path)
		if w.Code != http.StatusOK {
			t.Fatalf("%s → %d,期望 200(%s)", path, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Limit-Truncated") != "true" {
			t.Errorf("%s 缺截断标记,消费方会误读条数(got %v)", path, w.Header())
		}
	}
}

// TestWorkflowVersionPathRejectsOverflow:版本号路径参数同样用 int 累加
// 解析,溢出后会变成负数/垃圾值,再去查版本快照(必然 404 但错误码不对)。
func TestWorkflowVersionPathRejectsOverflow(t *testing.T) {
	s := newTestServer(t)
	seedWorkflow(t, s, "wf-ver")

	code, body, raw := doReq(t, s, http.MethodGet, "/api/workflows/wf-ver/versions/99999999999999999999", "")
	if code != http.StatusBadRequest {
		t.Fatalf("溢出版本号 → %d,期望 400(%s)", code, raw)
	}
	assertErrorBody(t, "溢出版本号", body, raw, "")

	// 正常版本号仍可用
	code, _, raw = doReq(t, s, http.MethodGet, "/api/workflows/wf-ver/versions/1", "")
	if code != http.StatusOK {
		t.Errorf("版本 1 → %d(%s)", code, raw)
	}
}

// TestBackupResponseIsNotCacheable:备份导出是约定 8 唯一允许含明文
// 凭据的出口,必须显式禁止缓存,否则浏览器/代理可能把明文 API Key
// 留在磁盘缓存里。
func TestBackupResponseIsNotCacheable(t *testing.T) {
	s := newTestServer(t)

	w := doServe(t, s, http.MethodGet, "/api/backup", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/backup → %d(%s)", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("备份导出 Cache-Control = %q,期望含 no-store", cc)
	}
}

// TestExecutionNodeOutputMaskedAtAPI:节点明细的 output/error 直接来自
// Agent 的 stdout/stderr(常含 ANTHROPIC_API_KEY=sk-... 这类凭据)。
// 事件流已在总线出口脱敏,但节点明细是直写 SQLite、原样返回 ——
// API 读出口必须再脱敏一次(约定 8)。
func TestExecutionNodeOutputMaskedAtAPI(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })

	const secret = "ANTHROPIC_API_KEY=sk-live-should-not-leak"
	exec := &model.Execution{ID: "exec-mask", WorkflowID: "wf-x", State: model.ExecutionCompleted}
	if err := a.Repo.SaveExecution(exec); err != nil {
		t.Fatalf("save execution: %v", err)
	}
	if err := a.Repo.SaveExecutionNode(&model.ExecutionNode{
		ID:          "exec-mask:n1:1",
		ExecutionID: exec.ID,
		NodeID:      "n1",
		NodeType:    model.NodeTypeAgent,
		State:       model.NodeSuccess,
		Output:      "cli stderr: " + secret,
		Error:       "failed: " + secret,
	}); err != nil {
		t.Fatalf("save node: %v", err)
	}

	s := NewServer(a)
	_, _, raw := doReq(t, s, http.MethodGet, "/api/executions/exec-mask/nodes", "")
	if strings.Contains(string(raw), "sk-live-should-not-leak") {
		t.Errorf("节点明细接口泄漏明文凭据: %s", raw)
	}
	_, _, raw = doReq(t, s, http.MethodGet, "/api/executions/exec-mask/export", "")
	if strings.Contains(string(raw), "sk-live-should-not-leak") {
		t.Errorf("执行导出接口泄漏明文凭据: %s", raw)
	}
}

// TestJSONBodyTooLargeReturns413:超限请求体此前一律 400 + 一句
// "http: request body too large"(语义不对:这是 413 的场景),
// 且各端点表现不一。现在统一 413 + code=PAYLOAD_TOO_LARGE。
func TestJSONBodyTooLargeReturns413(t *testing.T) {
	s := newTestServer(t)
	huge := `{"task":"` + strings.Repeat("x", 2<<20) + `"}`

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/workflows"},
		{http.MethodPost, "/api/backup/restore"},
		{http.MethodPost, "/api/workflows/wf-x/run"},
		{http.MethodPost, "/api/workflows/validate"},
		{http.MethodPost, "/api/workflows/import"},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(huge))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s → %d,期望 413(%s)", c.method, c.path, w.Code, w.Body.String())
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s %s: 错误体不是 JSON: %s", c.method, c.path, w.Body.String())
			continue
		}
		if body["code"] != "PAYLOAD_TOO_LARGE" {
			t.Errorf("%s %s: code = %v,期望 PAYLOAD_TOO_LARGE", c.method, c.path, body["code"])
		}
	}
}

// TestInvalidJSONHasStructuredCode:非法 JSON 的错误体必须带结构化 code,
// 消费方(前端 ApiError)才能按 code 分支而不是解析文本。
func TestInvalidJSONHasStructuredCode(t *testing.T) {
	s := newTestServer(t)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/workflows/validate", `not-json`},
		{http.MethodPost, "/api/backup/restore", `{`},
		{http.MethodPost, "/api/settings", `not-json`},
	} {
		code, body, raw := doReq(t, s, c.method, c.path, c.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s %s → %d,期望 400(%s)", c.method, c.path, code, raw)
			continue
		}
		assertErrorBody(t, c.path, body, raw, "INVALID_JSON")
	}
}
