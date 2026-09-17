// Package application 实现应用服务层(Wails API 与 HTTP API 的统一后端)。
// 前端只依赖这些 Application API,不直接操作底层 Go 对象。
package application

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

	DataDir        string
	LogLevel       string
	DisabledSkills map[string]bool
	// 供 Wails/SSE 桥接实时事件。
	eventsMu      sync.RWMutex
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
	bus := event.NewBus()
	bus.KeepHistory = true

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
		LogLevel:       "info",
		DisabledSkills: map[string]bool{},
		eventHandlers:  map[int]func(event.UIEvent){},
	}
	app.Engine = wfruntime.NewEngine(agents, skills, perms, bus, gitSvc, repo)
	app.Engine.SubworkflowRunner = app.runSubworkflow
	app.Engine.IsSkillDisabled = func(id string) bool { return app.DisabledSkills[id] }

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
		gitSvc.WorkingDir = wd
	}
	// 日志级别持久化
	var lv string
	if err := repo.GetSetting("log_level", &lv); err == nil && lv != "" {
		logx.SetLevel(lv)
	}
	app.LogLevel = lv
	if app.LogLevel == "" {
		app.LogLevel = "info"
	}
	// 禁用 Skill 恢复
	if disabled, err := repo.ListDisabledSkills(); err == nil {
		app.DisabledSkills = disabled
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
			if json.Unmarshal(raw, &cfg) == nil {
				_ = a.applyAgentConfig(id, cfg)
			}
		}
	}
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

// runSubworkflow 同步执行嵌套工作流。
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
		e, err := a.Repo.GetExecution(exec.ID)
		if err == nil {
			switch e.State {
			case model.ExecutionCompleted:
				return map[string]any{"state": "COMPLETED", "nodes": e.NodeStates}, nil
			case model.ExecutionFailed, model.ExecutionCancelled:
				return nil, fmt.Errorf("子工作流失败: %s", e.Error)
			}
		}
		sleepMs(100)
	}
	return nil, fmt.Errorf("子工作流超时")
}

func sleepMs(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = crypto_rand.Read(b)
	return hex.EncodeToString(b)
}

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

// ConnectEvents 把总线事件桥接到 UI 处理器(进程内一次调用)。
func (a *App) ConnectEvents() {
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
	if !res.Valid {
		raw, _ := json.Marshal(res.Errors)
		return nil, model.NewError(model.KindValidationError, "WORKFLOW_INVALID", string(raw))
	}
	if err := s.app.Repo.SaveWorkflow(wf, true); err != nil {
		return nil, err
	}
	return s.app.Repo.GetWorkflow(wf.ID)
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
	return s.app.Engine.Start(context.Background(), wf, task, variables)
}

func (s *ExecutionService) List(workflowID string, limit int) ([]*model.Execution, error) {
	return s.app.Repo.ListExecutions(workflowID, limit)
}

func (s *ExecutionService) Get(id string) (*model.Execution, error) {
	return s.app.Repo.GetExecution(id)
}

func (s *ExecutionService) Nodes(id string) ([]*model.ExecutionNode, error) {
	return s.app.Repo.ListExecutionNodes(id)
}

func (s *ExecutionService) Events(id string) ([]map[string]any, error) {
	return s.app.Repo.ListEvents(id, 0)
}

func (s *ExecutionService) Artifacts(id string) ([]*model.Artifact, error) {
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
	return s.app.Engine.Resume(context.Background(), id, response)
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
	nodes, _ := s.app.Repo.ListExecutionNodes(id)
	events, _ := s.app.Repo.ListEvents(id, 0)
	artifacts, _ := s.app.Repo.ListArtifacts(id)
	return map[string]any{
		"execution": exec,
		"nodes":     nodes,
		"events":    events,
		"artifacts": artifacts,
	}, nil
}

// Delete 删除执行记录(级联删除节点/事件/制品)。
func (s *ExecutionService) Delete(id string) error {
	return s.app.Repo.DeleteExecution(id)
}

// RetryNode 重试失败节点或跳过继续(skip=true)。
func (s *ExecutionService) RetryNode(id string, skip bool) (*model.Execution, error) {
	return s.app.Engine.RetryNode(context.Background(), id, skip)
}

// ---- AgentService ----

// AgentService Agent 管理与权限配置。
type AgentService struct {
	app *App
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
	var configs map[string]map[string]any
	_ = s.app.Repo.GetSetting("agent_policies", &configs)
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

// GetConfig 返回某 Agent 的运行配置(环境变量值已掩码为 ***)。
func (s *AgentService) GetConfig(id string) (agent.AgentConfig, error) {
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
			Enabled: !s.app.DisabledSkills[sk.ID()],
		})
	}
	return out
}

// SetEnabled 启用/禁用 Skill(禁用后运行时拒绝调用)。
func (s *SkillService) SetEnabled(id string, enabled bool) error {
	if _, err := s.app.Skills.Get(id); err != nil {
		return err
	}
	if enabled {
		delete(s.app.DisabledSkills, id)
	} else {
		s.app.DisabledSkills[id] = true
	}
	return s.app.Repo.SaveSkillEnabled(id, enabled)
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
func (s *GitService) SetWorkingDir(dir string) error {
	s.app.GitSvc.WorkingDir = dir
	return s.app.Repo.SaveSetting("git_working_dir", dir)
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
}

// Info 返回当前设置。
func (s *SettingsService) Info() *SettingsInfo {
	var wd string
	_ = s.app.Repo.GetSetting("git_working_dir", &wd)
	if wd == "" {
		wd = s.app.GitSvc.WorkingDir
	}
	return &SettingsInfo{
		DataDir:       s.app.DataDir,
		GitWorkingDir: wd,
		LogLevel:      s.app.LogLevel,
	}
}

// Update 更新设置:git_working_dir / log_level(日志级别即时生效)。
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

	restoredWf, skipped := 0, 0
	for _, raw := range norm.Workflows {
		wf := model.Workflow{}
		if err := json.Unmarshal(raw, &wf); err != nil || wf.ID == "" {
			continue
		}
		if existing, err := s.app.Repo.GetWorkflow(wf.ID); err == nil && existing != nil {
			skipped++
			continue
		}
		wf.Version = 0
		if err := s.app.Repo.SaveWorkflow(&wf, false); err != nil {
			return nil, err
		}
		restoredWf++
	}
	restoredCfg := 0
	for id, raw := range norm.AgentConfigs {
		if err := s.app.Repo.SaveAgentConfig(id, raw); err != nil {
			return nil, err
		}
		cfg := agent.AgentConfig{}
		if json.Unmarshal(raw, &cfg) == nil {
			_ = s.app.applyAgentConfig(id, cfg)
		}
		restoredCfg++
	}
	return map[string]any{"workflows_restored": restoredWf, "workflows_skipped": skipped, "agent_configs_restored": restoredCfg}, nil
}

func (s *SettingsService) Update(gitWorkingDir, logLevel string) (*SettingsInfo, error) {
	if gitWorkingDir != "" {
		s.app.GitSvc.WorkingDir = gitWorkingDir
		_ = s.Set("git_working_dir", gitWorkingDir)
	}
	if logLevel != "" {
		s.app.LogLevel = logLevel
		logx.SetLevel(logLevel)
		_ = s.Set("log_level", logLevel)
	}
	return s.Info(), nil
}
