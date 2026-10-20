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
	app.reloadAgents()

	// 服务层
	app.Workflows = &WorkflowService{app: app}
	app.Executions = &ExecutionService{app: app}
	app.AgentSvc = &AgentService{app: app}
	app.SkillSvc = &SkillService{app: app}
	app.GitAPI = &GitService{app: app}
	app.Settings = &SettingsService{app: app}

	// 工作目录设置
	var wd string
	if err := repo.GetSetting("git_working_dir", &wd); err == nil && wd != "" {
		gitSvc.SetWorkingDir(wd)
	}
	// 日志级别持久化
	var lv string
	if err := repo.GetSetting("log_level", &lv); err == nil && lv != "" {
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
	var allowScript bool
	if err := repo.GetSetting("allow_script_nodes", &allowScript); err == nil {
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
	switch id {
	case "claude-code":
		return a.Agents.Replace(claude.New(claude.Config{
			Perms:                 a.Perms,
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
// behavior 为 nil 表示使用内置默认脚本;非 nil 时按 mode 合并覆盖默认。
func buildMock(id string, behavior map[string]any) agent.Agent {
	name := id
	switch id {
	case "mock-claude":
		name = "Mock Claude(演示)"
	case "mock-pi":
		name = "Mock Pi(演示)"
	}
	merged := defaultMockBehavior(id)
	if behavior != nil {
		for mode, raw := range behavior {
			merged[mode] = raw
		}
	}
	scripts := map[string]*mock.Script{}
	for mode, raw := range merged {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sc := &mock.Script{}
		if arr, ok := m["decisions"].([]any); ok {
			for _, d := range arr {
				sc.Decisions = append(sc.Decisions, agent.AgentDecision(strings.ToUpper(fmt.Sprint(d))))
			}
		}
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

// reloadAgents 根据持久化配置注册 Agent 适配器。
// 注意:必须复用传入 Engine 的同一个 permission.Manager 实例。
func (a *App) reloadAgents() {
	// Claude Code 适配器
	claudeAgt := claude.New(claude.Config{Perms: a.Perms})
	_ = a.Agents.Register(claudeAgt)

	// Pi Agent 适配器
	piAgt := pi.New(pi.Config{Perms: a.Perms})
	_ = a.Agents.Register(piAgt)

	// Mock Agents(演示与测试:不依赖外部 CLI,内置默认演示脚本)
	_ = a.Agents.Register(buildMock("mock-claude", nil))
	_ = a.Agents.Register(buildMock("mock-pi", nil))
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
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
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

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = crypto_rand.Read(b)
	return hex.EncodeToString(b)
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

// Save 创建或更新(传入带 ID 的 workflow;存在则版本+1)。
func (s *WorkflowService) Save(wf *model.Workflow) (*model.Workflow, error) {
	if wf.ID == "" {
		wf.ID = "wf-" + strings.ToLower(randHex(6))
	}
	// 保存前必须校验
	res := s.app.Engine.Validate(wf)
	// 警告此前被整个丢掉(只回传 res.Errors):DEPRECATED_NODE_CONFIG 这类
	// 警告只有显式 validate 端点看得见,直连 API 的调用方看不到 ——
	// 静默丢失(约定 #14 同族)。这里先在应用层记进日志(不丢);
	// 要让调用方也拿到,需要改 API 契约(见交付报告)。
	logValidationWarnings("save", wf.ID, res)
	if !res.Valid {
		raw, _ := json.Marshal(res.Errors)
		return nil, model.NewError(model.KindValidationError, "WORKFLOW_INVALID", string(raw))
	}
	if err := s.app.Repo.SaveWorkflow(wf, true); err != nil {
		return nil, err
	}
	return s.app.Repo.GetWorkflow(wf.ID)
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
	wf.ID = "wf-" + strings.ToLower(randHex(6))
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
func (s *WorkflowService) ImportYAML(content string) (*model.Workflow, error) {
	doc, err := dsl.Parse([]byte(content))
	if err != nil {
		return nil, err
	}
	wf := doc.ToModel()
	if existing, err := s.app.Repo.GetWorkflow(wf.ID); err == nil && existing != nil {
		wf.ID = "wf-" + strings.ToLower(randHex(6))
	}
	wf.Version = 0
	return s.Save(wf)
}

// Validate 返回结构化校验结果。
func (s *WorkflowService) Validate(wf *model.Workflow) *validator.Result {
	return s.app.Engine.Validate(wf)
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

// Run 启动执行(异步,立即返回 Execution 记录)。
// 使用脱离请求的生命周期 context:HTTP 响应返回后执行继续在后台进行。
func (s *ExecutionService) Run(ctx context.Context, workflowID, task string, variables map[string]any) (*model.Execution, error) {
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
	// 这里只为把警告记出来,不改变判定结果。
	logValidationWarnings("run", workflowID, s.app.Engine.Validate(wf))
	exec, err := s.app.Engine.Start(context.Background(), wf, task, variables)
	if err != nil {
		return nil, err
	}
	// 返回的是引擎的深拷贝(cloneExec),脱敏不影响运行期。
	maskExecution(exec)
	return exec, nil
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

func (s *ExecutionService) Artifacts(id string) ([]*model.Artifact, error) {
	if _, err := s.app.Repo.GetExecution(id); err != nil {
		return nil, err
	}
	return s.app.Repo.ListArtifacts(id)
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
	return map[string]any{
		"execution": exec,
		"nodes":     nodes,
		"events":    events,
		"artifacts": artifacts,
	}, nil
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
	// 合并掩码值:*** → 沿用已存值
	stored, err := s.app.loadAgentConfig(id)
	if err != nil {
		return err
	}
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
	cfg := agent.AgentConfig{}
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

func (s *SettingsService) Set(key string, value any) error {
	return s.app.Repo.SaveSetting(key, value)
}

// SettingsInfo 是设置页展示的只读信息 + 可修改项。
type SettingsInfo struct {
	DataDir       string `json:"data_dir"`
	GitWorkingDir string `json:"git_working_dir"`
	LogLevel      string `json:"log_level"`
	// AllowScriptNodes 放行 script 节点(任意本地命令执行),默认 false。
	AllowScriptNodes bool `json:"allow_script_nodes"`
}

// Info 返回当前设置。
func (s *SettingsService) Info() *SettingsInfo {
	var wd string
	_ = s.app.Repo.GetSetting("git_working_dir", &wd)
	if wd == "" {
		wd = s.app.GitSvc.WorkingDir()
	}
	return &SettingsInfo{
		DataDir:          s.app.DataDir,
		GitWorkingDir:    wd,
		LogLevel:         s.app.LogLevelValue(),
		AllowScriptNodes: s.app.ScriptNodesAllowed(),
	}
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
		_ = s.app.applyAgentConfig(id, cfg)
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
	}, nil
}

// 日志级别的白名单不在这里另写一份:唯一来源是 logx(应用真正能应用的
// 级别集合)。此前这里有一份独立的白名单,两份必然漂移 —— 漂移方向是
// "用户能选一个后端不认的值"(设置页显示已保存、实际跑的是另一个级别)。
// 校验用 logx.ValidateLevel(不改变当前级别),应用用 logx.SetLevelChecked。

func (s *SettingsService) Update(gitWorkingDir, logLevel string, allowScriptNodes *bool) (*SettingsInfo, error) {
	// 先落库再改内存:此前先改内存、持久化错误被忽略,界面提示"已保存"
	// 但重启即丢失。
	if gitWorkingDir != "" {
		if err := s.Set("git_working_dir", gitWorkingDir); err != nil {
			return nil, err
		}
		s.app.GitSvc.SetWorkingDir(gitWorkingDir)
	}
	if logLevel != "" {
		// 级别语义的唯一来源是 logx(应用真正能应用的级别集合):
		// 此前这里另写了一份白名单,两份必然漂移 —— 漂移方向是
		// "用户能选一个后端不认的值"(设置页显示已保存、实际跑另一个级别)。
		//
		// 顺序:先校验(不落库)→ 再落库 → 最后应用。
		//   - 校验在落库之前:非法级别不许进库(库里躺着一个应用不了的
		//     级别,重启后仍然无效);
		//   - 应用在落库之后:与既有"先落库再改内存"的约定一致,
		//     落库失败时内存与全局日志级别都不动。
		if err := logx.ValidateLevel(logLevel); err != nil {
			return nil, model.NewError(model.KindValidationError, "INVALID_LOG_LEVEL", err.Error())
		}
		level := strings.ToLower(strings.TrimSpace(logLevel))
		if err := s.Set("log_level", level); err != nil {
			return nil, err
		}
		s.app.setLogLevel(level)
		// SetLevelChecked 是"应用级别"的唯一入口:非法时保持当前级别并报错,
		// 错误真正回报给调用方(接口层 400),不再只是 WARN。
		if err := logx.SetLevelChecked(level); err != nil {
			return nil, model.NewError(model.KindValidationError, "INVALID_LOG_LEVEL", err.Error())
		}
	}
	if allowScriptNodes != nil {
		if err := s.Set("allow_script_nodes", *allowScriptNodes); err != nil {
			return nil, err
		}
		s.app.scriptNodes.Store(*allowScriptNodes)
		logx.Warn("script 节点放行开关已变更", "allow_script_nodes", *allowScriptNodes)
	}
	return s.Info(), nil
}
