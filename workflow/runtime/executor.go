package runtime

import (
	"context"
	"encoding/json"
	"errors"
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

// defaultScriptTimeoutSeconds 是 script 节点的默认超时(节点未配置
// timeout_seconds 时使用)。
const defaultScriptTimeoutSeconds = 120

// ExecuteNode 实现 compiler.NodeRunner:记录节点明细后按类型分发执行。
// compiler 负责编排;本方法只关心单节点的业务语义。
func (e *Engine) ExecuteNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	started := time.Now()
	// 尝试次数:循环/重试导致的重复执行记为 attempt 递增
	attempt := nextAttempt(st, node.ID)
	st.Set(attemptKey(node.ID), attempt)
	en := &model.ExecutionNode{
		// 明细主键带 attempt:此前固定为 exec:node,SaveExecutionNode 按主键
		// upsert —— 循环第二轮与节点级重试会把上一轮的 output/result_json/
		// started_at/duration_ms 整行覆盖,监控页只剩最后一轮,重试与
		// 迭代历史完全不可见。改为 exec:node:attempt 后每次执行独立成行。
		ID:          fmt.Sprintf("%s:%s:%d", env.Exec.ID, node.ID, attempt),
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
	// 节点级禁用:config.enabled = false → 置为 SKIPPED,路由直通
	if enabled, ok := node.Config["enabled"]; ok {
		if b, ok := enabled.(bool); ok && !b {
			return compiler.NodeOutcome{State: model.NodeSkipped, Summary: "节点已禁用"}
		}
	}
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
	case model.NodeTypeParallel:
		// 并行节点:由 ADK ParallelAgent 处理
		return compiler.NodeOutcome{State: model.NodeSuccess}
	case model.NodeTypeMerge:
		return e.runMergeNode(ctx, env, node, st)
	default:
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: fmt.Sprintf("未实现的节点类型: %s", node.Type),
		}
	}
}

