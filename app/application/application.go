// Package application 实现应用服务层(Wails API 与 HTTP API 的统一后端)。
// 前端只依赖这些 Application API,不直接操作底层 Go 对象。
package application

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentworkflow/agent"
	"agentworkflow/agent/claude"
	"agentworkflow/agent/mock"
	"agentworkflow/agent/pi"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/logx"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
	wfruntime "agentworkflow/workflow/runtime"
	"agentworkflow/workflow/validator"
)

// App 是应用级上下文,聚合全部服务。
type App struct {
	Repo   *persistence.DB
	Bus    *event.Bus
	Engine *wfruntime.Engine
	Agents agent.Registry
	Skills skill.Registry
	Perms  *permission.Manager
	GitSvc *git.Service

	DataDir string
	// defaultGitDir 是启动时的默认 Git 工作目录(进程当前目录)。
	// 设置页把 git_working_dir 清空时回到这里 —— "清空"= 走默认目录,
	// 而不是把 git 服务的目录置空(cmd.Dir="" 会让子进程落在不确定的
	// 当前目录里)。
	defaultGitDir string
	// logLevel 同样不导出:设置页写、Info 读,跨 goroutine(见 logLevelMu)。
	logLevel string
	// skillsMu 保护 disabledSkills。此前是裸 map:执行协程在 runSkillNode
	// 里经 IsSkillDisabled 读,Skills 页开关经 SetEnabled 写 ——
	// 并发读写 map 在 -race 下是 DATA RACE,生产环境是 runtime 的
	// "concurrent map read and map write" 直接 fatal(带执行中的应用退出)。
	//
	// 字段**不导出**:导出等于把"必须持锁"这个约定交给调用方自觉,而
	// package 内的 4 处访问都已有 SkillDisabled / setSkillDisabled 封装。
	// 同理 LogLevel 见下。
	skillsMu       sync.RWMutex
	disabledSkills map[string]bool
	// scriptNodes:设置页「允许执行 script 节点」。原子类型:设置页可在
	// 执行运行中切换,裸 bool 会被并发读写(同 logx SetLevel 的教训)。
	scriptNodes atomic.Bool
	// logLevelMu 保护 logLevel(设置页写、Settings.Info 读)。
	logLevelMu sync.RWMutex
	// 供 Wails/SSE 桥接实时事件。
	eventsMu      sync.RWMutex
	connectOnce   sync.Once
	eventHandlers map[int]func(event.UIEvent)
	nextHandlerID int

	Workflows  *WorkflowService
	Executions *ExecutionService
	AgentSvc   *AgentService
	SkillSvc   *SkillService
	GitAPI     *GitService
	Settings   *SettingsService
}

// NewApp 初始化应用(打开数据库、注册 Agent/Skill、恢复执行)。
// examples 为示例工作流目录的 FS(通常由 main 包 go:embed 提供,
// 打包后的 .app 从任意 CWD 启动都能种子;传 nil 跳过种子)。
func NewApp(dataDir string, examples fs.FS) (*App, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	repo, err := persistence.Open(filepath.Join(dataDir, "app.db"))
	if err != nil {
		return nil, err
	}
	// 注意:生产总线不保留事件历史(无消费者,长期运行会无界增长);
	// 事件已实时持久化 SQLite,回放走 /api/executions/{id}/events。
	bus := event.NewBus()

	agents := agent.NewRegistry()
	skills := skill.NewRegistry()
	perms := permission.NewManager()
	// Git 默认工作目录 = 进程当前目录(桌面/服务器通用),可在设置页覆盖
	cwd, _ := os.Getwd()
	gitSvc := git.New(cwd)

	app := &App{
		Repo:           repo,
		Bus:            bus,
		Agents:         agents,
		Skills:         skills,
		Perms:          perms,
		GitSvc:         gitSvc,
		DataDir:        dataDir,
		defaultGitDir:  cwd,
		logLevel:       "info",
		disabledSkills: map[string]bool{},
		eventHandlers:  map[int]func(event.UIEvent){},
	}
	app.Engine = wfruntime.NewEngine(agents, skills, perms, bus, gitSvc, repo)
	app.Engine.SubworkflowRunner = app.runSubworkflow
	app.Engine.IsSkillDisabled = app.SkillDisabled
	app.Engine.AllowScriptNodes = app.ScriptNodesAllowed

	// 内置 Skill 注册
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			return nil, err
		}
	}

	// 注册默认 Agent(配置来自 settings/agent_configs)
	//
	// 内置 Agent 注册失败必须让启动失败:少一个内置 Agent,引用它的节点
	// 在运行期才报"Agent 不存在",而用户看到的是"工作流跑不起来"。
	// 详见 reloadAgents 的说明。
	if err := app.reloadAgents(); err != nil {
		return nil, err
	}

	// 服务层
	app.Workflows = &WorkflowService{app: app}
	app.Executions = &ExecutionService{app: app}
	app.AgentSvc = &AgentService{app: app}
	app.SkillSvc = &SkillService{app: app}
	app.GitAPI = &GitService{app: app}
	app.Settings = &SettingsService{app: app}

	// 工作目录设置
	//
	// 读失败必须留痕:此前 `err == nil && wd != ""` 把读错误吞得干干净净
	// —— 行损坏(存的不是字符串)或 DB 故障时,进程静默用默认目录,
	// 界面显示的目录不是用户设置的那个,而全程零日志(约定 14:
	// 该失败却成功)。"从未设置过"是正常初始状态,不算失败。
	var wd string
	if err := repo.GetSetting("git_working_dir", &wd); err != nil {
		if !isSettingNotFound(err) {
			logx.Error("读取 git_working_dir 设置失败,已回落到默认目录", "error", err.Error())
		}
	} else if wd != "" {
		gitSvc.SetWorkingDir(wd)
	}
	// 日志级别持久化(读失败同样要留痕:静默回落到 info 会让用户以为
	// 自己的 debug 级别还在,实际早就不是了)
	var lv string
	if err := repo.GetSetting("log_level", &lv); err != nil {
		if !isSettingNotFound(err) {
			logx.Error("读取 log_level 设置失败,已回落到 info", "error", err.Error())
		}
	} else if lv != "" {
		logx.SetLevel(lv)
	}
	app.logLevel = lv
	if app.logLevel == "" {
		app.logLevel = "info"
	}
	// 禁用 Skill 恢复
	if disabled, err := repo.ListDisabledSkills(); err == nil {
		app.skillsMu.Lock()
		app.disabledSkills = disabled
		app.skillsMu.Unlock()
	}
	// script 节点放行开关(默认关闭:该节点可执行任意本地命令;
	// 设置项缺失时保持关闭)
	//
	// 读失败按"关闭"处理(fail closed,见 AGENTS.md 约定 11),但仍要留痕:
	// 行坏了却看起来像"用户没开",排查时看不出是哪一步坏的。
	var allowScript bool
	if err := repo.GetSetting("allow_script_nodes", &allowScript); err != nil {
		if !isSettingNotFound(err) {
			logx.Warn("读取 allow_script_nodes 设置失败,已按关闭处理", "error", err.Error())
		}
	} else {
		app.scriptNodes.Store(allowScript)
	}

	// 崩溃恢复
	if err := app.Engine.RecoverPending(); err != nil {
		logx.Warn("恢复执行状态失败", "error", err)
	}

	// 首次启动:导入示例工作流
	if examples != nil {
		sub, err := fs.Sub(examples, "workflows/examples")
		if err == nil {
			app.seedExamples(sub)
		}
	}

	logx.Info("应用初始化完成", "data_dir", dataDir)
	return app, nil
}

// loadAgentConfig 读取某 Agent 的持久化配置(未配置返回零值)。
func (a *App) loadAgentConfig(id string) (agent.AgentConfig, error) {
	raw, err := a.Repo.GetAgentConfig(id)
	if err != nil || raw == nil {
		return agent.AgentConfig{}, err
	}
	cfg := agent.AgentConfig{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return agent.AgentConfig{}, model.NewError(model.KindPersistenceError, "AGENT_CONFIG_CORRUPT", err.Error())
	}
	return cfg, nil
}

// applyAgentConfig 按配置热重建适配器实例(注册表 Replace)。
func (a *App) applyAgentConfig(id string, cfg agent.AgentConfig) error {
	// 纵深防御:剔除凭据位上的字面量 "***"。掩码哨兵不是真值 ——
	// 库里若存着旧版本 GET→PUT 往返的产物("--api-key ***"),直接下发
	// 会让每次 CLI 调用都带 --api-key *** → 鉴权失败,而用户只看到
	// "已保存"。合并(UpdateConfig)已保证新写入的配置干净,这里是给
	// 历史脏数据与启动恢复路径兜底。
	cfg.ExtraArgs = agent.SanitizeExtraArgs(cfg.ExtraArgs)
	switch id {
	case "claude-code":
		return a.Agents.Replace(claude.New(claude.Config{
			Model:                 cfg.Model,
			BaseURL:               cfg.BaseURL,
			AgentEnv:              cfg.Env,
			ExtraArgs:             cfg.ExtraArgs,
			DefaultTimeoutSeconds: cfg.TimeoutSeconds,
		}))
	case "pi-agent":
		return a.Agents.Replace(pi.New(pi.Config{
			Perms:                 a.Perms,
			Model:                 cfg.Model,
			BaseURL:               cfg.BaseURL,
			AgentEnv:              cfg.Env,
			DefaultArgs:           cfg.ExtraArgs,
			DefaultTimeoutSeconds: cfg.TimeoutSeconds,
		}))
	case "mock-claude", "mock-pi", "mock":
		return a.Agents.Replace(buildMock(id, cfg.Behavior))
	default:
		// 其他内置 Agent 不支持运行时配置,静默保留
		return nil
	}
}

// defaultMockBehavior 返回内置演示脚本(用户配置在其上合并覆盖)。
func defaultMockBehavior(id string) map[string]any {
	switch id {
	case "mock-claude":
		return map[string]any{
			"plan":   map[string]any{"summary_template": "分析任务:{task};制定三步实施方案"},
			"review": map[string]any{"decisions": []any{"REJECTED", "APPROVED"}, "summary_template": "Review 第 {call} 次:发现的问题已修复"},
		}
	case "mock-pi":
		return map[string]any{
			"execute": map[string]any{"summary_template": "已完成任务:{task}"},
			"fix":     map[string]any{"summary_template": "已修复 Review 问题(第 {call} 轮)"},
		}
	}
	return map[string]any{}
}

