// Package runtime 是构建在 ADK 之上的 Workflow Runtime。
// Engine 负责:执行生命周期、状态机、HITL 暂停/恢复、取消与崩溃恢复。
// 工作流结构的真正执行由 ADK Runner / Workflow Agents 完成。
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/contextx"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/logx"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/template"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
	"agentworkflow/workflow/validator"
)

// TemplateRenderer 是 prompt 模板渲染器。
//
// 运行期只用带错误返回的入口(RenderErr):渲染失败必须让节点失败,
// 不能静默回退成原始模板 —— 否则用户以为提示词生效了,实际 Agent 收到
// 的是一段**没渲染过的模板文本**,还照常返回 SUCCESS(约定 14)。
//
// 抽成接口而不是直接用 *template.Engine,原因只有一个:让"渲染失败 →
// 节点失败"这条路径可测。template.Engine 的失败入口是渲染期 panic,
// 而普通 DSL 输入无法稳定触发它(实测:畸形模板 "{{v"、会 panic 的
// Formatter、空数据,RenderErr 一律返回 nil error),没有缝就只能靠
// 注入一个会失败的渲染器来验证这条路径。
type TemplateRenderer interface {
	// Render 渲染模板并报告缺失变量(渲染失败时回退原始模板)。
	//
	// 运行期**不用**它(渲染失败必须可见,不能回退原始模板),保留这个
	// 方法是为了不缩窄 Engine.Tpl 原来的公开能力:它是 *template.Engine
	// 的方法,外部调用方不该因为这次收口而编译不过。
	Render(tpl string, data map[string]any) (string, []string)
	// RenderErr 渲染模板,渲染失败时返回 error。
	RenderErr(tpl string, data map[string]any) (string, []string, error)
}

// 编译期断言:默认实现必须满足接口。
var _ TemplateRenderer = (*template.Engine)(nil)

// Engine 依赖注入:全部通过构造函数传入,无全局状态。
type Engine struct {
	Agents coreagent.Registry
	Skills skill.Registry
	Perms  *permission.Manager
	CtxMgr *contextx.Manager
	Tpl    TemplateRenderer
	Bus    *event.Bus
	Git    *git.Service
	Repo   *persistence.DB
	// SubworkflowRunner 支持嵌套工作流(由 application 层注入,避免循环依赖)。
	SubworkflowRunner func(ctx context.Context, workflowID, task string, input map[string]any) (map[string]any, error)
	// IsSkillDisabled 由 application 层注入(检查 Skill 启用状态)。
	IsSkillDisabled func(id string) bool
	// AllowScriptNodes 由 application 层注入(设置页「允许执行 script 节点」)。
	// script 节点以 /bin/sh -c 执行 DSL 里的任意命令,而工作流可以来自
	// YAML 导入,默认关闭:钩子未注入时一律拒绝执行(fail closed)。
	AllowScriptNodes func() bool

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	// running 统计正在执行的 execute() 协程(优雅关闭等待用)。
	running atomic.Int64
	// eventPersistFailures 统计事件落库失败次数。
	// 事件落库失败 = 审计流缺记录,必须可观测(不能静默丢弃)。
	eventPersistFailures atomic.Int64
	// escalateExpected / escalateUnexpected 统计 ADK Escalate 事件的分类:
	// 预期内(循环正常出口 / HITL 暂停)与异常(失败 / 循环上限 / 停滞)。
	escalateExpected   atomic.Int64
	escalateUnexpected atomic.Int64
	// persistFailGuard 防止"落库失败 → 发 runtime.error 事件 → 该事件
	// 落库又失败 → 再发 …"的递归(发事件时置位,期间不再发)。
	persistFailGuard atomic.Bool
	// stateCommitFailures 统计"状态迁移未落库"的次数(CAS 冲突或落库错误)。
	// 状态没落库 = 事件流与 DB 可能不一致(约定 #14),必须可观测。
	stateCommitFailures atomic.Int64
}

// EscalateStats 返回 Escalate 事件分类计数(预期内 / 异常)。
// 正常跑通的工作流应当 expected > 0 且 unexpected == 0。
func (e *Engine) EscalateStats() (int64, int64) {
	return e.escalateExpected.Load(), e.escalateUnexpected.Load()
}

// EventPersistFailures 返回事件落库失败次数(审计流缺记录的计数)。
func (e *Engine) EventPersistFailures() int64 { return e.eventPersistFailures.Load() }

// StateCommitFailures 返回状态迁移未落库的次数(CAS 冲突/落库错误)。
// 计数 > 0 说明有过"状态没落库"的时刻 —— 事件流与 DB 在那一刻不一致,
// 靠"不发终态事件"保持自洽(见 commitTerminal)。
func (e *Engine) StateCommitFailures() int64 { return e.stateCommitFailures.Load() }

// RunningCount 返回正在执行的执行协程数。
func (e *Engine) RunningCount() int64 { return e.running.Load() }

