package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/contextx"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/skill"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// ExecuteNode 实现 compiler.NodeRunner:记录节点明细后按类型分发执行。
// compiler 负责编排;本方法只关心单节点的业务语义。
func (e *Engine) ExecuteNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	started := time.Now()
	// 尝试次数:循环/重试导致的重复执行记为 attempt 递增
	attempt := 1
	if v, ok := st.Get(attemptKey(node.ID)); ok {
		if f, ok := v.(float64); ok {
			attempt = int(f) + 1
		} else if i, ok := v.(int); ok {
			attempt = i + 1
		}
	}
	st.Set(attemptKey(node.ID), attempt)
	en := &model.ExecutionNode{
		ID:          env.Exec.ID + ":" + node.ID,
		ExecutionID: env.Exec.ID,
		NodeID:      node.ID,
		NodeType:    node.Type,
		NodeName:    node.Name,
		State:       model.NodeRunning,
		Attempt:     attempt,
		StartedAt:   started.Format(time.RFC3339Nano),
	}
	if err := e.Repo.SaveExecutionNode(en); err != nil {
		e.Bus.Emit(event.New("runtime.error", env.Exec.ID, node.ID, map[string]any{"error": err.Error()}))
	}

	outcome := e.dispatchNode(ctx, env, node, st)

	en.State = outcome.State
	en.Output = truncate(outcome.Output, 20000)
	en.Error = outcome.Error
	en.FinishedAt = time.Now().Format(time.RFC3339Nano)
	en.DurationMS = time.Since(started).Milliseconds()
	if raw, err := json.Marshal(outcome); err == nil {
		en.ResultJSON = string(raw)
	}
	if err := e.Repo.SaveExecutionNode(en); err != nil {
		e.Bus.Emit(event.New("runtime.error", env.Exec.ID, node.ID, map[string]any{"error": err.Error()}))
	}
	return outcome
}

// dispatchNode 按节点类型分发。
func (e *Engine) dispatchNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	switch node.Type {
	case model.NodeTypeAgent:
		return e.runAgentNode(ctx, env, node, st)
	case model.NodeTypeSkill:
		return e.runSkillNode(ctx, env, node, st)
	case model.NodeTypeGit:
		return e.runGitNode(ctx, env, node, st)
	case model.NodeTypeScript:
		return e.runScriptNode(ctx, node)
	case model.NodeTypeHuman:
		return e.runHumanNode(ctx, env, node, st)
	case model.NodeTypeSubWorkflow:
		return e.runSubworkflowNode(ctx, env, node, st)
	case model.NodeTypeCondition:
		// 条件节点本身无副作用:路由由 routeAgent 完成
		return compiler.NodeOutcome{State: model.NodeSuccess}
	case model.NodeTypeParallel, model.NodeTypeMerge:
		// 结构节点:由 ADK Parallel/Sequential 处理
		return compiler.NodeOutcome{State: model.NodeSuccess}
	default:
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: fmt.Sprintf("未实现的节点类型: %s", node.Type),
		}
	}
}

// cfgStr / cfgInt 读取节点配置的辅助函数。
func cfgStr(node *model.Node, key string) string {
	if v, ok := node.Config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func cfgInt(node *model.Node, key string) int {
	if v, ok := node.Config[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
		if i, ok := v.(int); ok {
			return i
		}
	}
	return 0
}

// runAgentNode 执行 Agent 节点:
// 权限检查 → 上下文构造(最小化) → Prompt 模板渲染 → 调用 Agent → 结构化结果。
// 支持节点级 retry(指数/固定退避)与节点级 model 覆盖。
func (e *Engine) runAgentNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	agentID := cfgStr(node, "agent")
	mode := cfgStr(node, "mode")

	// 权限策略:执行前检查(fs:read 为最低要求)
	if err := e.Perms.Check(agentID, permission.FSRead); err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	if mode == "execute" || mode == "fix" {
		if err := e.Perms.Check(agentID, permission.FSWrite); err != nil {
			return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
		}
	}

	a, err := e.Agents.Get(agentID)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}

	// Prompt:DSL 模板 + 最小化上下文,不写死在 Go 代码里
	ec := &contextx.ExecutionContext{
		WorkflowID:  env.Exec.WorkflowID,
		ExecutionID: env.Exec.ID,
		Variables:   env.Vars,
		State:       e.stateView(st),
	}
	data := e.CtxMgr.Build(mode, ec, node.ID)
	prompt := cfgStr(node, "prompt")
	if prompt == "" {
		if tplRaw, ok := node.Config["prompt"].(map[string]any); ok {
			prompt, _ = tplRaw["template"].(string)
		}
	}
	rendered, _ := e.Tpl.Render(prompt, data)
	instructions := rendered
	if extra := cfgStr(node, "instructions"); extra != "" {
		r2, _ := e.Tpl.Render(extra, data)
		instructions = strings.TrimSpace(rendered + "\n\n" + r2)
	}

	// 节点级 model 覆盖(DSL config.model)> Agent 默认模型
	nodeModel := cfgStr(node, "model")

	req := coreagent.AgentRequest{
		Task:         env.Exec.Task,
		Mode:         mode,
		Model:        nodeModel,
		Context:      data,
		Instructions: instructions,
		WorkingDir:   cfgStr(node, "working_dir"),
		Environment:  nil,
		Timeout:      cfgInt(node, "timeout_seconds"),
	}
	if wd := e.Git.WorkingDir; req.WorkingDir == "" && wd != "" {
		req.WorkingDir = wd
	}

	// 重试策略:retry.max_attempts(默认 1 = 不重试),backoff: fixed | exponential
	maxAttempts, backoff := retryPolicyOf(node)
	var outcome compiler.NodeOutcome
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			delay := retryBackoffDelay(attempt, backoff)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return compiler.NodeOutcome{State: model.NodeFailed, Error: "执行取消"}
			}
		}

		// Agent 输出事件(实时)
		done := make(chan struct{})
		go e.streamAgentOutput(ctx, env, node.ID, a.ID(), req, done)

		ectx := ctx
		if req.Timeout > 0 {
			var cancel context.CancelFunc
			ectx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
			outcome = e.invokeAgent(ectx, a, req)
			cancel()
		} else {
			outcome = e.invokeAgent(ctx, a, req)
		}
		close(done)

		if outcome.State != model.NodeFailed || ctx.Err() != nil {
			break
		}
		if attempt < maxAttempts {
			e.Bus.Emit(event.New("agent.retry", env.Exec.ID, node.ID, map[string]any{
				"agent": agentID, "attempt": attempt, "max_attempts": maxAttempts,
				"error": outcome.Error,
			}))
		}
	}
	if outcome.State == model.NodeSuccess {
		// 保存节点输出到上下文(review/plan 等按需引用)
		e.CtxMgr.SaveNodeOutput(mode, node.ID, map[string]any{
			"summary": outcome.Summary, "output": outcome.Output, "issues": outcome.Data["issues"],
		}, e.stateView(st))
	}
	return outcome
}

