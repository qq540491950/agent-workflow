package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/contextx"
	"agentworkflow/event"
	"agentworkflow/logx"
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
func (e *Engine) ExecuteNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) (outcome compiler.NodeOutcome) {
	// 节点适配器 panic 时本函数**不会正常返回**:异常沿栈上抛,由 compiler
	// 的 runNodeGuarded 兜底转成 FAILED outcome(约定 #12 的分层:兜底在
	// 节点执行这一层,与并行分支在不在同一 goroutine 无关)。
	//
	// 正因为不返回,下面的"失败记账"代码根本不会执行 —— 必须在 defer 里
	// 记,否则"节点炸了"这件事只有 ADK 事件流知道,而并行区的级联取消
	// 会把那条事件一起丢掉(详见 failure_ledger.go)。
	//
	// 记账之后**原样上抛**:兜底仍然是 compiler 的 runNodeGuarded,这里
	// 只负责留底,不改分层。
	defer func() {
		if r := recover(); r != nil {
			e.failures.record(env.Exec.ID, node.ID,
				fmt.Sprintf("节点 %s 执行 panic: %v", node.ID, r), ctx.Err() != nil)
			panic(r)
		}
	}()
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

	outcome = e.dispatchNode(ctx, env, node, st)

	// 节点失败 → 记失败台账。
	//
	// 判据是"失败时 ctx 未被取消":ctx 已取消 = 这是**取消**(用户取消 /
	// 超时 / 并行区里兄弟分支失败后的级联取消),不是"这个节点坏了"。
	// 把取消也算成失败,真因就会被 "context canceled" 顶掉(约定 #14)。
	//
	// 为什么必须在这里记:失败信息只存在于 ADK 事件的 StateDelta 里,而
	// 并行区的级联取消会让**出问题的那个分支**的终态事件与取消赛跑 —
	// 输的那条事件整段丢失(详见 failure_ledger.go)。引擎是唯一见过节点
	// outcome 的地方,必须自己留底。
	if outcome.State == model.NodeFailed {
		e.failures.record(env.Exec.ID, node.ID, outcome.Error, ctx.Err() != nil)
	}

	en.State = outcome.State
	// 落库前脱敏(约定 #8):节点 output/error 直接来自 Agent 的
	// stdout/stderr,而 stderr 常带 "ANTHROPIC_API_KEY=sk-..." 这类凭据。
	// 此前只修了 API 读出口,DB 里仍是明文 —— 审计文件/备份 DB 依然
	// 泄漏。脱敏只作用于**落库的副本**:返回给编译器的 outcome 原样
	// 保留(下游节点可能引用 output 原文,决策路由也读它)。
	//
	// 注意:artifact(git diff)由 SaveArtifact 单独落库(见 runGitNode)——
	// 那里按"值的形态"脱敏(凭据形态的值替换为 ***,代码里的标识符原样
	// 保留),diff 的可读性与审计证据不受影响;此处 logx.Mask 的按键名
	// 整值替换会把代码标识符一起抹掉,不适合 diff 产物。
	en.Output = truncate(logx.Mask(outcome.Output), 20000)
	en.Error = logx.Mask(outcome.Error)
	en.FinishedAt = time.Now().Format(time.RFC3339Nano)
	en.DurationMS = time.Since(started).Milliseconds()
	if raw, err := json.Marshal(maskedOutcome(outcome)); err == nil {
		en.ResultJSON = string(raw)
	}
	if err := e.Repo.SaveExecutionNode(en); err != nil {
		e.Bus.Emit(event.New("runtime.error", env.Exec.ID, node.ID, map[string]any{"error": err.Error()}))
	}
	return outcome
}

// maskedOutcome 返回用于**落库**的 outcome 副本(自由文本已脱敏)。
//
// 只改副本:执行语义读的是原 outcome(决策走结构化字段,下游节点可能
// 引用 output 原文),脱敏只影响展示与审计。
func maskedOutcome(out compiler.NodeOutcome) compiler.NodeOutcome {
	out.Summary = logx.Mask(out.Summary)
	out.Output = logx.Mask(out.Output)
	out.Error = logx.Mask(out.Error)
	if out.Data != nil {
		if m, ok := logx.MaskValue(out.Data).(map[string]any); ok {
			out.Data = m
		}
	}
	return out
}

// dispatchNode 按节点类型分发。
func (e *Engine) dispatchNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	// 节点级开关:config.enabled = false → 置为 SKIPPED,路由直通。
	// 取值非法时按配置错误失败(fail closed),不静默按"启用"执行。
	enabled, err := nodeEnabled(node)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	if !enabled {
		return compiler.NodeOutcome{State: model.NodeSkipped, Summary: "节点已禁用"}
	}
	switch node.Type {
	case model.NodeTypeAgent:
		return e.runAgentNode(ctx, env, node, st)
	case model.NodeTypeSkill:
		return e.runSkillNode(ctx, env, node, st)
	case model.NodeTypeGit:
		return e.runGitNode(ctx, env, node, st)
	case model.NodeTypeScript:
		return e.runScriptNode(ctx, env, node)
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
	userResp, _ := resp["response"].(string)
	// reject 是显式否决,必须让工作流失败而不是"复用上一次的成功响应"
	if strings.EqualFold(strings.TrimSpace(userResp), "reject") {
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
		_ = st.Set(contextx.StateKeyUserInstruction, inst)
	}
	// 复用原响应,但明确标注这是"用户已答复,复用原响应",不是 Agent 重新执行
	out := prev
	out.State = model.NodeSuccess
	out.Summary = strings.TrimSpace(prev.Summary + " (用户已答复,复用原响应)")
	// Decision 必须按用户答复**重新映射**,不能沿用 WAIT_USER 之前的值。
	//
	// invokeAgent 构造 outcome 时总会填 Decision,而 status=WAIT_USER 与
	// decision=REJECTED 可以同时成立(Agent 一边要求人工确认、一边给了
	// 倾向性结论 —— 真实场景很常见)。若照搬,用户点 Approve 之后
	// lower.go 的 case NodeSuccess 仍会把 KeyDecision 写成 REJECTED,
	// 路由走回拒绝分支 —— 用户的批准被无视。
	//
	// 注意与 routeAgent 的恢复消费路径(那里 case "approve" 也会写
	// DecisionApproved)的关系:那条路径只在**决策节点**上跑,会抢先覆盖;
	// 本函数覆盖的是**非路由**的普通节点 —— 那条链上 KeyDecision 的唯一
	// 来源就是这里。两条路径现已一致。
	switch strings.ToLower(strings.TrimSpace(userResp)) {
	case "approve":
		out.Decision = string(coreagent.DecisionApproved)
	case "continue":
		// 继续 = 清空决策,让下游路由重新评估
		out.Decision = ""
	case "reject":
		// 上面的 reject 分支已返回,这里到不了
	}
	return out, true
}

