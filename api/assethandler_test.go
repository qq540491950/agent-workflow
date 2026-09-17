package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 回归:桌面模式下 AssetHandler 挂载在 Wails AssetServer 的 /api 前缀,
// Wails 命中前缀时会剥掉它(源码 assetserver.go:121 TrimPrefix),
// AssetHandler 必须补回前缀,否则 /api/health 等路由 404、
// 导出/备份下载链接落入 SPA fallback 下载到 HTML。
func TestAssetHandlerReprefixesStrippedPath(t *testing.T) {
	s := NewServer(nil) // /api/health 不依赖 app
	h := &AssetHandler{Srv: s}

	// 模拟 Wails:请求 /api/health,剥掉 /api 后交给 AssetHandler
	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	r.URL.Path = "/health"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("stripped path /health → %d, want 200", w.Code)
	}
	if body := w.Body.String(); len(body) == 0 || body[0] != '{' {
		t.Errorf("body = %q, want JSON", body)
	}

	// 已带 /api 前缀的路径幂等
	r2 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("full path /api/health → %d, want 200", w2.Code)
	}

	// 未注册的路由仍是 404(而非落入其他处理器)
	r3 := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	r3.URL.Path = "/nope"
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("unknown path → %d, want 404", w3.Code)
	}
}