// invokeAgent 调用 Agent 并把结果映射为节点结果。
func (e *Engine) invokeAgent(ctx context.Context, a coreagent.Agent, req coreagent.AgentRequest) compiler.NodeOutcome {
	resp, err := a.Execute(ctx, req)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	outcome := compiler.NodeOutcome{
		State:    model.NodeSuccess,
		Decision: string(resp.Decision),
		Summary:  resp.Summary,
		Output:   resp.Output,
		Data:     resp.Data,
	}
	switch resp.Status {
	case coreagent.StatusFailed:
		outcome.State = model.NodeFailed
		outcome.Error = resp.Error
	case coreagent.StatusWaitUser:
		outcome.State = model.NodeWaiting
		if outcome.Data == nil {
			outcome.Data = map[string]any{}
		}
		outcome.Data["responses"] = []string{"approve", "reject", "continue", "instruction"}
	}
	return outcome
}

// retryPolicyOf 读取节点 retry 配置。
func retryPolicyOf(node *model.Node) (maxAttempts int, backoff string) {
	maxAttempts = 1
	backoff = "fixed"
	raw, ok := node.Config["retry"].(map[string]any)
	if !ok {
		return
	}
	if v, ok := raw["max_attempts"]; ok {
		switch n := v.(type) {
		case float64:
			maxAttempts = int(n)
		case int:
			maxAttempts = n
		}
	}
	if v, ok := raw["backoff"].(string); ok && v != "" {
		backoff = v
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if maxAttempts > 10 {
		maxAttempts = 10
	}
	return
}

// retryBackoffDelay 计算第 attempt 次重试前的等待时间。
func retryBackoffDelay(attempt int, backoff string) time.Duration {
	if backoff == "exponential" {
		// 1s, 2s, 4s... 上限 30s
		d := time.Second << (attempt - 1)
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		return d
	}
	return 2 * time.Second
}

// runSkillNode 执行 Skill 节点。
func (e *Engine) runSkillNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	skillID := cfgStr(node, "skill")
	s, err := e.Skills.Get(skillID)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	args, _ := node.Config["args"].(map[string]any)
	resp, err := s.Execute(ctx, skill.SkillRequest{
		NodeID:   node.ID,
		Args:     args,
		Context:  e.stateView(st),
		Executor: cfgStr(node, "agent"),
	})
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	e.Bus.Emit(event.New(event.SkillExecuted, env.Exec.ID, node.ID, map[string]any{
		"skill": skillID, "status": resp.Status,
	}))
	outcome := compiler.NodeOutcome{
		State:   model.NodeSuccess,
		Summary: resp.Output,
		Output:  resp.Output,
		Data:    resp.Data,
	}
	if resp.Status == "FAILED" || resp.Status == "failed" {
		outcome.State = model.NodeFailed
		outcome.Error = resp.Error
	}
	if resp.Decision != "" {
		outcome.Decision = resp.Decision
	}
	e.CtxMgr.SaveNodeOutput("skill", node.ID, map[string]any{"summary": resp.Output, "output": resp.Output}, e.stateView(st))
	return outcome
}

