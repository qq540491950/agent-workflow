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
		validateNodeConfig(n, agents, skills, addErr, addWarn)
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

// deprecatedHints 是"已废弃配置键"的替代做法提示(键 → 提示语)。
//
// 键本身在 workflow/compiler/nodeconfig.go 的契约表里(唯一来源);
// 这里只负责"怎么告诉用户正确的做法"。
var deprecatedHints = map[string]string{
	"expression": "路由条件要写在**连线**上(选中连线 → 条件表达式);节点上的表达式运行期不读取",
}

// validateNodeConfig 按节点类型检查必需配置。
//
// 必填配置的唯一来源是 compiler.NodeConfigSpecFor(运行期实际读取哪些键,
// 契约表就写哪些键);本函数只保留校验器特有的检查:引用是否存在、
// 取值是否在允许集合内、表达式语法、废弃键提示。
func validateNodeConfig(n model.Node, agents AgentResolver, skills SkillResolver,
	addErr func(code, node, format string, args ...any),
	addWarn func(code, node, format string, args ...any)) {
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

	// 废弃配置键:运行期与编译器都不读(唯一来源:compiler.NodeConfigSpecFor
	// 的 Deprecated 列表)。校验期**不再**要求它,也不再因为它拒绝保存 ——
	// 要求用户填一个没有作用的键、或者因为填错一个没有作用的键而存不了
	// 工作流,都是约定 #13 的反例("用户被自己的校验器挡住")。
	//
	// 这里只发一条**警告**:告诉用户这个键没有作用、正确的做法是什么。
	// 存量工作流里已经写了它 → 不报错、不失效,只是提醒。
	for _, key := range spec.Deprecated {
		val := str(key)
		if val == "" {
			continue
		}
		msg := fmt.Sprintf("节点 %q 的配置键 %q 已废弃(运行期不读取,不影响执行结果)", n.ID, key)
		if hint, ok := deprecatedHints[key]; ok {
			msg += ";" + hint
		}
		// 语法仍然查(拼错时在警告里说明),但**不再**因此报错 ——
		// 一个不起作用的字段写错了,不该挡住保存。
		if err := expressionSyntaxError(val); err != nil {
			msg += fmt.Sprintf(";该值语法也非法: %v", err)
		}
		addWarn("DEPRECATED_NODE_CONFIG", n.ID, "%s", msg)
	}

	// 未知配置键:键名拼错(timeout_seconds 写成 timeout_second)时,运行期
	// **照常执行节点**,只是那个设置根本没生效 —— 用户以为超时是 30 秒,
	// 实际用的是默认值。此前校验期只查必填键,拼错的键既不报错也不警告,
	// 与"没写"完全无法区分(约定 14:静默丢弃与崩溃同样危险)。
	//
	// **只警告,不报错**:存量工作流里确实存在拼错或已废弃的键,一旦报错,
	// 用户就打不开自己的老工作流(约定 13 的反例)。警告不阻断保存,前端
	// 会把它显示在画布错误面板与 toast 里(前端 validate 分支已经会渲染
	// res.warnings)。
	//
	// 键的清单只有一份来源:compiler.NodeConfigSpecFor(约定 13)——
	// 已废弃的键也在清单里,因此它们只报"已废弃",不会同时报"未知"。
	//
	// 被禁用的节点不参与本检查:运行期在类型分发前就跳过它,一个配置键都
	// 不读(见上面 enabled 分支的提前 return)—— 那里报出来对用户无意义。
	knownKeys := knownConfigKeys(spec)
	for _, key := range unknownConfigKeys(n, knownKeys) {
		msg := fmt.Sprintf("节点 %q 的配置键 %q 未被识别(运行期会忽略它,该设置不会生效)", n.ID, key)
		if hint := closestConfigKey(key, knownKeys); hint != "" {
			msg += fmt.Sprintf(";是否想写 %q?", hint)
		}
		msg += fmt.Sprintf(";该节点类型允许的键: %s", strings.Join(knownKeys, ", "))
		addWarn("UNKNOWN_NODE_CONFIG", n.ID, "%s", msg)
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
	case model.NodeTypeGit:
		// operation 的必填已由上面的通用循环报出;这里只查取值合法性
		// (允许集合与运行期 runGitNode 的 switch 同源)。
		if op := str("operation"); op != "" && !slices.Contains(compiler.GitOperations(), op) {
			addErr("INVALID_GIT_OPERATION", n.ID, "git 节点 %q 的 operation %q 非法(允许: %s)", n.ID, op, strings.Join(compiler.GitOperations(), "|"))
		}
	}
}

// knownConfigKeys 返回该节点类型"被识别"的全部配置键(排序后)。
//
// 唯一来源是 compiler.NodeConfigSpecFor(约定 13:校验期与运行期同源):
//
//   - Required / Optional:运行期会读的键;
//   - ValidatorOnly:只有校验期使用、运行期不读的键;
//   - Deprecated:已废弃的键(仍在清单里 → 报"已废弃"而不是"未知");
//   - GlobalConfigKeys:所有节点类型都支持的全局键(enabled)。
//
// 子键(如 prompt.template / retry.max_attempts)**不查**:map 型配置键
// 的内容是开放的(prompt 既可以是字符串也可以是 {template:...},args /
// input 更是自由映射),逐个子键校验会误伤大量合法写法。
func knownConfigKeys(spec compiler.NodeConfigSpec) []string {
	seen := map[string]bool{}
	for _, k := range spec.Required {
		seen[k] = true
	}
	for _, k := range spec.Optional {
		seen[k] = true
	}
	for _, k := range spec.ValidatorOnly {
		seen[k] = true
	}
	for _, k := range spec.Deprecated {
		seen[k] = true
	}
	for _, k := range compiler.GlobalConfigKeys() {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// unknownConfigKeys 返回节点配置里不被识别的键(排序)。
func unknownConfigKeys(n model.Node, known []string) []string {
	set := make(map[string]bool, len(known))
	for _, k := range known {
		set[k] = true
	}
	var out []string
	for k := range n.Config {
		if !set[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// closestConfigKey 在允许的键里找"最可能是用户想写的那个"。
//
// 编辑距离 ≤ 2 才算相近(timeout_seconds → timeout_second 距离 1;
// 距离太远的键给建议反而误导)。找不到相近的返回空串。
func closestConfigKey(key string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := editDistance(key, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance 是 Levenshtein 距离(插入/删除/替换各算 1)。
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = prev[j-1] + cost
			if v := prev[j] + 1; v < cur[j] {
				cur[j] = v
			}
			if v := cur[j-1] + 1; v < cur[j] {
				cur[j] = v
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// checkExpression 使用 expr 引擎做条件表达式语法检查。
func checkExpression(expression string, addErr func(code, node, format string, args ...any), nodeID string) {
	if err := expressionSyntaxError(expression); err != nil {
		addErr("INVALID_EXPRESSION", nodeID, "条件表达式 %q 非法: %v", expression, err)
	}
}

// expressionSyntaxError 返回表达式的语法错误(nil = 合法)。
//
// 校验环境与运行期同源(见 newExpressionEnv),因此这里判合法的表达式在
// 运行期也一定合法(约定 #13)。
func expressionSyntaxError(expression string) error {
	_, err := expr.Compile(expression, expr.Env(newExpressionEnv()))
	return err
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
