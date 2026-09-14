package permission

import (
	"testing"

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