// nodeEnabled 读取节点级开关 config.enabled。
//
// 语义来源唯一:compiler.NodeEnabledStateOf(约定 #13 校验期与运行期同源 ——
// validator/validator.go 用的是同一个函数,两边不会各写一份解析)。
//   - 缺省 / true → 执行
//   - false        → 节点被跳过(SKIPPED),路由直通
//   - 非布尔       → 配置错误,节点失败(fail closed)
//
// 为什么非布尔要失败而不是"当作启用":静默按启用处理会让用户以为
// 节点已关掉,实际却执行了副作用(写文件/跑测试/提交代码)。
// 校验期已经会拦下这种写法(INVALID_NODE_ENABLED),这里是纵深防御:
// 恢复/重试路径用的是历史快照,未必经过当次校验。
//
// 另:这里显式按字面量读一次 enabled —— workflow/compiler 的契约探针
// (nodeconfig_contract_test.go)用 AST 扫描运行期源码,按字面量下标
// 识别"运行期读了哪些配置键",写成 compiler.NodeEnabledStateOf(node.Config)
// 会被判定为"运行期没读 enabled"。
func nodeEnabled(node *model.Node) (bool, error) {
	if node == nil {
		return true, nil
	}
	raw := node.Config["enabled"]
	switch compiler.NodeEnabledStateOf(node.Config) {
	case compiler.NodeEnabledDefault, compiler.NodeEnabledOn:
		return true, nil
	case compiler.NodeEnabledOff:
		return false, nil
	default: // compiler.NodeEnabledInvalid
		return false, fmt.Errorf("节点 %s 的 %s 取值非法: %v(必须是布尔值 true/false)",
			node.ID, compiler.NodeEnabledKey, raw)
	}
}

