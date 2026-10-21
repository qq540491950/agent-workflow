package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 本文件锁住"POST /api/agents/{id}/permission 是**局部更新**"这一契约。
//
// 缺口(迭代 20 / P2-2):服务端此前用 policyOf 把请求体解析成一份**完整**
// 策略 —— 缺键一律当 false,再整表覆盖:
//
//	{"filesystem_read": true}  →  filesystem_write / git_read /
//	                              git_commit / git_push 全被置 false
//
// 能力被**静默吊销**,客户端与用户都收不到任何提示(约定 14 的同类:
// 不该动的却动了)。同仓 DSL permissions 段早就用掩码区分了
// "没写"与"写了 false"(permission.policyMask),同一个概念两套语义。
//
// 现在:只有**显式给出**的能力位被改写,缺省的保持原值。
// 解析口径复用 permission.ParsePolicyPatch(与 DSL 段同源,约定 13)。

// agentPermissions 读回某 Agent 的当前生效权限。
func agentPermissions(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	_, _, raw := doReq(t, s, http.MethodGet, "/api/agents", "")
	var agents []map[string]any
	if err := json.Unmarshal(raw, &agents); err != nil {
		t.Fatalf("agents 非数组: %v (%s)", err, raw)
	}
	for _, a := range agents {
		if a["id"] == id {
			perms, _ := a["permissions"].(map[string]any)
			if perms == nil {
				t.Fatalf("agent %s 的 permissions 缺失: %s", id, raw)
			}
			return perms
		}
	}
	t.Fatalf("agents 列表未含 %s: %s", id, raw)
	return nil
}

// TestAgentPermissionPartialUpdateKeepsOthers 部分载荷只改被点名的能力位,
// 其余能力位保持原值(不得被静默吊销)。
func TestAgentPermissionPartialUpdateKeepsOthers(t *testing.T) {
	s := newTestServer(t)
	const id = "mock-claude"

	// 先显式开两项(mock-claude 的内置默认只有 fs:read + git:read)
	code, body, raw := doReq(t, s, http.MethodPost, "/api/agents/"+id+"/permission",
		`{"filesystem_write":true,"git_commit":true}`)
	if code != http.StatusOK {
		t.Fatalf("设置权限 → %d (%s)", code, raw)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("ok = %v, want true (%s)", body["ok"], raw)
	}

	// 只发一项:git_push。其余四项必须保持原值。
	code, _, raw = doReq(t, s, http.MethodPost, "/api/agents/"+id+"/permission",
		`{"git_push":true}`)
	if code != http.StatusOK {
		t.Fatalf("部分权限更新 → %d (%s)", code, raw)
	}
	perms := agentPermissions(t, s, id)
	want := map[string]bool{
		"filesystem_read":  true, // 内置默认,没被碰过
		"filesystem_write": true, // 上一轮显式打开
		"git_read":         true, // 内置默认,没被碰过
		"git_commit":       true, // 上一轮显式打开
		"git_push":         true, // 本次显式打开
	}
	for k, w := range want {
		if perms[k] != w {
			t.Errorf("权限 %s = %v, want %v(部分载荷不得吊销未点名的能力): %v",
				k, perms[k], w, perms)
		}
	}

	// 再显式关掉一项:其余保持。
	code, _, raw = doReq(t, s, http.MethodPost, "/api/agents/"+id+"/permission",
		`{"git_push":false}`)
	if code != http.StatusOK {
		t.Fatalf("关闭单项 → %d (%s)", code, raw)
	}
	perms = agentPermissions(t, s, id)
	if perms["git_push"] != false {
		t.Errorf("git_push = %v, want false", perms["git_push"])
	}
	if perms["filesystem_write"] != true || perms["git_commit"] != true {
		t.Errorf("未点名的能力被吊销: %v", perms)
	}
}

// TestAgentPermissionExplicitFalseIsHonored 显式写 false 仍然是"关闭"
// (与"没写"区分开:这是三态语义的核心)。
func TestAgentPermissionExplicitFalseIsHonored(t *testing.T) {
	s := newTestServer(t)
	const id = "mock-claude"

	code, _, raw := doReq(t, s, http.MethodPost, "/api/agents/"+id+"/permission",
		`{"filesystem_read":false}`)
	if code != http.StatusOK {
		t.Fatalf("关闭 fs:read → %d (%s)", code, raw)
	}
	perms := agentPermissions(t, s, id)
	if perms["filesystem_read"] != false {
		t.Errorf("显式 false 未生效: %v", perms)
	}
	// 其余能力位保持(mock-claude 内置默认 git:read = true)
	if perms["git_read"] != true {
		t.Errorf("未点名的能力被吊销: %v", perms)
	}
}

// TestAgentPermissionNonBoolRejected 非布尔值必须报错,不能静默当 false
// (静默当 false = 静默吊销能力,约定 14)。
func TestAgentPermissionNonBoolRejected(t *testing.T) {
	s := newTestServer(t)
	code, body, raw := doReq(t, s, http.MethodPost, "/api/agents/mock-claude/permission",
		`{"filesystem_read":"yes"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("非布尔权限值 → %d, want 400 (%s)", code, raw)
	}
	assertErrorBody(t, "非布尔权限值", body, raw, "BAD_REQUEST")
}

// TestAgentPermissionFullMaskStillReplaces 整表载荷(前端 Agents 页发的是
// 全量 5 键)的行为不变:未点名的能力位按载荷取 false。
func TestAgentPermissionFullMaskStillReplaces(t *testing.T) {
	s := newTestServer(t)
	const id = "mock-claude"

	code, _, raw := doReq(t, s, http.MethodPost, "/api/agents/"+id+"/permission",
		`{"filesystem_read":true,"filesystem_write":false,"git_read":true,`+
			`"git_commit":false,"git_push":false}`)
	if code != http.StatusOK {
		t.Fatalf("整表权限 → %d (%s)", code, raw)
	}
	perms := agentPermissions(t, s, id)
	if perms["filesystem_read"] != true || perms["git_read"] != true {
		t.Errorf("整表载荷未生效: %v", perms)
	}
	if perms["filesystem_write"] != false || perms["git_commit"] != false ||
		perms["git_push"] != false {
		t.Errorf("整表载荷里的 false 未生效: %v", perms)
	}
}
