// Package runtime 是构建在 ADK 之上的 Workflow Runtime。
// Engine 负责:执行生命周期、状态机、HITL 暂停/恢复、取消与崩溃恢复。
// 工作流结构的真正执行由 ADK Runner / Workflow Agents 完成。
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	coreagent "agentworkflow/agent"
	"agentworkflow/contextx"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/template"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
	"agentworkflow/workflow/validator"
)

// Engine 依赖注入:全部通过构造函数传入,无全局状态。
type Engine struct {
	Agents coreagent.Registry
	Skills skill.Registry
	Perms  *permission.Manager
	CtxMgr *contextx.Manager
	Tpl    *template.Engine
	Bus    *event.Bus
	Git    *git.Service
	Repo   *persistence.DB
	// SubworkflowRunner 支持嵌套工作流(由 application 层注入,避免循环依赖)。
	SubworkflowRunner func(ctx context.Context, workflowID, task string, input map[string]any) (map[string]any, error)

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
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
	bus.Subscribe(func(ev event.UIEvent) {
		if ev.ExecutionID != "" {
			_ = repo.SaveEvent(ev)
		}
	})
	return e
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
	)
}

// Start 创建并异步执行一个 Execution。
// 返回创建好的 Execution(状态 RUNNING),执行在后台进行。
func (e *Engine) Start(ctx context.Context, wf *model.Workflow, task string, variables map[string]any) (*model.Execution, error) {
	if res := e.Validate(wf); !res.Valid {
		err := model.NewError(model.KindValidationError, "WORKFLOW_INVALID", "工作流校验失败")
		raw, _ := json.Marshal(res.Errors)
		err.Detail = string(raw)
		return nil, err
	}

	now := time.Now().Format(time.RFC3339Nano)
	exec := &model.Execution{
		ID:              newID("exec"),
		WorkflowID:      wf.ID,
		WorkflowVersion: wf.Version,
		WorkflowName:    wf.Name,
		State:           model.ExecutionCreated,
		Task:            task,
		Variables:       variables,
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
	e.save(exec)
	e.Bus.Emit(event.New(event.WorkflowStarted, exec.ID, "", map[string]any{
		"workflow": wf.Name, "task": task,
	}))

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[exec.ID] = cancel
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancels, exec.ID)
			e.mu.Unlock()
		}()
		e.execute(runCtx, exec, "", nil)
	}()
	return exec, nil
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
	e.Bus.Emit(event.New(event.HumanInputReceived, exec.ID, exec.CurrentNodeID, userResponse))
	if err := e.transition(exec, model.ExecutionRunning); err != nil {
		return nil, err
	}
	e.save(exec)
	e.Bus.Emit(event.New(event.WorkflowResumed, exec.ID, exec.CurrentNodeID, nil))

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[exec.ID] = cancel
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancels, exec.ID)
			e.mu.Unlock()
		}()
		e.execute(runCtx, exec, "resume", userResponse)
	}()
	return exec, nil
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
	exec, err := e.loadExec(executionID)
	if err != nil {
		return err
	}
	if err := e.transitionAndSave(exec, model.ExecutionCancelled); err != nil {
		return err
	}
	e.Bus.Emit(event.New(event.WorkflowCancelled, exec.ID, exec.CurrentNodeID, nil))
	return nil
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
	if skip {
		exec.StateData[compiler.StatusKey(nodeID)] = string(model.NodeSkipped)
	} else {
		delete(exec.StateData, compiler.StatusKey(nodeID))
	}
	delete(exec.StateData, compiler.KeyAbort)
	delete(exec.StateData, "_error")
	exec.Error = ""

	if err := e.transition(exec, model.ExecutionRunning); err != nil {
		return nil, err
	}
	e.save(exec)
	e.Bus.Emit(event.New(event.WorkflowResumed, exec.ID, nodeID, map[string]any{
		"retry": !skip, "skip": skip,
	}))

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[exec.ID] = cancel
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancels, exec.ID)
			e.mu.Unlock()
		}()
		e.execute(runCtx, exec, "resume", nil)
	}()
	return exec, nil
}