// runGitNode 执行 Git 节点(权限由 Permission Manager 控制)。
func (e *Engine) runGitNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	op := cfgStr(node, "operation")
	switch op {
	case "status", "diff", "log", "branch":
		if err := e.Perms.Check("workflow", permission.GitRead); err != nil {
			return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
		}
	case "commit":
		if err := e.Perms.Check("workflow", permission.GitCommit); err != nil {
			return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
		}
	case "checkout":
		// checkout 属于写操作,但由工作流显式编排,允许
	default:
		return compiler.NodeOutcome{State: model.NodeFailed, Error: "不支持的 git 操作: " + op}
	}
	args, _ := node.Config["args"].(map[string]any)
	out, err := e.Git.Run(ctx, op, args)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	e.Bus.Emit(event.New(event.GitExecuted, env.Exec.ID, node.ID, map[string]any{
		"operation": op, "output": truncate(out, 500),
	}))
	// diff 结果保存为上下文,供 review 节点引用
	if op == "diff" {
		st.Set("git_diff", out)
		_ = e.Repo.SaveArtifact(&model.Artifact{
			ID: newID("art"), ExecutionID: env.Exec.ID, NodeID: node.ID,
			Name: "git-diff", ContentType: "text/x-diff", Content: out,
			CreatedAt: time.Now().Format(time.RFC3339Nano),
		})
	}
	return compiler.NodeOutcome{State: model.NodeSuccess, Output: out, Summary: "git " + op}
}

// runScriptNode 执行脚本节点(本地 shell)。
func (e *Engine) runScriptNode(ctx context.Context, node *model.Node) compiler.NodeOutcome {
	command := cfgStr(node, "command")
	if command == "" {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: "script 节点缺少 command"}
	}
	shell := cfgStr(node, "shell")
	if shell == "" {
		shell = "/bin/sh"
	}
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, shell, "-c", command)
	if e.Git.WorkingDir != "" {
		cmd.Dir = e.Git.WorkingDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error(), Output: string(out)}
	}
	return compiler.NodeOutcome{State: model.NodeSuccess, Output: string(out)}
}

// runHumanNode 暂停执行等待用户输入(配合 routeAgent/恢复机制)。
func (e *Engine) runHumanNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	// 恢复时:用户输入已注入状态,直接消费
	if ui, ok := st.Get(compiler.KeyUserInput); ok && ui != nil {
		if m, ok := ui.(map[string]any); ok && len(m) > 0 {
			raw, _ := json.Marshal(m)
			return compiler.NodeOutcome{
				State:   model.NodeSuccess,
				Summary: "用户输入: " + string(raw),
				Output:  string(raw),
				Data:    m,
			}
		}
	}
	prompt := cfgStr(node, "prompt")
	if prompt == "" {
		prompt = "工作流需要人工确认"
	}
	responses, _ := node.Config["responses"].([]any)
	var respNames []string
	for _, r := range responses {
		respNames = append(respNames, fmt.Sprint(r))
	}
	if len(respNames) == 0 {
		respNames = []string{"approve", "reject", "continue", "instruction"}
	}
	return compiler.NodeOutcome{
		State:   model.NodeWaiting,
		Summary: prompt,
		Data:    map[string]any{"responses": respNames},
	}
}

// runSubworkflowNode 执行嵌套工作流。
func (e *Engine) runSubworkflowNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	wfID := cfgStr(node, "workflow_id")
	if e.SubworkflowRunner == nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: "subworkflow runner 未配置"}
	}
	input, _ := node.Config["input"].(map[string]any)
	result, err := e.SubworkflowRunner(ctx, wfID, env.Exec.Task, input)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	raw, _ := json.Marshal(result)
	return compiler.NodeOutcome{State: model.NodeSuccess, Output: string(raw), Data: result}
}

// stateView 将 StateAccess 转成普通 map(只读视图,供上下文构造)。
func (e *Engine) stateView(st compiler.StateAccess) map[string]any {
	view := map[string]any{}
	for _, key := range []string{
		"task", "plan", "git_diff", "review", "test_result", "issues",
		"iteration", "last_output", "relevant_files", "output", "user_instruction",
	} {
		if v, ok := st.Get(key); ok {
			view[key] = v
		}
	}
	if v, ok := st.Get(compiler.KeyLoopCount); ok {
		view["iteration"] = v
	}
	return view
}

// streamAgentOutput 周期性发出 agent.output 事件(UI 实时刷新)。
// Agent 执行是阻塞调用,这里只透传开始/结束信号。
func (e *Engine) streamAgentOutput(ctx context.Context, env *compiler.RunEnv, nodeID, agentID string, req coreagent.AgentRequest, done <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.Bus.Emit(event.New(event.AgentOutput, env.Exec.ID, nodeID, map[string]any{
				"agent": agentID, "status": "running",
			}))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// attemptKey 生成节点尝试次数的状态键。
func attemptKey(nodeID string) string {
	return "node:" + nodeID + ":attempt"
}

var _ = strings.TrimSpace
