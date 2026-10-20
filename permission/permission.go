// Package permission 实现统一的权限模型。
// 每个 Agent 对文件系统与 Git 的能力由 Permission Policy 决定,
// Adapter 在执行前必须咨询 Permission Manager;禁止 Agent 自行突破权限。
//
// # 单一来源(迭代 12)
//
// **有效权限 = 全局策略(实时) ∧ 工作流 permissions 段(只收紧)**。
//
// 此前判定有两个来源,结论可能相反:
//
//   - 运行期节点检查用"执行启动时的快照"(e.Perms.Clone());
//   - 适配器(pi)用全局实时值(a.cfg.Perms)。
//
// 于是:
//
//  1. DSL permissions 段想**放开**某能力(全局已禁)→ 运行期放行、适配器拒绝
//     → 节点报"无权执行 fs:write",用户写的设置没生效;
//  2. Agents 页运行中**收紧**权限 → 在跑的执行仍用旧快照
//     (只有 pi 适配器的全局检查兜住 write 类节点,mock/自定义 Agent 无兜底)。
//
// 现在:
//
//   - 全局策略**实时**读取 → 收紧立即对在跑的执行生效(不再依赖启动时快照);
//   - permissions 段**只能收紧**,不能放宽全局禁用 → 运行期与适配器两侧
//     结论必然一致(有效权限恒为全局的子集);
//   - 放宽尝试在**校验期**就被报出来(见 Manager.ValidateTightenOnly),
//     不再等到运行期才以"无权执行"的形式暴露(约定 13/14)。
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

// policyMask 记录 permissions 段里**显式指定**的能力位。
//
// 为什么不用 Policy 直接存:Policy 的 bool 无法区分"没写"与"写了 false",
// 而覆盖段的语义必须区分 —— 没写的字段保持原值,写了 false 才关闭。
type policyMask struct {
	fsRead, fsWrite, gitRead, gitCommit, gitPush                bool
	hasFSRead, hasFSWrite, hasGitRead, hasGitCommit, hasGitPush bool
}

// denyAllMask 是"全部显式关闭"的掩码(结构非法时 fail closed 用)。
func denyAllMask() policyMask {
	return policyMask{
		hasFSRead: true, hasFSWrite: true, hasGitRead: true,
		hasGitCommit: true, hasGitPush: true,
	}
}

// maskFromPolicy 由完整策略构造掩码(全部字段显式指定)。
func maskFromPolicy(p Policy) policyMask {
	return policyMask{
		fsRead: p.FilesystemRead, fsWrite: p.FilesystemWrite,
		gitRead: p.GitRead, gitCommit: p.GitCommit, gitPush: p.GitPush,
		hasFSRead: true, hasFSWrite: true, hasGitRead: true,
		hasGitCommit: true, hasGitPush: true,
	}
}

// tighten 把掩码叠加到基础策略上:**只收紧**(求交)。
//
// 求交是"permissions 段只能收紧"的唯一实现点:掩码里显式写成 true 的
// 能力,只有在基础策略也允许时才保留 —— 覆盖段永远无法放宽全局禁用。
func (mk policyMask) tighten(base Policy) Policy {
	if mk.hasFSRead {
		base.FilesystemRead = base.FilesystemRead && mk.fsRead
	}
	if mk.hasFSWrite {
		base.FilesystemWrite = base.FilesystemWrite && mk.fsWrite
	}
	if mk.hasGitRead {
		base.GitRead = base.GitRead && mk.gitRead
	}
	if mk.hasGitCommit {
		base.GitCommit = base.GitCommit && mk.gitCommit
	}
	if mk.hasGitPush {
		base.GitPush = base.GitPush && mk.gitPush
	}
	return base
}

// requests 判断掩码是否**显式请求**打开某能力。
func (mk policyMask) requests(a Action) bool {
	switch a {
	case FSRead:
		return mk.hasFSRead && mk.fsRead
	case FSWrite:
		return mk.hasFSWrite && mk.fsWrite
	case GitRead:
		return mk.hasGitRead && mk.gitRead
	case GitCommit:
		return mk.hasGitCommit && mk.gitCommit
	case GitPush:
		return mk.hasGitPush && mk.gitPush
	default:
		return false
	}
}