// cfgStr / cfgFloat / cfgStringList 读取节点配置的辅助函数。
func cfgStr(node *model.Node, key string) string {
	if v, ok := node.Config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// cfgStringList 读取字符串列表型节点配置。
//
// **必须认全所有切片形态**(与 cfgFloat 同一族理由):配置值的类型随来源而变 ——
//
//	YAML / JSON 解析 → []any
//	程序化构造(导入器、嵌入方、测试) → []string
//
// 此前 HITL 的 responses 只认 []any(`node.Config["responses"].([]any)`):
// 值是 []string 时断言失败且被 `_` 吞掉 → 用户配置的选项列表**静默丢弃**、
// 回落到默认四项,全程无报错(#256)。
//
// 返回空切片表示"没配"或"配的不是列表"(调用方决定怎么兜底)。
func cfgStringList(node *model.Node, key string) []string {
	v, ok := node.Config[key]
	if !ok || v == nil {
		return nil
	}
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

// cfgFloat 读取浮点型节点配置(数值形态随来源而变:int/uint/float64/json.Number)。
//
// 只认**数值**形态:字符串 "30" 不是数值 —— 运行期不得把字符串悄悄转成
// 秒数(那是"用户配的值没生效"的另一种形态)。
//
// 非数值 / 缺省 → 0(= 调用方按"没配"处理)。
func cfgFloat(node *model.Node, key string) float64 {
	v, ok := node.Config[key]
	if !ok {
		return 0
	}
	return toFloat(v)
}

// toFloat 把任意数值形态转成 float64;不是数值时返回 0。
//
// **为什么必须先走浮点**:整数化会**先溢出**—— int(1e20) 在 int64 上是
// -9223372036854775808(未定义值的实现结果),等钳制逻辑看到它时已经是负数
// 了。用户配的 1e20 秒会静默变成"负数"(进而"不设超时")。钳制必须发生在
// 整数化**之前**。
func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint8:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case float32:
		return float64(n)
	case float64:
		return n
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return 0
}

// timeoutSecondsOf 把 timeout_seconds 的原始取值**钳制**到合法范围(秒)。
//
// 取值范围只有一份来源:workflow/compiler 的 TimeoutSecondsMin/Max
// (约定 13:校验期与运行期同源)。
//
// 钳制语义:
//
//   - 超上界 → 钳到上界(86400 = 24h)。不钳的话 time.Duration(raw)*time.Second
//     会溢出成负数 → context.WithTimeout 拿到负 deadline → 节点**立即超时**
//     (用户配的 24 小时变成 0 秒)。这不是 fail closed:超大值原本就表现为
//     "立即超时",钳到 24h 是**修复**而不是失败;
//   - 负值 → 归 0(0 = 不设节点超时,与运行期 `req.Timeout > 0` 的口径一致);
//   - 非数值 / 非整数 → 归 0(校验期已拦,运行期兜底:不得静默改用别的值)。
//
// 存量工作流里早就写着超大值(上界存在之前存进去的),校验期只拦**新写/新改**
// 的工作流 —— 运行期这道钳制是它们唯一的防线。
//
// raw 由调用点用 `cfgFloat(node, "timeout_seconds")` 读出:**字面量**是契约
// 要求 —— workflow/compiler/nodeconfig_contract_test.go 的 AST 探针只认
// `cfg*(node, "字面量")` 形态,键名一旦变成变量/常量,探针就判"运行期不再
// 读这个键",契约表与运行期就此漂移(约定 13)。
func timeoutSecondsOf(raw any) int {
	f := toFloat(raw)
	if math.IsNaN(f) {
		return 0
	}
	if f < float64(compiler.TimeoutSecondsMin) {
		return compiler.TimeoutSecondsMin
	}
	if f > float64(compiler.TimeoutSecondsMax) {
		return compiler.TimeoutSecondsMax
	}
	if f != math.Trunc(f) {
		// 小数秒:校验期判非法(INVALID_NODE),运行期归 0 —— 不静默截断改写
		// 用户配置(截断会把"配的 30.5 秒"变成"配的 30 秒")。
		return 0
	}
	return int(f)
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
	// 写权限:mode→是否需要写权限的**唯一来源**是 agent.ModeRequiresWrite
	// (与 pi 适配器的兜底检查同一份;取值域在 workflow/model)。
	//
	// 此前这里只查 execute|fix,而 pi 适配器查 execute|fix|submit ——
	// 两份集合各自演化(约定 13 的同源精神)。后果:`mode: submit` 的节点
	// 不受**工作流级 permissions 段**约束(适配器只看得到全局策略),
	// 用户在 DSL 里收紧写权限,submit 节点照样跑 —— 以为生效了实际没有
	// (约定 14)。
	//
	// 未知/空 mode 一律按"需要写权限"处理(fail closed):mode 拼错
	// ("exectue")时,若按"不在只读清单里"处理,这道门会被跳过 ——
	// 一个可能要改文件的节点在权限收紧的工作流里畅通无阻。
	if coreagent.ModeRequiresWrite(mode) {
		if err := e.perms(env).Check(agentID, permission.FSWrite); err != nil {
			return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
		}
	}

	// mode 的**取值域**(唯一来源:model.AllModes):非法 mode → 节点失败
	// (fail closed),不执行任何副作用。
	//
	// 此前任意字符串都被接受:拼错的 mode 照常跑完并报 SUCCESS,用户
	// 以为节点按 execute 跑了,实际它跑的是"一个没人认识的 mode" ——
	// 上下文链里没有它的槽位,权限判定也落不到正确的分支(约定 14)。
	//
	// 顺序:写权限检查在前。未知 mode 的 ModeRequiresWrite 恒为 true,
	// 所以写权限这道门**不会**因为 mode 非法而被跳过;两道门都过不去时,
	// 先报权限原因还是先报 mode 非法,取决于这里的位置(约定 13 的
	// 同源精神:两道门用同一份取值域)。
	if !model.IsValidMode(mode) {
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: fmt.Sprintf("节点 %s 的 mode %q 非法(允许: %s)",
				node.ID, mode, strings.Join(model.AllModes(), "|")),
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
	data, emptySlots := e.CtxMgr.BuildWithSlots(mode, ec, node.ID)
	prompt := cfgStr(node, "prompt")
	if prompt == "" {
		// prompt 有两种写法:字符串,或 {template: ...}(契约表里 prompt 的
		// 子键就是 template)。写成对象却没给可用的 template 时,此前
		// prompt 会变成空串 —— Agent 收到**空提示词**照常执行,用户以为
		// 提示词生效了(约定 14:该失败却成功)。显式失败(fail closed)。
		if tplRaw, ok := node.Config["prompt"].(map[string]any); ok {
			tpl, isStr := tplRaw["template"].(string)
			if !isStr {
				return compiler.NodeOutcome{
					State: model.NodeFailed,
					Error: fmt.Sprintf("节点 %s 的 prompt 必须是字符串,或 {template: \"...\"} 对象(对象里没有可用的 template)", node.ID),
				}
			}
			prompt = tpl
		}
	}
	rendered, missing, err := e.Tpl.RenderErr(prompt, data)
	if err != nil {
		// 渲染失败 → 节点失败(约定 14)。此前用 Render:渲染失败时它**吞掉
		// error 并回退原始模板**,于是 Agent 收到的是一段没渲染过的模板
		// 文本,照常执行、照常 SUCCESS —— 用户完全不知道提示词根本没渲染。
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: fmt.Sprintf("节点 %s 的 prompt 模板渲染失败: %v", node.ID, err),
		}
	}
	instructions := rendered
	instructionsTpl := ""
	if extra := cfgStr(node, "instructions"); extra != "" {
		r2, m2, rerr := e.Tpl.RenderErr(extra, data)
		if rerr != nil {
			return compiler.NodeOutcome{
				State: model.NodeFailed,
				Error: fmt.Sprintf("节点 %s 的 instructions 模板渲染失败: %v", node.ID, rerr),
			}
		}
		missing = append(missing, m2...)
		instructionsTpl = extra
		instructions = strings.TrimSpace(rendered + "\n\n" + r2)
	}
	// 缺失变量必须可见:此前 `rendered, _ :=` 把 missing 列表直接丢掉 ——
	// prompt 里 {{revwie}} 拼错、或引用了上游尚未产出的 {{git_diff}} 时,
	// Agent 收到残缺 prompt 并照常返回 SUCCESS/decision,没有任何告警。
	// 不让节点失败(可能是合法的可选变量),但要发事件 + 写日志。
	e.reportMissingVars(env, node, missing)
	// "上游没产出"同样必须可见(#280):键**存在**但值为空时,模板引擎按
	// "键存在=可解析"的口径不报缺失 → {{plan}} 渲染成空串 → 下游照常
	// 返回 SUCCESS,用户以为"方案传给下游了"。
	e.reportEmptySlots(env, node, emptySlots, referencedVars(e.Tpl, prompt, instructionsTpl))

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
		Timeout:      timeoutSecondsOf(cfgFloat(node, "timeout_seconds")),
		// 有状态适配器(mock)据此按执行隔离计数,避免同一工作流
		// 并发运行时决策序列互相错位。
		ExecutionID: env.Exec.ID,
	}
	if wd := e.Git.WorkingDir(); req.WorkingDir == "" && wd != "" {
		req.WorkingDir = wd
	}

	// 重试策略:retry.max_attempts(默认 1 = 不重试),backoff: fixed | exponential
	//
	// 配置非法 → 节点**失败**(fail closed):此前字符串 "3" 被当成 0 再钳到 1
	// → 重试被静默关闭,节点照常成功(约定 14)。
	// 每次尝试的流式输出与节点级超时都在闭包内重来(退避后超时额度重新计时)。
	outcome := e.withNodeRetries(ctx, env, node, func(ectx context.Context) compiler.NodeOutcome {
		done := make(chan struct{})
		go e.streamAgentOutput(ctx, env, node.ID, a.ID(), req, done)

		attempt := ectx
		if req.Timeout > 0 {
			var cancel context.CancelFunc
			attempt, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
			defer cancel()
		}
		out := e.invokeAgent(attempt, a, req)
		close(done)
		return out
	})
	if outcome.State == model.NodeSuccess {
		// 保存节点输出到上下文(review/plan 等按需引用)。
		//
		// 写的是**活的**会话状态(st),不是 e.stateView(st) —— 后者是
		// 新建的一次性副本,写完即丢:plan/review/issues/last_output/
		// test_result 从未写回会话状态,下游节点读到的恒为空值,
		// {{plan}} / {{review}} / {{issues}} / {{test_result}} 永远渲染
		// 成空串(第 19 轮 P0:整条跨节点上下文链是死的)。
		if err := e.CtxMgr.SaveNodeOutput(mode, node.ID, nodeOutputOf(outcome), st); err != nil {
			// 写不进会话状态 = 上下文链断了,下游节点会拿到**空上下文**
			// 照常执行并照常 SUCCESS(约定 #14:该失败却成功等同 P0)。
			// 显式失败,原因可读。
			return compiler.NodeOutcome{
				State: model.NodeFailed,
				Error: fmt.Sprintf("节点 %s 的输出写入会话状态失败: %v", node.ID, err),
			}
		}
	}
	return outcome
}