// buildMock 按行为配置构造 Mock Agent(演示用,决策序列可自定义)。
// behavior 为 nil 表示使用内置默认脚本;非 nil 时按 mode **逐键合并**覆盖默认。
//
// 逐键合并(P2-5):用户只写 decisions 时,默认脚本里的 summary_template
// 必须保留。此前是 `merged[mode] = raw` **整体替换** —— 用户写一个键,
// 默认脚本的其余键全丢(约定 14:不该动的却动了)。
func buildMock(id string, behavior map[string]any) agent.Agent {
	name := id
	switch id {
	case "mock-claude":
		name = "Mock Claude(演示)"
	case "mock-pi":
		name = "Mock Pi(演示)"
	}
	merged := defaultMockBehavior(id)
	for mode, raw := range behavior {
		m, ok := raw.(map[string]any)
		if !ok {
			continue // 结构错由 validateMockBehavior 在保存期报出
		}
		base, _ := merged[mode].(map[string]any)
		if base == nil {
			base = map[string]any{}
		}
		for k, v := range m {
			base[k] = v // 只覆盖用户写了的键
		}
		merged[mode] = base
	}
	scripts := map[string]*mock.Script{}
	for mode, raw := range merged {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sc := &mock.Script{}
		sc.Decisions = mockBehaviorDecisions(m["decisions"])
		if tpl, ok := m["summary_template"].(string); ok {
			sc.SummaryTemplate = tpl
		}
		if fail, ok := m["fail_with"].(string); ok && fail != "" {
			sc.FailWith = fail
		}
		scripts[mode] = sc
	}
	def := scripts["*"]
	delete(scripts, "*")
	return mock.New(mock.Options{ID: id, Name: name, Scripts: scripts, Default: def})
}

// mockBehaviorDecisions 把 behavior 里的 decisions 读成决策序列。
//
// 两种形态都认:[]any(JSON 往返后的形态)与 []string(直接构造的形态)。
// 元素统一转大写 —— 决策取值必须大写(路由按大写匹配)。
func mockBehaviorDecisions(v any) []agent.AgentDecision {
	var out []agent.AgentDecision
	appendOne := func(d any) {
		out = append(out, agent.AgentDecision(strings.ToUpper(fmt.Sprint(d))))
	}
	switch arr := v.(type) {
	case []any:
		for _, d := range arr {
			appendOne(d)
		}
	case []string:
		for _, d := range arr {
			appendOne(d)
		}
	}
	return out
}

// mockAgentIDs 是内置 Mock Agent 的 ID 集合。
//
// 与 applyAgentConfig 的 mock 分支保持一致:behavior 只被 buildMock 读,
// 别的 Agent 上它是**死配置** —— 不该因为它挡住保存。
var mockAgentIDs = map[string]bool{
	"mock-claude": true,
	"mock-pi":     true,
	"mock":        true,
}

// mockBehaviorFields 是 Mock 行为脚本**被运行期读取**的字段
// (唯一来源:buildMock 读的那几个键;前端类型与之对应)。
//
// 顺序即报错提示里的顺序,与 docs/api.md 的契约写法一致。
var mockBehaviorFields = []string{"decisions", "summary_template", "fail_with"}