// merge 与另一掩码求交(两次覆盖叠加时用:只收紧,不会互相放开)。
func (mk policyMask) merge(other policyMask) policyMask {
	out := policyMask{}
	mergeField := func(hasA, hasB, valA, valB bool) (bool, bool) {
		if !hasA && !hasB {
			return false, false
		}
		v := true
		if hasA {
			v = v && valA
		}
		if hasB {
			v = v && valB
		}
		return true, v
	}
	out.hasFSRead, out.fsRead = mergeField(mk.hasFSRead, other.hasFSRead, mk.fsRead, other.fsRead)
	out.hasFSWrite, out.fsWrite = mergeField(mk.hasFSWrite, other.hasFSWrite, mk.fsWrite, other.fsWrite)
	out.hasGitRead, out.gitRead = mergeField(mk.hasGitRead, other.hasGitRead, mk.gitRead, other.gitRead)
	out.hasGitCommit, out.gitCommit = mergeField(mk.hasGitCommit, other.hasGitCommit, mk.gitCommit, other.gitCommit)
	out.hasGitPush, out.gitPush = mergeField(mk.hasGitPush, other.hasGitPush, mk.gitPush, other.gitPush)
	return out
}

// Manager 管理全部 Agent 的权限策略。
//
// 两种形态:
//
//   - **全局管理器**(NewManager):进程唯一的策略来源,Agent 页改的是它。
//   - **作用域视图**(Clone):工作流运行期使用 —— 实时跟随全局策略,
//     叠加一份"只收紧"的本地覆盖(DSL permissions 段)。
type Manager struct {
	mu       sync.RWMutex
	policies map[string]Policy
	// overrides 是作用域视图的本地覆盖(只收紧)。
	overrides map[string]policyMask
	// parent 非 nil 时本实例是作用域视图:有效策略 = parent 的**实时**策略
	// ∧ 本地覆盖。parent 为 nil 时 policies 就是绝对策略。
	parent *Manager
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

// Set 设置某 Agent 的策略(全局管理器:绝对设置;作用域视图:本地收紧)。
func (m *Manager) Set(agentID string, p Policy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.parent != nil {
		// 作用域视图:本地收紧(与父级求交),不写穿到父级。
		m.overrides[agentID] = maskFromPolicy(p)
		return
	}
	m.policies[agentID] = p
}

// Get 获取某 Agent 的**有效**策略(未知 Agent 返回零值 = 全拒绝)。
//
// 作用域视图返回的是"全局实时策略 ∧ 本地覆盖",因此全局策略一改,
// 在跑的执行立刻看到(不再依赖执行启动时的快照)。
func (m *Manager) Get(agentID string) Policy {
	if m.parent != nil {
		base := m.parent.Get(agentID)
		m.mu.RLock()
		mask, ok := m.overrides[agentID]
		m.mu.RUnlock()
		if !ok {
			return base
		}
		return mask.tighten(base)
	}
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
		Message: m.denyMessage(agentID, a),
	}
}

// denyMessage 生成拒绝原因。
//
// 作用域视图里若本地覆盖(permissions 段)**显式请求**了这一能力,
// 而全局策略禁用了它 —— 原因必须说清楚是"permissions 段想放宽全局禁用",
// 否则用户看到的只是"无权执行 fs:write",与自己的写法对不上
// (排查方向被带偏,约定 14)。
func (m *Manager) denyMessage(agentID string, a Action) string {
	base := fmt.Sprintf("agent %q 无权执行 %s", agentID, a)
	if m.parent == nil {
		return base
	}
	m.mu.RLock()
	mask, ok := m.overrides[agentID]
	m.mu.RUnlock()
	if ok && mask.requests(a) && !m.parent.Get(agentID).Allow(a) {
		return fmt.Sprintf("agent %q 无权执行 %s(工作流 permissions 段请求放开该能力,"+
			"但全局策略已禁用:permissions 段只能收紧,不能放宽全局禁用)", agentID, a)
	}
	return base
}

// ApplyWorkflowOverrides 支持 DSL 中的 permissions 段覆盖默认策略。
// 形如: {"claude-code": {"filesystem": {"read": true, "write": false}, "git": {...}}}
//
// 结构非法的覆盖项按"拒绝"处理(fail closed)而不是静默忽略:
// 此前非法结构(值不是映射 / 字段不是布尔 / 字段名拼错)一律被丢弃,
// 于是 DSL 里写错的收紧规则(例如想把 pi-agent 的写权限关掉)被静默吞掉,
// Agent 仍按默认的宽松策略执行 —— 约定 14:该失败却成功等同 P0。
// 需要拿到具体原因时请用 ApplyChecked。
//
// 语义:permissions 段**只能收紧**(求交),不能放宽全局已禁用的能力。
// 放宽尝试由 ValidateTightenOnly 在校验期报出来。
func (m *Manager) ApplyWorkflowOverrides(overrides map[string]any) {
	if err := m.ApplyChecked(overrides); err != nil {
		logx.Warn("权限覆盖非法,相关 Agent 权限已全部拒绝", "error", err)
	}
}

