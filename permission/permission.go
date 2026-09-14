// Package permission 实现统一的权限模型。
// 每个 Agent 对文件系统与 Git 的能力由 Permission Policy 决定,
// Adapter 在执行前必须咨询 Permission Manager;禁止 Agent 自行突破权限。
package permission

import (
	"fmt"
	"sync"

	"agentworkflow/workflow/model"
)

// Action 是可授权的动作。
type Action string

const (
	FSRead   Action = "fs:read"
	FSWrite  Action = "fs:write"
	GitRead  Action = "git:read"
	GitCommit Action = "git:commit"
	GitPush  Action = "git:push"
)

// Policy 是单个 Agent 的权限集合。未显式授权的动作一律拒绝。
type Policy struct {
	// FilesystemRead / FilesystemWrite 控制对工作目录的读写。
	FilesystemRead  bool `json:"filesystem_read" yaml:"filesystem_read"`
	FilesystemWrite bool `json:"filesystem_write" yaml:"filesystem_write"`
	// GitRead / GitCommit / GitPush 控制 Git 能力。
	GitRead   bool `json:"git_read" yaml:"git_read"`
	GitCommit bool `json:"git_commit" yaml:"git_commit"`
	GitPush   bool `json:"git_push" yaml:"git_push"`
}

// Allow 返回策略是否允许指定动作。
func (p Policy) Allow(a Action) bool {
	switch a {
	case FSRead:
		return p.FilesystemRead
	case FSWrite:
		return p.FilesystemWrite
	case GitRead:
		return p.GitRead
	case GitCommit:
		return p.GitCommit
	case GitPush:
		return p.GitPush
	default:
		return false
	}
}

// Manager 管理全部 Agent 的权限策略。
type Manager struct {
	mu       sync.RWMutex
	policies map[string]Policy
}

func NewManager() *Manager {
	m := &Manager{policies: map[string]Policy{}}
	// 内置默认策略:
	// Claude Code 只读,禁止修改文件/提交 —— 通过权限策略强制执行。
	m.policies["claude-code"] = Policy{
		FilesystemRead: true, FilesystemWrite: false,
		GitRead: true, GitCommit: false, GitPush: false,
	}
	// Pi Agent 负责执行/修改/修复,可写文件,可 commit(不可 push)。
	m.policies["pi-agent"] = Policy{
		FilesystemRead: true, FilesystemWrite: true,
		GitRead: true, GitCommit: true, GitPush: false,
	}
	// Mock Agent 仅读。
	m.policies["mock"] = Policy{FilesystemRead: true}
	return m
}

// Set 设置某 Agent 的策略。
func (m *Manager) Set(agentID string, p Policy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[agentID] = p
}

// Get 获取某 Agent 的策略(未知 Agent 返回零值 = 全拒绝)。
func (m *Manager) Get(agentID string) Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policies[agentID]
}

// Check 判断 Agent 是否被允许执行动作;拒绝时返回结构化错误。
func (m *Manager) Check(agentID string, a Action) error {
	if m.Get(agentID).Allow(a) {
		return nil
	}
	return &model.Error{
		Kind:    model.KindPermissionError,
		Code:    "PERMISSION_DENIED",
		Message: fmt.Sprintf("agent %q 无权执行 %s", agentID, a),
	}
}

// ApplyWorkflowOverrides 支持 DSL 中的 permissions 段覆盖默认策略。
// 形如: {"claude-code": {"filesystem": {"read": true, "write": false}, "git": {...}}}
func (m *Manager) ApplyWorkflowOverrides(overrides map[string]any) {
	if len(overrides) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for agentID, raw := range overrides {
		base := m.policies[agentID]
		sec, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fs, ok := sec["filesystem"].(map[string]any); ok {
			base.FilesystemRead = boolOf(fs["read"], base.FilesystemRead)
			base.FilesystemWrite = boolOf(fs["write"], base.FilesystemWrite)
		}
		if g, ok := sec["git"].(map[string]any); ok {
			base.GitRead = boolOf(g["read"], base.GitRead)
			base.GitCommit = boolOf(g["commit"], base.GitCommit)
			base.GitPush = boolOf(g["push"], base.GitPush)
		}
		m.policies[agentID] = base
	}
}

// Snapshot 返回当前全部策略(给前端展示)。
func (m *Manager) Snapshot() map[string]Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Policy, len(m.policies))
	for k, v := range m.policies {
		out[k] = v
	}
	return out
}

func boolOf(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}