// WaitIdle 等待全部执行协程收尾,超时返回当前计数。
func (e *Engine) WaitIdle(timeout time.Duration) int64 {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e.RunningCount() == 0 {
			return 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	return e.RunningCount()
}

// NewEngine 创建运行时引擎。
func NewEngine(agents coreagent.Registry, skills skill.Registry, perms *permission.Manager,
	bus *event.Bus, gitSvc *git.Service, repo *persistence.DB) *Engine {
	e := &Engine{
		Agents:  agents,
		Skills:  skills,
		Perms:   perms,
		CtxMgr:  contextx.NewManager(),
		Tpl:     template.NewEngine(),
		Bus:     bus,
		Git:     gitSvc,
		Repo:    repo,
		cancels: map[string]context.CancelFunc{},
	}
	// 事件持久化:所有携带 ExecutionID 的事件写入 SQLite(审计/恢复/UI 回放)
	//
	// 保持**同步**落库:事件顺序即状态顺序是总线层的刻意契约
	// (见 event.Bus.Emit 的说明),而且崩溃场景下"已发出的事件必须已经
	// 落库" —— 异步化会让进程被强杀时丢掉最后一段审计记录,恰好是
	// 最需要审计的时刻。代价是 DB 慢会拖慢执行协程,但单连接 WAL 下
	// 一次 INSERT 是亚毫秒级,相对一次 Agent 调用可忽略。
	// 该契约由 TestEventPersistedSynchronously 钉死。
	bus.Subscribe(func(ev event.UIEvent) {
		e.persistEvent(ev)
	})
	return e
}

// persistEvent 把事件写入 SQLite(审计/恢复/回放)。
//
// 落库失败必须**可观测**:此前是 `_ = repo.SaveEvent(ev)` 静默丢弃,
// DB 故障时审计流凭空缺记录,事后无法判断"是没发生"还是"没记下来"。
// 现在:计数 + ERROR 日志 + 发一条 runtime.error 事件。
//
// 落库失败**不中断**工作流执行:事件持久化失败不是执行失败,把它打成
// FAILED 会把"审计流断了"变成"工作流挂了",制造新的状态混乱
// (约定 #14 的反面:不该失败的别失败)。真正的执行失败由节点状态与
// KeyAbort 裁决,与此无关。
func (e *Engine) persistEvent(ev event.UIEvent) {
	if ev.ExecutionID == "" || e.Repo == nil {
		return
	}
	if err := e.Repo.SaveEvent(ev); err != nil {
		e.onEventPersistFailed(ev, err)
	}
}

// onEventPersistFailed 处理事件落库失败:计数 + 日志 + 发 runtime.error。
//
// 发事件而不是只记日志:落库失败时审计流已经断了,运维/用户还需要
// 一个"活的"信号(SSE/UI 能收到)。用 persistFailGuard 防递归 ——
// runtime.error 事件自己也会落库失败,没有护栏就是无限发事件。
func (e *Engine) onEventPersistFailed(ev event.UIEvent, err error) {
	e.eventPersistFailures.Add(1)
	logx.Error("事件落库失败(审计流缺记录)", "type", ev.Type,
		"execution", ev.ExecutionID, "node", ev.NodeID, "error", err.Error())
	if !e.persistFailGuard.CompareAndSwap(false, true) {
		// 已在处理落库失败:不再发事件(否则 runtime.error 自己落库失败
		// 又触发一次,无限递归)。
		return
	}
	defer e.persistFailGuard.Store(false)
	e.Bus.Emit(event.New("runtime.error", ev.ExecutionID, ev.NodeID, map[string]any{
		"reason":        "event_persist_failed",
		"event_type":    ev.Type,
		"persist_error": err.Error(),
	}))
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// Validate 在运行前再次校验(注册表信息已完备)。
func (e *Engine) Validate(wf *model.Workflow) *validator.Result {
	return validator.Validate(wf,
		func(id string) bool { _, err := e.Agents.Get(id); return err == nil },
		func(id string) bool { _, err := e.Skills.Get(id); return err == nil },
		func(id string) bool { _, err := e.Repo.GetWorkflow(id); return err == nil },
	)
}

// Start 创建并异步执行一个 Execution。
// 返回创建好的 Execution(状态 RUNNING),执行在后台进行。
func (e *Engine) Start(ctx context.Context, wf *model.Workflow, task string, variables map[string]any) (*model.Execution, error) {
	if res := e.Validate(wf); !res.Valid {
		// 错误消息里带上**具体**的校验原因:此前只有"工作流校验失败",
		// 用户既定位不到问题也改不了(例如 permissions 段写错时,
		// 错误文本不指明是哪一段)—— 死路。
		err := model.NewError(model.KindValidationError, "WORKFLOW_INVALID",
			"工作流校验失败: "+validator.Summary(res.Errors))
		raw, _ := json.Marshal(res.Errors)
		err.Detail = string(raw)
		return nil, err
	}
	// 重置本次工作流引用的可重置 Agent(如 Mock),使决策序列从头开始。
	// 只重置被引用的:嵌套工作流启动不得干扰并发运行的其他执行
	// (如父工作流的 Mock 决策序列被中途清零)。
	//
	// 注意:AgentRequest 带 ExecutionID,mock 的计数按执行隔离,所以同
	// 一工作流并发启动两次也互不干扰;此处只重置无执行上下文的共享
	// 空间(单测 / Agents 页 Test),不会清掉正在运行的执行的状态。
	refs := map[string]bool{}
	for i := range wf.Nodes {
		if id, ok := wf.Nodes[i].Config["agent"].(string); ok {
			refs[id] = true
		}
	}
	for _, a := range e.Agents.List() {
		if !refs[a.ID()] {
			continue
		}
		if r, ok := a.(interface{ ResetMock() }); ok {
			r.ResetMock()
		}
	}

	now := time.Now().Format(time.RFC3339Nano)
	// 变量合并:DSL variables: 段为底,运行请求传入的 variables 覆盖。
	// 此前只取请求参数,DSL 里声明的 variables 完全不生效 ——
	// {{variable.xxx}} 渲染空串、`variables.xxx == ...` 条件恒假,
	// 而 DSL 解析与导出都保留了这段(需求 §12 示例即用它)。
	vars := map[string]any{}
	for k, v := range wf.Variables {
		vars[k] = v
	}
	for k, v := range variables {
		vars[k] = v
	}
	exec := &model.Execution{
		ID:              newID("exec"),
		WorkflowID:      wf.ID,
		WorkflowVersion: wf.Version,
		WorkflowName:    wf.Name,
		State:           model.ExecutionCreated,
		Task:            task,
		Variables:       vars,
		Iterations:      map[string]int{},
		NodeStates:      map[string]string{},
		CreatedAt:       now,
		Snapshot:        cloneWorkflow(wf),
	}
	if exec.Variables == nil {
		exec.Variables = map[string]any{}
	}
	if err := e.transition(exec, model.ExecutionRunning); err != nil {
		return nil, err
	}
	exec.StartedAt = now
	// 首次写入:整行插入(此刻行还不存在,状态列随行一起落定)。
	// 之后所有状态变更一律走 CAS,整行写不再碰 state 列。
	if err := e.insertExecution(exec); err != nil {
		return nil, err
	}
	e.Bus.Emit(event.New(event.WorkflowStarted, exec.ID, "", map[string]any{
		"workflow": wf.Name, "task": task,
	}))

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[exec.ID] = cancel
	e.mu.Unlock()

	// 返回派发前的快照:goroutine 会并发改写 exec,调用方(如 REST 响应
	// 的 JSON 序列化)不得与它共享可变结构
	snapshot := cloneExec(exec)
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancels, exec.ID)
			e.mu.Unlock()
		}()
		e.execute(runCtx, exec, "", nil)
	}()
	return snapshot, nil
}

