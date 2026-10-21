package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件是"错误契约"的回归锁(迭代 30):状态码语义 / panic 兜底 /
// 错误文本不外泄内部细节 / 路由级 404 文案。
//
// ---- 背景(第二十九轮扫描) ----
//
// 1. 内部错误(DB 锁死 / Agent 崩溃 / 超时)**一律报 400** —— 用户与
//    监控无法区分"用户填错了"与"后端炸了";api/server_test.go 里的
//    "错误必须是 4xx"断言把这条错误行为**钉成了契约**。
// 2. wrap **没有 recover** —— panic 会一路冒到 net/http:服务器模式
//    客户端收到 EOF(前端报"Failed to fetch",误判成"后端没启动"),
//    桌面模式则把 Go 包路径泄漏到界面上。
// 3. 错误文本夹带 Go/SQL/git 内部细节
//    ("json: cannot unmarshal string into Go struct field
//    AgentConfig.extra_args of type []string")。

// serveErr 让一个合成 handler 返回指定错误,走**真实的 wrap + HTTP 栈**。
func serveErr(t *testing.T, s *Server, err error) (int, map[string]any, []byte) {
	t.Helper()
	h := s.wrap(func(w http.ResponseWriter, r *http.Request) error { return err })
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	raw := rec.Body.Bytes()
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return rec.Code, m, raw
}

// TestErrorStatusMappingByKind:每种错误类别 → 期望的 HTTP 状态码。
//
// **向后兼容是硬要求**:既有 4xx 映射(NOT_FOUND→404、PAYLOAD_TOO_LARGE
// →413、PermissionError→403、其余校验/状态类→400)一律不变;
// 新增的 5xx 只覆盖内部类错误。
func TestErrorStatusMappingByKind(t *testing.T) {
	s := newTestServer(t)

	cases := []struct {
		name string
		err  error
		want int
	}{
		// ---- 既有 4xx 映射(不得改变)----
		{"校验类", model.NewError(model.KindValidationError, "WORKFLOW_INVALID", "x"), 400},
		{"资源缺失", model.NewError(model.KindValidationError, "NOT_FOUND", "x"), 404},
		{"请求体过大", model.NewError(model.KindValidationError, "PAYLOAD_TOO_LARGE", "x"), 413},
		{"权限不足", model.NewError(model.KindPermissionError, "PERMISSION_DENIED", "x"), 403},
		{"状态不允许(恢复)", model.NewError(model.KindStateError, "INVALID_RESUME", "x"), 400},
		{"状态不允许(迁移)", model.NewError(model.KindStateError, "INVALID_TRANSITION", "x"), 400},
		{"git 失败", model.NewError(model.KindGitError, "GIT_COMMAND_FAILED", "x"), 400},
		{"已取消", model.NewError(model.KindCancelledError, "AGENT_CANCELLED", "x"), 400},
		{"普通错误(无结构)", errors.New("普通错误"), 400},
		// ---- 新增 5xx:内部类 ----
		{"持久化失败", model.NewError(model.KindPersistenceError, "DB_ERROR", "x"), 500},
		{"库错误(状态类 kind)", model.NewError(model.KindStateError, "DB_ERROR", "x"), 500},
		{"内部错误", model.NewError(model.KindWorkflowError, "INTERNAL", "x"), 500},
		{"Agent 失败", model.NewError(model.KindAgentError, "AGENT_FAILED", "x"), 502},
		{"外部进程异常", model.NewError(model.KindProcessError, "CLAUDE_PROCESS_ERROR", "x"), 502},
		{"外部调用超时", model.NewError(model.KindTimeoutError, "CLAUDE_TIMEOUT", "x"), 504},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body, raw := serveErr(t, s, c.err)
			if code != c.want {
				t.Errorf("状态码 = %d,期望 %d (%s)", code, c.want, raw)
			}
			// 错误体形状不变:{error, code, kind}
			if msg, _ := body["error"].(string); msg == "" {
				t.Errorf("错误体缺 error: %s", raw)
			}
			if cd, _ := body["code"].(string); cd == "" {
				t.Errorf("错误体缺 code: %s", raw)
			}
			if kd, _ := body["kind"].(string); kd == "" {
				t.Errorf("错误体缺 kind: %s", raw)
			}
		})
	}
}

// TestInternalErrorIsNotReportedAsBadRequest:内部错误必须能被监控区分。
//
// 这是 P1-2 的核心断言:DB 失败不能报 400(否则监控的 4xx 告警会被
// "后端炸了"这类噪声淹没,用户也分不清是自己填错还是服务出问题)。
func TestInternalErrorIsNotReportedAsBadRequest(t *testing.T) {
	s := newTestServer(t)
	code, _, raw := serveErr(t, s, model.NewError(model.KindPersistenceError, "DB_ERROR", "sql: database is locked"))
	if code < 500 {
		t.Errorf("持久化失败应报 5xx,实际 %d (%s)", code, raw)
	}
}