// nodeOutputOf 把节点结果整理成 contextx.SaveNodeOutput 的入参。
//
// issues 只在 Agent 真的给出了时才带上:键**存在但值为 nil**会让
// SaveNodeOutput 往会话状态写一个空槽位(污染 state_json,且让
// "该键有没有值"这件事变得不可判)。
func nodeOutputOf(outcome compiler.NodeOutcome) map[string]any {
	res := map[string]any{
		"summary": outcome.Summary,
		"output":  outcome.Output,
	}
	if outcome.Data != nil {
		if issues, ok := outcome.Data["issues"]; ok {
			res["issues"] = issues
		}
	}
	return res
}

// reportMissingVars 上报模板渲染时缺失的变量。
// 只告警不让节点失败:部分变量确实是可选的(上游分支未走到)。
// 但拼错的变量名与"上游没产出"必须区分得开,否则用户无从下手。
func (e *Engine) reportMissingVars(env *compiler.RunEnv, node *model.Node, missing []string) {
	if len(missing) == 0 {
		return
	}
	// 去重 + 排序,保证事件内容稳定可比对
	seen := map[string]bool{}
	uniq := make([]string, 0, len(missing))
	for _, m := range missing {
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		uniq = append(uniq, m)
	}
	sort.Strings(uniq)
	msg := "prompt 模板变量缺失(渲染为空串): " + strings.Join(uniq, ", ")
	logx.Warn(msg, "execution", env.Exec.ID, "node", node.ID)
	// 用独立的 node.warning 类型,不复用 NodeWaiting —— 后者语义是
	// "暂停等待人工",前端会据此渲染 HITL 交互面板,拿来做告警会误导。
	env.Bus.Emit(event.New("node.warning", env.Exec.ID, node.ID, map[string]any{
		"node": node.ID, "reason": "missing_template_vars", "missing": uniq, "message": msg,
	}))
}

// reportEmptySlots 上报"上游没有产出"的上下文槽位(#280)。
//
// 与 reportMissingVars 的分工(两条 reason 不同,用户才能区分"我拼错了"
// 与"上游没给我东西"):
//   - missing_template_vars:键**不存在**(变量名拼错)→ 模板层报;
//   - empty_context_slots:键**存在但值为空**(上游没产出)→ 上下文层报。
//
// **可见但不阻断**(语义选择的理由见 docs/OPTIMIZATION.md #280):
// 空上下文下 Agent 仍可能凭自身知识给出有效结论,硬失败(方案甲)会把
// "可选引用"变成硬约束 —— 需求.md 十六节把 review 的上下文列为"**至少**
// 提供",而那份清单本身就含有天然为空的项(Previous Review / Git Diff /
// Test Result);需求.md 三十五节只规定模板引擎支持哪些变量,**没有**
// "必需/可选"契约表 → 方案丙(区分必需与可选)在当前 DSL 里没有承载点。
// 所以:发事件 + 记日志,节点照常成功。
//
// 只报**被提示词引用到**的空槽位:没被引用的空槽位与这个节点无关,
// 报出来是纯噪音。
func (e *Engine) reportEmptySlots(env *compiler.RunEnv, node *model.Node, emptySlots []string, referenced map[string]bool) {
	if len(emptySlots) == 0 || len(referenced) == 0 {
		return
	}
	var hit []string
	for _, s := range emptySlots {
		if referenced[s] {
			hit = append(hit, s)
		}
	}
	if len(hit) == 0 {
		return
	}
	sort.Strings(hit)
	msg := "上游未产出,提示词里的上下文槽位渲染成空串: " + strings.Join(hit, ", ")
	logx.Warn(msg, "execution", env.Exec.ID, "node", node.ID)
	// 同一个事件类型(node.warning)、不同的 reason —— 前端与审计流可以
	// 按 reason 区分"拼错了"与"上游没产出"。
	env.Bus.Emit(event.New("node.warning", env.Exec.ID, node.ID, map[string]any{
		"node": node.ID, "reason": "empty_context_slots", "slots": hit, "message": msg,
	}))
}

// referencedVars 返回模板里引用到的变量名集合(去重)。
//
// 口径:**用空数据表渲染一次** —— 模板引擎对"数据表里没有的键"一律报成
// missing(这正是"键存在=可解析"的反面),于是 missing 列表恰好就是
// "这个模板引用了哪些变量"。
//
// 为什么不自己解析 {{...}} 占位符:解析口径必须只有一份(约定 13 的同源
// 精神 —— template 包是唯一定义处)。自己写一套正则解析,将来 template
// 包改了占位符语法,这里会静默失配。
//
// 渲染出错(理论上不会:同一个模板用真实数据渲染已经成功)→ 返回空集合,
// 调用方跳过空槽位告警(不阻断执行)。
func referencedVars(tpl TemplateRenderer, texts ...string) map[string]bool {
	out := map[string]bool{}
	if tpl == nil {
		return out
	}
	for _, txt := range texts {
		if txt == "" {
			continue
		}
		_, missing, err := tpl.RenderErr(txt, map[string]any{})
		if err != nil {
			continue
		}
		for _, m := range missing {
			if m != "" {
				out[m] = true
			}
		}
	}
	return out
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
	switch normalizeAgentStatus(resp.Status) {
	case string(coreagent.StatusSuccess):
		// 契约内:成功(保持默认状态)
	case string(coreagent.StatusFailed):
		outcome.State = model.NodeFailed
		outcome.Error = resp.Error
		if outcome.Error == "" {
			// 失败却没有原因:用户只看到"失败",无从下手
			outcome.Error = "Agent " + a.ID() + " 返回 FAILED"
		}
	case string(coreagent.StatusWaitUser):
		outcome.State = model.NodeWaiting
		if outcome.Data == nil {
			outcome.Data = map[string]any{}
		}
		outcome.Data["responses"] = []string{"approve", "reject", "continue", "instruction"}
	default:
		// 契约外状态 → **显式失败**(约定 14),与 Skill 侧同一口径:
		// 工作流无法判断这个 Agent 到底成功没有,按成功继续就是
		// "该失败却成功"。
		outcome.State = model.NodeFailed
		if strings.TrimSpace(string(resp.Status)) == "" {
			outcome.Error = fmt.Sprintf("Agent %q 未返回状态(契约要求 SUCCESS/FAILED/WAIT_USER)", a.ID())
		} else {
			outcome.Error = fmt.Sprintf("Agent %q 返回未知状态 %q(契约只允许 SUCCESS/FAILED/WAIT_USER)",
				a.ID(), resp.Status)
		}
	}
	return outcome
}