// Resume 恢复一个 WAITING_USER/PAUSED 的执行(人工输入后)。
func (e *Engine) Resume(ctx context.Context, executionID string, userResponse map[string]any) (*model.Execution, error) {
	exec, err := e.loadExec(executionID)
	if err != nil {
		return nil, err
	}
	if exec.State != model.ExecutionWaitingUser && exec.State != model.ExecutionPaused {
		return nil, model.NewError(model.KindStateError, "INVALID_RESUME",
			fmt.Sprintf("执行 %s 状态为 %s,不能恢复", executionID, exec.State))
	}
	// 数据库级 CAS:并发 Resume(如双击批准)只允许一个调用方成功,
	// 否则同一执行会被两份 goroutine 同时重放。
	//
	// CAS 是**唯一**的状态写入口:此后不再有整行写覆盖 state 列,
	// 因此"CAS 成功 → 整行写"之间的窗口里即便有并发写,也不会把状态
	// 改回去(状态列根本不在这条 UPDATE 里)。
	ok, err := e.Repo.CasExecutionState(executionID,
		[]model.ExecutionState{model.ExecutionWaitingUser, model.ExecutionPaused}, model.ExecutionRunning)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, model.NewError(model.KindStateError, "INVALID_RESUME",
			fmt.Sprintf("执行 %s 正在被恢复或状态已变化", executionID))
	}
	exec.State = model.ExecutionRunning
	e.Bus.Emit(event.New(event.HumanInputReceived, exec.ID, exec.CurrentNodeID, userResponse))
	e.save(exec)
	e.Bus.Emit(event.New(event.WorkflowResumed, exec.ID, exec.CurrentNodeID, nil))

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[exec.ID] = cancel
	e.mu.Unlock()
	snapshot := cloneExec(exec)
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancels, exec.ID)
			e.mu.Unlock()
		}()
		e.execute(runCtx, exec, "resume", userResponse)
	}()
	return snapshot, nil
}