// ValidateOverrides 只做**结构校验**,不应用覆盖、不修改任何策略。
//
// 与 ApplyChecked 用的是同一份解析逻辑(parseAgentOverrides),所以
// "校验期说合法"与"运行期能应用"必然一致(约定 #13:校验期与运行期同源)。
//
// 用途:校验期用它拦住"DSL 里写了非法 permissions 段"的工作流 ——
// 结构非法的覆盖会让运行期把该 Agent 的权限**全拒**(fail closed),
// 用户看到的却是"没有权限"而不是"permissions 写错了",排查方向被带偏。
//
// 错误信息里的 Agent 按 ID 排序(map 遍历顺序随机,不定序的错误文本
// 会让同一份 DSL 每次报出不同的消息)。
func ValidateOverrides(overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	var problems []string
	for agentID, raw := range overrides {
		if _, err := parseAgentOverrides(raw); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q: %v", agentID, err))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("权限覆盖非法: %s", strings.Join(problems, "; "))
}

// ValidateTightenOnly 检查 permissions 段是否试图**放宽**全局已禁用的能力。
//
// 语义:permissions 段只能收紧 —— 有效权限 = 全局策略 ∧ 覆盖段。
// 放宽尝试在运行期会被**忽略**(有效权限仍按全局),用户却以为生效了,
// 真正生效的是全局策略 —— 必须在**校验期**报出来(约定 13/14)。
//
// 判据:覆盖段显式写成 true 的能力,而全局策略里是 false。
// 结构非法的覆盖不在这里报(由 ValidateOverrides 报,错误码不同)。
//
// 基线是本管理器的**当前**策略:应用层传的是运行期用的同一个全局管理器,
// 所以"校验期判定"与"运行期实际"必然一致。
func (m *Manager) ValidateTightenOnly(overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	var problems []string
	for agentID, raw := range overrides {
		mask, err := parseAgentOverrides(raw)
		if err != nil {
			continue // 结构非法:由 ValidateOverrides 报
		}
		base := m.Get(agentID)
		for _, a := range []Action{FSRead, FSWrite, GitRead, GitCommit, GitPush} {
			if !mask.requests(a) || base.Allow(a) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"agent %q 的 %s 在全局策略里是禁用的,permissions 段只能收紧、不能把它打开"+
					"(请先在 Agents 页为该 Agent 开启该能力,或从 permissions 段删掉这一项)",
				agentID, a))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("permissions 段试图放宽全局已禁用的能力: %s", strings.Join(problems, "; "))
}

// ApplyChecked 应用覆盖并返回错误(结构非法时)。
// 非法覆盖项对应的 Agent 权限被置为"全部拒绝"(fail closed)。
//
// 覆盖只做**收紧**(与当前策略求交),不能放宽已禁用的能力。
func (m *Manager) ApplyChecked(overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var problems []string
	for agentID, raw := range overrides {
		mask, err := parseAgentOverrides(raw)
		if err != nil {
			// 结构非法:该 Agent 一律拒绝(而不是沿用默认策略)
			m.denyAgentLocked(agentID)
			problems = append(problems, fmt.Sprintf("agent %q: %v", agentID, err))
			continue
		}
		if m.parent != nil {
			// 作用域视图:本地覆盖(可叠加,只收紧)
			if old, ok := m.overrides[agentID]; ok {
				mask = mask.merge(old)
			}
			m.overrides[agentID] = mask
			continue
		}
		// 全局管理器:覆盖段与当前策略求交(只收紧)
		m.policies[agentID] = mask.tighten(m.policies[agentID])
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("权限覆盖非法: %s", strings.Join(problems, "; "))
	}
	return nil
}

// denyAgentLocked 把某 Agent 的权限置为全拒(fail closed)。
// 调用方必须已持有 m.mu。
func (m *Manager) denyAgentLocked(agentID string) {
	if m.parent != nil {
		m.overrides[agentID] = denyAllMask()
		return
	}
	m.policies[agentID] = Policy{}
}

// parseAgentOverrides 解析单个 Agent 的权限覆盖,返回**显式指定**的能力位。
//
// 这是覆盖段解析的**唯一来源**(约定 13):校验期(ValidateOverrides /
// ValidateTightenOnly)与运行期(ApplyChecked)共用它 —— 两边对同一份
// DSL 的结论必然一致。
func parseAgentOverrides(raw any) (policyMask, error) {
	var mk policyMask
	sec, ok := raw.(map[string]any)
	if !ok {
		return mk, fmt.Errorf("权限覆盖不是映射(实际 %T)", raw)
	}
	if extra := unknownFields(sec, "filesystem", "git"); extra != "" {
		return mk, fmt.Errorf("权限覆盖含未知段 %s", extra)
	}
	if fs, ok := sec["filesystem"]; ok && fs != nil {
		fsm, ok := fs.(map[string]any)
		if !ok {
			return mk, fmt.Errorf("filesystem 段不是映射(实际 %T)", fs)
		}
		if extra := unknownFields(fsm, "read", "write"); extra != "" {
			return mk, fmt.Errorf("filesystem 段含未知字段 %s", extra)
		}
		var err error
		if mk.fsRead, mk.hasFSRead, err = boolField(fsm, "read"); err != nil {
			return mk, err
		}
		if mk.fsWrite, mk.hasFSWrite, err = boolField(fsm, "write"); err != nil {
			return mk, err
		}
	}
	if g, ok := sec["git"]; ok && g != nil {
		gm, ok := g.(map[string]any)
		if !ok {
			return mk, fmt.Errorf("git 段不是映射(实际 %T)", g)
		}
		if extra := unknownFields(gm, "read", "commit", "push"); extra != "" {
			return mk, fmt.Errorf("git 段含未知字段 %s", extra)
		}
		var err error
		if mk.gitRead, mk.hasGitRead, err = boolField(gm, "read"); err != nil {
			return mk, err
		}
		if mk.gitCommit, mk.hasGitCommit, err = boolField(gm, "commit"); err != nil {
			return mk, err
		}
		if mk.gitPush, mk.hasGitPush, err = boolField(gm, "push"); err != nil {
			return mk, err
		}
	}
	return mk, nil
}

// boolField 解析覆盖段里的布尔字段。返回:值、是否**显式指定**、错误。
//
//   - 键不存在 → (false, false, nil):未指定,保持原值;
//   - 是布尔 → (值, true, nil);
//   - 其它类型 → 错误。写错的收紧规则不能被静默丢弃(否则是 fail open)。
func boolField(section map[string]any, field string) (bool, bool, error) {
	raw, ok := section[field]
	if !ok || raw == nil {
		return false, false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, true, fmt.Errorf("字段 %q 的值不是布尔(实际 %T)", field, raw)
	}
	return b, true, nil
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

// Clone 返回**作用域视图**:实时跟随本管理器的策略,本地覆盖只做收紧。
//
// 语义变更(迭代 12):此前是"策略表的深拷贝" —— 执行启动时拍一张快照,
// 运行中在 Agents 页收紧权限对**在跑的执行无效**(快照仍是旧值),
// 而适配器(pi)读的是全局实时值 → 权限判定出现两个来源、结论可能相反。
//
// 现在:有效策略 = 全局实时策略 ∧ 本地覆盖(只收紧)。
//
//   - 全局收紧 → 对在跑的执行立即生效(不再依赖启动时快照);
//   - 本地覆盖(DSL permissions 段)只能收紧,不能放宽全局禁用。
//
// 保留 Clone 这个名字是因为运行期(engine.go)按"克隆出一个执行级实例"
// 的语义调用它;行为变化集中在这里说明,运行期调用点无需改动。
func (m *Manager) Clone() *Manager {
	return &Manager{
		policies:  map[string]Policy{},
		overrides: map[string]policyMask{},
		parent:    m,
	}
}

// Apply 应用覆盖到当前实例(与 ApplyWorkflowOverrides 相同结构)。
func (m *Manager) Apply(overrides map[string]any) {
	m.ApplyWorkflowOverrides(overrides)
}

// Snapshot 返回当前**有效**策略(给前端展示)。
func (m *Manager) Snapshot() map[string]Policy {
	if m.parent != nil {
		out := m.parent.Snapshot()
		m.mu.RLock()
		defer m.mu.RUnlock()
		for id, mask := range m.overrides {
			out[id] = mask.tighten(out[id])
		}
		return out
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Policy, len(m.policies))
	for k, v := range m.policies {
		out[k] = v
	}
	return out
}
