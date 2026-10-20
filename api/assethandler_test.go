package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	app "agentworkflow/app/application"
)

// 回归:桌面模式下 AssetHandler 挂载在 Wails AssetServer 的 /api 前缀,
// Wails 命中前缀时会剥掉它(源码 assetserver.go:121 TrimPrefix),
// AssetHandler 必须补回前缀,否则 /api/health 等路由 404、
// 导出/备份下载链接落入 SPA fallback 下载到 HTML。
func TestAssetHandlerReprefixesStrippedPath(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a) // /api/health 不依赖 app 状态
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

// 回归:补前缀逻辑不得让路径穿越逃出 /api 命名空间,也不得把
// "已经带前缀"的路径重复补成 /api/api(那会让全部路由 404)。
func TestAssetHandlerPrefixCompletionIsSafe(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	h := &AssetHandler{Srv: s}

	// 1) 剥掉前缀的路径能命中路由
	r := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	r.URL.Path = "/workflows"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("剥前缀 /workflows → %d,期望 200", w.Code)
	}

	// 2) 已带前缀的路径幂等(不得变成 /api/api/...)
	r2 := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Errorf("完整路径 /api/workflows → %d,期望 200", w2.Code)
	}

	// 3) 路径穿越不得命中 API 路由(命中也只能是规范化重定向/404,
	//    不能返回 API 数据)
	r3 := httptest.NewRequest(http.MethodGet, "/api/../api/workflows", nil)
	r3.URL.Path = "/../api/workflows"
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, r3)
	if w3.Code == http.StatusOK {
		t.Errorf("路径穿越命中了 API 路由: %d %s", w3.Code, w3.Body.String())
	}

	// 4) 穿越到 /api 之外(如 /etc/passwd)不得命中任何路由
	r4 := httptest.NewRequest(http.MethodGet, "/api/../../etc/passwd", nil)
	r4.URL.Path = "/../../etc/passwd"
	w4 := httptest.NewRecorder()
	h.ServeHTTP(w4, r4)
	if w4.Code == http.StatusOK {
		t.Errorf("穿越到 /api 之外命中了路由: %d %s", w4.Code, w4.Body.String())
	}
}