// Cancel 取消执行(运行中的通过 context 取消;等待用户的直接标记取消)。
func (e *Engine) Cancel(ctx context.Context, executionID string) error {
	e.mu.Lock()
	cancel, running := e.cancels[executionID]
	e.mu.Unlock()
	if running {
		cancel()
		return nil
	}
	// 非运行路径用 CAS:并发取消只产生一次状态变更与事件
	ok, err := e.Repo.CasExecutionState(executionID,
		[]model.ExecutionState{model.ExecutionCreated, model.ExecutionPaused, model.ExecutionWaitingUser},
		model.ExecutionCancelled)
	if err != nil {
		return err
	}
	if !ok {
		exec, err := e.loadExec(executionID)
		if err != nil {
			return err
		}
		return model.NewError(model.KindStateError, "INVALID_TRANSITION",
			fmt.Sprintf("执行 %s 状态为 %s,不能取消", executionID, exec.State))
	}
	e.Bus.Emit(event.New(event.WorkflowCancelled, executionID, "", nil))
	return nil
}

// setExecState 写执行记录的状态快照 —— **状态写入的统一入口**。
//
// 为什么必须有统一入口:
//
//  1. 落库脱敏按**键分类**进行(见 state_mask.go):新增状态键若漏登记,
//     会按"默认拒绝"被当自由文本脱敏 → 运行期读到的值被改写
//     (约定 #14 同族:执行语义悄悄变掉)。把状态写入收敛到一处,
//     "改了状态键"这件事才可审计(TestStateWritesAreClassified 也据此
//     扫得到每一个写入点)。
//  2. 直接对 exec.StateData 下标赋值在 map 为 nil 时会 panic
//     ("assignment to entry in nil map"):内存里新建的执行记录
//     (Start 路径、备份恢复、测试构造)的 StateData 就是 nil。
//     RetryNode 跑在**调用方** goroutine 里(不在 execute 的兜底
//     recover 覆盖范围内),panic 会一路上抛(约定 #12)。
//     (从库里读回的记录不会是 nil,但统一入口不能依赖"调用方恰好
//     先读过一遍库"。)
func setExecState(exec *model.Execution, key string, val any) {
	if exec == nil {
		return
	}
	if exec.StateData == nil {
		exec.StateData = map[string]any{}
	}
	exec.StateData[key] = val
}

// RetryNode 对失败执行发起恢复:重试失败节点(skip=false)或跳过它(skip=true)。
// 已成功的节点在重放时自动跳过;FAILED→RUNNING 是用户显式重试的合法转换。
func (e *Engine) RetryNode(ctx context.Context, executionID string, skip bool) (*model.Execution, error) {
	exec, err := e.loadExec(executionID)
	if err != nil {
		return nil, err
	}
	if exec.State != model.ExecutionFailed {
		return nil, model.NewError(model.KindStateError, "INVALID_RETRY",
			fmt.Sprintf("执行 %s 状态为 %s,仅 FAILED 可重试", executionID, exec.State))
	}
	nodeID := exec.CurrentNodeID
	if nodeID == "" {
		return nil, model.NewError(model.KindStateError, "NO_FAILED_NODE", "无法定位失败节点")
	}
	// 数据库级 CAS:并发重试只允许一个调用方成功。
	//
	// 状态与元信息分两步落:StateData 的修改要先知道 CAS 赢了才能做,
	// 所以顺序不能反。但元信息写入**不含状态列**,不会推翻 CAS 的裁决
	// ——"重试中"与"失败节点的旧状态"短暂不一致只影响展示,不影响裁决。
	ok, err := e.Repo.CasExecutionState(executionID,
		[]model.ExecutionState{model.ExecutionFailed}, model.ExecutionRunning)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, model.NewError(model.KindStateError, "INVALID_RETRY",
			fmt.Sprintf("执行 %s 正在被重试或状态已变化", executionID))
	}
	exec.State = model.ExecutionRunning
	if skip {
		setExecState(exec, compiler.StatusKey(nodeID), string(model.NodeSkipped))
	} else {
		delete(exec.StateData, compiler.StatusKey(nodeID))
	}
	delete(exec.StateData, compiler.KeyAbort)
	delete(exec.StateData, "_error")
	exec.Error = ""

	e.save(exec)
	e.Bus.Emit(event.New(event.WorkflowResumed, exec.ID, nodeID, map[string]any{
		"retry": !skip, "skip": skip,
	}))

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[exec.ID] = cancel
	e.mu.Unlock()
	snapshot := cloneExec(exec)
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancels, exec.ID)
			e.mu.Unlock()
		}()
		e.execute(runCtx, exec, "resume", nil)
	}()
	return snapshot, nil
}

// RecoverPending 在应用启动时调用:把上次崩溃遗留的 RUNNING 执行标记为
// FAILED(可再次运行),WAITING_USER/PAUSED 保持可恢复状态。
func (e *Engine) RecoverPending() error {
	pending, err := e.Repo.ListRunningExecutions()
	if err != nil {
		return err
	}
	for _, exec := range pending {
		if err := e.recoverExecution(exec); err != nil {
			logx.Warn("崩溃恢复标记失败(状态已被并发改写)",
				"execution", exec.ID, "error", err.Error())
		}
	}
	return nil
}