// TestWrapRecoversPanic:handler panic → 500 + 标准错误体,不外泄原文。
//
// **不外泄**包含两层:①panic 原文(git/Go 内部信息)不进响应体;
// ②Go 的包路径(桌面模式 Wails 的 CallError 形如
// "agentworkflow/app/application.WorkflowService.Get: panic: …")不进响应体。
func TestWrapRecoversPanic(t *testing.T) {
	s := newTestServer(t)

	h := s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		panic("runtime error: index out of range [3] with length 2")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应报 500,实际 %d (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("错误体不是合法 JSON: %v (%s)", err, rec.Body.String())
	}
	if body["code"] != "INTERNAL" {
		t.Errorf("code = %v,期望 INTERNAL (%s)", body["code"], rec.Body.String())
	}
	if body["kind"] != string(model.KindWorkflowError) {
		t.Errorf("kind = %v,期望 %s (%s)", body["kind"], model.KindWorkflowError, rec.Body.String())
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("错误体缺 error: %s", rec.Body.String())
	}
	// panic 原文不得出站
	if strings.Contains(rec.Body.String(), "index out of range") {
		t.Errorf("panic 原文泄漏进响应体: %s", rec.Body.String())
	}
}

// TestWrapPanicAfterResponseCommitted:响应已提交后 panic → 不得再写错误体。
//
// 否则客户端拿到的是"半截正文 + 500 错误体"的坏 JSON。
func TestWrapPanicAfterResponseCommitted(t *testing.T) {
	s := newTestServer(t)

	h := s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte(`{"partial":true}`))
		panic("boom")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if strings.Contains(rec.Body.String(), "服务内部错误") {
		t.Errorf("响应已提交后不应再写错误体: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `{"partial":true}`) {
		t.Errorf("已提交的正文本不该被破坏: %s", rec.Body.String())
	}
}

// TestBadRequestDoesNotLeakGoInternals:错误文本不得夹带 Go 内部细节。
//
// 三个曾泄漏的调用点(迭代 30,P2-4):
//   - PUT /api/agents/{id}/config:json.Unmarshal 的原文
//     ("json: cannot unmarshal string into Go struct field
//     AgentConfig.extra_args of type []string");
//   - POST /api/agents/{id}/permission:permission 包的
//     ("…的值不是布尔(实际 %T)");
//   - POST /api/workflows:读请求体失败时的 Go 错误。
func TestBadRequestDoesNotLeakGoInternals(t *testing.T) {
	s := newTestServer(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"Agent 配置字段类型不对", http.MethodPut, "/api/agents/mock-claude/config",
			`{"env":{"A":"1"},"extra_args":"not-an-array"}`},
		{"权限字段不是布尔", http.MethodPost, "/api/agents/mock-claude/permission",
			`{"filesystem_read":"yes"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body, raw := doReq(t, s, c.method, c.path, c.body)
			if code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d,期望 400 (%s)", code, raw)
			}
			msg, _ := body["error"].(string)
			for _, leak := range []string{
				"Go struct field", "Go value of type", "[]string", "AgentConfig",
				"(实际 ", "%T",
			} {
				if strings.Contains(msg, leak) {
					t.Errorf("错误文本泄漏内部细节 %q: %s", leak, msg)
				}
			}
			if msg == "" {
				t.Errorf("错误体缺 error: %s", raw)
			}
		})
	}
}

// TestReadBodyFailureIsHumanMessage:读请求体失败时给的是人话。
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestReadBodyFailureIsHumanMessage(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/workflows", failingReader{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d,期望 400 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	msg, _ := body["error"].(string)
	if strings.Contains(msg, "boom") {
		t.Errorf("错误文本泄漏底层错误: %s", msg)
	}
	if !strings.Contains(msg, "读取请求体失败") {
		t.Errorf("错误文本应是人话: %s", msg)
	}
}

// TestErrorBodyMasksCredentials:错误文本里的凭据不得出站。
//
// 错误消息常夹带命令输出/子进程 stderr,里面可能有凭据
// (约定 8:API 返回掩码)。
func TestErrorBodyMasksCredentials(t *testing.T) {
	s := newTestServer(t)
	const secret = "sk-ant-supersecret1234567890abcdef"
	code, body, raw := serveErr(t, s, model.NewError(model.KindGitError, "GIT_COMMAND_FAILED",
		"git push 失败: ANTHROPIC_API_KEY="+secret))
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d,期望 400 (%s)", code, raw)
	}
	msg, _ := body["error"].(string)
	if strings.Contains(msg, secret) {
		t.Errorf("错误文本含凭据明文: %s", msg)
	}
	if !strings.Contains(msg, "ANTHROPIC_API_KEY=***") {
		t.Errorf("错误文本应保留键名并遮住值: %s", msg)
	}
}

// TestUnknownRouteErrorIsUserFacing:路由级 404 的文案面向用户。
//
// 此前是"接口不存在" —— 写给开发者看的,普通用户看到它不知道该做什么。
func TestUnknownRouteErrorIsUserFacing(t *testing.T) {
	s := newTestServer(t)
	code, body, raw := doReq(t, s, http.MethodGet, "/api/no-such-endpoint", "")
	if code != http.StatusNotFound {
		t.Fatalf("状态码 = %d,期望 404 (%s)", code, raw)
	}
	msg, _ := body["error"].(string)
	if msg == "接口不存在" {
		t.Errorf("404 文案仍是开发者口径: %s", msg)
	}
	if !strings.Contains(msg, "不存在") {
		t.Errorf("404 文案应说明资源不存在: %s", msg)
	}
	if body["code"] != "NOT_FOUND" {
		t.Errorf("code = %v,期望 NOT_FOUND (%s)", body["code"], raw)
	}
}

// io.Reader 的编译期检查(failingReader 必须真的实现 io.Reader)。
var _ io.Reader = failingReader{}