// answeredOutcome 处理"WAIT_USER 节点被用户答复后恢复"的路径。
//
// 语义(已与产品确认):复用 WAIT_USER 那次的响应 —— 用户现在的答复是对
// 那个 prompt 的回应,不是要求把同样的活再干一遍。Agent/Skill 节点的
// 副作用(写文件、跑测试、git 操作)只应发生一次。
//
// 返回值 ok=true 表示本次应直接返回该 outcome 而不再调用 Agent/Skill。
func answeredOutcome(st compiler.StateAccess, nodeID string) (compiler.NodeOutcome, bool) {
	// 必须真的有用户答复才算"已答复":只有 _answered 而没有 _user_input
	// 的场景(理论上不该出现)按未答复处理更安全。
	ui, hasInput := st.Get(compiler.KeyUserInput)
	if !hasInput || ui == nil {
		return compiler.NodeOutcome{}, false
	}
	raw, ok := st.Get(compiler.AnsweredKey(nodeID))
	if !ok {
		return compiler.NodeOutcome{}, false
	}
	var prev compiler.NodeOutcome
	if err := json.Unmarshal([]byte(fmt.Sprint(raw)), &prev); err != nil {
		// 快照损坏:不猜,按未答复处理(重跑一次胜过静默吞掉用户答复)
		return compiler.NodeOutcome{}, false
	}
	resp, _ := ui.(map[string]any)
	// reject 是显式否决,必须让工作流失败而不是"复用上一次的成功响应"
	if r, _ := resp["response"].(string); strings.EqualFold(strings.TrimSpace(r), "reject") {
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: "用户在节点 " + nodeID + " 拒绝继续",
		}, true
	}
	// 答复内容写入上下文,供后续节点用 {{user_instruction}} 引用
	inst, _ := resp["instruction"].(string)
	if inst == "" {
		if r, _ := resp["response"].(string); r != "" &&
			!strings.EqualFold(r, "approve") && !strings.EqualFold(r, "reject") &&
			!strings.EqualFold(r, "continue") {
			inst = r
		}
	}
	if inst != "" {
		_ = st.Set("user_instruction", inst)
	}
	// 复用原响应,但明确标注这是"用户答复后完成",不是 Agent 重新执行
	out := prev
	out.State = model.NodeSuccess
	out.Summary = strings.TrimSpace(prev.Summary + " (用户已答复,复用原响应)")
	return out, true
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

	// WAIT_USER 恢复:复用 WAIT_USER 那次的响应,不再重复调用 Agent。
	// 此前 lower.go 把 _user_input 塞回状态就放行,这里从不读它 ——
	// Agent 被第二次真实调用,用户答复丢失,且 execute/fix 模式会
	// 重复写文件、重复跑测试、重复 git 操作。
	if out, ok := answeredOutcome(st, node.ID); ok {
		_ = st.Delete(compiler.KeyUserInput)
		return out
	}

	// 权限策略:执行前检查(fs:read 为最低要求);工作流覆盖优先
	if err := e.perms(env).Check(agentID, permission.FSRead); err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	if mode == "execute" || mode == "fix" {
		if err := e.perms(env).Check(agentID, permission.FSWrite); err != nil {
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
	// 与 agent 节点同语义:WAIT_USER 被答复后复用原响应,不重复执行 Skill
	if out, ok := answeredOutcome(st, node.ID); ok {
		_ = st.Delete(compiler.KeyUserInput)
		return out
	}
	skillID := cfgStr(node, "skill")
	s, err := e.Skills.Get(skillID)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	// Skill 禁用检查(Skills 页开关)
	if e.IsSkillDisabled != nil && e.IsSkillDisabled(skillID) {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: "Skill 已被禁用: " + skillID}
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
	if resp.Status == "WAIT_USER" {
		// Skill 接口承诺 WAIT_USER:暂停等待用户输入(与人工节点一致)
		outcome.State = model.NodeWaiting
		if outcome.Data == nil {
			outcome.Data = map[string]any{}
		}
		outcome.Data["responses"] = []string{"approve", "reject", "continue", "instruction"}
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
		if err := e.perms(env).Check("workflow", permission.GitRead); err != nil {
			return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
		}
	case "commit":
		if err := e.perms(env).Check("workflow", permission.GitCommit); err != nil {
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
// 显式开关门禁:该节点等价于任意本地命令执行,而工作流可由 YAML 导入
// 获得,故默认拒绝,需用户在设置页显式放行(需求 §42 禁止绕过权限系统)。
func (e *Engine) runScriptNode(ctx context.Context, node *model.Node) compiler.NodeOutcome {
	command := cfgStr(node, "command")
	if command == "" {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: "script 节点缺少 command"}
	}
	if e.AllowScriptNodes == nil || !e.AllowScriptNodes() {
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: "script 节点未放行:该节点可执行任意本地命令,需在设置页开启「允许执行 script 节点」后重试",
		}
	}
	shell := cfgStr(node, "shell")
	if shell == "" {
		shell = "/bin/sh"
	}
	// 超时与工作目录读节点配置(此前硬编码 120s、且始终用全局 Git 目录,
	// 节点上写的 timeout_seconds / working_dir 都不生效)
	timeout := defaultScriptTimeoutSeconds
	if s := cfgInt(node, "timeout_seconds"); s > 0 {
		timeout = s
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, shell, "-c", command)
	// 脚本派生的后台子进程可能持有输出管道:进程组击杀 + WaitDelay
	// 兜底,保证调用方及时返回(与 Agent 适配器同一策略)
	coreagent.ConfigureProcess(cmd)
	cmd.WaitDelay = 5 * time.Second
	workDir := cfgStr(node, "working_dir")
	if workDir == "" {
		workDir = e.Git.WorkingDir
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.CombinedOutput()
	// 进程已成功退出、仅孤儿子进程延迟关管道时,输出已完整捕获,不算失败
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error(), Output: string(out)}
	}
	return compiler.NodeOutcome{State: model.NodeSuccess, Output: string(out)}
}

// runMergeNode 汇合并行分支结果:收集所有入边分支节点的状态,
// 输出聚合摘要(全部 SUCCESS / 部分失败一目了然)。
func (e *Engine) runMergeNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	var branches []string
	var failed []string
	for _, edge := range env.WF.Edges {
		if edge.To != node.ID {
			continue
		}
		branchID := edge.From
		branches = append(branches, branchID)
		status := ""
		if v, ok := st.Get(compiler.StatusKey(branchID)); ok {
			status = fmt.Sprint(v)
		}
		if status == string(model.NodeFailed) {
			failed = append(failed, branchID)
		}
	}
	summary := fmt.Sprintf("汇合 %d 个分支", len(branches))
	if len(failed) > 0 {
		summary += fmt.Sprintf(",失败分支: %s", strings.Join(failed, ", "))
	} else {
		summary += ",全部成功"
	}
	e.Bus.Emit(event.New("merge.completed", env.Exec.ID, node.ID, map[string]any{
		"branches": branches, "failed": failed,
	}))
	return compiler.NodeOutcome{
		State:   model.NodeSuccess,
		Summary: summary,
		Data:    map[string]any{"branches": branches, "failed": failed},
	}
}