// recoverExecution 把一条"崩溃遗留"的执行标记为 FAILED(可重试)。
//
// 用 CAS 而不是整行写:列表是在稍早时刻取的,期间执行可能已经跑完
// (COMPLETED)。整行写会把"已完成"覆盖成"失败" —— 用户看到"失败",
// 实际早就成功了(约定 #14 同族问题:该失败却成功 / 该成功却失败)。
// CAS 失败 = 状态已被别人改走,此时不动它。
func (e *Engine) recoverExecution(exec *model.Execution) error {
	switch exec.State {
	case model.ExecutionRunning, model.ExecutionCreated:
		exec.Error = "应用重启导致执行中断,可重新运行"
		return e.transitionAndSave(exec, model.ExecutionFailed)
	case model.ExecutionWaitingUser, model.ExecutionPaused:
		// 保持状态,可通过 Resume 继续
		return nil
	}
	return nil
}

// execute 是执行主循环:编译 → ADK Runner 执行 → 收尾。
func (e *Engine) execute(ctx context.Context, exec *model.Execution, mode string, userInput map[string]any) {
	e.running.Add(1)
	defer e.running.Add(-1)
	// 兜底:执行在独立 goroutine 里跑,节点适配器/模板/ADK 内部任意 panic
	// 都会带走整个进程(桌面模式即整应用退出,且该执行在库中永久停留
	// RUNNING)。统一捕获后落库为 FAILED 并发事件,便于诊断与重试。
	// 注册在 running 之后 → panic 时先恢复,再递减运行计数。
	defer func() {
		if r := recover(); r != nil {
			logx.Error("执行 panic(已兜底)", "execution", exec.ID,
				"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			e.failExecution(exec, fmt.Sprintf("执行 panic: %v", r))
		}
	}()

	// snapshot_json 为空/损坏时 GetExecution 会静默留 nil(历史数据或
	// 写入中断),此处显式失败而不是在下面解引用时 panic。
	if exec.Snapshot == nil {
		e.failExecution(exec, "执行缺少工作流快照(snapshot 损坏),无法继续;请重新运行该工作流")
		return
	}

	// 工作流级单轮执行超时
	if ts := exec.Snapshot.Settings.TimeoutSeconds; ts > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(ts)*time.Second)
		defer cancel()
	}
	// 工作流级权限覆盖:克隆全局策略后应用(不影响其他运行中的执行)
	runPerms := e.Perms.Clone()
	if len(exec.Snapshot.Permission) > 0 {
		// 非法结构 → **明确失败**,而不是"静默把该 Agent 的权限全拒"。
		//
		// 两种做法的差别:全拒+WARN 时,用户看到的失败原因是
		// "agent 无权执行 fs:write",而真正的原因是"permissions 段写错了"
		// —— 排查方向被带偏(约定 #14:静默降级与"该失败却成功"同族)。
		// 这里把原因原样挂到执行失败原因上。
		//
		// 校验期已拦住非法结构(workflow/validator:INVALID_PERMISSION_OVERRIDE,
		// 同一份解析逻辑),这里的检查是纵深防御:历史快照(规则之前保存的
		// 工作流、备份恢复出来的执行)未必经过当次校验。
		if err := runPerms.ApplyChecked(exec.Snapshot.Permission); err != nil {
			e.failExecution(exec, "工作流权限覆盖非法,已终止执行: "+err.Error())
			return
		}
	}

	env := &compiler.RunEnv{
		Exec:          exec,
		WF:            exec.Snapshot,
		Runner:        e,
		Bus:           e.Bus,
		Vars:          exec.Variables,
		MaxIterations: loopLimit(exec.Snapshot),
		Perms:         runPerms,
	}
	// 节点检查点:把"当前在跑哪个节点"落库(节流见 compiler 侧)。
	// 此前 CurrentNodeID 只存在于内存,运行中崩溃时库中仍是 Start 时的
	// 空值 → RecoverPending 标 FAILED 后 RetryNode 恒返回
	// NO_FAILED_NODE("无法定位失败节点"),崩溃恢复后无法从失败处重试。
	env.OnNodeCheckpoint = func(nodeID string) {
		// 状态仍写 RUNNING(执行没结束);只更新定位信息。
		// 失败时静默:检查点只是尽力而为的诊断信息,不能因为它失败
		// 而中断工作流(真正致命的问题由事件/日志暴露)。
		_ = e.Repo.TouchExecutionNode(exec.ID, nodeID)
	}

	root, err := compiler.Compile(env)
	if err != nil {
		e.failExecution(exec, err.Error())
		return
	}

	sessSvc := session.InMemoryService()
	userID, sessionID := "local", exec.ID

	// 初始状态:任务/变量;恢复模式时回放持久化的状态快照
	initialState := map[string]any{
		compiler.KeyTask: exec.Task,
		"variables":      exec.Variables,
	}
	if mode == "resume" {
		// 回放的是**已脱敏**的状态快照(state_json 落库前按键分类脱敏,
		// 见 state_mask.go):自由文本键(output / node:<id>:result …)在这里
		// 是脱敏副本,结构化键(decision / node:<id>:status …)原样保留。
		//
		// 口径差:output 同时是条件变量,于是**恢复时**的条件在脱敏文本上
		// 求值,而**实时运行**时在原文上求值。影响面窄(仅当输出含凭据
		// 形态子串且条件恰好检查那一段),成因与取舍见 state_mask.go 的
		// "output 键的双重身份"一节 —— 两条路要么泄漏凭据、要么改掉
		// 条件变量集合,无法完美消除,只能写明。
		for k, v := range exec.StateData {
			initialState[k] = v
		}
		// 清除中断标记,注入用户输入
		delete(initialState, compiler.KeyAbort)
		initialState[compiler.KeyUserInput] = userInput
	}
	if _, err := sessSvc.Create(ctx, &session.CreateRequest{
		AppName: "agent-workflow", UserID: userID, SessionID: sessionID, State: initialState,
	}); err != nil {
		e.failExecution(exec, "创建会话失败: "+err.Error())
		return
	}

	r, err := runner.New(runner.Config{
		AppName:        "agent-workflow",
		Agent:          root,
		SessionService: sessSvc,
	})
	if err != nil {
		e.failExecution(exec, "创建 Runner 失败: "+err.Error())
		return
	}

	msg := &genai.Content{Parts: []*genai.Part{{Text: exec.Task}}}
	var lastErr error
	for ev, err := range r.Run(ctx, userID, sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			lastErr = err
			break
		}
		if ev != nil {
			// Escalate 意味着循环退出/失败中断/HITL 暂停,是关键语义信号,
			// 但**不是**每一种 Escalate 都是异常:循环出口条件成立、
			// HITL 暂停都是预期内的正常路径。此前一律记 WARN,于是任何
			// 含循环的工作流正常跑通都会产出 WARN 告警,噪声掩盖真正
			// 的异常。按状态增量分类后再决定级别。
			if ev.Actions.Escalate {
				e.onEscalate(exec, ev)
			} else if ev.IsFinalResponse() {
				logx.Debug("adk final response", "execution", exec.ID, "author", ev.Author)
			}
		}
		if ctx.Err() != nil {
			break
		}
	}

	// 读取最终状态,同步回 Execution 并持久化(供恢复与 UI 查询)。
	// 超时/取消路径 ctx 已取消,必须用不取消的派生 ctx,否则必然读取失败。
	readCtx := context.WithoutCancel(ctx)
	resp, err := sessSvc.Get(readCtx, &session.GetRequest{AppName: "agent-workflow", UserID: userID, SessionID: sessionID})
	if err == nil && resp.Session != nil {
		e.collectResult(exec, resp.Session)
	} else {
		// 会话不可读时至少保留节点级状态快照
		e.syncNodeStates(exec)
	}

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			e.failExecution(exec, fmt.Sprintf("工作流执行超时(%ds)", exec.Snapshot.Settings.TimeoutSeconds))
			return
		}
		e.commitCancelled(exec)
		return
	default:
	}

	abort, hasAbort := exec.StateData[compiler.KeyAbort]
	if hasAbort {
		switch s := fmt.Sprint(abort); {
		case s == "WAIT_USER":
			e.commitTerminal(exec, model.ExecutionWaitingUser, event.WorkflowWaitingUser, nil)
			return
		case strings.HasPrefix(s, "FAILED:"):
			msg := fmt.Sprint(exec.StateData["_error"])
			if lastErr != nil {
				msg += ": " + lastErr.Error()
			}
			e.failExecution(exec, msg)
			return
		case strings.HasPrefix(s, "LOOP_LIMIT:"):
			e.failExecution(exec, fmt.Sprint(exec.StateData["_error"]))
			return
		}
	}
	if lastErr != nil {
		e.failExecution(exec, lastErr.Error())
		return
	}
	e.commitTerminal(exec, model.ExecutionCompleted, event.WorkflowCompleted, map[string]any{
		"nodes":      exec.NodeStates,
		"iterations": exec.Iterations,
	})
}

