package permission

import (
	"sync"
	"testing"
	"time"

	"agentworkflow/workflow/model"
)

func TestDefaultPolicies(t *testing.T) {
	m := NewManager()
	// Claude Code 默认只读:禁止写文件/提交/推送
	if err := m.Check("claude-code", FSRead); err != nil {
		t.Errorf("claude-code fs:read should be allowed: %v", err)
	}
	if err := m.Check("claude-code", FSWrite); err == nil {
		t.Error("claude-code fs:write should be denied")
	}
	if err := m.Check("claude-code", GitCommit); err == nil {
		t.Error("claude-code git:commit should be denied")
	}
	if err := m.Check("claude-code", GitPush); err == nil {
		t.Error("claude-code git:push should be denied")
	}
	// Pi Agent 可写、可 commit、禁止 push
	if err := m.Check("pi-agent", FSWrite); err != nil {
		t.Errorf("pi-agent fs:write should be allowed: %v", err)
	}
	if err := m.Check("pi-agent", GitCommit); err != nil {
		t.Errorf("pi-agent git:commit should be allowed: %v", err)
	}
	if err := m.Check("pi-agent", GitPush); err == nil {
		t.Error("pi-agent git:push should be denied")
	}
}

func TestUnknownAgentDenied(t *testing.T) {
	m := NewManager()
	for _, a := range []Action{FSRead, FSWrite, GitRead, GitCommit, GitPush} {
		if err := m.Check("intruder", a); err == nil {
			t.Errorf("unknown agent action %s should be denied", a)
		}
	}
}

func TestPermissionErrorIsStructured(t *testing.T) {
	m := NewManager()
	err := m.Check("claude-code", GitCommit)
	if !model.IsKind(err, model.KindPermissionError) {
		t.Fatalf("expected PermissionError, got %v", err)
	}
}

func TestWorkflowOverrides(t *testing.T) {
	m := NewManager()
	m.ApplyWorkflowOverrides(map[string]any{
		"claude-code": map[string]any{
			"filesystem": map[string]any{"read": true, "write": false},
			"git":        map[string]any{"read": true, "commit": false, "push": false},
		},
		"pi-agent": map[string]any{
			"git": map[string]any{"commit": false},
		},
	})
	if err := m.Check("pi-agent", GitCommit); err == nil {
		t.Error("pi-agent commit should be denied after override")
	}
	if err := m.Check("pi-agent", FSWrite); err != nil {
		t.Error("pi-agent fs:write should stay allowed")
	}
}

// 回归:DSL permissions 段结构非法时必须 fail closed。
// 此前非法结构(值不是映射 / 字段不是布尔 / 字段名拼错)被静默忽略 ——
// 于是"想把 pi-agent 的写权限关掉"的收紧规则被丢弃,Agent 仍按默认的
// 宽松策略执行(约定 14:该失败却成功等同 P0)。
func TestMalformedOverrideFailsClosed(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]any
		agentID   string
		action    Action
	}{
		{
			name:      "覆盖不是映射",
			overrides: map[string]any{"pi-agent": "readonly"},
			agentID:   "pi-agent",
			action:    FSWrite,
		},
		{
			name: "段不是映射",
			overrides: map[string]any{
				"pi-agent": map[string]any{"filesystem": "deny-all"},
			},
			agentID: "pi-agent",
			action:  FSWrite,
		},
		{
			name: "字段值不是布尔",
			overrides: map[string]any{
				"pi-agent": map[string]any{
					"filesystem": map[string]any{"write": "false"},
				},
			},
			agentID: "pi-agent",
			action:  FSWrite,
		},
		{
			name: "字段名拼错",
			overrides: map[string]any{
				"pi-agent": map[string]any{
					"filesystem": map[string]any{"wirte": false},
				},
			},
			agentID: "pi-agent",
			action:  FSWrite,
		},
		{
			name: "段名拼错",
			overrides: map[string]any{
				"pi-agent": map[string]any{"filesystemz": map[string]any{"write": false}},
			},
			agentID: "pi-agent",
			action:  FSWrite,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			m := NewManager()
			// 前置:pi-agent 默认可写
			if err := m.Check(c.agentID, c.action); err != nil {
				t.Fatalf("前置条件: %s 应默认允许", c.action)
			}
			m.ApplyWorkflowOverrides(c.overrides)
			if err := m.Check(c.agentID, c.action); err == nil {
				t.Errorf("非法覆盖被静默忽略, %s 仍被放行(fail open)", c.action)
			}
		})
	}
}

// ApplyChecked 必须把非法结构报出来(供调用方展示给用户)。
func TestApplyCheckedReportsError(t *testing.T) {
	m := NewManager()
	if err := m.ApplyChecked(map[string]any{
		"pi-agent": map[string]any{"filesystem": map[string]any{"write": "no"}},
	}); err == nil {
		t.Fatal("非法覆盖应返回 error")
	}
	// 合法覆盖不报错
	if err := m.ApplyChecked(map[string]any{
		"pi-agent": map[string]any{"filesystem": map[string]any{"write": false}},
	}); err != nil {
		t.Fatalf("合法覆盖不应报错: %v", err)
	}
}

// 合法覆盖仍然生效(不能因为 fail closed 把正常路径也拒了)。
func TestValidOverrideStillApplies(t *testing.T) {
	m := NewManager()
	m.ApplyWorkflowOverrides(map[string]any{
		"pi-agent": map[string]any{
			"filesystem": map[string]any{"write": false},
			"git":        map[string]any{"push": false},
		},
	})
	if err := m.Check("pi-agent", FSWrite); err == nil {
		t.Error("pi-agent 写权限应被覆盖关闭")
	}
	if err := m.Check("pi-agent", FSRead); err != nil {
		t.Errorf("未涉及的权限应保持原值: %v", err)
	}
	if err := m.Check("pi-agent", GitCommit); err != nil {
		t.Errorf("未涉及的权限应保持原值: %v", err)
	}
}

// 判定必须默认拒绝:未知动作 / 大小写不同的动作 / 大小写不同的 Agent ID
// 一律拒绝,不允许前缀或近似匹配绕过。
func TestCheckFailsClosedOnUnknownTargets(t *testing.T) {
	m := NewManager()
	cases := []struct {
		name   string
		agent  string
		action Action
	}{
		{"未知动作", "claude-code", Action("fs:write")},
		{"动作大小写不同", "claude-code", Action("FS:READ")},
		{"动作带前缀", "claude-code", Action("fs:read:all")},
		{"Agent 大小写不同", "Claude-Code", FSRead},
		{"Agent 带前缀", "x-claude-code", FSRead},
		{"空 Agent", "", FSRead},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if err := m.Check(c.agent, c.action); err == nil {
				t.Errorf("%s: agent=%q action=%q 应被拒绝", c.name, c.agent, c.action)
			}
		})
	}
}

// TestManagerConcurrentAccess 权限表的并发读写(约定 15:共享可变状态加锁)。
func TestManagerConcurrentAccess(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Check("claude-code", FSRead)
					_ = m.Snapshot()
					_ = m.Clone()
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.Set("agent-x", Policy{FilesystemRead: i%2 == 0})
			m.ApplyWorkflowOverrides(map[string]any{
				"claude-code": map[string]any{
					"filesystem": map[string]any{"read": true},
				},
			})
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
