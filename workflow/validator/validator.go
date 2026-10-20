// Package validator 对 Workflow 定义执行 Schema 与语义校验。
// 校验错误为结构化信息(code/node/message),前端可据此定位画布节点。
package validator

import (
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr"

	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// ValidationError 是单条校验错误。
type ValidationError struct {
	Code    string `json:"code"`
	Node    string `json:"node,omitempty"`
	Edge    string `json:"edge,omitempty"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Result 是一次校验的结果。
type Result struct {
	Valid    bool              `json:"valid"`
	Errors   []ValidationError `json:"errors"`
	Warnings []ValidationError `json:"warnings"`
}

// AgentResolver 判断 Agent 是否存在(由调用方注入 Agent Registry)。
type AgentResolver func(id string) bool

// WorkflowResolver 判断子工作流是否存在。
type WorkflowResolver func(id string) bool

// SkillResolver 判断 Skill 是否存在。
type SkillResolver func(id string) bool

// WorkflowResolver 判断子工作流是否存在(可选)。

// Validate 对 Workflow 执行全部校验规则。
// workflowResolver 非空时额外校验 subworkflow 引用(警告级别)。
func Validate(wf *model.Workflow, agents AgentResolver, skills SkillResolver, workflowResolver ...WorkflowResolver) *Result {
	var wfResolver WorkflowResolver
	if len(workflowResolver) > 0 {
		wfResolver = workflowResolver[0]
	}
	res := &Result{Valid: true, Errors: []ValidationError{}, Warnings: []ValidationError{}}
	addErr := func(code, node, format string, args ...any) {
		res.Valid = false
		res.Errors = append(res.Errors, ValidationError{Code: code, Node: node, Message: fmt.Sprintf(format, args...)})
	}
	addWarn := func(code, node, format string, args ...any) {
		res.Warnings = append(res.Warnings, ValidationError{Code: code, Node: node, Message: fmt.Sprintf(format, args...)})
	}

	if wf.ID == "" {
		addErr("MISSING_ID", "", "workflow.id 不能为空")
	}
	if wf.Name == "" {
		addErr("MISSING_NAME", "", "workflow.name 不能为空")
	}

	// 工作流级权限覆盖(DSL permissions 段)的结构校验。
	//
	// 结构非法(如 filesystem.write 写成字符串、段名拼错)会让运行期把
	// 该 Agent 的权限**全拒**(fail closed)—— 用户看到的却是"没有权限",
	// 排查方向被带偏(约定 #14:该失败却成功/静默降级都不行)。
	// 校验期用同一份解析逻辑(permission.ValidateOverrides)报出来,
	// 用户在保存时就看到"permissions 写错了"(约定 #13:校验期与运行期同源)。
	if err := permission.ValidateOverrides(wf.Permission); err != nil {
		addErr("INVALID_PERMISSION_OVERRIDE", "", "%v", err)
	}

	nodeIDs := make(map[string]bool, len(wf.Nodes))
	nodeByID := make(map[string]model.Node, len(wf.Nodes))
	for _, n := range wf.Nodes {
		if n.ID == "" {
			addErr("INVALID_NODE", "", "存在缺少 id 的节点")
			continue
		}
		if nodeIDs[n.ID] {
			addErr("DUPLICATE_NODE", n.ID, "节点 id %q 重复", n.ID)
			continue
		}
		nodeIDs[n.ID] = true
		nodeByID[n.ID] = n
		// 节点类型配置检查
		validateNodeConfig(n, agents, skills, addErr)
		// subworkflow 存在性(警告级别)
		if n.Type == model.NodeTypeSubWorkflow && wfResolver != nil {
			wid := ""
			if v, ok := n.Config["workflow_id"].(string); ok {
				wid = v
			}
			if wid != "" && !wfResolver(wid) {
				addWarn("SUBWORKFLOW_NOT_FOUND", n.ID, "子工作流 %q 不存在(运行时将失败)", wid)
			}
		}
		// 条件表达式语法检查
		for _, e := range wf.OutgoingEdges(n.ID) {
			if e.Condition != "" {
				checkExpression(e.Condition, addErr, n.ID)
			}
		}
	}
	for i, e := range wf.Edges {
		if !nodeIDs[e.From] {
			addErr("INVALID_EDGE", e.From, "第 %d 条边的 from 节点 %q 不存在", i+1, e.From)
		}
		if !nodeIDs[e.To] {
			addErr("INVALID_EDGE", e.To, "第 %d 条边的 to 节点 %q 不存在", i+1, e.To)
		}
		if e.From == e.To && e.From != "" {
			addErr("SELF_LOOP", e.From, "节点 %q 不能连接自身", e.From)
		}
	}

	if len(wf.Nodes) == 0 {
		addErr("EMPTY_WORKFLOW", "", "Workflow 至少需要一个节点")
		return res
	}

	// start 节点(无入边)数量
	var starts []string
	for _, n := range wf.Nodes {
		if len(wf.IncomingEdges(n.ID)) == 0 {
			starts = append(starts, n.ID)
		}
	}
	if len(starts) == 0 {
		addErr("NO_START", "", "不存在起始节点(所有节点都有入边)")
	} else if len(starts) > 1 {
		addErr("MULTIPLE_START", joinIDs(starts), "存在 %d 个起始节点: %v", len(starts), starts)
	}

	// 可达性
	reachable := map[string]bool{}
	if len(starts) >= 1 {
		markReachable(wf, starts[0], reachable)
	}
	for _, n := range wf.Nodes {
		if !reachable[n.ID] {
			addErr("UNREACHABLE_NODE", n.ID, "节点 %q 无法从起始节点到达", n.ID)
		}
	}

	// 孤立节点(无入边也无出边,且不是唯一节点)
	for _, n := range wf.Nodes {
		if len(wf.Nodes) > 1 && len(wf.IncomingEdges(n.ID)) == 0 && len(wf.OutgoingEdges(n.ID)) == 0 {
			addErr("ISOLATED_NODE", n.ID, "节点 %q 是孤立节点", n.ID)
		}
	}

	// 环检测(覆盖全部节点,包括不可达子图):每个环上必须至少有一条条件边
	cycles := findCycles(wf)
	for _, cyc := range cycles {
		hasCondition := false
		for _, e := range wf.Edges {
			if cyc[e.From] && cyc[e.To] && e.Condition != "" {
				hasCondition = true
				break
			}
		}
		if !hasCondition {
			addErr("CYCLE_WITHOUT_CONDITION", firstNodeID(cyc), "检测到循环但没有终止条件(需要至少一条条件边)")
		}
	}
	if len(cycles) > 0 && wf.Settings.MaxIterations <= 0 {
		addWarn("LOOP_WITHOUT_MAX_ITERATIONS", "", "Workflow 包含循环,建议设置 settings.max_iterations 防止无限循环")
	}

	// 结束路径:从任意节点是否存在无出边的可到达终止节点
	hasExit := false
	for _, n := range wf.Nodes {
		if reachable[n.ID] && len(wf.OutgoingEdges(n.ID)) == 0 {
			hasExit = true
			break
		}
	}
	if !hasExit {
		addErr("NO_EXIT_PATH", "", "不存在结束路径(没有可到达的终止节点)")
	}

	return res
}

// missingConfigByKey 把"必填配置键"映射到校验错误码与提示语。
//
// 必填清单本身不在这里 —— 唯一来源是 compiler.NodeConfigSpecFor
// (见 AGENTS.md 硬性约定 #13)。本表只负责"怎么报":
// 键名与契约表里的 Required 一一对应,由
// TestRequiredConfigKeysHaveErrorCodes 锁定(多一个少一个都失败)。
type missingConfig struct {
	code    string
	message string
}

var missingConfigByKey = map[string]missingConfig{
	"agent":       {code: "MISSING_AGENT", message: "agent 节点 %q 缺少 agent 配置"},
	"mode":        {code: "MISSING_MODE", message: "agent 节点 %q 缺少 mode 配置"},
	"skill":       {code: "MISSING_SKILL", message: "skill 节点 %q 缺少 skill 配置"},
	"command":     {code: "MISSING_COMMAND", message: "script 节点 %q 缺少 command 配置"},
	"workflow_id": {code: "MISSING_WORKFLOW_ID", message: "subworkflow 节点 %q 缺少 workflow_id 配置"},
	"operation":   {code: "INVALID_GIT_OPERATION", message: "git 节点 %q 缺少 operation 配置"},
}

// validateNodeConfig 按节点类型检查必需配置。
//
// 必填配置的唯一来源是 compiler.NodeConfigSpecFor(运行期实际读取哪些键,
// 契约表就写哪些键);本函数只保留校验器特有的检查:引用是否存在、
// 取值是否在允许集合内、表达式语法。
func validateNodeConfig(n model.Node, agents AgentResolver, skills SkillResolver, addErr func(code, node, format string, args ...any)) {
	str := func(key string) string {
		if v, ok := n.Config[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	// 节点类型必须已注册(与"是否禁用"无关:类型没注册就是没注册)。
	spec, known := compiler.NodeConfigSpecFor(n.Type)
	if !known {
		addErr("UNKNOWN_NODE_TYPE", n.ID, "节点 %q 的类型 %q 未注册", n.ID, string(n.Type))
		return
	}

	// 节点级开关:运行期只认布尔(false 才跳过)。非布尔值让运行期
	// **fail closed**(节点失败,不执行任何副作用)—— 用户以为禁用了
	// 其实没禁用,必须在校验期报出来(两边结论一致,约定 #13)。
	switch compiler.NodeEnabledStateOf(n.Config) {
	case compiler.NodeEnabledInvalid:
		addErr("INVALID_NODE_ENABLED", n.ID, "节点 %q 的 %s 必须是布尔值(true=启用,false=运行期跳过该节点)", n.ID, compiler.NodeEnabledKey)
		return
	case compiler.NodeEnabledOff:
		// 节点被禁用:运行期在类型分发之前就跳过它,一个配置键都不读
		// (executor.go 的 dispatchNode)→ 不要求任何配置,也不校验引用是否存在。
		return
	}

	// 必填配置(唯一来源:compiler.NodeConfigSpecFor)
	for _, key := range spec.Required {
		if str(key) != "" {
			continue
		}
		if mc, ok := missingConfigByKey[key]; ok {
			addErr(mc.code, n.ID, mc.message, n.ID)
			continue
		}
		addErr("MISSING_CONFIG", n.ID, "节点 %q 缺少必填配置 %q", n.ID, key)
	}

	switch n.Type {
	case model.NodeTypeAgent:
		if str("agent") != "" && agents != nil && !agents(str("agent")) {
			addErr("AGENT_NOT_FOUND", n.ID, "节点 %q 引用了不存在的 Agent %q", n.ID, str("agent"))
		}
	case model.NodeTypeSkill:
		if str("skill") != "" && skills != nil && !skills(str("skill")) {
			addErr("SKILL_NOT_FOUND", n.ID, "节点 %q 引用了不存在的 Skill %q", n.ID, str("skill"))
		}
	case model.NodeTypeCondition:
		// expression 运行期与编译器都不读(路由条件在**边上**),
		// 所以不是必填项;但仍然做语法检查,拼错了要报错。
		if expr := str("expression"); expr != "" {
			checkExpression(expr, addErr, n.ID)
		}
	case model.NodeTypeGit:
		// operation 的必填已由上面的通用循环报出;这里只查取值合法性
		// (允许集合与运行期 runGitNode 的 switch 同源)。
		if op := str("operation"); op != "" && !slices.Contains(compiler.GitOperations(), op) {
			addErr("INVALID_GIT_OPERATION", n.ID, "git 节点 %q 的 operation %q 非法(允许: %s)", n.ID, op, strings.Join(compiler.GitOperations(), "|"))
		}
	}
}

// checkExpression 使用 expr 引擎做条件表达式语法检查。
func checkExpression(expression string, addErr func(code, node, format string, args ...any), nodeID string) {
	_, err := expr.Compile(expression, expr.Env(newExpressionEnv()))
	if err != nil {
		addErr("INVALID_EXPRESSION", nodeID, "条件表达式 %q 非法: %v", expression, err)
	}
}

// newExpressionEnv 构造与运行期同源的校验环境。
//
// 唯一来源是 compiler.NewConditionEnv(与 compiler.EvalCondition 共用同一份
// 变量定义)—— 校验期与运行期必须同源,见 AGENTS.md 硬性约定 #13。
//
// 此前校验器用 struct env,只有 decision/status/output/iteration 四个字段,
// `variables.env == "prod"` 会被判成 unknown name 而拒绝保存,但同样表达式
// 在运行期完全合法 —— 校验器比运行时更严,导致用户无法使用运行期已支持的
// 变量。现在两侧共用同一份定义,不会再漂移。
func newExpressionEnv() map[string]any {
	return compiler.NewConditionEnv(nil)
}

// markReachable BFS 标记从 starts 可达的节点。
func markReachable(wf *model.Workflow, start string, reachable map[string]bool) {
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		for _, e := range wf.OutgoingEdges(id) {
			if !reachable[e.To] {
				queue = append(queue, e.To)
			}
		}
	}
}

// findCycles 在整张图中查找基础环(每个环返回节点集合)。
func findCycles(wf *model.Workflow) []map[string]bool {
	var cycles []map[string]bool
	visited := map[string]bool{}
	onStack := map[string]bool{}
	var stack []string

	var dfs func(id string)
	dfs = func(id string) {
		visited[id] = true
		onStack[id] = true
		stack = append(stack, id)
		for _, e := range wf.OutgoingEdges(id) {
			if !visited[e.To] {
				dfs(e.To)
			} else if onStack[e.To] {
				cyc := map[string]bool{}
				found := false
				for _, n := range stack {
					if n == e.To {
						found = true
					}
					if found {
						cyc[n] = true
					}
				}
				cycles = append(cycles, cyc)
			}
		}
		stack = stack[:len(stack)-1]
		onStack[id] = false
	}
	for _, n := range wf.Nodes {
		if !visited[n.ID] {
			dfs(n.ID)
		}
	}
	return cycles
}

func firstNodeID(cyc map[string]bool) string {
	for id := range cyc {
		return id
	}
	return ""
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

// Summary 把校验错误压成一行可读文本(最多 3 条)。
//
// 用途:运行期/接口层的错误消息此前只有"工作流校验失败"六个字,用户
// 看不到**哪一条**规则不满足 —— 例如 permissions 段写错时,用户既不能
// 定位也不能修(死路)。带上摘要后,错误文本本身就指明了要改什么。
func Summary(errs []ValidationError) string {
	if len(errs) == 0 {
		return ""
	}
	const maxShow = 3
	parts := make([]string, 0, maxShow)
	for i, e := range errs {
		if i >= maxShow {
			parts = append(parts, fmt.Sprintf("(另有 %d 条)", len(errs)-maxShow))
			break
		}
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, "; ")
}
