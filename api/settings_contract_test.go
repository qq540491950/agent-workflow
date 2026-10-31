package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 本文件锁住"POST /api/settings 省略 git_working_dir = 不修改"这一契约。
//
// 缺口(迭代 10 交接):body 里的 git_working_dir 是普通的 string ——
// 客户端**省略**该字段时,Go 把它解成零值 ""。而应用层的语义是
// "空串 = 清除"(落库空串 + 回到启动默认目录)。
//
// 于是:一个只想改日志级别的 REST 客户端
//
//	POST /api/settings {"log_level":"debug"}
//
// 会顺手把用户的 git 工作目录**清空** —— 用户没要求改它,却改了,
// 而且界面上完全看不出(约定 14 的同类:该失败/该不动的却动了)。
//
// 现在:字段改为 *string —— nil(省略)= 不修改(回填当前值),
// 显式 "" = 清除(用户主动清空的语义不变)。

// TestSettingsOmittedGitWorkingDirIsNotModified 省略 git_working_dir
// 时,已存的 git 工作目录必须原样保留。
func TestSettingsOmittedGitWorkingDirIsNotModified(t *testing.T) {
	s := newTestServer(t)
	dir := t.TempDir()

	// 先设一个明确的 git 工作目录
	dirBody, _ := json.Marshal(map[string]string{"git_working_dir": dir})
	code, body, raw := doReq(t, s, http.MethodPost, "/api/settings", string(dirBody))
	if code != http.StatusOK || body["git_working_dir"] != dir {
		t.Fatalf("POST settings git_working_dir → %d (%s)", code, raw)
	}

	// 只改日志级别:git_working_dir 被省略 → 必须保持不变
	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings",
		`{"log_level":"debug"}`)
	if code != http.StatusOK {
		t.Fatalf("POST settings log_level → %d (%s)", code, raw)
	}
	if body["git_working_dir"] != dir {
		t.Errorf("省略 git_working_dir 不得改动它:得到 %v, want %v",
			body["git_working_dir"], dir)
	}

	// 回读确认:真的落库了,不是只在响应里回显
	_, back, _ := doReq(t, s, http.MethodGet, "/api/settings", "")
	if back["git_working_dir"] != dir {
		t.Errorf("省略 git_working_dir 后回读必须仍是原值:得到 %v, want %v",
			back["git_working_dir"], dir)
	}
}

// TestSettingsExplicitEmptyGitWorkingDirClears 显式传空串 = 清除
// (用户在设置页清空输入框的语义不变 —— 不能因为改了契约就清不掉)。
func TestSettingsExplicitEmptyGitWorkingDirClears(t *testing.T) {
	s := newTestServer(t)
	dir := t.TempDir()

	dirBody, _ := json.Marshal(map[string]string{"git_working_dir": dir})
	code, body, raw := doReq(t, s, http.MethodPost, "/api/settings", string(dirBody))
	if code != http.StatusOK || body["git_working_dir"] != dir {
		t.Fatalf("POST settings git_working_dir → %d (%s)", code, raw)
	}

	code, body, raw = doReq(t, s, http.MethodPost, "/api/settings",
		`{"git_working_dir":""}`)
	if code != http.StatusOK {
		t.Fatalf("POST settings 清空 git_working_dir → %d (%s)", code, raw)
	}
	if body["git_working_dir"] == dir {
		t.Errorf("显式空串必须清除 git_working_dir,但仍是 %v", body["git_working_dir"])
	}
}