// normalizeAgentStatus 归一化 Agent 返回的状态(去首尾空白 + 转大写)。
//
// agent.AgentStatus 是字符串类型,外部实现可能返回 "failed" / "wait_user"。
// 运行期此前**精确匹配**大小写:小写 "failed" 不匹配任何分支 → 落到
// 默认 → 节点按**成功**继续(该失败却成功,约定 14)。
//
// 口径与 Skill 侧一致(skill.NormalizeStatus:去空白 + 转大写)。
// agent 包目前只有 NormalizeDecision、没有状态归一化函数,所以这里在
// 运行期本地归一(不改 agent 包的所有权范围)。
func normalizeAgentStatus(s coreagent.AgentStatus) string {
	return strings.ToUpper(strings.TrimSpace(string(s)))
}

// withNodeRetries 按节点级 retry 配置包裹一次执行(默认 1 次 = 不重试,
// 退避 fixed 2s / exponential 1s→30s 上限,见 retryBackoffDelay)。
//
// 只对 NodeFailed 重试;SUCCESS / WAIT_USER / SKIPPED 原样返回(WAIT_USER
// 是"等人",不是可重试的失败)。每次重试前发 node.retry 事件(带
// attempt / max_attempts / error);退避等待期间 ctx 取消 → 立即失败,
// 不吞取消。配置非法 → 节点**失败**(fail closed):此前字符串 "3" 被当成
// 0 再钳到 1 → 重试被静默关闭,节点照常成功(约定 14)。
//
// agent 与 script 两类节点共用本循环(子进程层是最常见的瞬时失败源),
// 事件名统一为 node.retry(历史执行里的 agent.retry 行原样保留,审计页
// 按原文展示)。
func (e *Engine) withNodeRetries(ctx context.Context, env *compiler.RunEnv, node *model.Node, run func(ctx context.Context) compiler.NodeOutcome) compiler.NodeOutcome {
	policy, rerr := retryPolicyOf(node)
	if rerr != nil {
		return compiler.NodeOutcome{
			State: model.NodeFailed,
			Error: fmt.Sprintf("节点 %s 的 retry 配置非法: %v", node.ID, rerr),
		}
	}
	var outcome compiler.NodeOutcome
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if attempt > 1 {
			delay := policy.delayFor(attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return compiler.NodeOutcome{State: model.NodeFailed, Error: "执行取消"}
			}
		}
		outcome = run(ctx)
		if outcome.State != model.NodeFailed || ctx.Err() != nil {
			break
		}
		if attempt < policy.MaxAttempts {
			e.Bus.Emit(event.New("node.retry", env.Exec.ID, node.ID, map[string]any{
				"node_type": string(node.Type), "attempt": attempt, "max_attempts": policy.MaxAttempts,
				"error": outcome.Error,
			}))
		}
	}
	return outcome
}

// retryPolicy 是节点级重试策略的解析结果(可执行形态)。
type retryPolicy struct {
	MaxAttempts int
	Backoff     string // fixed | exponential
	// InitialSeconds 首次重试前的等待秒数。缺省 2(与历史行为一致:
	// fixed 恒 2s;exponential 首次重试 2s 起步);0 = 立即重试。
	InitialSeconds float64
	// MaxSeconds 退避增长的上限秒数;<=0 = 不封顶。缺省:fixed 不封顶
	// (无增长,封顶会违背用户显式设置的 initial),exponential 30
	// (历史行为)。
	MaxSeconds float64
}

// delayFor 计算第 attempt 次尝试失败后、下一次重试前的等待时长。
//
// fixed:恒为 initial;exponential:initial * 2^(attempt-2)(首次重试即
// initial,此后翻倍),受 MaxSeconds 封顶。缺省值与历史行为逐位一致:
// fixed 2s;exponential 2s/4s/8s…封顶 30s(历史公式 1s<<(attempt-1) 的
// 可观察序列与此相同)。
func (p retryPolicy) delayFor(attempt int) time.Duration {
	delay := p.InitialSeconds
	if p.Backoff == "exponential" {
		delay *= math.Pow(2, float64(attempt-2))
	}
	if p.MaxSeconds > 0 && delay > p.MaxSeconds {
		delay = p.MaxSeconds
	}
	if delay < 0 || math.IsNaN(delay) {
		return 0
	}
	// attempt ≤ 10(钳制)且秒数 ≤ 86400 → 2^8*86400 ≈ 2.2e7s,换算成
	// time.Duration 不会溢出。
	return time.Duration(delay * float64(time.Second))
}

// retryPolicyOf 读取节点 retry 配置。
//
// **fail closed**(约定 14 + P2-6):读不出合法取值 → 返回错误,让节点
// **失败**并说明原因。
//
// 缺陷背景:此前 max_attempts 用 toInt 读,而 toInt 只认数值形态 ——
// 字符串 "3" 认不出来 → 0 → 钳到 1 → **重试被静默关闭**:用户在 DSL 里
// 写了 max_attempts: "3" 期望重试 3 次,实际只跑 1 次,节点照常 SUCCESS,
// 没有任何提示(约定 14:静默丢弃与崩溃同样危险)。
//
// 合法形态(YAML 整数 / JSON 整数 / json.Number)照旧;字符串**不合法**
// (DSL 里写 "3" 是笔误,不是"重试 3 次")。
//
// 校验期同口径(workflow/validator/validator.go 的 retry 子键校验),
// 两边结论一致(约定 13)。
func retryPolicyOf(node *model.Node) (retryPolicy, error) {
	p := retryPolicy{MaxAttempts: 1, Backoff: "fixed"}
	raw, ok := node.Config["retry"].(map[string]any)
	if !ok {
		// retry 存在但不是对象:同样不能静默按"不重试"处理。
		if _, present := node.Config["retry"]; present {
			return retryPolicy{}, fmt.Errorf("retry 必须是对象(如 {max_attempts: 3, backoff: fixed})")
		}
		return p, nil
	}
	if v, ok := raw["max_attempts"]; ok {
		n, cerr := retryMaxAttempts(v)
		if cerr != nil {
			return retryPolicy{}, cerr
		}
		p.MaxAttempts = n
	}
	if v, ok := raw["backoff"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return retryPolicy{}, fmt.Errorf("retry.backoff 必须是字符串(fixed 或 exponential),实际是 %T", v)
		}
		if s != "" {
			p.Backoff = s
		}
	}
	// 退避参数缺省(迭代 36):与历史行为一致 —— fixed 恒 2s 不封顶;
	// exponential 首次重试 2s、翻倍增长、封顶 30s。
	p.InitialSeconds = 2
	p.MaxSeconds = 0
	if p.Backoff == "exponential" {
		p.MaxSeconds = 30
	}
	if v, ok := raw["initial_backoff_seconds"]; ok {
		f, cerr := retrySecondsOf(v, "initial_backoff_seconds")
		if cerr != nil {
			return retryPolicy{}, cerr
		}
		p.InitialSeconds = f
	}
	if v, ok := raw["max_backoff_seconds"]; ok {
		f, cerr := retrySecondsOf(v, "max_backoff_seconds")
		if cerr != nil {
			return retryPolicy{}, cerr
		}
		p.MaxSeconds = f
	}
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	if p.MaxAttempts > 10 {
		p.MaxAttempts = 10
	}
	return p, nil
}