// collectResult 把 ADK 会话状态同步回 Execution 记录。
func (e *Engine) collectResult(exec *model.Execution, sess session.Session) {
	st := sess.State()
	data := map[string]any{}
	for k, v := range st.All() {
		data[k] = v
	}
	exec.StateData = data
	e.syncNodeStates(exec)
}

// syncNodeStates 从 StateData 重建节点级视图(会话不可读时的兜端,
// 以及 collectResult 之后的统一派生)。
//
// **全量重建**而非"有则覆盖":此前只覆盖 StateData 里存在的 key,于是
//   - RetryNode 的 `delete(exec.StateData, StatusKey)` 不会让 NodeStates
//     里的旧 FAILED 消失 → 视图与真值长期不一致;
//   - loopResetter 用空串当哨兵(`Set(StatusKey(id), "")`),会被
//     `fmt.Sprint("")` 忠实复制成 `NodeStates[id] = ""` → UI 出现空状态。
//
// 新建表只收"非空且非 nil"的值,再整体替换,派生视图与真值同生共死。
//
// nil 判定:stateAccess.Delete 以 `Set(key, nil)` 伪造删除(ADK 的 State
// 只有 Get/Set/All),而 Get 把 nil 当缺失。若这里 `fmt.Sprint(nil)` 会写出
// 字面量 "<nil>" 当成节点状态。
func (e *Engine) syncNodeStates(exec *model.Execution) {
	if exec.StateData == nil || exec.Snapshot == nil {
		return
	}
	ns := make(map[string]string, len(exec.Snapshot.Nodes))
	for _, n := range exec.Snapshot.Nodes {
		v, ok := exec.StateData[compiler.StatusKey(n.ID)]
		if !ok || v == nil {
			continue
		}
		if s := fmt.Sprint(v); s != "" {
			ns[n.ID] = s
		}
	}
	exec.NodeStates = ns

	if v, ok := exec.StateData[compiler.KeyCurrentNode]; ok && v != nil {
		if s := fmt.Sprint(v); s != "" {
			exec.CurrentNodeID = s
		}
	}
	// Iterations 同理全量重建(全程只有 "loop" 一个 key)
	its := map[string]int{}
	if v, ok := exec.StateData[compiler.KeyLoopCount]; ok {
		// 状态经 JSON 持久化往返,int 已变 float64
		// (读取侧已在 model.NormalizeStateJSON 归一,这里是纵深防御)
		if i, ok := compiler.IntOfOk(v); ok {
			its["loop"] = i
		}
	}
	exec.Iterations = its
}

