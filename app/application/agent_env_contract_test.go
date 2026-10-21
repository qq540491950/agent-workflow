package application

import (
	"encoding/json"
	"testing"

	"agentworkflow/agent"
)

// 本文件锁住 Agent 配置 env 字段的**第四态**契约。
//
// 约定 5 只定义了 env 表**内部**的三态(逐键):"***" 保留原值、
// 空串删除该键、无原值的 "***" 丢弃。整张表本身的"给不给"此前
// 没有定义 —— 而"没给"被当成了"清空全部"。
//
// 症状(迭代 20 / P1-2):一次只想改模型的 PUT(不带 env)会把库里
// ANTHROPIC_AUTH_TOKEN 等**全部凭据删掉**,而用户只看到"已保存";
// 下一次调用 Agent 就没有鉴权了(约定 14:不该动的却动了)。
//
// 现在:
//   - env 缺省(nil)= **不修改**(保留已存值);
//   - env = {}       = **显式清空全部**;
//   - env = {k:v}    = **整表替换**(逐键按约定 5 合并)。
//
// 前后端必须同步(约定 10):前端 Agents 页在 GET 失败时不得回传空表。

// storedEnv 读回库里落盘的 env(未掩码:库里存的是真值)。
func storedEnv(t *testing.T, a *App, id string) map[string]string {
	t.Helper()
	raw, err := a.Repo.GetAgentConfig(id)
	if err != nil {
		t.Fatalf("读取配置: %v", err)
	}
	cfg := agent.AgentConfig{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return cfg.Env
}

// TestUpdateConfigEnvOmittedKeepsStored env 缺省 = 不修改:
// 只想改模型的请求不得顺手删掉已存凭据。
func TestUpdateConfigEnvOmittedKeavesStored(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-live-123", "KEEP": "v1"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}

	// 不带 env(Env 为 nil = 缺省)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{Model: "m2"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	env := storedEnv(t, a, "claude-code")
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-live-123" {
		t.Errorf("env 缺省不得清空已存凭据: %v", env)
	}
	if env["KEEP"] != "v1" {
		t.Errorf("env 缺省不得改动其它键: %v", env)
	}
	// 其它字段照常生效(缺省只针对 env)
	cfg, err := a.AgentSvc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.Model != "m2" {
		t.Errorf("model = %q, want m2", cfg.Model)
	}
}

// TestUpdateConfigEnvEmptyObjectClears env = {} = 显式清空全部:
// "用户把环境变量全删了"必须真的删得掉。
func TestUpdateConfigEnvEmptyObjectClears(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-live-123"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}

	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Env: map[string]string{},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	if env := storedEnv(t, a, "claude-code"); len(env) != 0 {
		t.Errorf("env = {} 必须清空全部: %v", env)
	}
}

// TestUpdateConfigEnvReplacesTable env = {k:v} = 整表替换:
// 逐键按约定 5 合并,表里没有的键被删除(前端是整表回传)。
func TestUpdateConfigEnvReplacesTable(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Env: map[string]string{"A": "a1", "B": "b1"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}

	// A 保留(***),C 新增,B 不再出现 → 整表替换
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Env: map[string]string{"A": agent.MaskedValue, "C": "c1"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	env := storedEnv(t, a, "claude-code")
	if env["A"] != "a1" {
		t.Errorf("*** 未保留原值: %v", env)
	}
	if env["C"] != "c1" {
		t.Errorf("新增项丢失: %v", env)
	}
	if _, exists := env["B"]; exists {
		t.Errorf("整表替换时未出现的键应被删除: %v", env)
	}
}

// TestResetConfigClearsEnv 恢复默认必须仍能清空 env
// ("env 缺省 = 不修改"之后,清空必须走显式空表这条唯一的路)。
func TestResetConfigClearsEnv(t *testing.T) {
	a := newTestApp(t)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		Model: "m", Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-live-123"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}
	if err := a.AgentSvc.ResetConfig("claude-code"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if env := storedEnv(t, a, "claude-code"); len(env) != 0 {
		t.Errorf("恢复默认后 env 应被清空: %v", env)
	}
}