// retrySecondsOf 解析 retry 的退避秒数(initial_backoff_seconds /
// max_backoff_seconds)。
//
// 合法形态:数值(整数 / 浮点 / json.Number);字符串 / 布尔不合法
// (fail closed,与 max_attempts 同口径)。必须非负;上界
// compiler.RetryBackoffSecondsMax(与 timeout_seconds 同界,防
// time.Duration 溢出)。
func retrySecondsOf(v any, key string) (float64, error) {
	var f float64
	switch n := v.(type) {
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint64:
		f = float64(n)
	case float32:
		f = float64(n)
	case float64:
		f = n
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, fmt.Errorf("retry.%s 必须是数字(实际是 %q)", key, n.String())
		}
		f = parsed
	default:
		return 0, fmt.Errorf("retry.%s 必须是数字(实际是 %T)", key, v)
	}
	if f < 0 || math.IsNaN(f) {
		return 0, fmt.Errorf("retry.%s 必须是非负数(实际是 %v)", key, f)
	}
	if f > compiler.RetryBackoffSecondsMax {
		f = compiler.RetryBackoffSecondsMax
	}
	return f, nil
}

// retryMaxAttempts 解析 retry.max_attempts,返回可判定的尝试次数。
//
// 合法形态:YAML 整数(int/int64/uint…)、JSON 整数(float64,且必须是整数值)、
// json.Number。字符串 / 布尔 / 小数 / 负数一律报错(fail closed)。
//
// 上限:超过 10 次按 10 次算(与 retryPolicyOf 的钳制一致);uint64 超过
// int32 范围时先钳到上限再返回,避免转换溢出成负数。
func retryMaxAttempts(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return retryAttemptsClamped(int64(n))
	case int8:
		return retryAttemptsClamped(int64(n))
	case int16:
		return retryAttemptsClamped(int64(n))
	case int32:
		return retryAttemptsClamped(int64(n))
	case int64:
		return retryAttemptsClamped(n)
	case uint:
		return retryAttemptsClamped(int64(n))
	case uint8:
		return retryAttemptsClamped(int64(n))
	case uint16:
		return retryAttemptsClamped(int64(n))
	case uint32:
		return retryAttemptsClamped(int64(n))
	case uint64:
		if n > math.MaxInt32 {
			return 10, nil // 超上限:按 10 次算(与钳制一致)
		}
		return retryAttemptsClamped(int64(n))
	case float32:
		return retryAttemptsFromFloat(float64(n))
	case float64:
		return retryAttemptsFromFloat(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("retry.max_attempts 必须是整数(实际是 %q)", n.String())
		}
		return retryAttemptsClamped(i)
	}
	return 0, fmt.Errorf("retry.max_attempts 必须是整数(实际是 %T)", v)
}

// retryAttemptsClamped 校验非负并返回尝试次数(上限 10)。
func retryAttemptsClamped(n int64) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("retry.max_attempts 必须是非负整数(实际是 %d)", n)
	}
	if n > 10 {
		return 10, nil
	}
	return int(n), nil
}

