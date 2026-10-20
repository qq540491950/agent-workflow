// Package permission 实现统一的权限模型。
// 每个 Agent 对文件系统与 Git 的能力由 Permission Policy 决定,
// Adapter 在执行前必须咨询 Permission Manager;禁止 Agent 自行突破权限。
package permission

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"agentworkflow/logx"
	"agentworkflow/workflow/model"
)

// Action 是可授权的动作。
type Action string

const (
	FSRead    Action = "fs:read"
	FSWrite   Action = "fs:write"
	GitRead   Action = "git:read"
	GitCommit Action = "git:commit"
	GitPush   Action = "git:push"
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
	// workflow 执行主体(Git 节点等):默认可读仓库、可提交(推送永远禁用)。
	m.policies["workflow"] = Policy{
		FilesystemRead: true,
		GitRead:        true,
		GitCommit:      true,
	}
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
//
// 结构非法的覆盖项按"拒绝"处理(fail closed)而不是静默忽略:
// 此前非法结构(值不是映射 / 字段不是布尔 / 字段名拼错)一律被丢弃,
// 于是 DSL 里写错的收紧规则(例如想把 pi-agent 的写权限关掉)被静默吞掉,
// Agent 仍按默认的宽松策略执行 —— 约定 14:该失败却成功等同 P0。
// 需要拿到具体原因时请用 ApplyChecked。
func (m *Manager) ApplyWorkflowOverrides(overrides map[string]any) {
	if err := m.ApplyChecked(overrides); err != nil {
		logx.Warn("权限覆盖非法,相关 Agent 权限已全部拒绝", "error", err)
	}
}

// ApplyChecked 应用覆盖并返回错误(结构非法时)。
// 非法覆盖项对应的 Agent 权限被置为"全部拒绝"(fail closed)。
func (m *Manager) ApplyChecked(overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var problems []string
	for agentID, raw := range overrides {
		base := m.policies[agentID]
		updated, err := applyAgentOverrides(base, raw)
		if err != nil {
			// 结构非法:该 Agent 一律拒绝(而不是沿用默认策略)
			m.policies[agentID] = Policy{}
			problems = append(problems, fmt.Sprintf("agent %q: %v", agentID, err))
			continue
		}
		m.policies[agentID] = updated
	}
	if len(problems) > 0 {
		return fmt.Errorf("权限覆盖非法: %s", strings.Join(problems, "; "))
	}
	return nil
}

// applyAgentOverrides 把单个 Agent 的权限覆盖合并到基础策略上。
// 结构非法时返回错误(调用方据此 fail closed)。
func applyAgentOverrides(base Policy, raw any) (Policy, error) {
	sec, ok := raw.(map[string]any)
	if !ok {
		return base, fmt.Errorf("权限覆盖不是映射(实际 %T)", raw)
	}
	if extra := unknownFields(sec, "filesystem", "git"); extra != "" {
		return base, fmt.Errorf("权限覆盖含未知段 %s", extra)
	}
	if fs, ok := sec["filesystem"]; ok && fs != nil {
		fsm, ok := fs.(map[string]any)
		if !ok {
			return base, fmt.Errorf("filesystem 段不是映射(实际 %T)", fs)
		}
		if extra := unknownFields(fsm, "read", "write"); extra != "" {
			return base, fmt.Errorf("filesystem 段含未知字段 %s", extra)
		}
		var err error
		if base.FilesystemRead, err = boolOfStrict(fsm, "read", base.FilesystemRead); err != nil {
			return base, err
		}
		if base.FilesystemWrite, err = boolOfStrict(fsm, "write", base.FilesystemWrite); err != nil {
			return base, err
		}
	}
	if g, ok := sec["git"]; ok && g != nil {
		gm, ok := g.(map[string]any)
		if !ok {
			return base, fmt.Errorf("git 段不是映射(实际 %T)", g)
		}
		if extra := unknownFields(gm, "read", "commit", "push"); extra != "" {
			return base, fmt.Errorf("git 段含未知字段 %s", extra)
		}
		var err error
		if base.GitRead, err = boolOfStrict(gm, "read", base.GitRead); err != nil {
			return base, err
		}
		if base.GitCommit, err = boolOfStrict(gm, "commit", base.GitCommit); err != nil {
			return base, err
		}
		if base.GitPush, err = boolOfStrict(gm, "push", base.GitPush); err != nil {
			return base, err
		}
	}
	return base, nil
}

// boolOfStrict 解析覆盖值:
//   - 键不存在 → 保持原值;
//   - 是布尔 → 用该值;
//   - 是其它类型 → 视为"拒绝"(false)并报错。写错的收紧规则不能被
//     静默丢弃(否则是 fail open)。
func boolOfStrict(section map[string]any, field string, def bool) (bool, error) {
	raw, ok := section[field]
	if !ok || raw == nil {
		return def, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("字段 %q 的值不是布尔(实际 %T)", field, raw)
	}
	return b, nil
}

// unknownFields 返回不在允许列表里的字段名(逗号分隔,有序)。
func unknownFields(section map[string]any, allowed ...string) string {
	ok := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		ok[a] = true
	}
	var extra []string
	for k := range section {
		if !ok[k] {
			extra = append(extra, k)
		}
	}
	if len(extra) == 0 {
		return ""
	}
	sort.Strings(extra)
	return strings.Join(extra, ",")
}

// Clone 返回策略表的深拷贝(用于工作流运行期作用域覆盖)。
func (m *Manager) Clone() *Manager {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := NewManager()
	for id, p := range m.policies {
		out.policies[id] = p
	}
	return out
}

// Apply 应用覆盖到当前实例(与 ApplyWorkflowOverrides 相同结构)。
func (m *Manager) Apply(overrides map[string]any) {
	m.ApplyWorkflowOverrides(overrides)
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
