package application

import (
	"errors"
	"testing"

	"agentworkflow/agent"
)

// failingReplaceRegistry 让指定 Agent 的 Replace 失败(模拟"运行时应用
// 配置失败"这条路径)。
//
// 用它而不是改生产代码加开关:agent.Registry 是接口,替换注册表就能
// 注入失败,不需要在 App 上留测试专用字段。
type failingReplaceRegistry struct {
	agent.Registry
	failID string
}

func (r *failingReplaceRegistry) Replace(a agent.Agent) error {
	if a != nil && a.ID() == r.failID {
		return errors.New("模拟:替换适配器实例失败")
	}
	return r.Registry.Replace(a)
}

// 回归(迭代 14B):备份恢复时**运行时应用失败**,不得计入"已恢复"。
//
// 此前 `_ = s.app.applyAgentConfig(id, cfg)` 把错误吞掉,照样
// restoredCfg++ —— 假成功:用户看到"已恢复 1 个 Agent 配置",以为凭据
// 回来了,实际运行时的适配器实例根本没换(这一路调用仍走旧凭据/旧端点)。
// 同一函数上一段刚为"未知 Agent"修过同类假成功,这条路径漏网。
func TestRestoreBackupDoesNotCountFailedApplyAsRestored(t *testing.T) {
	a := newTestApp(t)
	// 让 claude-code 的适配器替换失败
	a.Agents = &failingReplaceRegistry{Registry: a.Agents, failID: "claude-code"}

	backup := map[string]any{
		"version":   1,
		"workflows": []any{},
		"agent_configs": map[string]any{
			"claude-code": map[string]any{"model": "m", "env": map[string]any{"TOK": "sk"}},
		},
	}
	res, err := a.Settings.RestoreBackup(backup)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n, _ := res["agent_configs_restored"].(int); n != 0 {
		t.Errorf("agent_configs_restored = %v,期望 0(运行时应用失败不得算已恢复)", res["agent_configs_restored"])
	}
	ids, _ := res["agent_configs_apply_failed_ids"].([]string)
	if len(ids) != 1 || ids[0] != "claude-code" {
		t.Errorf("应列出应用失败的 Agent ID,实际: %v", res["agent_configs_apply_failed_ids"])
	}
	// 配置本身合法且已落库(下次启动 reloadAgents 会重新应用)——
	// 有意**不回滚**:回滚会丢掉用户刚恢复出来的合法配置。
	raw, err := a.Repo.GetAgentConfig("claude-code")
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if raw == nil {
		t.Error("应用失败不应回滚已落库的合法配置(下次启动会重试应用)")
	}
}

// 回归(迭代 14B):对照组 —— 应用成功时仍然计入"已恢复",且不出现在
// 失败列表里(防止把计数口径改坏)。
func TestRestoreBackupCountsSuccessfulApply(t *testing.T) {
	a := newTestApp(t)

	backup := map[string]any{
		"version":   1,
		"workflows": []any{},
		"agent_configs": map[string]any{
			"claude-code": map[string]any{"model": "m", "env": map[string]any{"TOK": "sk"}},
		},
	}
	res, err := a.Settings.RestoreBackup(backup)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n, _ := res["agent_configs_restored"].(int); n != 1 {
		t.Errorf("agent_configs_restored = %v,期望 1", res["agent_configs_restored"])
	}
	ids, _ := res["agent_configs_apply_failed_ids"].([]string)
	if len(ids) != 0 {
		t.Errorf("应用成功时失败列表应为空,实际: %v", ids)
	}
}
