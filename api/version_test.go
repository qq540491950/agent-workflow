package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// 版本信息:app/version 恒在;adk/wails 从构建信息读取。
// 注意:go test 的测试二进制不记录依赖版本(bi.Deps 为空,Go 1.25 验证),
// 因此依赖键只在真实构建产物中存在(由 scripts/smoke.sh 的 /api/health 覆盖);
// 此处仅对恒定字段与副本语义断言。
func TestVersionInfo(t *testing.T) {
	info := VersionInfo()
	if info["app"] == "" || info["version"] == "" {
		t.Fatalf("missing app/version: %v", info)
	}
	if v, ok := info["adk"]; ok && !strings.Contains(v, "google.golang.org/adk") {
		t.Errorf("adk = %q, want module path + version", v)
	}

	// 返回副本:调用方修改不得污染缓存
	info["app"] = "tampered"
	if VersionInfo()["app"] == "tampered" {
		t.Error("VersionInfo must return a copy")
	}
}

func TestVersionInfoJSONShape(t *testing.T) {
	raw, err := json.Marshal(VersionInfo())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON string map: %v", err)
	}
}