func (e *Engine) failExecution(exec *model.Execution, msg string) {
	exec.Error = msg
	if err := e.transitionAndSave(exec, model.ExecutionFailed); err != nil {
		// 状态冲突 = 库里已经是别的终态(如用户已取消):不覆盖,
		// 但错误原因仍要留下,否则用户只看到"已取消"却不知道为何失败。
		//
		// 事件流与 DB 必须一致(约定 #14):状态没落库就不能发 workflow.failed
		// ——否则事件流说"失败"、库里却是别的终态,审计流与 UI 互相打架。
		// 改发 runtime.error 说明"这次失败没能落成失败态"。
		e.stateCommitFailures.Add(1)
		cur := e.currentState(exec.ID)
		logx.Error("执行失败状态落库未生效", "execution", exec.ID,
			"current", cur, "error", err.Error())
		e.Bus.Emit(event.New("runtime.error", exec.ID, exec.CurrentNodeID, map[string]any{
			"reason":        "state_commit_conflict",
			"target_state":  string(model.ExecutionFailed),
			"current_state": cur,
			"error":         msg,
		}))
		// 失败原因仍要落库:saveMeta 只写非状态列,不会推翻别人的裁决。
		e.save(exec)
		return
	}
	e.Bus.Emit(event.New(event.WorkflowFailed, exec.ID, exec.CurrentNodeID, map[string]any{"error": msg}))
}

// commitTerminal 提交终态并按 CAS 结果决定**发不发**终态事件。
//
// **判断语义**(约定 #14:事件流与 DB 必须一致):
//
//	CAS 失败 = 库里的状态已被别的写者裁决(用户取消/显式重试/崩溃恢复)。
//	处理方式:
//
//	 1. 放弃本次迁移 —— 绝不覆盖别人的裁决。"已完成"被改回"运行中"、
//	    "已取消"被改回"运行中"是最伤用户信任的状态回退;
//	 2. 不发终态事件 —— 状态没落库,事件流就不能替别人宣布这个终态。
//	    否则事件流说"已完成"而库里还是"运行中":UI 与审计流互相打架,
//	    事后也说不清到底是哪边说了算;
//	 3. 分级记录 —— 终态路径上的冲突是**真异常**(库里不是我们期望的
//	    运行中,说明有别的写者在我们不知道的地方裁决了这条执行):
//	    记 ERROR + 发 runtime.error 事件,让问题可见。
//
// 取消路径的冲突另作处理(见 commitCancelled):那是预期竞态。
func (e *Engine) commitTerminal(exec *model.Execution, to model.ExecutionState, evType string, payload map[string]any) {
	err := e.transitionAndSave(exec, to)
	if err == nil {
		e.Bus.Emit(event.New(evType, exec.ID, exec.CurrentNodeID, payload))
		return
	}
	e.stateCommitFailures.Add(1)
	cur := e.currentState(exec.ID)
	logx.Error("终态迁移未生效:状态已被其他写者裁决,不再发终态事件(事件流与 DB 保持一致)",
		"execution", exec.ID, "to", string(to), "current", cur, "error", err.Error())
	e.Bus.Emit(event.New("runtime.error", exec.ID, exec.CurrentNodeID, map[string]any{
		"reason":        "state_commit_conflict",
		"target_state":  string(to),
		"current_state": cur,
		"error":         err.Error(),
	}))
}

