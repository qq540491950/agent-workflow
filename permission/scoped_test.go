package permission

import (
	"strings"
	"testing"
)

// 本文件锁住"权限判定单一来源"的核心语义(迭代 12):
//
//	有效权限 = 全局策略(实时) ∧ 工作流 permissions 段(只收紧)
//
// 三个必须成立的性质:
//
//  1. 全局策略被**实时**读取 → 收紧立即对在跑的执行生效
//     (此前是执行启动时的快照,运行中改权限对在跑的执行无效);
//  2. permissions 段**只能收紧** → 覆盖段永远无法放宽全局禁用
//     (此前是"替换"语义,能放宽,与适配器的全局判定结论相反);
//  3. 放宽尝试在**校验期**就能报出来(ValidateTightenOnly)。

// TestScopedViewFollowsParentLive 全局策略一改,作用域视图立刻看到。
func TestScopedViewFollowsParentLive(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true, FilesystemWrite: true})
	scoped := global.Clone()

	if err := scoped.Check("mock-a", FSWrite); err != nil {
		t.Fatalf("前置条件:应允许写: %v", err)
	}
	// 运行中收紧全局权限
	global.Set("mock-a", Policy{FilesystemRead: true})
	if err := scoped.Check("mock-a", FSWrite); err == nil {
		t.Error("全局收紧后,在跑的执行必须立即受限(不得停留在启动时的快照)")
	}
	if err := scoped.Check("mock-a", FSRead); err != nil {
		t.Errorf("未被收紧的能力应保持可用: %v", err)
	}
}

// TestScopedViewOverrideCannotLoosen permissions 段不能放宽全局禁用。
func TestScopedViewOverrideCannotLoosen(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true}) // 全局禁写
	scoped := global.Clone()
	// DSL 想放开写权限
	if err := scoped.ApplyChecked(map[string]any{
		"mock-a": map[string]any{"filesystem": map[string]any{"write": true}},
	}); err != nil {
		t.Fatalf("合法结构不应报错: %v", err)
	}
	if err := scoped.Check("mock-a", FSWrite); err == nil {
		t.Error("permissions 段不得放宽全局已禁用的能力")
	}
	// 有效策略恒为全局策略的子集
	if got := scoped.Get("mock-a"); got.FilesystemWrite {
		t.Error("有效策略不得比全局更宽松")
	}
}

// TestScopedViewOverrideTightens 收紧必须生效。
func TestScopedViewOverrideTightens(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true, FilesystemWrite: true})
	scoped := global.Clone()
	if err := scoped.ApplyChecked(map[string]any{
		"mock-a": map[string]any{"filesystem": map[string]any{"write": false}},
	}); err != nil {
		t.Fatalf("合法结构不应报错: %v", err)
	}
	if err := scoped.Check("mock-a", FSWrite); err == nil {
		t.Error("permissions 段的收紧必须生效")
	}
	if err := scoped.Check("mock-a", FSRead); err != nil {
		t.Errorf("未涉及的能力应保持可用: %v", err)
	}
}

// TestScopedViewDenyMessageExplainsLoosening 拒绝原因必须说清是
// "permissions 段想放宽全局禁用",而不是笼统的"无权执行"。
func TestScopedViewDenyMessageExplainsLoosening(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true}) // 全局禁写
	scoped := global.Clone()
	_ = scoped.ApplyChecked(map[string]any{
		"mock-a": map[string]any{"filesystem": map[string]any{"write": true}},
	})
	err := scoped.Check("mock-a", FSWrite)
	if err == nil {
		t.Fatal("应被拒绝")
	}
	msg := err.Error()
	if !strings.Contains(msg, "permissions") {
		t.Errorf("拒绝原因应指出 permissions 段,实际: %q", msg)
	}
	if !strings.Contains(msg, "fs:write") {
		t.Errorf("拒绝原因应包含动作,实际: %q", msg)
	}
}

// TestValidateTightenOnlyDetectsLoosening 校验期必须报出"想放宽"。
func TestValidateTightenOnlyDetectsLoosening(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true}) // 全局禁写
	err := global.ValidateTightenOnly(map[string]any{
		"mock-a": map[string]any{"filesystem": map[string]any{"write": true}},
	})
	if err == nil {
		t.Fatal("放宽全局禁用的 permissions 段必须在校验期报出来")
	}
	if !strings.Contains(err.Error(), "mock-a") || !strings.Contains(err.Error(), "fs:write") {
		t.Errorf("错误消息应指明 Agent 与能力,实际: %q", err.Error())
	}
}

// TestValidateTightenOnlyAllowsTightening 收紧/等值写法不得被误报。
func TestValidateTightenOnlyAllowsTightening(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true, FilesystemWrite: true})
	for _, overrides := range []map[string]any{
		{"mock-a": map[string]any{"filesystem": map[string]any{"write": false}}},
		{"mock-a": map[string]any{"filesystem": map[string]any{"read": true}}},
		{"mock-a": map[string]any{"filesystem": map[string]any{"read": false}}},
		{},
		nil,
	} {
		if err := global.ValidateTightenOnly(overrides); err != nil {
			t.Errorf("收紧写法不应被误报: %v", err)
		}
	}
}

// TestGlobalApplyCheckedCannotLoosen 全局管理器上的覆盖同样只能收紧
// (同一份语义,不分场景)。
func TestGlobalApplyCheckedCannotLoosen(t *testing.T) {
	m := NewManager()
	// pi-agent 默认可写;用覆盖段试图"再放开"一个已被全局禁用的能力
	m.Set("pi-agent", Policy{FilesystemRead: true, FilesystemWrite: true})
	if err := m.ApplyChecked(map[string]any{
		"pi-agent": map[string]any{"filesystem": map[string]any{"write": true}},
	}); err != nil {
		t.Fatalf("合法结构不应报错: %v", err)
	}
	if err := m.Check("pi-agent", FSWrite); err != nil {
		t.Errorf("未被收紧的能力应保持可用: %v", err)
	}
	// 收紧仍然生效
	if err := m.ApplyChecked(map[string]any{
		"pi-agent": map[string]any{"filesystem": map[string]any{"write": false}},
	}); err != nil {
		t.Fatalf("合法结构不应报错: %v", err)
	}
	if err := m.Check("pi-agent", FSWrite); err == nil {
		t.Error("收紧必须生效")
	}
}

// TestScopedViewSnapshotIsEffective Snapshot 返回的是有效策略。
func TestScopedViewSnapshotIsEffective(t *testing.T) {
	global := NewManager()
	global.Set("mock-a", Policy{FilesystemRead: true, FilesystemWrite: true})
	scoped := global.Clone()
	_ = scoped.ApplyChecked(map[string]any{
		"mock-a": map[string]any{"filesystem": map[string]any{"write": false}},
	})
	snap := scoped.Snapshot()
	if snap["mock-a"].FilesystemWrite {
		t.Error("Snapshot 必须返回有效策略(收紧已生效)")
	}
	if !snap["mock-a"].FilesystemRead {
		t.Error("未被收紧的能力应保持可用")
	}
}