// validateMockBehavior 校验 Mock 行为脚本的结构(未知键 / 错类型 → 报错)。
//
// 契约(前端自由 JSON 框的正式契约,见 docs/api.md):
//
//	behavior := { <mode>: { decisions?: string[], summary_template?: string, fail_with?: string } }
//
// 为什么要校验:此前 buildMock 对不认识的键 `continue` 静默丢弃 —— 用户把
// decisions 写成 decision,脚本不生效,界面却提示"已保存"(约定 14:静默
// 丢弃与崩溃同样危险)。
//
// 例外:**凭据位**放行。behavior 是自由 JSON 框,用户可能把 api_key 这类
// 凭据放进去 —— 那是既有契约(GET 掩码、往返合并,见 agent.Masked);
// 判定口径与掩码同源(logx.MaskValue 会把它的值换成 "***" 的键就是凭据位)。
//
// 错误码复用 BAD_REQUEST(前端 CODE_HINTS 已有该码 → 不用改前端,
// 约定 10 的双模式契约)。
//
// 错误信息只回显键名与类型,不回显值:值可能是凭据,回显就是新的泄漏面。
func validateMockBehavior(behavior map[string]any) error {
	for mode, raw := range behavior {
		m, ok := raw.(map[string]any)
		if !ok {
			return model.NewError(model.KindValidationError, "BAD_REQUEST",
				fmt.Sprintf("behavior 的 %q 必须是对象(如 {\"decisions\": [\"APPROVED\"]}),实际是 %T", mode, raw))
		}
		for key, v := range m {
			if err := validateMockBehaviorField(mode, key, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateMockBehaviorField 校验 behavior 里单个 mode 对象的字段。
func validateMockBehaviorField(mode, key string, v any) error {
	switch key {
	case "decisions":
		switch arr := v.(type) {
		case []any:
			for _, d := range arr {
				if _, ok := d.(string); !ok {
					return model.NewError(model.KindValidationError, "BAD_REQUEST",
						fmt.Sprintf("behavior 的 %q.decisions 必须是字符串数组,元素是 %T", mode, d))
				}
			}
		case []string:
		default:
			return model.NewError(model.KindValidationError, "BAD_REQUEST",
				fmt.Sprintf("behavior 的 %q.decisions 必须是字符串数组,实际是 %T", mode, v))
		}
		return nil
	case "summary_template", "fail_with":
		if _, ok := v.(string); !ok {
			return model.NewError(model.KindValidationError, "BAD_REQUEST",
				fmt.Sprintf("behavior 的 %q.%s 必须是字符串,实际是 %T", mode, key, v))
		}
		return nil
	}
	if isCredentialBehaviorKey(key) {
		return nil // 凭据位:放行(掩码契约)
	}
	return model.NewError(model.KindValidationError, "BAD_REQUEST",
		fmt.Sprintf("behavior 的 %q 里有未知的字段 %q(可用: %s;凭据位如 api_key 也允许)",
			mode, key, strings.Join(mockBehaviorFields, " / ")))
}

// isCredentialBehaviorKey 判断某个键是不是"凭据位"(掩码规则会替换它的值)。
//
// 口径与掩码**同源**:用 logx.MaskValue 探一次 —— 值被换成 "***" 的键
// 就是凭据位(logx/credential.go 的值形态规则 + logx/mask.go 的键名规则)。
//
// 为什么放行凭据位:behavior 是用户的自由 JSON 框,凭据放进去是既有契约
// (GET 掩码、往返合并,见 agent.Masked / agent.MergeMaskedBehavior)。
// 把它们当"未知键"报错会打死这条契约。
func isCredentialBehaviorKey(key string) bool {
	masked, ok := logx.MaskValue(map[string]any{key: "probe"}).(map[string]any)
	if !ok {
		return false
	}
	v, ok := masked[key]
	if !ok {
		return false
	}
	s, ok := v.(string)
	return ok && s == agent.MaskedValue
}

// reloadAgents 根据持久化配置注册 Agent 适配器。
// 注意:必须复用传入 Engine 的同一个 permission.Manager 实例。
//
// 内置 Agent 注册失败**必须上抛**(调用方 = NewApp → 启动即失败):
// 内置 Agent 是整个系统的基础 —— 少一个,它就不在注册表里(Agents 页
// 不列出它,引用它的节点在运行期报"Agent 不存在"),而此前
// `_ = a.Agents.Register(...)` 把失败吞得干干净净,**一行日志都没有**:
// "内置 Agent 缺失"这件事完全不可见(约定 14:该失败却成功)。
//
// 可达性:注册表在 NewApp 里是**新建的空表**,4 个内置 Agent 的 ID 互不
// 重复(claude-code / pi-agent / mock-claude / mock-pi)→ 当前代码路径下
// 注册失败**不可达**。正因不可达,上抛不会误伤正常启动;而"不可达"不等于
// "永远不可达" —— 将来 reloadAgents 变成可重复调用(重建适配器)时,
// 失败就会重新变成静默故障。
func (a *App) reloadAgents() error {
	// Claude Code 适配器
	// 注意:不传 Permission Manager —— claude 适配器不读它(CLI 层已硬禁写工具),
	// 权限判定在运行期节点检查处(查有效策略)。见 agent/claude 的包说明。
	claudeAgt := claude.New(claude.Config{})
	if err := a.Agents.Register(claudeAgt); err != nil {
		return a.builtinAgentRegisterFailed(claudeAgt.ID(), err)
	}

	// Pi Agent 适配器
	piAgt := pi.New(pi.Config{Perms: a.Perms})
	if err := a.Agents.Register(piAgt); err != nil {
		return a.builtinAgentRegisterFailed(piAgt.ID(), err)
	}

	// Mock Agents(演示与测试:不依赖外部 CLI,内置默认演示脚本)
	if err := a.Agents.Register(buildMock("mock-claude", nil)); err != nil {
		return a.builtinAgentRegisterFailed("mock-claude", err)
	}
	if err := a.Agents.Register(buildMock("mock-pi", nil)); err != nil {
		return a.builtinAgentRegisterFailed("mock-pi", err)
	}
	// Mock Agent 权限:模拟真实角色的只读/读写能力
	a.Perms.Set("mock-claude", permission.Policy{FilesystemRead: true, GitRead: true})
	a.Perms.Set("mock-pi", permission.Policy{
		FilesystemRead: true, FilesystemWrite: true,
		GitRead: true, GitCommit: true,
	})

	// 从 DB 覆盖权限策略
	var configs map[string]map[string]any
	if err := a.Repo.GetSetting("agent_policies", &configs); err == nil {
		for id, p := range configs {
			a.Perms.Set(id, permission.Policy{
				FilesystemRead:  boolOf(p["filesystem_read"]),
				FilesystemWrite: boolOf(p["filesystem_write"]),
				GitRead:         boolOf(p["git_read"]),
				GitCommit:       boolOf(p["git_commit"]),
				GitPush:         boolOf(p["git_push"]),
			})
		}
	}

	// 从 DB 应用各 Agent 的模型/端点/环境变量配置(启动即生效)
	if saved, err := a.Repo.ListAgentConfigs(); err == nil {
		for id, raw := range saved {
			cfg := agent.AgentConfig{}
			if err := json.Unmarshal(raw, &cfg); err != nil {
				// 损坏的配置此前被静默跳过:配置在库里却不生效,排查时
				// 看不出是哪一步坏的。至少留一条告警。
				logx.Warn("Agent 配置解析失败,已跳过", "agent", id, "error", err.Error())
				continue
			}
			if err := a.applyAgentConfig(id, cfg); err != nil {
				logx.Warn("Agent 配置应用失败", "agent", id, "error", err.Error())
			}
		}
	} else {
		logx.Warn("读取 Agent 配置失败", "error", err.Error())
	}
	return nil
}

// builtinAgentRegisterFailed 记录内置 Agent 注册失败,并返回上抛用的错误。
//
// 失败必须同时留下**日志**与**错误**:日志让"内置 Agent 缺失"有痕迹
// (此前一行都没有),错误让启动直接失败 —— 一个缺了内置 Agent 的应用
// 跑起来,只会在用户跑工作流时才炸,而错误信息与根因(注册表里少了
// 一个内置 Agent)毫无关联。
func (a *App) builtinAgentRegisterFailed(id string, err error) error {
	logx.Error("内置 Agent 注册失败", "agent", id, "error", err.Error())
	return fmt.Errorf("内置 Agent %q 注册失败: %w", id, err)
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

// cloneStringMap 复制一张字符串 map(nil → nil)。
//
// 用于"缺省 = 不修改"的字段:把已存值原样带回提交的配置,
// 不能直接复用原 map(后续的合并循环会就地改它)。
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SkillDisabled 报告 Skill 是否被禁用(执行协程读,需加锁)。
func (a *App) SkillDisabled(id string) bool {
	a.skillsMu.RLock()
	defer a.skillsMu.RUnlock()
	return a.disabledSkills[id]
}

// setSkillDisabled 更新内存态(调用方需持写锁)。
func (a *App) setSkillDisabled(id string, disabled bool) {
	a.skillsMu.Lock()
	defer a.skillsMu.Unlock()
	if disabled {
		a.disabledSkills[id] = true
	} else {
		delete(a.disabledSkills, id)
	}
}

// LogLevelValue 返回当前日志级别(设置页写、Info 读,需加锁)。
func (a *App) LogLevelValue() string {
	a.logLevelMu.RLock()
	defer a.logLevelMu.RUnlock()
	return a.logLevel
}

// setLogLevel 更新内存态日志级别。
func (a *App) setLogLevel(level string) {
	a.logLevelMu.Lock()
	defer a.logLevelMu.Unlock()
	a.logLevel = level
}

// runSubworkflow 同步执行嵌套工作流。
// 等待循环感知父执行的取消:父被取消时级联取消子执行并立即返回,
// 而不是让父 goroutine 在轮询里阻塞最长 10 分钟。
func (a *App) runSubworkflow(ctx context.Context, workflowID, task string, input map[string]any) (map[string]any, error) {
	wf, err := a.Repo.GetWorkflow(workflowID)
	if err != nil {
		return nil, fmt.Errorf("子工作流 %s 不存在", workflowID)
	}
	exec, err := a.Engine.Start(ctx, wf, task, input)
	if err != nil {
		return nil, err
	}
	// 等待完成(子工作流为阻塞执行)
	for i := 0; i < 6000; i++ { // 最多 10 分钟
		select {
		case <-ctx.Done():
			// 父执行已取消:级联取消子执行
			_ = a.Engine.Cancel(context.Background(), exec.ID)
			return nil, model.NewError(model.KindCancelledError, "SUBWORKFLOW_CANCELLED", "父执行取消,子工作流已中止")
		case <-time.After(100 * time.Millisecond):
		}
		e, err := a.Repo.GetExecution(exec.ID)
		if err == nil {
			switch e.State {
			case model.ExecutionCompleted:
				return map[string]any{"state": "COMPLETED", "nodes": e.NodeStates}, nil
			case model.ExecutionFailed, model.ExecutionCancelled:
				return nil, fmt.Errorf("子工作流失败: %s", e.Error)
			}
		}
	}
	return nil, fmt.Errorf("子工作流超时")
}

// randRead 是 crypto/rand.Read 的间接层(**测试接缝**)。
//
// 为什么需要它:熵源失败必须能被测试复现 —— 生产环境里 crypto/rand 极少
// 失败,但"失败即全零 ID"的后果是**覆盖既有工作流**(见 randHex)。
var randRead = func(b []byte) (int, error) { return crypto_rand.Read(b) }

// randHex 生成 n 字节随机数的十六进制串。
//
// **熵源失败必须让调用方知道**:此前是 `_, _ = crypto_rand.Read(b)` ——
// 失败时 b 保持**全零**,于是工作流 ID 恒为 "wf-000000000000" →
// 与既有工作流撞车 → SaveWorkflow 按 ID **覆盖** → 静默数据丢失
// (约定 14 同族:该失败却成功)。
//
// 现在失败即返回错误:调用方(保存/复制/导入)直接失败,不动既有数据。
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := randRead(b); err != nil {
		return "", fmt.Errorf("生成随机 ID 失败(熵源不可用): %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ScriptNodesAllowed 报告 script 节点是否已放行(供引擎门禁与设置页读取)。
func (a *App) ScriptNodesAllowed() bool { return a.scriptNodes.Load() }

// Subscribe 注册 UI 事件处理器(Wails Emit / SSE 都通过这里)。
func (a *App) Subscribe(h func(event.UIEvent)) func() {
	a.eventsMu.Lock()
	defer a.eventsMu.Unlock()
	id := a.nextHandlerID
	a.nextHandlerID++
	a.eventHandlers[id] = h
	return func() {
		a.eventsMu.Lock()
		defer a.eventsMu.Unlock()
		delete(a.eventHandlers, id)
	}
}

// EventSubscriberCount 返回当前注册的 UI 事件订阅者数量。
// 用于观察 SSE 连接断开后订阅是否释放(连接泄漏的可观测点)。
func (a *App) EventSubscriberCount() int {
	a.eventsMu.RLock()
	defer a.eventsMu.RUnlock()
	return len(a.eventHandlers)
}

// ConnectEvents 把总线事件桥接到 UI 处理器。
// 幂等:桌面(main)与 HTTP 服务器(api.NewServer)都会调用,只生效一次。
// 此前仅桌面模式调用,服务器模式的 SSE 客户端收不到任何实时事件。
func (a *App) ConnectEvents() {
	a.connectOnce.Do(func() {
		a.Bus.Subscribe(func(ev event.UIEvent) {
			a.eventsMu.RLock()
			handlers := make([]func(event.UIEvent), 0, len(a.eventHandlers))
			for _, h := range a.eventHandlers {
				handlers = append(handlers, h)
			}
			a.eventsMu.RUnlock()
			for _, h := range handlers {
				h(ev)
			}
		})
	})
}

// seedExamples 导入示例工作流(按示例文件的工作流 ID 补种缺失的)。
// 使用嵌入 FS 而非磁盘相对路径:打包后的应用从任意工作目录启动都有效。
func (a *App) seedExamples(dir fs.FS) {
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}
		raw, err := fs.ReadFile(dir, entry.Name())
		if err != nil {
			continue
		}
		doc, err := dsl.Parse(raw)
		if err != nil {
			logx.Warn("示例工作流解析失败", "file", entry.Name(), "error", err)
			continue
		}
		wf := doc.ToModel()
		// 已存在同 ID 工作流则跳过(用户可能已修改)
		if existing, err := a.Repo.GetWorkflow(wf.ID); err == nil && existing != nil {
			continue
		}
		if res := validator.Validate(wf, nil, nil); !res.Valid {
			logx.Warn("示例工作流校验失败", "file", entry.Name())
			continue
		}
		if err := a.Repo.SaveWorkflow(wf, false); err != nil {
			logx.Warn("示例工作流保存失败", "file", entry.Name(), "error", err)
		}
	}
}

// ---- WorkflowService ----

// WorkflowService 工作流 CRUD/校验/导出。
type WorkflowService struct {
	app *App
}

func (s *WorkflowService) List() ([]*model.Workflow, error) {
	return s.app.Repo.ListWorkflows()
}

func (s *WorkflowService) Get(id string) (*model.Workflow, error) {
	return s.app.Repo.GetWorkflow(id)
}

// WorkflowResult 是工作流写操作(保存/导入)的返回结果。
//
// 形状:**工作流字段仍在顶层**(与既有 REST 响应契约一致 —— 第三方客户端
// 直接读 body.id / body.nodes,不能被包一层),额外附加两个字段:
//
//   - warnings:校验警告(不阻断保存,但必须让调用方看到 —— 此前只进日志,
//     直连 API 的调用方永远看不到,用户以为设置生效了);
//   - ignored_keys:导入时被解析器丢弃的键(仅导入路径有值)。
//
// ignored_keys 的处理沿用导入接口既有的前例(平铺附加字段,不用
// {workflow: ...} 包一层):既不破坏既有客户端,也不让"导入 → 原样回存"
// 这种往返失败。
type WorkflowResult struct {
	*model.Workflow
	// Warnings 校验警告(结构化,与校验结果的 warnings 同源)。
	Warnings []validator.ValidationError `json:"warnings,omitempty"`
	// IgnoredKeys 导入时被丢弃的键(仅导入路径有值)。
	IgnoredKeys []string `json:"ignored_keys,omitempty"`
}

// UnmarshalJSON 显式实现:model.Workflow 自带 UnmarshalJSON(权限段别名
// 兼容),嵌入本类型后该方法会被**提升**到 WorkflowResult —— 若不屏蔽,
// 反序列化时 warnings/ignored_keys 会被整个丢掉。
//
// 两个坑(都实测过):
//  1. 提升后的 UnmarshalJSON 会在 **nil** 嵌入指针上被调用 ——
//     model.Workflow 里对 *w 赋值会 panic;
//  2. 更隐蔽:encoding/json 命中提升上来的 UnmarshalJSON 后,把**整个**
//     JSON 对象交给它,外层自己的字段(warnings/ignored_keys)因此再也
//     拿不到 —— 所以不能靠"别名类型 + 一次 Unmarshal"绕过去。
//
// 因此这里显式分两段解析:工作流本体交给 model.Workflow 自己的解析器
// (它按惯例忽略无关键),附加字段用一个不含嵌入字段的小结构单独解析。
func (r *WorkflowResult) UnmarshalJSON(data []byte) error {
	wf := &model.Workflow{}
	if err := json.Unmarshal(data, wf); err != nil {
		return err
	}
	var extra struct {
		Warnings    []validator.ValidationError `json:"warnings"`
		IgnoredKeys []string                    `json:"ignored_keys"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	r.Workflow = wf
	r.Warnings = extra.Warnings
	r.IgnoredKeys = extra.IgnoredKeys
	return nil
}

// Save 创建或更新(传入带 ID 的 workflow;存在则版本+1)。
//
// 模型级入口:调用方已经持有模型,**没有原始载荷**,因此不做顶层键校验
// (键在反序列化时已丢失,无从检查)。用户提交路径一律走 SavePayload。
//
// 校验警告仍然不丢:既进日志,也在 SavePayload/ImportYAML 的返回值里带回。
func (s *WorkflowService) Save(wf *model.Workflow) (*model.Workflow, error) {
	saved, _, err := s.saveModel(wf)
	return saved, err
}

// SavePayload 保存工作流(载荷级入口:两种模式的用户提交路径都用它)。
//
// rawJSON 是调用方提交的**原始 JSON 文本** —— 必须保留原始键:未知顶层键
// 在反序列化时会被静默丢弃,只有拿到原始载荷才能报 UNKNOWN_FIELD。
// 桌面模式此前走 Save(模型级),非法载荷被静默接受 —— 双模式行为不一致。
func (s *WorkflowService) SavePayload(rawJSON string) (*WorkflowResult, error) {
	wf, err := ParseWorkflowPayload([]byte(rawJSON))
	if err != nil {
		return nil, err
	}
	saved, warnings, err := s.saveModel(wf)
	if err != nil {
		return nil, err
	}
	return &WorkflowResult{Workflow: saved, Warnings: warnings}, nil
}

// validateWorkflow 校验工作流:静态规则(引擎) + permissions 段的
// "只能收紧"规则(应用层)。
//
// 为什么要分两处:引擎的校验是纯静态的(不依赖运行期状态),而
// "permissions 段是否在放宽全局禁用"必须拿**当前全局策略**做基线 ——
// 全局策略在 Agents 页可随时改,是运行期状态。基线用的是
// s.app.Perms,与运行期(e.Perms)是**同一个**管理器,所以
// "校验期判定"与"运行期实际"必然一致(约定 13)。
//
// 规则本身实现在 permission 包里(唯一来源):覆盖段只能收紧,
// 有效权限 = 全局策略 ∧ 覆盖段。
func (s *WorkflowService) validateWorkflow(wf *model.Workflow) *validator.Result {
	res := s.app.Engine.Validate(wf)
	if !res.Valid {
		return res
	}
	// permissions 段想放宽全局禁用 → 校验期就报出来。
	// 运行期这类写法会被**忽略**(有效权限仍按全局),用户却以为生效了
	// (约定 14:用户以为生效了实际没有)。
	if err := s.app.Perms.ValidateTightenOnly(wf.Permission); err != nil {
		res.Valid = false
		res.Errors = append(res.Errors, validator.ValidationError{
			Code:    "PERMISSION_OVERRIDE_LOOSENS",
			Message: err.Error(),
		})
	}
	return res
}

// saveModel 校验并保存工作流模型:返回保存后的工作流与校验警告。
//
// 警告此前被整个丢掉(只回传 res.Errors):DEPRECATED_NODE_CONFIG 这类
// 警告只有显式 validate 端点看得见,直连 API 的调用方看不到 ——
// 静默丢失(约定 #14 同族)。这里既记进日志(不丢),也随结果带回。
func (s *WorkflowService) saveModel(wf *model.Workflow) (*model.Workflow, []validator.ValidationError, error) {
	if wf.ID == "" {
		id, err := randHex(6)
		if err != nil {
			// 熵源不可用 → 拒绝保存。用全零 ID 继续 = 覆盖既有工作流。
			return nil, nil, err
		}
		wf.ID = "wf-" + strings.ToLower(id)
	}
	// 保存前必须校验
	res := s.validateWorkflow(wf)
	logValidationWarnings("save", wf.ID, res)
	if !res.Valid {
		raw, _ := json.Marshal(res.Errors)
		return nil, nil, model.NewError(model.KindValidationError, "WORKFLOW_INVALID", string(raw))
	}
	if err := s.app.Repo.SaveWorkflow(wf, true); err != nil {
		return nil, nil, err
	}
	saved, err := s.app.Repo.GetWorkflow(wf.ID)
	if err != nil {
		return nil, nil, err
	}
	return saved, res.Warnings, nil
}

// validationWarningSink 是"校验警告"的落地出口。
//
// 默认写日志;测试可以替换它,断言"保存/运行路径没有把警告丢掉"。
// 包级变量跨 goroutine 读写必须加锁(约定 #15)。
var (
	validationWarningMu   sync.RWMutex
	validationWarningSink = logValidationWarning
)

// logValidationWarning 默认出口:写日志。
func logValidationWarning(scope, id string, w validator.ValidationError) {
	logx.Warn("工作流校验警告", "scope", scope, "id", id,
		"code", w.Code, "node", w.Node, "message", w.Message)
}

// setValidationWarningSink 替换警告出口(测试用),返回恢复函数。
func setValidationWarningSink(f func(scope, id string, w validator.ValidationError)) func() {
	validationWarningMu.Lock()
	old := validationWarningSink
	validationWarningSink = f
	validationWarningMu.Unlock()
	return func() {
		validationWarningMu.Lock()
		validationWarningSink = old
		validationWarningMu.Unlock()
	}
}

// logValidationWarnings 把校验**警告**显式记出来。
//
// 保存/运行路径此前只回传 res.Errors,res.Warnings 被整个丢掉:
// 警告(如 DEPRECATED_NODE_CONFIG)只有显式 validate 端点可见,直连 API
// 的调用方看不到 —— 静默丢失(约定 #14 同族:"该报告却没报告")。
//
// 这里在应用层把它记下来,保证不丢;要让**调用方**也拿到,需要给
// API 契约增加 warnings 字段(api/ 与 frontend/ 不在本轮改动范围内,
// 精确改法见交付报告)。
func logValidationWarnings(scope, id string, res *validator.Result) {
	if res == nil || len(res.Warnings) == 0 {
		return
	}
	validationWarningMu.RLock()
	sink := validationWarningSink
	validationWarningMu.RUnlock()
	for _, w := range res.Warnings {
		sink(scope, id, w)
	}
}

func (s *WorkflowService) Delete(id string) error {
	return s.app.Repo.DeleteWorkflow(id)
}

// Duplicate 复制工作流。
func (s *WorkflowService) Duplicate(id string) (*model.Workflow, error) {
	wf, err := s.app.Repo.GetWorkflow(id)
	if err != nil {
		return nil, err
	}
	// 熵源不可用时必须失败:用全零 ID 继续 = 覆盖既有工作流(约定 14)。
	newID, err := randHex(6)
	if err != nil {
		return nil, err
	}
	wf.ID = "wf-" + strings.ToLower(newID)
	wf.Name = wf.Name + " (副本)"
	wf.Version = 0
	if err := s.app.Repo.SaveWorkflow(wf, false); err != nil {
		return nil, err
	}
	return wf, nil
}

func (s *WorkflowService) SetEnabled(id string, enabled bool) (*model.Workflow, error) {
	wf, err := s.app.Repo.GetWorkflow(id)
	if err != nil {
		return nil, err
	}
	wf.Enabled = enabled
	if err := s.app.Repo.SaveWorkflow(wf, false); err != nil {
		return nil, err
	}
	return wf, nil
}

// ImportYAML 导入 DSL 文本创建工作流。
// ID 冲突时自动分配新 ID;导入前必须通过校验。
//
// 返回值携带"被解析器丢弃的键"(IgnoredKeys)与校验警告:此前两者只落在
// 内存与日志里,用户看不到"这份 DSL 有 N 个键被忽略"(设置没生效,却显示
// 导入成功)—— 服务器模式的 import 端点自己又解析了一遍 DSL 才补上这个
// 字段,桌面模式(走本绑定)则完全没有。统一由应用层产出,两种模式同源。
func (s *WorkflowService) ImportYAML(content string) (*WorkflowResult, error) {
	doc, err := dsl.Parse([]byte(content))
	if err != nil {
		return nil, err
	}
	wf := doc.ToModel()
	if existing, err := s.app.Repo.GetWorkflow(wf.ID); err == nil && existing != nil {
		// 熵源不可用时必须失败:用全零 ID 继续 = 覆盖既有工作流(约定 14)。
		newID, err := randHex(6)
		if err != nil {
			return nil, err
		}
		wf.ID = "wf-" + strings.ToLower(newID)
	}
	wf.Version = 0
	saved, warnings, err := s.saveModel(wf)
	if err != nil {
		return nil, err
	}
	return &WorkflowResult{
		Workflow:    saved,
		Warnings:    warnings,
		IgnoredKeys: doc.IgnoredKeys,
	}, nil
}

// Validate 返回结构化校验结果。
func (s *WorkflowService) Validate(wf *model.Workflow) *validator.Result {
	return s.validateWorkflow(wf)
}

// VersionDSL 返回指定版本的 DSL 文本(从版本快照导出)。
func (s *WorkflowService) VersionDSL(id string, version int) (string, error) {
	wf, err := s.app.Repo.GetWorkflowVersion(id, version)
	if err != nil {
		return "", err
	}
	out, err := dsl.FromModel(wf).EncodeYAML()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ExportYAML 导出 DSL。
func (s *WorkflowService) ExportYAML(id string) (string, error) {
	wf, err := s.app.Repo.GetWorkflow(id)
	if err != nil {
		return "", err
	}
	out, err := dsl.FromModel(wf).EncodeYAML()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *WorkflowService) Versions(id string) ([]int, error) {
	return s.app.Repo.ListWorkflowVersions(id)
}

// ---- ExecutionService ----

// ExecutionService 执行管理。
type ExecutionService struct {
	app *App
}

// ExecutionResult 是执行启动的结果:执行记录 + 启动前的校验警告。
//
// 形状:**执行字段仍在顶层**(与既有 REST 响应契约一致),warnings 是
// 附加字段(与工作流保存结果同源的处理方式)。
type ExecutionResult struct {
	*model.Execution
	// Warnings 启动前的校验警告(不阻断运行,但必须让调用方看到)。
	Warnings []validator.ValidationError `json:"warnings,omitempty"`
}

// Run 启动执行(异步,立即返回 Execution 记录)。
// 使用脱离请求的生命周期 context:HTTP 响应返回后执行继续在后台进行。
func (s *ExecutionService) Run(ctx context.Context, workflowID, task string, variables map[string]any) (*ExecutionResult, error) {
	wf, err := s.app.Repo.GetWorkflow(workflowID)
	if err != nil {
		return nil, err
	}
	if !wf.Enabled {
		return nil, model.NewError(model.KindValidationError, "WORKFLOW_DISABLED", "工作流已禁用")
	}
	// 警告不阻断运行,但必须可见:此前运行路径只回传 res.Errors,
	// res.Warnings(例如 DEPRECATED_NODE_CONFIG)被整个丢掉,直连 API 的
	// 调用方看不到(约定 #14 同族)。引擎内部还会再校验一次(以它为准),
	// 这里只为把警告记出来并带回,不改变判定结果。
	res := s.app.Engine.Validate(wf)
	logValidationWarnings("run", workflowID, res)
	exec, err := s.app.Engine.Start(context.Background(), wf, task, variables)
	if err != nil {
		return nil, err
	}
	// 返回的是引擎的深拷贝(cloneExec),脱敏不影响运行期。
	maskExecution(exec)
	return &ExecutionResult{Execution: exec, Warnings: res.Warnings}, nil
}

func (s *ExecutionService) List(workflowID string, limit int) ([]*model.Execution, error) {
	execs, err := s.app.Repo.ListExecutions(workflowID, limit)
	if err != nil {
		return nil, err
	}
	for _, e := range execs {
		maskExecution(e)
	}
	return execs, nil
}

func (s *ExecutionService) Get(id string) (*model.Execution, error) {
	exec, err := s.app.Repo.GetExecution(id)
	if err != nil {
		return nil, err
	}
	maskExecution(exec)
	return exec, nil
}

// 三个子资源端点先确认执行存在:此前直接查子表,不存在的执行 ID 返回
// 200 + 空数组,前端无法区分"这个执行没有节点明细"与"执行 ID 打错了"。
func (s *ExecutionService) Nodes(id string) ([]*model.ExecutionNode, error) {
	if _, err := s.app.Repo.GetExecution(id); err != nil {
		return nil, err
	}
	nodes, err := s.app.Repo.ListExecutionNodes(id)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		maskNode(n)
	}
	return nodes, nil
}

func (s *ExecutionService) Events(id string) ([]map[string]any, error) {
	if _, err := s.app.Repo.GetExecution(id); err != nil {
		return nil, err
	}
	return s.app.Repo.ListEvents(id, 0)
}

// Artifacts 返回某次执行的制品(内容已脱敏,约定 8)。
//
// 制品此前**完全没有**脱敏:git diff 节点把 diff 原文落库,这里原样
// 返回 —— 而同一份内容在 /nodes 里是脱敏的,两个出口口径不一致。
// 脱敏按"值的形态"判定(agent.MaskCredentialValues):凭据形态的值被
// 替换,代码标识符原样保留(diff 的可读性与审计价值不退化)。
func (s *ExecutionService) Artifacts(id string) ([]*model.Artifact, error) {
	if _, err := s.app.Repo.GetExecution(id); err != nil {
		return nil, err
	}
	arts, err := s.app.Repo.ListArtifacts(id)
	if err != nil {
		return nil, err
	}
	for _, art := range arts {
		maskArtifact(art)
	}
	return arts, nil
}

// Stats 返回每工作流执行统计。
func (s *ExecutionService) Stats() ([]persistence.WorkflowStats, error) {
	return s.app.Repo.GetWorkflowStats()
}

// AllEvents 跨执行审计事件流。
func (s *ExecutionService) AllEvents(eventType string, limit int) ([]map[string]any, error) {
	return s.app.Repo.ListAllEvents(eventType, limit)
}

// ProvideInput 人工输入后恢复执行(同样使用脱离请求的 context)。
func (s *ExecutionService) ProvideInput(ctx context.Context, id string, response map[string]any) (*model.Execution, error) {
	exec, err := s.app.Engine.Resume(context.Background(), id, response)
	if err != nil {
		return nil, err
	}
	// 返回的是引擎的深拷贝(cloneExec),脱敏不影响运行期。
	maskExecution(exec)
	return exec, nil
}

func (s *ExecutionService) Cancel(ctx context.Context, id string) error {
	return s.app.Engine.Cancel(ctx, id)
}

// Export 汇出执行完整记录(执行+节点+事件+制品),用于审计归档。
func (s *ExecutionService) Export(id string) (map[string]any, error) {
	exec, err := s.app.Repo.GetExecution(id)
	if err != nil {
		return nil, err
	}
	// 执行记录本身也要脱敏(约定 8:API 返回掩码):state_data 里带着
	// 每个节点的 outcome 原文。
	maskExecution(exec)
	// 审计归档必须完整:此前吞掉三次查询错误,导出静默缺项
	nodes, err := s.app.Repo.ListExecutionNodes(id)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		maskNode(n)
	}
	events, err := s.app.Repo.ListEvents(id, 0)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.app.Repo.ListArtifacts(id)
	if err != nil {
		return nil, err
	}
	// 制品同样要脱敏(约定 8):导出是 API 出口,不是"备份导出"那个
	// 允许明文的例外。备份导出(设置页)才是唯一例外。
	for _, art := range artifacts {
		maskArtifact(art)
	}
	return map[string]any{
		"execution": exec,
		"nodes":     nodes,
		"events":    events,
		"artifacts": artifacts,
	}, nil
}

// maskArtifact 脱敏制品内容里的凭据形态值(约定 8)。
//
// 只改**返回给调用方的副本**,不改库里那份:库里那份是审计证据的原始
// 留存(也可能来自本轮修复之前的历史数据),API 出口负责不让明文出去。
//
// 脱敏按"值的形态"判定而不是"键名敏感就整值替换"(后者会把代码里的
// 标识符一并抹掉,毁掉 diff 的可读性 —— 第八轮为此把"artifact 不脱敏"
// 钉死在测试里)。键名判定复用 agent.IsSensitiveEnvName(与 ExtraArgs
// 的掩码同一份来源)。
func maskArtifact(a *model.Artifact) {
	if a == nil {
		return
	}
	a.Content = agent.MaskCredentialValues(a.Content)
}

// maskNode 脱敏节点明细里的自由文本(output/error/result_json)。
// 这些字段直接来自 Agent 的 stdout/stderr,常含 "ANTHROPIC_API_KEY=sk-..."
// 这类凭据:事件流已在总线出口脱敏,但节点明细是直写 SQLite、原样返回 ——
// API 读出口必须再脱敏一次(约定 8:API 返回掩码)。
func maskNode(n *model.ExecutionNode) {
	if n == nil {
		return
	}
	n.Output = logx.Mask(n.Output)
	n.Error = logx.Mask(n.Error)
	n.ResultJSON = maskResultJSON(n.ResultJSON)
}

// maskExecution 脱敏执行记录里的自由文本出口(error / task / variables /
// state_data)。
//
// 约定 8:API 返回掩码。节点明细已由 maskNode 脱敏,但**执行记录本身**
// 的这几个出口此前没有:
//
//   - state_data:会话状态快照,里面有每个节点的 outcome 原文
//     (node:<id>:result)、最近一次输出(output)与错误文本(_error) ——
//     直接来自 Agent 的 stdout/stderr;
//   - error:执行失败原因(同样常带 CLI stderr);
//   - task / variables:用户输入,事件流里已经脱敏,这里不能是例外。
//
// state_data 复用 runtime 的 MaskStateData(与落库侧**同一份**按键分类):
// 结构化键(decision / node:<id>:status / _abort 等)被运行期读取,用于
// 路由与决策,必须原样保留;两份实现各自演化就会出现"落库脱敏了、
// 读出口又把状态改坏"的漂移(约定 #13 的同源精神)。
//
// 只改**返回给调用方的副本**,不改运行期正在用的结构:
//   - Get/List/Export 读的是库里的新对象,改了不影响运行期;
//   - Run/ProvideInput/RetryNode 返回的是引擎的深拷贝(cloneExec),
//     同样与运行期不共享可变结构。
func maskExecution(exec *model.Execution) {
	if exec == nil {
		return
	}
	exec.Error = logx.Mask(exec.Error)
	exec.Task = logx.Mask(exec.Task)
	exec.Variables = maskAnyMap(exec.Variables)
	exec.StateData = wfruntime.MaskStateData(exec.StateData)
}

// maskAnyMap 对 map 做结构化脱敏(返回新 map,不改原对象)。
func maskAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	masked, ok := logx.MaskValue(m).(map[string]any)
	if !ok {
		return m
	}
	return masked
}

// maskResultJSON 对 result_json 做结构化脱敏(解析 → 脱敏 → 重新序列化)。
// 直接对 JSON 文本做正则替换可能破坏转义引号,产出非法 JSON。
func maskResultJSON(raw string) string {
	if raw == "" {
		return raw
	}
	var obj any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return logx.Mask(raw)
	}
	masked, ok := logx.MaskValue(obj).(map[string]any)
	if !ok {
		return logx.Mask(raw)
	}
	out, err := json.Marshal(masked)
	if err != nil {
		return logx.Mask(raw)
	}
	return string(out)
}

// Delete 删除执行记录(级联删除节点/事件/制品)。
func (s *ExecutionService) Delete(id string) error {
	return s.app.Repo.DeleteExecution(id)
}

// RetryNode 重试失败节点或跳过继续(skip=true)。
func (s *ExecutionService) RetryNode(id string, skip bool) (*model.Execution, error) {
	exec, err := s.app.Engine.RetryNode(context.Background(), id, skip)
	if err != nil {
		return nil, err
	}
	// 返回的是引擎的深拷贝(cloneExec),脱敏不影响运行期。
	maskExecution(exec)
	return exec, nil
}

// ---- AgentService ----

// AgentService Agent 管理与权限配置。
type AgentService struct {
	app *App
	// permMu 保护 agent_policies 设置项的"读整表 → 改一项 → 写整表"。
	// 此前是裸读-改-写:并发授权(多 Agent 分别设置权限)会互相覆盖,
	// 后写者拿着旧快照写回,先写者的策略整条丢失 —— 权限面板上表现为
	// "某个 Agent 的权限莫名恢复默认"。
	permMu sync.Mutex
}

func (s *AgentService) List() []map[string]any {
	out := []map[string]any{}
	for _, a := range s.app.Agents.List() {
		p := s.app.Perms.Get(a.ID())
		model := ""
		if cfg, err := s.app.loadAgentConfig(a.ID()); err == nil {
			model = cfg.Model
		}
		out = append(out, map[string]any{
			"id":          a.ID(),
			"name":        a.Name(),
			"model":       model,
			"permissions": p,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

// SetPermission 更新某 Agent 的权限策略。
func (s *AgentService) SetPermission(id string, policy permission.Policy) error {
	if err := s.app.requireAgent(id); err != nil {
		return err
	}
	// 整表读-改-写必须串行化(见 permMu):并发调用会丢更新。
	s.permMu.Lock()
	defer s.permMu.Unlock()
	// 注意:读失败必须上抛。此前吞掉错误后 configs 为 nil → 重建空 map,
	// 随后的整表写会把其他 Agent 已配置的权限策略全部清空。
	var configs map[string]map[string]any
	if err := s.app.Repo.GetSetting("agent_policies", &configs); err != nil {
		var se *model.Error
		// 设置项尚未创建属于正常初始状态;其余错误上抛
		if !errors.As(err, &se) || se.Code != "NOT_FOUND" {
			return err
		}
	}
	if configs == nil {
		configs = map[string]map[string]any{}
	}
	configs[id] = map[string]any{
		"filesystem_read":  policy.FilesystemRead,
		"filesystem_write": policy.FilesystemWrite,
		"git_read":         policy.GitRead,
		"git_commit":       policy.GitCommit,
		"git_push":         policy.GitPush,
	}
	if err := s.app.Repo.SaveSetting("agent_policies", configs); err != nil {
		return err
	}
	s.app.Perms.Set(id, policy)
	return nil
}

// requireAgent 校验 Agent 是否在注册表中,不存在则返回 404 NOT_FOUND。
func (a *App) requireAgent(id string) error {
	if _, err := a.Agents.Get(id); err != nil {
		return model.NewError(model.KindValidationError, "NOT_FOUND",
			fmt.Sprintf("Agent %q 不存在", id))
	}
	return nil
}

// GetConfig 返回某 Agent 的运行配置(环境变量值已掩码为 ***)。
func (s *AgentService) GetConfig(id string) (agent.AgentConfig, error) {
	// 未知 Agent 必须 404 NOT_FOUND:此前直接读配置行,不存在的 ID 返回
	// 200 + 空配置,前端看不出"这个 Agent 不存在"还是"它没配过" ——
	// 而 PUT/reset 更糟,会凭空为不存在的 ID 落库一行配置。
	if err := s.app.requireAgent(id); err != nil {
		return agent.AgentConfig{}, err
	}
	cfg, err := s.app.loadAgentConfig(id)
	if err != nil {
		return agent.AgentConfig{}, err
	}
	return cfg.Masked(), nil
}

// UpdateConfig 保存某 Agent 的运行配置并热重建适配器实例。
// 隔离原则:配置只影响本应用对该 Agent 的调用(CLI 参数 + 子进程环境变量),
// 不会修改用户的本地配置文件;环境变量值为 "***" 表示保留原值,空串表示删除。
func (s *AgentService) UpdateConfig(id string, cfg agent.AgentConfig) error {
	if err := s.app.requireAgent(id); err != nil {
		return err
	}
	// Mock 行为脚本的结构校验(P2-5):未知键 / 错类型直接报错。
	//
	// 此前 buildMock 对不认识的键 `continue` 静默丢弃 —— 用户把 decisions
	// 写成 decision,脚本不生效,界面却提示"已保存"(约定 14)。
	//
	// 只对 Mock Agent 生效:behavior 只被 buildMock 读(applyAgentConfig 的
	// mock 分支),别的 Agent 上它是**死配置** —— 不该因为它挡住保存。
	if mockAgentIDs[id] {
		if err := validateMockBehavior(cfg.Behavior); err != nil {
			return err
		}
	}
	// 合并掩码值:*** → 沿用已存值
	stored, err := s.app.loadAgentConfig(id)
	if err != nil {
		return err
	}
	// default_working_dir 是**死设置**:运行期从不读取它
	// (claude.Config / pi.Config 都没有该字段,applyAgentConfig 也不传;
	// 节点级 config.working_dir 为空时回退到全局 Git 工作目录)。
	//
	// 此前 UI 有输入框、值存进库、界面提示"已保存",运行时 Agent 仍在
	// 全局 Git 目录下读写文件 —— 用户以为生效了,实际没有(约定 14)。
	//
	// 现在:新值一律**明确拒绝**(而不是继续静默接受);
	// 存量值原样回传放行(存量配置不能因此存不下去),清空也放行。
	if cfg.DefaultWorkingDir != "" && cfg.DefaultWorkingDir != stored.DefaultWorkingDir {
		return model.NewError(model.KindValidationError, "UNSUPPORTED_AGENT_SETTING",
			"default_working_dir 不生效(运行期从不读取该设置):"+
				"请改用节点级 config.working_dir,或清空该字段")
	}
	// env 的**第四态**契约。
	//
	// 约定 5 只定义了 env 表**内部**的三态(逐键):"***" 保留原值、
	// 空串删除该键、无原值的 "***" 丢弃。整张表本身的"给不给"此前
	// 没有定义 —— 而"没给"被当成了"清空全部":
	//
	//   缺省(nil)→ **不修改**:保留库里已存的环境变量。
	//   {}       → **显式清空全部**。
	//   {k:v}    → **整表替换**:逐键按约定 5 合并,表里没有的键被删除
	//              (前端是整表回传:用户删掉的行就是要删掉)。
	//
	// 为什么"缺省"必须是"不修改":一个只想改模型的请求
	// (PUT {"model":"x"})此前会顺手把 ANTHROPIC_AUTH_TOKEN 等
	// **全部凭据删掉**,而用户只看到"已保存" —— 下一次调用 Agent
	// 就没有鉴权了(约定 14:不该动的却动了)。
	//
	// 前后端必须同步(约定 10):前端 Agents 页在 GET 失败时**不得**
	// 回传一份空表(那等于清空全部凭据)—— 失败时不给出可保存的配置,
	// 保存按钮禁用。
	if cfg.Env != nil {
		for k, v := range cfg.Env {
			switch v {
			case agent.MaskedValue:
				if old, ok := stored.Env[k]; ok {
					cfg.Env[k] = old
				} else {
					delete(cfg.Env, k)
				}
			case "":
				delete(cfg.Env, k)
			}
		}
		if len(cfg.Env) == 0 {
			cfg.Env = nil
		}
	} else {
		// 缺省 = 不修改:把已存的 env 原样带回(库里是**真值**,不是掩码)。
		cfg.Env = cloneStringMap(stored.Env)
	}
	// ExtraArgs 的 *** 同样表示"保留原值"(约定 5),但它是有序的
	// []string,不能按键合并 —— 必须按**选项名**对齐(agent.MergeExtraArgs):
	// 按下标对齐会在用户增删参数后错位,把 A 选项的凭据写到 B 选项的
	// 位置,比"明文覆盖"的原缺陷更糟。
	//
	// 前端把整份配置原样回传(Agents.tsx:打开配置拿到掩码 → 只改模型 →
	// 保存),不回传就会把库里的真实凭据覆盖成字面量 "***"。
	cfg.ExtraArgs = agent.MergeExtraArgs(cfg.ExtraArgs, stored.ExtraArgs)
	// behavior 里的 *** 同义("保留原值"):它是自由结构,GET 时按
	// logx.MaskValue 的口径脱敏(见 agent.Masked),不回传合并就会把
	// 凭据覆盖成哨兵。按**同路径**对齐(mode → 字段 → 值)。
	if cfg.Behavior != nil {
		cfg.Behavior = agent.MergeMaskedBehavior(cfg.Behavior, stored.Behavior)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.app.Repo.SaveAgentConfig(id, raw); err != nil {
		return err
	}
	return s.app.applyAgentConfig(id, cfg)
}

// ResetConfig 清空某 Agent 的运行配置,恢复适配器内置默认。
func (s *AgentService) ResetConfig(id string) error {
	if err := s.app.requireAgent(id); err != nil {
		return err
	}
	// env 必须是**显式空表**:UpdateConfig 的契约里"env 缺省 = 不修改",
	// 而"恢复默认"要清空环境变量 —— 不显式给空表就清不掉
	// (约定 5 的第四态:必须把"清空"与"不给"区分开)。
	cfg := agent.AgentConfig{Env: map[string]string{}}
	return s.UpdateConfig(id, cfg)
}

// Test 测试 Agent 可用性:检查注册与 CLI 是否安装(claude/pi)。
// CLI 未安装时给出明确提示(演示可使用 Mock Agent)。
func (s *AgentService) Test(ctx context.Context, id string) (string, error) {
	a, err := s.app.Agents.Get(id)
	if err != nil {
		return "", err
	}
	switch id {
	case "claude-code":
		if _, err := exec.LookPath("claude"); err != nil {
			return "已注册,但未找到 claude CLI(演示请使用 mock-claude)", nil
		}
		return "已注册且 claude CLI 可用", nil
	case "pi-agent":
		if _, err := exec.LookPath("pi-agent"); err != nil {
			return "已注册,但未找到 pi-agent CLI(演示请使用 mock-pi)", nil
		}
		return "已注册且 pi-agent CLI 可用", nil
	}
	return fmt.Sprintf("Agent %s(%s) 已注册,可被工作流引用", a.Name(), a.ID()), nil
}

// ---- SkillService ----

// SkillService Skill 管理。
type SkillService struct {
	app *App
}

func (s *SkillService) List() []skill.DTO {
	out := []skill.DTO{}
	for _, sk := range s.app.Skills.List() {
		out = append(out, skill.DTO{
			ID: sk.ID(), Name: sk.Name(), Description: sk.Description(),
			Enabled: !s.app.SkillDisabled(sk.ID()),
		})
	}
	return out
}

// SetEnabled 启用/禁用 Skill(禁用后运行时拒绝调用)。
// 先落库再改内存:此前先改内存,持久化失败时界面显示已保存但重启即失效。
func (s *SkillService) SetEnabled(id string, enabled bool) error {
	if _, err := s.app.Skills.Get(id); err != nil {
		return err
	}
	if err := s.app.Repo.SaveSkillEnabled(id, enabled); err != nil {
		return err
	}
	s.app.setSkillDisabled(id, !enabled)
	return nil
}

// Test 执行一次 Skill 测试调用。
func (s *SkillService) Test(ctx context.Context, id string) (string, error) {
	sk, err := s.app.Skills.Get(id)
	if err != nil {
		return "", err
	}
	resp, err := sk.Execute(ctx, skill.SkillRequest{Args: map[string]any{"message": "skill test"}, Context: map[string]any{}})
	if err != nil {
		return "", err
	}
	if resp.Status != "SUCCESS" {
		return resp.Output, fmt.Errorf("skill 返回 %s: %s", resp.Status, resp.Error)
	}
	return resp.Output, nil
}

// ---- GitService ----

// GitService Git API。
type GitService struct {
	app *App
}

func (s *GitService) Status(ctx context.Context) (*git.Status, error) {
	return s.app.GitSvc.Status(ctx)
}

func (s *GitService) Diff(ctx context.Context, staged bool) (string, error) {
	return s.app.GitSvc.Diff(ctx, staged)
}

func (s *GitService) Log(ctx context.Context, maxCount int) ([]git.LogEntry, error) {
	return s.app.GitSvc.Log(ctx, maxCount)
}

func (s *GitService) Commit(ctx context.Context, message string) (string, error) {
	return s.app.GitSvc.Commit(ctx, message)
}

// SetWorkingDir 更新 Git 工作目录。
// 先落库再改内存:此前反过来,落库失败时接口返回错误但内存态已被改掉
// (界面提示失败,Git 面板却已指向新目录;重启后又回到旧目录)。
func (s *GitService) SetWorkingDir(dir string) error {
	if err := s.app.Repo.SaveSetting("git_working_dir", dir); err != nil {
		return err
	}
	s.app.GitSvc.SetWorkingDir(dir)
	return nil
}

// ---- SettingsService ----

// SettingsService 设置。
type SettingsService struct {
	app *App
}

func (s *SettingsService) Get(key string, out any) error {
	return s.app.Repo.GetSetting(key, out)
}

// writableSettingKeys 返回 Set 白名单里的键(副本)。
//
// 白名单只含"设置页语义明确、有校验、不涉及并发读改写"的三个键。
// 刻意**不含** agent_policies:它由 AgentService 在 permMu 下"读整表→
// 改一项→写整表",绕过去直写会丢并发更新(并发保护失效)。
//
// 返回副本而不是包级变量:白名单是只读语义,不该是可被改写的全局状态
// (约定 15:共享可变状态一律加锁 —— 更好的做法是压根不给它可变的余地)。
func writableSettingKeys() []string {
	return []string{"git_working_dir", "log_level", "allow_script_nodes"}
}

// isWritableSettingKey 判断 key 是否在 Set 白名单里。
func isWritableSettingKey(key string) bool {
	for _, k := range writableSettingKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// Set 写入设置。
//
// **本方法经 main.go 的 wails.NewService(app.Settings) 暴露在 Wails IPC 上**
// (前端绑定 frontend/bindings/agentworkflow/app/application/settingsservice.ts
// 里的 Set(key, value) 就是它)—— 它是对外入口,不能成为绕过校验的后门。
//
// 此前是"任意键写入设置表",于是:
//   - 直写 agent_policies → 绕过 AgentService.permMu 的串行化,并发授权
//     丢更新(并发保护失效);
//   - 直写 git_working_dir / log_level → 绕过 Update 里的存在性校验与
//     日志级别校验(迭代 20 刚加的校验被绕开)。
//
// 现在:只接受**白名单键**,并复用 Update 的同一套校验;写入后**立即生效**
// (内存态与库值一致,不会出现"提示已保存、实际没生效")。
func (s *SettingsService) Set(key string, value any) error {
	if !isWritableSettingKey(key) {
		return model.NewError(model.KindValidationError, "BAD_REQUEST",
			fmt.Sprintf("设置键 %q 不允许通过 Set 写入(允许: %s);"+
				"其他设置请走各自的专用接口", key, strings.Join(writableSettingKeys(), " / ")))
	}
	switch key {
	case "git_working_dir":
		return s.setGitWorkingDir(value)
	case "log_level":
		return s.setLogLevelValue(value)
	case "allow_script_nodes":
		return s.setAllowScriptNodes(value)
	default:
		// 白名单里有键却没有处理逻辑 → 必须显式报错,不能"静默成功"。
		return model.NewError(model.KindValidationError, "BAD_REQUEST",
			fmt.Sprintf("设置键 %q 暂不支持通过 Set 写入", key))
	}
}

// setGitWorkingDir 写入并应用 Git 工作目录。
//
// 复用 Update 的同一套校验(validateGitWorkingDir):目录必须存在且是目录 ——
// 否则"保存成功"之后,每个 git / script / agent 节点都因 cmd.Dir 不存在而
// 失败,报错与设置页毫无关联(约定 14)。
//
// 空串 = 清除(落库空串 + 回到启动默认目录),不需要存在性校验。
//
// 先落库再改内存:落库失败时内存态不动,不会出现"界面提示已保存、重启即失效"。
func (s *SettingsService) setGitWorkingDir(value any) error {
	dir, ok := value.(string)
	if !ok {
		return model.NewError(model.KindValidationError, "BAD_REQUEST",
			fmt.Sprintf("设置键 git_working_dir 的值必须是字符串(实际是 %T)", value))
	}
	if dir != "" {
		if err := validateGitWorkingDir(dir); err != nil {
			return err
		}
	}
	if err := s.app.Repo.SaveSetting("git_working_dir", dir); err != nil {
		return err
	}
	if dir != "" {
		s.app.GitSvc.SetWorkingDir(dir)
	} else {
		s.app.GitSvc.SetWorkingDir(s.app.defaultGitDir)
	}
	return nil
}

// setLogLevelValue 写入并应用日志级别。
//
// 级别语义的唯一来源是 logx:校验用 logx.ValidateLevel(不改变当前级别),
// 应用用 logx.SetLevelChecked(唯一入口)。落库归一为小写。
func (s *SettingsService) setLogLevelValue(value any) error {
	lvl, ok := value.(string)
	if !ok {
		return model.NewError(model.KindValidationError, "BAD_REQUEST",
			fmt.Sprintf("设置键 log_level 的值必须是字符串(实际是 %T)", value))
	}
	if err := logx.ValidateLevel(lvl); err != nil {
		return model.NewError(model.KindValidationError, "INVALID_LOG_LEVEL", err.Error())
	}
	lvl = strings.ToLower(strings.TrimSpace(lvl))
	if err := s.app.Repo.SaveSetting("log_level", lvl); err != nil {
		return err
	}
	s.app.setLogLevel(lvl)
	// SetLevelChecked 是"应用级别"的唯一入口:非法时保持当前级别并报错,
	// 错误真正回报给调用方(接口层 400),不再只是 WARN。
	if err := logx.SetLevelChecked(lvl); err != nil {
		return model.NewError(model.KindValidationError, "INVALID_LOG_LEVEL", err.Error())
	}
	return nil
}

// setAllowScriptNodes 写入并应用 script 节点放行开关。
//
// 值必须是布尔:此前 value 是 any,写进一个字符串 "true" 就会在库里留下
// 一行"读不出来"的值(解不进 bool)—— 自己写坏的设置行。
func (s *SettingsService) setAllowScriptNodes(value any) error {
	allow, ok := value.(bool)
	if !ok {
		return model.NewError(model.KindValidationError, "BAD_REQUEST",
			fmt.Sprintf("设置键 allow_script_nodes 的值必须是布尔值(实际是 %T)", value))
	}
	if err := s.app.Repo.SaveSetting("allow_script_nodes", allow); err != nil {
		return err
	}
	s.app.scriptNodes.Store(allow)
	logx.Warn("script 节点放行开关已变更", "allow_script_nodes", allow)
	return nil
}

// SettingsInfo 是设置页展示的只读信息 + 可修改项。
type SettingsInfo struct {
	DataDir       string `json:"data_dir"`
	GitWorkingDir string `json:"git_working_dir"`
	LogLevel      string `json:"log_level"`
	// AllowScriptNodes 放行 script 节点(任意本地命令执行),默认 false。
	AllowScriptNodes bool `json:"allow_script_nodes"`
	// GitWorkingDirReadFailed 标记 git_working_dir 这一行**读不出来**
	// (行内容损坏 / DB 读失败)。
	//
	// 为 true 时,上面的 GitWorkingDir 是**内存态**(Git 服务当前目录),
	// 不是库里的值 —— 消费方必须把它标出来,不能当"库里那个"展示或回存。
	//
	// 为什么必须有这个标记:此前读错误被 `_ =` 吞掉,静默回落到内存态
	// → 界面显示的不是库里那个 → 用户一保存就把损坏行覆盖成内存值,
	// 损坏事实就此消失(约定 14:该失败却成功)。
	//
	// 用 omitempty:正常情况(false)下响应体形状与此前完全一致,
	// 不影响既有消费方。
	GitWorkingDirReadFailed bool `json:"git_working_dir_read_failed,omitempty"`
}

// isSettingNotFound 判断 err 是否表示"这个设置项从未设置过"。
//
// 设置项不存在是**正常初始状态**(全新安装),必须与"读失败"区分开:
// 前者不该报错、不该被标注成失败;后者必须报错并标注
// (见 SettingsService.Info 与 NewApp 里的启动读取)。
//
// 口径与 AgentService.SetPermission 里对 NOT_FOUND 的判断一致。
func isSettingNotFound(err error) bool {
	var se *model.Error
	return errors.As(err, &se) && se.Code == "NOT_FOUND"
}

// Info 返回当前设置。
//
// git_working_dir 的读失败必须与"从未设置过"分开处理:
//   - 从未设置过(NOT_FOUND):正常初始状态 → 显示当前生效目录;
//   - 读失败(行损坏 / DB 故障):**不能**拿内存态冒充库值 → 明确标注
//     (GitWorkingDirReadFailed)并记 ERROR,否则用户一保存就把损坏行
//     覆盖成内存值,损坏事实消失(约定 14)。
func (s *SettingsService) Info() *SettingsInfo {
	info := &SettingsInfo{
		DataDir:          s.app.DataDir,
		LogLevel:         s.app.LogLevelValue(),
		AllowScriptNodes: s.app.ScriptNodesAllowed(),
	}
	var wd string
	switch err := s.app.Repo.GetSetting("git_working_dir", &wd); {
	case err == nil:
		info.GitWorkingDir = wd
		if wd == "" {
			// 空串 = 清除(回到启动默认目录)
			info.GitWorkingDir = s.app.GitSvc.WorkingDir()
		}
	case isSettingNotFound(err):
		// 从未设置过:正常初始状态,显示当前生效目录。
		info.GitWorkingDir = s.app.GitSvc.WorkingDir()
	default:
		// 读失败:内存态目录**不是**库里的值 —— 标注出来并留痕,
		// 绝不让它冒充库值(否则界面显示的不是库里那个,而用户无从察觉)。
		info.GitWorkingDir = s.app.GitSvc.WorkingDir()
		info.GitWorkingDirReadFailed = true
		logx.Error("读取 git_working_dir 设置失败:显示的是内存态目录,不是库里的值",
			"error", err.Error())
	}
	return info
}

// Update 更新设置:git_working_dir / log_level(日志级别即时生效)/
// allow_script_nodes(script 节点放行开关,默认关闭)。
// allowScriptNodes 为 nil 表示不修改,保持既有值。
// ExportBackup 导出全部工作流与 Agent 配置(备份用;含明文敏感环境变量,请妥善保管)。
func (s *SettingsService) ExportBackup() (map[string]any, error) {
	wfs, err := s.app.Repo.ListWorkflows()
	if err != nil {
		return nil, err
	}
	configs, err := s.app.Repo.ListAgentConfigs()
	if err != nil {
		return nil, err
	}
	cfgMap := map[string]json.RawMessage{}
	for id, raw := range configs {
		// 环境变量保留明文(备份用途),其余数据同样原样导出
		cfgMap[id] = raw
	}
	return map[string]any{
		"exported_at":   time.Now().Format(time.RFC3339),
		"version":       1,
		"workflows":     wfs,
		"agent_configs": cfgMap,
	}, nil
}

// RestoreBackup 恢复备份:已存在同 ID 工作流跳过(避免覆盖用户修改),
// Agent 配置覆盖写入。
// 输入统一经 JSON 归一:API/前端路径是 []any + map[string]any,
// 进程内路径(ExportBackup 直出)是 []model.Workflow + map[string]json.RawMessage。
func (s *SettingsService) RestoreBackup(backup map[string]any) (map[string]any, error) {
	b, err := json.Marshal(backup)
	if err != nil {
		return nil, fmt.Errorf("备份格式无效: %w", err)
	}
	var norm struct {
		Workflows    []json.RawMessage          `json:"workflows"`
		AgentConfigs map[string]json.RawMessage `json:"agent_configs"`
	}
	if err := json.Unmarshal(b, &norm); err != nil {
		return nil, fmt.Errorf("备份格式无效: %w", err)
	}

	restoredWf, skipped, invalid := 0, 0, 0
	invalidIDs := []string{}
	for _, raw := range norm.Workflows {
		wf := model.Workflow{}
		if err := json.Unmarshal(raw, &wf); err != nil || wf.ID == "" {
			invalid++
			continue
		}
		if existing, err := s.app.Repo.GetWorkflow(wf.ID); err == nil && existing != nil {
			skipped++
			continue
		}
		// 恢复也要过校验:此前直接 Repo.SaveWorkflow 绕过 WorkflowService.Save
		// 与 Engine.Validate,悬空边/空图/未知节点的非法定义会落库并在列表里
		// 正常可见,直到运行时才炸(与 ImportYAML 的严格度不一致)。
		// 备份是不可信输入,这里按"跳过并计数"处理,不阻断其余条目。
		if res := s.app.Engine.Validate(&wf); !res.Valid {
			invalid++
			invalidIDs = append(invalidIDs, wf.ID)
			continue
		}
		wf.Version = 0
		if err := s.app.Repo.SaveWorkflow(&wf, false); err != nil {
			return nil, err
		}
		restoredWf++
	}
	restoredCfg := 0
	invalidCfg := 0
	unknownCfgIDs := []string{}
	applyFailedIDs := []string{}
	for id, raw := range norm.AgentConfigs {
		cfg := agent.AgentConfig{}
		// 配置解不出结构就不能落库:此前无条件写入,解不出时静默跳过应用,
		// 结果是"库里有配置但运行不生效",排查时看不出是哪一步坏的。
		if err := json.Unmarshal(raw, &cfg); err != nil {
			invalidCfg++
			continue
		}
		// 未知 Agent:配置永远不会被应用(applyAgentConfig 对未知 ID
		// 静默跳过),落库只是留一行死数据,还会让"已恢复 N 个"变成假成功
		// —— 用户以为凭据恢复了,实际运行时根本没带上。
		if _, err := s.app.Agents.Get(id); err != nil {
			invalidCfg++
			unknownCfgIDs = append(unknownCfgIDs, id)
			continue
		}
		if err := s.app.Repo.SaveAgentConfig(id, raw); err != nil {
			return nil, err
		}
		// 运行时应用失败 ≠ 已恢复:配置已落库,但适配器实例没换成功 ——
		// 这一路的调用仍走旧实例(旧凭据/旧端点/旧参数)。
		// 此前 `_ = s.app.applyAgentConfig(id, cfg)` 把错误吞掉,照样
		// 计入"已恢复" —— 与上面"未知 Agent"同一类假成功,只是漏网在这里。
		//
		// 处理口径:不计入"已恢复",单独列出失败的 Agent ID(用户能去
		// 逐个排查)。配置本身合法且已落库,下次启动时 reloadAgents 会
		// 重新应用(失败会记 WARN),所以**不回滚**落库 —— 回滚会丢掉
		// 用户刚恢复出来的合法配置。
		if err := s.app.applyAgentConfig(id, cfg); err != nil {
			applyFailedIDs = append(applyFailedIDs, id)
			logx.Warn("备份恢复:Agent 配置应用失败,未计入已恢复",
				"agent", id, "error", err.Error())
			continue
		}
		restoredCfg++
	}
	return map[string]any{
		"workflows_restored":     restoredWf,
		"workflows_skipped":      skipped,
		"workflows_invalid":      invalid,
		"workflows_invalid_ids":  invalidIDs,
		"agent_configs_restored": restoredCfg,
		"agent_configs_invalid":  invalidCfg,
		// 本机不存在的 Agent(配置无法生效,未落库)
		"agent_configs_unknown_ids": unknownCfgIDs,
		// 已落库但运行时应用失败(不算已恢复;下次启动会重试应用)
		"agent_configs_apply_failed_ids": applyFailedIDs,
	}, nil
}

// 日志级别的白名单不在这里另写一份:唯一来源是 logx(应用真正能应用的
// 级别集合)。此前这里有一份独立的白名单,两份必然漂移 —— 漂移方向是
// "用户能选一个后端不认的值"(设置页显示已保存、实际跑的是另一个级别)。
// 校验用 logx.ValidateLevel(不改变当前级别),应用用 logx.SetLevelChecked。

func (s *SettingsService) Update(gitWorkingDir, logLevel string, allowScriptNodes *bool) (*SettingsInfo, error) {
	// 顺序:**先全量校验 → 再统一落库/应用**。
	//
	// 此前先落库并应用 git_working_dir,之后才校验 log_level:
	// 传"非法级别 + 合法目录"时接口返回 400,但目录**已经改了、也落库了** ——
	// 用户看到"保存失败",实际有一半设置生效(约定 14:部分成功被当成失败)。
	//
	// 现在:校验不过则一个字段都不落库、内存态一个都不改。
	// 落库与应用之间仍保持"先落库再改内存"(落库失败时内存态不动,
	// 重启后也不会与界面回显不一致)。
	//
	// 级别语义的唯一来源是 logx(应用真正能应用的级别集合):
	// 校验用 logx.ValidateLevel(不改变当前级别),应用用 logx.SetLevelChecked。
	var level string
	if logLevel != "" {
		if err := logx.ValidateLevel(logLevel); err != nil {
			return nil, model.NewError(model.KindValidationError, "INVALID_LOG_LEVEL", err.Error())
		}
		level = strings.ToLower(strings.TrimSpace(logLevel))
	}
	// git_working_dir 必须**存在且是目录**:此前完全不校验 ——
	// 用户填一个不存在的路径,界面提示"已保存",此后每个 git / script /
	// agent 节点因 cmd.Dir 不存在而失败,报错信息与设置页毫无关联
	// (约定 14:该失败却成功)。
	//
	// 校验放在**落库之前**(与 log_level 同一口径):目录非法时一个字段
	// 都不落库、内存态一个都不改 —— 不能出现"目录没改成、日志级别改成了"
	// 的部分成功。
	//
	// 空串 = 清除(回到启动默认目录),不需要存在性校验。
	if gitWorkingDir != "" {
		if err := validateGitWorkingDir(gitWorkingDir); err != nil {
			return nil, err
		}
	}
	// git_working_dir:**空串 = 清除**(落库空串 + 回到启动默认目录)。
	//
	// 此前 "" 被当作"不修改":用户在设置页清空输入框点保存,后端什么都不做,
	// 但界面提示"已保存" —— 显示与实际不一致(约定 14 的同类问题)。
	// 前端每次保存都回传当前值(Settings.tsx 的 save),所以 "" 只在用户
	// 主动清空时出现。
	//
	// 落库与生效统一交给 Set(Set 内部会再校验一次,幂等):"从设置页改"
	// 与"经 Wails IPC 直接 Set"必须是同一条语义路径,否则两套实现必然
	// 分叉出不同行为(Set 曾是绕过校验的后门,见 Set 的说明)。
	if err := s.Set("git_working_dir", gitWorkingDir); err != nil {
		return nil, err
	}
	if level != "" {
		if err := s.Set("log_level", level); err != nil {
			return nil, err
		}
	}
	if allowScriptNodes != nil {
		if err := s.Set("allow_script_nodes", *allowScriptNodes); err != nil {
			return nil, err
		}
	}
	return s.Info(), nil
}

// validateGitWorkingDir 校验 Git 工作目录:必须**存在且是目录**。
//
// 存在的理由(迭代 20 / P2-4):此前不校验 → 用户填一个不存在的路径,
// 设置页提示"已保存",此后每个 git / script / agent 节点因 cmd.Dir
// 不存在而失败,报错信息(来自子进程)与设置页毫无关联 —— 用户查不到
// "是目录填错了"(约定 14:该失败却成功)。
//
// 校验放在应用层而不是 git/git.go:git 包只负责"设成什么就用什么",
// 不该夹带业务校验;应用层是设置语义的唯一入口。
func validateGitWorkingDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return model.NewError(model.KindValidationError, "INVALID_GIT_WORKING_DIR",
				fmt.Sprintf("Git 工作目录不存在: %s(本次保存未生效:其他设置也没有保存;"+
					"请改成存在的目录,或清空该字段回到默认目录)", dir))
		}
		return model.NewError(model.KindValidationError, "INVALID_GIT_WORKING_DIR",
			fmt.Sprintf("Git 工作目录不可访问: %s(%v)", dir, err))
	}
	if !info.IsDir() {
		return model.NewError(model.KindValidationError, "INVALID_GIT_WORKING_DIR",
			fmt.Sprintf("Git 工作目录不是目录: %s(本次保存未生效:其他设置也没有保存;"+
				"请改成目录路径,或清空该字段回到默认目录)", dir))
	}
	return nil
}