// retryAttemptsFromFloat 校验浮点形态:必须是整数值(3 可以,3.5 不行)。
func retryAttemptsFromFloat(f float64) (int, error) {
	if f != math.Trunc(f) {
		return 0, fmt.Errorf("retry.max_attempts 必须是整数(实际是 %v)", f)
	}
	if f < 0 {
		return 0, fmt.Errorf("retry.max_attempts 必须是非负整数(实际是 %v)", f)
	}
	if f > 10 {
		return 10, nil
	}
	return int(f), nil
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
	// args 必须是对象:此前写法 `args, _ := node.Config["args"].(map[string]any)`
	// 在类型断言失败时把参数变成 nil,**不报任何错** —— 用户在节点上写的
	// 参数全部静默丢失,Skill 拿到空参数照常执行(约定 14:该失败却成功)。
	// 显式失败(fail closed):args 不存在或为空值是合法的,类型不对不是。
	args, argsOK := node.Config["args"].(map[string]any)
	if !argsOK {
		if raw, exists := node.Config["args"]; exists && raw != nil {
			return compiler.NodeOutcome{
				State: model.NodeFailed,
				Error: fmt.Sprintf("节点 %s 的 args 必须是对象(键值对),实际是 %T", node.ID, raw),
			}
		}
		args = nil
	}
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
	// 状态归一化:Skill 接口承诺 SUCCESS | FAILED | WAIT_USER,但状态是
	// 字符串,外部实现可能返回 "wait_user" / "Failed" / " wait_user "。
	// 归一化的唯一来源是 skill.NormalizeStatus(去空白 + 转大写)——
	// 散落的字面量匹配漏掉一种形态,就意味着"该失败/该等待却成功"
	// (约定 14)。此前只精确匹配 "WAIT_USER" 与 {"FAILED","failed"}:
	// 小写 "wait_user" 被当成功,人工确认这一步被静默跳过。
	switch skill.NormalizeStatus(resp.Status) {
	case skill.StatusSuccess:
		// 契约内:成功(保持默认状态)
	case skill.StatusFailed:
		outcome.State = model.NodeFailed
		outcome.Error = resp.Error
		if outcome.Error == "" {
			// 失败却没有原因:用户只看到"失败"两个字,无从下手
			outcome.Error = "Skill " + skillID + " 返回 FAILED"
		}
	case skill.StatusWaitUser:
		// Skill 接口承诺 WAIT_USER:暂停等待用户输入(与人工节点一致)
		outcome.State = model.NodeWaiting
		if outcome.Data == nil {
			outcome.Data = map[string]any{}
		}
		outcome.Data["responses"] = []string{"approve", "reject", "continue", "instruction"}
	default:
		// 契约外状态 → **显式失败**(约定 14)。
		//
		// 旧口径是"未知状态按成功处理(不猜)":但"不猜"应当是"不猜某个
		// 具体状态值",而不是"猜它是成功"。契约只承诺三个状态,返回第四种
		// 取值说明这个 Skill 出了问题(实现有 bug / 第三方用了别的口径),
		// 此时工作流**无法知道它到底成功没有** —— 按成功继续就是
		// "该失败却成功",用户看到的却是"执行成功"。
		// 失败原因里点名状态值与契约,用户照着改即可。
		outcome.State = model.NodeFailed
		if strings.TrimSpace(resp.Status) == "" {
			outcome.Error = fmt.Sprintf("Skill %q 未返回状态(契约要求 SUCCESS/FAILED/WAIT_USER)", skillID)
		} else {
			outcome.Error = fmt.Sprintf("Skill %q 返回未知状态 %q(契约只允许 SUCCESS/FAILED/WAIT_USER)",
				skillID, resp.Status)
		}
	}
	if resp.Decision != "" {
		outcome.Decision = resp.Decision
	}
	// 技能节点的产物同样写进活的会话状态(此前写进一次性副本,等于没写)。
	if err := e.CtxMgr.SaveNodeOutput("skill", node.ID, map[string]any{
		"summary": resp.Output, "output": resp.Output,
	}, st); err != nil {
		outcome.State = model.NodeFailed
		if outcome.Error == "" {
			outcome.Error = fmt.Sprintf("节点 %s 的输出写入会话状态失败: %v", node.ID, err)
		}
	}
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
	// git 节点同理:args 必须是对象。git 服务靠 args 里的
	// working_dir / staged / max_count / name 生效,类型不对时这些参数
	// 会被静默丢弃(跑成"默认 git status")—— 显式失败(fail closed)。
	gitArgs, gitArgsOK := node.Config["args"].(map[string]any)
	if !gitArgsOK {
		if raw, exists := node.Config["args"]; exists && raw != nil {
			return compiler.NodeOutcome{
				State: model.NodeFailed,
				Error: fmt.Sprintf("节点 %s 的 args 必须是对象(键值对),实际是 %T", node.ID, raw),
			}
		}
		gitArgs = nil
	}
	out, err := e.Git.Run(ctx, op, gitArgs)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	e.Bus.Emit(event.New(event.GitExecuted, env.Exec.ID, node.ID, map[string]any{
		"operation": op, "output": truncate(out, 500),
	}))
	// diff 结果保存为上下文,供 review 节点引用
	if op == "diff" {
		st.Set(contextx.StateKeyGitDiff, out)
		// 制品内容按"值的形态"脱敏后落库(约定 8)。
		//
		// 此前原文落库:diff 里若含用户不小心提交进仓库的凭据(.env 之类),
		// 明文既进 SQLite 又经 API 原样返回 —— 同一份内容在 /nodes 里
		// 是脱敏的,两个出口口径不一致。
		//
		// 脱敏只替换**凭据形态的值**(sk-/ghp_/AKIA/JWT/高熵长串),
		// 代码里的标识符(token = getToken())原样保留 —— diff 的可读性
		// 与审计证据不受影响(第八轮"artifact 不脱敏"的理由正是这个,
		// 本轮改用形态判定而不是"整值替换"来同时满足两边)。
		// 制品 ID 生成失败(熵源不可用)时不能退化成全零 ID ——
		// 那会让每次 diff 落到同一条制品记录上(互相覆盖)。
		// 与"落库失败"同口径:记错误、跳过制品,节点本身照常成功。
		artifactID, err := newID("art")
		if err != nil {
			logx.Error("制品 ID 生成失败,已跳过制品落库", "execution", env.Exec.ID,
				"node", node.ID, "error", err.Error())
			return compiler.NodeOutcome{State: model.NodeSuccess, Output: out, Summary: "git " + op}
		}
		artifact := &model.Artifact{
			ID: artifactID, ExecutionID: env.Exec.ID, NodeID: node.ID,
			Name: "git-diff", ContentType: "text/x-diff",
			Content:   coreagent.MaskCredentialValues(out),
			CreatedAt: time.Now().Format(time.RFC3339Nano),
		}
		if err := e.Repo.SaveArtifact(artifact); err != nil {
			// 此前是 _ = ... :落库失败被吞掉,制品静默缺失 ——
			// 用户在界面上看不到任何提示,只会以为"这次没有 diff"。
			logx.Error("制品落库失败", "execution", env.Exec.ID,
				"node", node.ID, "artifact", artifact.Name, "error", err.Error())
		}
	}
	return compiler.NodeOutcome{State: model.NodeSuccess, Output: out, Summary: "git " + op}
}

// runScriptNode 执行脚本节点(本地 shell)。
// 显式开关门禁:该节点等价于任意本地命令执行,而工作流可由 YAML 导入
// 获得,故默认拒绝,需用户在设置页显式放行(需求 §42 禁止绕过权限系统)。
func (e *Engine) runScriptNode(ctx context.Context, env *compiler.RunEnv, node *model.Node) compiler.NodeOutcome {
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
	if s := timeoutSecondsOf(cfgFloat(node, "timeout_seconds")); s > 0 {
		timeout = s
	}
	workDir := cfgStr(node, "working_dir")
	if workDir == "" {
		workDir = e.Git.WorkingDir()
	}
	// retry 包裹的是"执行一次脚本":门禁与配置检查在循环外(策略拒绝与
	// 写错的配置不是可重试的失败,重试只会重复同样的错误);节点级超时
	// 额度在闭包内,每次尝试重新计时(与 agent 节点语义一致)。
	return e.withNodeRetries(ctx, env, node, func(ectx context.Context) compiler.NodeOutcome {
		cctx, cancel := context.WithTimeout(ectx, time.Duration(timeout)*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, shell, "-c", command)
		// 脚本派生的后台子进程可能持有输出管道:进程组击杀 + WaitDelay
		// 兜底,保证调用方及时返回(与 Agent 适配器同一策略)
		coreagent.ConfigureProcess(cmd)
		cmd.WaitDelay = 5 * time.Second
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
	})
}