// commitCancelled 提交"已取消"终态。
//
// 取消路径的 CAS 冲突是**预期竞态**:用户点了取消 vs 执行正好结束,
// 或另一个写者(崩溃恢复/显式重试)已经裁决过这条执行。因此:
//
//   - 放弃迁移(不覆盖别人的裁决);
//   - 不发 workflow.cancelled —— 状态没落库,事件流不能替别人宣布"已取消";
//   - 记 WARN 即可:取消是用户高频操作,每次都报 ERROR 只会制造噪音,
//     把真正的异常淹掉(约定 #14 要的是"别静默",不是"每次都 ERROR")。
//
// 可观测性靠计数(StateCommitFailures)与库里的状态,不需要 ERROR 日志。
func (e *Engine) commitCancelled(exec *model.Execution) {
	err := e.transitionAndSave(exec, model.ExecutionCancelled)
	if err == nil {
		e.Bus.Emit(event.New(event.WorkflowCancelled, exec.ID, exec.CurrentNodeID, nil))
		return
	}
	e.stateCommitFailures.Add(1)
	logx.Warn("取消状态未落库(预期竞态:状态已被其他写者裁决)",
		"execution", exec.ID, "current", e.currentState(exec.ID), "error", err.Error())
}

// ---- 状态机 ----

func (e *Engine) transition(exec *model.Execution, to model.ExecutionState) error {
	if !model.CanTransitionExecution(exec.State, to) {
		return model.NewError(model.KindStateError, "INVALID_TRANSITION",
			fmt.Sprintf("非法状态转换 %s → %s", exec.State, to))
	}
	exec.State = to
	return nil
}

// transitionAndSave 迁移执行状态并落库。
//
// 状态迁移**只**经 CAS(约定 #4),且与元信息落库合成**一条**语句:
// CAS 失败 = 库里的状态已被别的写者落定(并发取消/重试/崩溃恢复),
// 此时绝不能覆盖 —— 覆盖会把"已完成"改回"运行中",或把"已取消"改回
// "运行中"(约定 #14 同族问题:用户看到的裁决被过期写推翻)。
//
// 合成一条语句还保证"状态"与"元信息"同时可见:分两次写会出现
// "状态已是 COMPLETED、节点状态还是上一轮"的窗口,轮询中的 UI 会看到
// "成功但一个节点都没跑"。
func (e *Engine) transitionAndSave(exec *model.Execution, to model.ExecutionState) error {
	if !model.CanTransitionExecution(exec.State, to) {
		return model.NewError(model.KindStateError, "INVALID_TRANSITION",
			fmt.Sprintf("非法状态转换 %s → %s", exec.State, to))
	}
	ok, err := e.casStateAndSave(exec, []model.ExecutionState{exec.State}, to)
	if err != nil {
		return err
	}
	if !ok {
		cur := e.currentState(exec.ID)
		logx.Warn("状态迁移被并发写者抢先,已放弃本次写入",
			"execution", exec.ID, "from", exec.State, "to", to, "current", cur)
		return model.NewError(model.KindStateError, "STATE_CONFLICT",
			fmt.Sprintf("执行 %s 当前状态为 %s,期望从 %s 迁移到 %s", exec.ID, cur, exec.State, to))
	}
	exec.State = to
	return nil
}

// save 落库执行记录(只写非状态列;状态由 CAS 单独落定)。
//
// 失败必须暴露,不能静默丢弃状态:发一条 runtime.error 事件 + 记日志。
func (e *Engine) save(exec *model.Execution) {
	if err := e.saveMeta(exec); err != nil {
		logx.Error("保存执行记录失败", "execution", exec.ID, "error", err.Error())
		e.Bus.Emit(event.New("runtime.error", exec.ID, "", map[string]any{"error": err.Error()}))
	}
}

func (e *Engine) loadExec(id string) (*model.Execution, error) {
	return e.Repo.GetExecution(id)
}

func loopLimit(wf *model.Workflow) int {
	if wf != nil && wf.Settings.MaxIterations > 0 {
		return wf.Settings.MaxIterations
	}
	return compiler.DefaultMaxIterations
}

// cloneExec 返回 Execution 的深拷贝(执行 goroutine 与调用方之间
// 不共享可变结构;JSON 往返的代价可接受,仅发生在每次派发时一次)。
func cloneExec(e *model.Execution) *model.Execution {
	raw, _ := json.Marshal(e)
	var out model.Execution
	_ = json.Unmarshal(raw, &out)
	return &out
}

func cloneWorkflow(wf *model.Workflow) *model.Workflow {
	raw, _ := json.Marshal(wf)
	var out model.Workflow
	_ = json.Unmarshal(raw, &out)
	return &out
}

// compile-time check: Engine 实现 compiler.NodeRunner(见 executor.go)
var _ compiler.NodeRunner = (*Engine)(nil)
