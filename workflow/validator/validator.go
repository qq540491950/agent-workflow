// Package validator 对 Workflow 定义执行 Schema 与语义校验。
// 校验错误为结构化信息(code/node/message),前端可据此定位画布节点。
package validator

import (
	"fmt"

	"github.com/expr-lang/expr"

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
	Valid   bool              `json:"valid"`
	Errors  []ValidationError `json:"errors"`
	Warnings []ValidationError `json:"warnings"`
}

// AgentResolver 判断 Agent 是否存在(由调用方注入 Agent Registry)。
type AgentResolver func(id string) bool

// SkillResolver 判断 Skill 是否存在。
type SkillResolver func(id string) bool

// Validate 对 Workflow 执行全部校验规则。
func Validate(wf *model.Workflow, agents AgentResolver, skills SkillResolver) *Result {
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

// validateNodeConfig 按节点类型检查必需配置。
func validateNodeConfig(n model.Node, agents AgentResolver, skills SkillResolver, addErr func(code, node, format string, args ...any)) {
	str := func(key string) string {
		if v, ok := n.Config[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	switch n.Type {
	case model.NodeTypeAgent:
		if str("agent") == "" {
			addErr("MISSING_AGENT", n.ID, "agent 节点 %q 缺少 agent 配置", n.ID)
		} else if agents != nil && !agents(str("agent")) {
			addErr("AGENT_NOT_FOUND", n.ID, "节点 %q 引用了不存在的 Agent %q", n.ID, str("agent"))
		}
		if str("mode") == "" {
			addErr("MISSING_MODE", n.ID, "agent 节点 %q 缺少 mode 配置", n.ID)
		}
	case model.NodeTypeSkill:
		if str("skill") == "" {
			addErr("MISSING_SKILL", n.ID, "skill 节点 %q 缺少 skill 配置", n.ID)
		} else if skills != nil && !skills(str("skill")) {
			addErr("SKILL_NOT_FOUND", n.ID, "节点 %q 引用了不存在的 Skill %q", n.ID, str("skill"))
		}
	case model.NodeTypeCondition:
		if str("expression") == "" {
			addErr("MISSING_EXPRESSION", n.ID, "condition 节点 %q 缺少 expression 配置", n.ID)
		} else {
			checkExpression(str("expression"), addErr, n.ID)
		}
	case model.NodeTypeScript:
		if str("command") == "" {
			addErr("MISSING_COMMAND", n.ID, "script 节点 %q 缺少 command 配置", n.ID)
		}
	case model.NodeTypeGit:
		op := str("operation")
		switch op {
		case "status", "diff", "log", "branch", "checkout", "commit":
		default:
			addErr("INVALID_GIT_OPERATION", n.ID, "git 节点 %q 的 operation %q 非法(允许: status|diff|log|branch|checkout|commit)", n.ID, op)
		}
	case model.NodeTypeSubWorkflow:
		if str("workflow_id") == "" {
			addErr("MISSING_WORKFLOW_ID", n.ID, "subworkflow 节点 %q 缺少 workflow_id 配置", n.ID)
		}
	case model.NodeTypeHuman, model.NodeTypeParallel, model.NodeTypeMerge:
		// 无必填配置
	default:
		addErr("UNKNOWN_NODE_TYPE", n.ID, "节点 %q 的类型 %q 未注册", n.ID, string(n.Type))
	}
}

// checkExpression 使用 expr 引擎做条件表达式语法检查。
func checkExpression(expression string, addErr func(code, node, format string, args ...any), nodeID string) {
	_, err := expr.Compile(expression, expr.Env(expressionEnv{}))
	if err != nil {
		addErr("INVALID_EXPRESSION", nodeID, "条件表达式 %q 非法: %v", expression, err)
	}
}

// expressionEnv 描述条件表达式可引用的变量。
type expressionEnv struct {
	Decision  string `expr:"decision"`
	Status    string `expr:"status"`
	Output    string `expr:"output"`
	Iteration int    `expr:"iteration"`
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