// RecoverPending 在应用启动时调用:把上次崩溃遗留的 RUNNING 执行标记为
// FAILED(可再次运行),WAITING_USER/PAUSED 保持可恢复状态。
func (e *Engine) RecoverPending() error {
	pending, err := e.Repo.ListRunningExecutions()
	if err != nil {
		return err
	}
	for _, exec := range pending {
		switch exec.State {
		case model.ExecutionRunning, model.ExecutionCreated:
			exec.Error = "应用重启导致执行中断,可重新运行"
			_ = e.transitionAndSave(exec, model.ExecutionFailed)
		case model.ExecutionWaitingUser, model.ExecutionPaused:
			// 保持状态,可通过 Resume 继续
		}
	}
	return nil
}

// execute 是执行主循环:编译 → ADK Runner 执行 → 收尾。
func (e *Engine) execute(ctx context.Context, exec *model.Execution, mode string, userInput map[string]any) {
	env := &compiler.RunEnv{
		Exec:          exec,
		WF:            exec.Snapshot,
		Runner:        e,
		Bus:           e.Bus,
		Vars:          exec.Variables,
		MaxIterations: loopLimit(exec.Snapshot),
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
		_ = ev
		if ctx.Err() != nil {
			break
		}
	}

	// 读取最终状态,同步回 Execution 并持久化(供恢复与 UI 查询)
	resp, err := sessSvc.Get(ctx, &session.GetRequest{AppName: "agent-workflow", UserID: userID, SessionID: sessionID})
	if err == nil && resp.Session != nil {
		e.collectResult(exec, resp.Session)
	} else {
		// 会话不可读时至少保留节点级状态快照
		e.syncNodeStates(exec)
	}

	select {
	case <-ctx.Done():
		_ = e.transitionAndSave(exec, model.ExecutionCancelled)
		e.Bus.Emit(event.New(event.WorkflowCancelled, exec.ID, exec.CurrentNodeID, nil))
		return
	default:
	}

	abort, hasAbort := exec.StateData[compiler.KeyAbort]
	if hasAbort {
		switch s := fmt.Sprint(abort); {
		case s == "WAIT_USER":
			_ = e.transitionAndSave(exec, model.ExecutionWaitingUser)
			e.Bus.Emit(event.New(event.WorkflowWaitingUser, exec.ID, exec.CurrentNodeID, nil))
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
	_ = e.transitionAndSave(exec, model.ExecutionCompleted)
	e.Bus.Emit(event.New(event.WorkflowCompleted, exec.ID, exec.CurrentNodeID, nil))
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

// syncNodeStates 从 StateData 恢复节点状态(会话不可读时的兜底)。
func (e *Engine) syncNodeStates(exec *model.Execution) {
	if exec.StateData == nil {
		return
	}
	for _, n := range exec.Snapshot.Nodes {
		if v, ok := exec.StateData[compiler.StatusKey(n.ID)]; ok {
			exec.NodeStates[n.ID] = fmt.Sprint(v)
		}
	}
	if v, ok := exec.StateData[compiler.KeyCurrentNode]; ok {
		exec.CurrentNodeID = fmt.Sprint(v)
	}
	if v, ok := exec.StateData[compiler.KeyLoopCount]; ok {
		if i, ok := v.(int); ok {
			exec.Iterations["loop"] = i
		}
	}
}

func (e *Engine) failExecution(exec *model.Execution, msg string) {
	exec.Error = msg
	_ = e.transitionAndSave(exec, model.ExecutionFailed)
	e.Bus.Emit(event.New(event.WorkflowFailed, exec.ID, exec.CurrentNodeID, map[string]any{"error": msg}))
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

func (e *Engine) transitionAndSave(exec *model.Execution, to model.ExecutionState) error {
	if err := e.transition(exec, to); err != nil {
		return err
	}
	e.save(exec)
	return nil
}

func (e *Engine) save(exec *model.Execution) {
	if err := e.Repo.SaveExecution(exec); err != nil {
		// 持久化失败必须暴露,不能静默丢弃状态
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
	return 5
}

func cloneWorkflow(wf *model.Workflow) *model.Workflow {
	raw, _ := json.Marshal(wf)
	var out model.Workflow
	_ = json.Unmarshal(raw, &out)
	return &out
}

// compile-time check: Engine 实现 compiler.NodeRunner(见 executor.go)
var _ compiler.NodeRunner = (*Engine)(nil)