// runMergeNode 汇合并行分支结果:输出聚合摘要(成功/失败/跳过/被截断)。
//
// 两个关键修正(此前都导致"并行区没跑完"被报成"全部成功"):
//
//  1. 统计对象错了。此前按"merge 的入边"统计,但入边 ≠ 并行区的分支:
//     分支内嵌路由时(edge decide --REJECTED--> patch --> merge),
//     条件不成立的 patch 本就不该跑,却是 merge 的一条入边。
//     现在改为从"喂给本 merge 的 parallel 节点"取分支清单。
//  2. "查不到状态"一律算成功。分支被 enabled:false 跳过、被
//     RetryNode(skip) 跳过,或因 KeyAbort 根本没跑到时 StatusKey 缺失,
//     于是输出"汇合 2 个分支,全部成功"。现在区分:
//     - 并行区的**无条件扇出**是必须执行的,没有状态 = 被截断 → 失败;
//     - 条件分支没状态是正常路由结果 → 只在摘要里标注,不算失败;
//     - 显式 SKIPPED → 标注,不算失败(用户主动禁用)。
func (e *Engine) runMergeNode(ctx context.Context, env *compiler.RunEnv, node *model.Node, st compiler.StateAccess) compiler.NodeOutcome {
	branchInfo, mustRun := e.mergeBranches(env, node.ID)
	branchIDs := make([]string, len(branchInfo))
	for i, b := range branchInfo {
		branchIDs[i] = b.id
	}
	var succeeded, failed, skipped, notRun []string
	for _, b := range branchInfo {
		status := ""
		if v, ok := st.Get(compiler.StatusKey(b.id)); ok {
			status = fmt.Sprint(v)
		}
		switch status {
		case string(model.NodeSuccess):
			succeeded = append(succeeded, b.id)
		case string(model.NodeFailed):
			failed = append(failed, b.id)
		case string(model.NodeSkipped):
			skipped = append(skipped, b.id)
		case "":
			// 无状态:无条件扇出 = 被截断/从未执行;条件分支 = 路由未选中
			if mustRun[b.id] {
				notRun = append(notRun, b.id)
			} else {
				skipped = append(skipped, b.id)
			}
		default:
			// WAITING 等中间态:分支还没跑完(并行区未收敛),按未完成处理
			notRun = append(notRun, b.id)
		}
	}

	summary := fmt.Sprintf("汇合 %d 个分支", len(branchIDs))
	if len(failed) > 0 {
		summary += ",失败分支: " + strings.Join(failed, ", ")
	}
	if len(notRun) > 0 {
		summary += ",未完成分支: " + strings.Join(notRun, ", ")
	}
	if len(skipped) > 0 {
		summary += ",跳过/未选中: " + strings.Join(skipped, ", ")
	}
	// 只有确实全部成功才这么说 —— 分支被跳过时不能说"全部成功"
	// (那正是本项修复要消除的失真)
	if len(failed) == 0 && len(notRun) == 0 && len(skipped) == 0 {
		summary += ",全部成功"
	}
	// 失败或必跑分支未完成 → merge 失败,让并行区没跑完在执行记录里可见
	state := model.NodeSuccess
	if len(failed) > 0 || len(notRun) > 0 {
		state = model.NodeFailed
	}
	out := compiler.NodeOutcome{
		State:   state,
		Summary: summary,
		Data: map[string]any{
			// 分支 ID 序列(供前端展示);struct 本身不可 JSON 序列化
			"branches": branchIDs, "succeeded": succeeded,
			"failed": failed, "skipped": skipped, "not_run": notRun,
		},
	}
	if state == model.NodeFailed {
		out.Error = summary
	}
	e.Bus.Emit(event.New("merge.completed", env.Exec.ID, node.ID, map[string]any{
		"branches": branchIDs, "succeeded": succeeded,
		"failed": failed, "skipped": skipped, "not_run": notRun,
	}))
	return out
}

// mergeBranch 是一个并行分支的根节点。
type mergeBranch struct {
	id   string
	cond string // 该扇出边上的条件(空 = 无条件 = 必须执行)
}

// mergeBranches 找出喂给 mergeNode 的并行区分支根。
// 优先找直接入边的 parallel 节点;没有则找能到达 mergeNode 的
// parallel 节点(并行区后面可能隔了别的节点)。
func (e *Engine) mergeBranches(env *compiler.RunEnv, mergeNode string) ([]mergeBranch, map[string]bool) {
	wf := env.WF
	var par *model.Node
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type != model.NodeTypeParallel {
			continue
		}
		for _, edge := range wf.OutgoingEdges(n.ID) {
			if edge.To == mergeNode {
				par = n
				break
			}
		}
		if par != nil {
			break
		}
	}
	if par == nil {
		// 没有直接相连的 parallel:退回到"入边即分支"(无并行区的裸 merge)
		var out []mergeBranch
		for _, edge := range wf.Edges {
			if edge.To == mergeNode {
				out = append(out, mergeBranch{id: edge.From, cond: edge.Condition})
			}
		}
		return out, nil
	}
	var branches []mergeBranch
	mustRun := map[string]bool{}
	for _, edge := range wf.OutgoingEdges(par.ID) {
		branches = append(branches, mergeBranch{id: edge.To, cond: edge.Condition})
		if edge.Condition == "" {
			mustRun[edge.To] = true
		}
	}
	return branches, mustRun
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
				st.Set(contextx.StateKeyUserInstruction, inst)
			} else if resp, _ := m["response"].(string); resp != "" && resp != "approve" && resp != "reject" && resp != "continue" {
				st.Set(contextx.StateKeyUserInstruction, resp)
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
	respNames := cfgStringList(node, "responses")
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
	// input 与 args 同理:必须是对象。类型不对时子工作流收到空 input,
	// 用户写的输入被静默丢弃(约定 14)。
	input, inputOK := node.Config["input"].(map[string]any)
	if !inputOK {
		if raw, exists := node.Config["input"]; exists && raw != nil {
			return compiler.NodeOutcome{
				State: model.NodeFailed,
				Error: fmt.Sprintf("节点 %s 的 input 必须是对象(键值对),实际是 %T", node.ID, raw),
			}
		}
		input = nil
	}
	result, err := e.SubworkflowRunner(ctx, wfID, env.Exec.Task, input)
	if err != nil {
		return compiler.NodeOutcome{State: model.NodeFailed, Error: err.Error()}
	}
	raw, _ := json.Marshal(result)
	return compiler.NodeOutcome{State: model.NodeSuccess, Output: string(raw), Data: result}
}

// stateView 将 StateAccess 转成普通 map(只读视图,供上下文构造)。
//
// 键名用 contextx 的常量(唯一定义处):这里读、contextx.Build 读、
// contextx.SaveNodeOutput 写,三处必须是同一份键名 —— 拼写漂移会让
// 上下文链静默失效(第 19 轮 P0)。
func (e *Engine) stateView(st compiler.StateAccess) map[string]any {
	view := map[string]any{}
	for _, key := range []string{
		contextx.StateKeyTask, contextx.StateKeyPlan, contextx.StateKeyGitDiff,
		contextx.StateKeyReview, contextx.StateKeyTestResult, contextx.StateKeyIssues,
		contextx.StateKeyIteration, contextx.StateKeyLastOutput,
		contextx.StateKeyRelevantFiles, compiler.KeyOutput, contextx.StateKeyUserInstruction,
	} {
		if v, ok := st.Get(key); ok {
			view[key] = v
		}
	}
	if v, ok := st.Get(compiler.KeyLoopCount); ok {
		view[contextx.StateKeyIteration] = v
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
// 状态经 SQLite state_json 持久化往返后 int 会变成 float64,
// YAML 来源的整数是 uint64 —— 都要认,否则 attempt 恒为 1,
// 循环/重试历史在明细表里互相覆盖。
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
	case int64:
		return int(n) + 1
	case uint64:
		return int(n) + 1
	case uint:
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
