package api

// 服务器模式的安全边界:Host / Origin / Content-Type / token。
// 这些用例走完整 Handler 中间件,不直接测私有函数。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	app "agentworkflow/app/application"
)

func newSecurityServer(t *testing.T, opts Options) *Server {
	t.Helper()
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	return NewServerWithOptions(a, opts)
}

func securityReq(t *testing.T, s *Server, method, path, body string,
	host string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestLoopbackSecurityBoundary(t *testing.T) {
	s := newSecurityServer(t, Options{Loopback: true, Addr: "127.0.0.1:18080"})

	// 受信 Host + 同源 Origin:放行
	code, _ := securityReq(t, s, http.MethodGet, "/api/health", "",
		"127.0.0.1:18080", map[string]string{"Origin": "http://127.0.0.1:18080"})
	if code != http.StatusOK {
		t.Fatalf("同源健康检查 = %d, want 200", code)
	}
	// 错误 Host:拒绝
	code, body := securityReq(t, s, http.MethodGet, "/api/health", "",
		"evil.example:18080", nil)
	if code != http.StatusForbidden || body["code"] != "FORBIDDEN_ORIGIN" {
		t.Fatalf("错误 Host = %d/%v, want 403 FORBIDDEN_ORIGIN", code, body["code"])
	}
	// 跨源 Origin:拒绝
	code, body = securityReq(t, s, http.MethodGet, "/api/health", "",
		"127.0.0.1:18080", map[string]string{"Origin": "https://evil.example"})
	if code != http.StatusForbidden || body["code"] != "FORBIDDEN_ORIGIN" {
		t.Fatalf("跨源 = %d/%v, want 403 FORBIDDEN_ORIGIN", code, body["code"])
	}
	// 写请求错误媒体类型:拒绝
	code, body = securityReq(t, s, http.MethodPost, "/api/workflows/ghost/run", `{}`,
		"127.0.0.1:18080", map[string]string{"Content-Type": "text/plain"})
	if code != http.StatusUnsupportedMediaType || body["code"] != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatalf("错误媒体类型 = %d/%v, want 415", code, body["code"])
	}
	// 正确媒体类型:进入业务层(ghost → 404,证明中间件放行)
	code, body = securityReq(t, s, http.MethodPost, "/api/workflows/ghost/run", `{}`,
		"127.0.0.1:18080", map[string]string{"Content-Type": "application/json"})
	if code != http.StatusNotFound || body["code"] != "NOT_FOUND" {
		t.Fatalf("正确媒体类型 = %d/%v, want 404 NOT_FOUND", code, body["code"])
	}
}

func TestRemoteTokenBoundary(t *testing.T) {
	s := newSecurityServer(t, Options{
		AuthToken: "secret-token",
		Addr:      "0.0.0.0:18080",
	})
	host := "192.168.1.5:18080"

	// 健康检查豁免 token
	code, _ := securityReq(t, s, http.MethodGet, "/api/health", "", host, nil)
	if code != http.StatusOK {
		t.Fatalf("远程健康检查 = %d, want 200", code)
	}
	// 无 token:401
	code, body := securityReq(t, s, http.MethodGet, "/api/workflows", "", host, nil)
	if code != http.StatusUnauthorized || body["code"] != "UNAUTHORIZED" {
		t.Fatalf("无 token = %d/%v, want 401", code, body["code"])
	}
	// 错误 token:401
	code, _ = securityReq(t, s, http.MethodGet, "/api/workflows", "", host,
		map[string]string{"Authorization": "Bearer wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("错误 token = %d, want 401", code)
	}
	// 正确 token:200
	code, _ = securityReq(t, s, http.MethodGet, "/api/workflows", "", host,
		map[string]string{"Authorization": "Bearer secret-token"})
	if code != http.StatusOK {
		t.Fatalf("正确 token = %d, want 200", code)
	}
}