// runHumanNode 暂停执行等待用户输入(配合 routeAgent/恢复机制)。
func (e *Engine) runHumanNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	// 恢复时:用户输入已注入状态,直接消费
	if ui, ok := st.Get(compiler.KeyUserInput); ok && ui != nil {
		if m, ok := ui.(map[string]any); ok && len(m) > 0 {
			raw, _ := json.Marshal(m)
			// 一次性状态:消费后立即清除。此前 _user_input 一直留在会话
			// 状态里(StateAccess 也没有 Delete),同一执行内的第二个
			// human 闸门会读到上一次的输入直接 SUCCESS,人工交互被静默
			// 跳过;它还会随 StateData 持久化并在恢复时回放。
			_ = st.Delete(compiler.KeyUserInput)
			// reject 是显式否决,必须中止工作流。此前无论什么响应都返回
			// NodeSuccess,拒绝后后续 execute/fix 照常执行、执行最终标
			// COMPLETED(与路由决策路径 lower.go 的 reject→KeyAbort 语义
			// 不一致)。改走通用 NodeFailed 分支:由 lower.go 统一设置
			// KeyAbort=FAILED:<nodeID>,engine 落库 FAILED。
			if resp, _ := m["response"].(string); strings.EqualFold(strings.TrimSpace(resp), "reject") {
				return compiler.NodeOutcome{
					State:   model.NodeFailed,
					Error:   "用户在节点 " + node.ID + " 拒绝继续",
					Summary: string(raw),
					Data:    m,
				}
			}
			// 人工指令写入上下文,后续 execute/fix 节点可用 {{user_instruction}} 引用
			if inst, _ := m["instruction"].(string); inst != "" {
				st.Set("user_instruction", inst)
			} else if resp, _ := m["response"].(string); resp != "" && resp != "approve" && resp != "reject" && resp != "continue" {
				st.Set("user_instruction", resp)
			}
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

// nextAttempt 计算本次执行的 attempt 序号(循环/重试重复执行时递增)。
// 状态经 SQLite state_json 持久化往返后 int 会变成 float64,两种都要认。
func nextAttempt(st compiler.StateAccess, nodeID string) int {
	prev, ok := st.Get(attemptKey(nodeID))
	if !ok {
		return 1
	}
	switch n := prev.(type) {
	case int:
		return n + 1
	case float64:
		return int(n) + 1
	default:
		return 1
	}
}

// perms 返回本次运行的权限检查器:工作流覆盖的运行期实例优先。
func (e *Engine) perms(env *compiler.RunEnv) RunPerms {
	if env.Perms != nil {
		return env.Perms
	}
	return e.Perms
}

// RunPerms 权限检查接口。
type RunPerms interface {
	Check(agentID string, a permission.Action) error
}
