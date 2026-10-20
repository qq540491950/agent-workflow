package application

import (
	"reflect"
	"strings"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/agent/claude"
	"agentworkflow/agent/pi"
)

// 本文件锁住"default_working_dir 是死设置"这一事实与其处理方式。
//
// 背景:UI(Agents 页)有一个"默认工作目录(可选)"输入框,值存进
// agent_configs,并提示"已保存";但运行期**从不读取**它 ——
// claude.Config / pi.Config 都没有该字段,applyAgentConfig 也不传,
// 节点级 working_dir 为空时回退到全局 Git 工作目录
// (workflow/runtime/executor.go)。
//
// 后果:用户填了目录 → 保存成功 → Agent 仍在全局 Git 目录下读写文件。
// 设置完全无效,却显示"已保存"(约定 14:用户以为生效了实际没有)。
//
// 现在的处理:
//   - 后端**明确拒绝**新值(而不是继续静默接受);
//   - 存量值原样回传放行(存量配置不能因此存不下去)、清空也放行;
//   - 适配器配置里不得出现 WorkingDir 字段(用反射锁住:该设置没有落点)。

// TestUpdateConfigRejectsNewDefaultWorkingDir 新值必须被明确拒绝。
func TestUpdateConfigRejectsNewDefaultWorkingDir(t *testing.T) {
	a := newTestApp(t)
	err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{
		DefaultWorkingDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("default_working_dir 是死设置,新值必须被拒绝(不得提示已保存)")
	}
	// 错误消息要说清楚替代做法
	if !strings.Contains(err.Error(), "working_dir") {
		t.Errorf("错误消息应指向节点级 working_dir,实际: %q", err.Error())
	}
	// 不得落库
	cfg, gerr := a.AgentSvc.GetConfig("claude-code")
	if gerr != nil {
		t.Fatalf("GetConfig: %v", gerr)
	}
	if cfg.DefaultWorkingDir != "" {
		t.Errorf("被拒绝的值不得落库: %q", cfg.DefaultWorkingDir)
	}
}

// TestUpdateConfigAcceptsLegacyDefaultWorkingDirRoundTrip 存量值原样回传必须放行
// (存量配置不能因为新校验而存不下去)。
func TestUpdateConfigAcceptsLegacyDefaultWorkingDirRoundTrip(t *testing.T) {
	a := newTestApp(t)
	// 模拟存量库:直接写库(绕过新校验)
	if err := a.Repo.SaveAgentConfig("claude-code",
		[]byte(`{"model":"m1","default_working_dir":"/legacy/dir"}`)); err != nil {
		t.Fatalf("写入存量配置: %v", err)
	}
	got, err := a.AgentSvc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.DefaultWorkingDir != "/legacy/dir" {
		t.Fatalf("存量值应原样读回,实际 = %q", got.DefaultWorkingDir)
	}
	// 前端 GET → 原样回传 → 必须保存成功
	if err := a.AgentSvc.UpdateConfig("claude-code", got); err != nil {
		t.Fatalf("存量配置原样回传不得失败: %v", err)
	}
}

// TestUpdateConfigAcceptsClearingDefaultWorkingDir 清空死设置必须放行。
func TestUpdateConfigAcceptsClearingDefaultWorkingDir(t *testing.T) {
	a := newTestApp(t)
	if err := a.Repo.SaveAgentConfig("claude-code",
		[]byte(`{"model":"m1","default_working_dir":"/legacy/dir"}`)); err != nil {
		t.Fatalf("写入存量配置: %v", err)
	}
	// 清空(用户把输入框里的死值删掉)
	if err := a.AgentSvc.UpdateConfig("claude-code", agent.AgentConfig{Model: "m1"}); err != nil {
		t.Fatalf("清空死设置不得失败: %v", err)
	}
	cfg, err := a.AgentSvc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.DefaultWorkingDir != "" {
		t.Errorf("清空后不应残留死设置: %q", cfg.DefaultWorkingDir)
	}
}

// TestAdaptersHaveNoWorkingDirLandingPoint 适配器配置里不得有 WorkingDir 字段。
//
// 这是"该设置没有落点"的结构性保证:一旦有人给 claude.Config / pi.Config
// 加了 WorkingDir 字段却没接上运行期回退链,就会又造出一个"存下了却
// 没生效"的设置(反射断言在此刻失败,而不是等到用户踩坑)。
func TestAdaptersHaveNoWorkingDirLandingPoint(t *testing.T) {
	if _, ok := reflect.TypeOf(claude.Config{}).FieldByName("WorkingDir"); ok {
		t.Error("claude.Config 不应有 WorkingDir 字段(default_working_dir 没有落点)")
	}
	if _, ok := reflect.TypeOf(pi.Config{}).FieldByName("WorkingDir"); ok {
		t.Error("pi.Config 不应有 WorkingDir 字段(default_working_dir 没有落点)")
	}
}
