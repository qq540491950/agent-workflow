package api

import (
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
	a.GitSvc.WorkingDir = repoDir

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
