package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 本文件锁住 PUT /api/agents/{id}/config 的 env **第四态**契约
// (迭代 20 / P1-2),在 HTTP 层验证(桌面模式走同一份应用层逻辑)。
//
//   - 请求体里**没有** env → 不修改(已存凭据不得被删掉);
//   - env = {}                  → 显式清空全部;
//   - env = {k:v}               → 整表替换。
//
// 此前"没有 env"被当成"清空全部":一个只想改模型的第三方客户端
// (PUT {"model":"x"})会把 ANTHROPIC_AUTH_TOKEN 等全部凭据删掉,
// 而响应是 200 —— 用户只看到"已保存"(约定 14:不该动的却动了)。

// agentConfigEnv 读回某 Agent 配置的 env(掩码后的形状)。
func agentConfigEnv(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	_, _, raw := doReq(t, s, http.MethodGet, "/api/agents/"+id+"/config", "")
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config 非对象: %v (%s)", err, raw)
	}
	env, _ := cfg["env"].(map[string]any)
	return env
}

// TestAgentConfigPutWithoutEnvKeepsStored PUT 不带 env 时已存凭据必须保留。
func TestAgentConfigPutWithoutEnvKeepsStored(t *testing.T) {
	s := newTestServer(t)
	const path = "/api/agents/mock-claude/config"

	code, _, raw := doReq(t, s, http.MethodPut, path, `{"env":{"TOK":"sk-live"}}`)
	if code != http.StatusOK {
		t.Fatalf("首次 PUT → %d (%s)", code, raw)
	}
	if env := agentConfigEnv(t, s, "mock-claude"); env["TOK"] != "***" {
		t.Fatalf("首次 PUT 未落库: %v", env)
	}

	// 只改模型:请求体里没有 env
	code, _, raw = doReq(t, s, http.MethodPut, path, `{"model":"m-2"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT 无 env → %d (%s)", code, raw)
	}
	env := agentConfigEnv(t, s, "mock-claude")
	if env["TOK"] != "***" {
		t.Errorf("请求体缺 env 时已存凭据必须保留: %v", env)
	}
}

// TestAgentConfigPutEmptyEnvClears env = {} 仍然清空全部
// ("用户把环境变量全删了"必须删得掉)。
func TestAgentConfigPutEmptyEnvClears(t *testing.T) {
	s := newTestServer(t)
	const path = "/api/agents/mock-claude/config"

	code, _, raw := doReq(t, s, http.MethodPut, path, `{"env":{"TOK":"sk-live"}}`)
	if code != http.StatusOK {
		t.Fatalf("首次 PUT → %d (%s)", code, raw)
	}

	code, _, raw = doReq(t, s, http.MethodPut, path, `{"env":{}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT 空 env → %d (%s)", code, raw)
	}
	if env := agentConfigEnv(t, s, "mock-claude"); len(env) != 0 {
		t.Errorf("env = {} 必须清空全部: %v", env)
	}
}
