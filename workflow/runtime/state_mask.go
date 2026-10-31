package runtime

// 会话状态快照(executions.state_json)的脱敏。
//
// 背景(约定 #8):state_json 存的是**整个** ADK 会话状态 —— 里面有每个
// 节点的 outcome 原文(node:<id>:result)、最近一次 Agent 输出(output)、
// 失败原因(_error)、git diff 文本(git_diff)、人工答复(_user_input)。
// 这些值直接来自 Agent 的 stdout/stderr,而 stderr 里常有
// "ANTHROPIC_API_KEY=sk-..." 这类凭据。
//
// 这条链路此前**完全没有脱敏**:落库是原样写(state_json),API 读出口
// 也原样返回(迭代 13 之前 app 层只脱敏了节点明细的 output/error/
// result_json)。审计文件、备份 DB 直接拿到明文。
//
// 为什么必须**按键分类**,而不是对整个 map 无差别脱敏:
//
//  1. 结构化键被运行期读取,用于路由与决策(decision 决定走哪条边、
//     node:<id>:status 决定 resume 时跳过哪些节点、_abort 决定是否中断)。
//     脱敏它们 = 改变执行语义。
//  2. "无差别脱敏"是按**键名**判定敏感度的:节点 ID 里带 token 的状态键
//     (node:my-token:status)会被整值替换成 "***" —— 节点状态直接丢失,
//     路由随之失效。这不是假想:logx 的键名规则把 "token" 结尾的键判为强敏感。
//
// 因此本文件维护一份"结构化键"清单(免脱敏),**其余键一律脱敏** ——
// 默认脱敏:将来新增的状态键不会因为漏登记就明文落库。
//
// "一律脱敏"具体是**两级判定**(规则来自 logx,唯一定义处):
//
//  1. **键名判定**:键名本身是敏感词(api_key / auth_token / password …)
//     → 整值替换为 "***"(与值的类型无关)。
//  2. **值形态判定**:值里含 key=value 形态的凭据 → 就地替换,保留上下文。
//
// 此前只有第 2 级:顶层字符串值("api_key":"sk-plain-value-999")原样落库,
// 与上面的"默认脱敏"承诺相反(约定 #8)。第 1 级的唯一例外是 outcome
// JSON 键(见 maskStateValue)。
//
// 清单的来源:workflow/compiler 的会话状态键常量与键构造函数(唯一定义处)
// + runtime 自己写入的键。TestStateWritesAreClassified 扫描 compiler
// 与 runtime 的源码,确保每个被写入的状态键都已分类 —— 凭印象维护清单
// 必然漏项(约定 #13 的同源精神)。
//
// 落库侧:persist.go 的 execMetaArgs 统一经 MaskStateData。
// 读出口侧:app/application 的 maskExecution 复用同一个函数 ——
// 两份实现各自演化就会出现"落库脱敏了但 API 又把明文吐回来"。

import (
	"encoding/json"
	"strings"

	"agentworkflow/logx"
	"agentworkflow/workflow/compiler"
)

// structuredStateKeys 是"结构化键"清单:值是枚举/节点 ID/计数/布尔,
// 运行期读它们做路由与决策,必须**原样保留**。
//
// 逐条说明(为什么必须原样):
//
//	decision      条件表达式 decision 的取值(APPROVED/REJECTED/…):路由依据
//	status        最近一次节点状态("success"):条件表达式 status 的取值
//	_abort        中断标记(FAILED:<node> / WAIT_USER / LOOP_LIMIT:<node>)
//	_resume_node  恢复起点(节点 ID):决定从哪个节点继续
//	_current_node 当前节点(节点 ID):失败后重试的落点
//	_loop_count   循环轮数(int;JSON 往返后是 float64):条件表达式 iteration
//	_loop_exited  循环出口标记(bool)
//	_loop_stuck   循环停滞标记(bool)
//	_loop_decision 循环体内的决策快照(枚举):多回边分支的判据
var structuredStateKeys = map[string]bool{
	compiler.KeyDecision:     true,
	compiler.KeyStatus:       true,
	compiler.KeyAbort:        true,
	compiler.KeyResumeNode:   true,
	compiler.KeyCurrentNode:  true,
	compiler.KeyLoopCount:    true,
	compiler.KeyLoopExited:   true,
	compiler.KeyLoopStuck:    true,
	compiler.KeyLoopDecision: true,
	// 失败分支记录:节点 ID 数组,恢复与审计读取,不含自由文本。
	compiler.KeyViaFailureBranch: true,
}

// 节点级状态键的后缀(node:<id>:<suffix>)。
const (
	// nodeSuffixStatus 节点状态:结构化(原样保留)。
	nodeSuffixStatus = ":status"
	// nodeSuffixAttempt 尝试次数:结构化(int;原样保留)。
	nodeSuffixAttempt = ":attempt"
	// nodeSuffixResult outcome 的 JSON 原文:自由文本(脱敏)。
	nodeSuffixResult = ":result"
	// nodeSuffixAnswered WAIT_USER 那次的 outcome JSON:自由文本(脱敏)。
	nodeSuffixAnswered = ":answered"
)

// 路由级状态键的前缀与后缀(route:<id>:<suffix>)。
const (
	// routePrefix 路由级状态键前缀。
	routePrefix = "route:"
	// routeSuffixBranch 出口分支下标:结构化(枚举,原样保留)。
	routeSuffixBranch = ":branch"
	// routeSuffixFailure 失败分支已选中标记:结构化(bool,原样保留)。
	routeSuffixFailure = ":failure"
)

// structuredStateKey 判定键是否为"结构化键"(值必须原样保留)。
func structuredStateKey(key string) bool {
	if structuredStateKeys[key] {
		return true
	}
	switch nodeScopedSuffix(key) {
	case nodeSuffixStatus, nodeSuffixAttempt:
		return true
	}
	// route:<id>:branch —— 路由选定的出口分支下标。
	//
	// 它是**路由依据**:恢复执行靠它回放上一次选定的分支(见
	// compiler/lower.go 的 selectExitBranch)。若被脱敏改写,恢复时
	// 回放的是另一个分支 —— "人批准 A 分支,恢复后走 B 分支"。
	// 值是 int,脱敏对它没有意义,但必须显式登记:漏登记 = 将来某天
	// 有人把它改成字符串(比如存条件表达式)时,明文/改写会静默发生。
	if strings.HasPrefix(key, routePrefix) &&
		(strings.HasSuffix(key, routeSuffixBranch) || strings.HasSuffix(key, routeSuffixFailure)) {
		return true
	}
	return false
}

// nodeScopedSuffix 返回 "node:<id>:<suffix>" 形态键的后缀(带冒号)。
// 不是节点级键时返回空串。
//
// 用**最后**一个冒号切分:节点 ID 本身可以含冒号,后缀仍是固定的。
func nodeScopedSuffix(key string) string {
	if !strings.HasPrefix(key, "node:") {
		return ""
	}
	i := strings.LastIndex(key, ":")
	if i < len("node:") {
		return ""
	}
	return key[i:]
}

// ---- output 键的双重身份:恢复路径的口径差(已收口,残余见下) ----
//
// output(compiler.KeyOutput)同时是:
//
//  1. **自由文本键**:值是最近一次 Agent 的输出原文,常带 CLI stderr
//     里的凭据 → 落库必须脱敏(否则明文进 state_json,违反约定 #8);
//  2. **条件变量**:条件表达式可以直接读 output(唯一定义见
//     compiler/conditionenv.go 的 conditionEnvVars)。
//
// 这两重身份带来一条口径差:
//
//   - **实时运行**:条件在**原文**上求值(会话状态在内存里,未经脱敏);
//   - **恢复运行**:条件在**脱敏后的文本**上求值 —— 恢复时引擎把
//     state_json(已脱敏)重新灌进会话(engine.go 的 resume 分支),
//     output 因此是脱敏副本。
//
// **这条差不是"窄到无所谓"**(第六轮的错误判断,终审复审已实测证伪):
// 它能让**恢复后走另一条分支**。实测链路:skill 节点输出
// "ANTHROPIC_API_KEY=sk-xyz" + 边条件 `output contains "sk-"`
// → 首次运行走 secret_path → HITL 答复触发 resume → 条件在脱敏副本
// ("ANTHROPIC_API_KEY=***")上重新求值 → 改走 normal_path。
// 在审批流里 = **人批准 A 分支,恢复后走 B 分支**(约定 #14 同族:
// 走错分支比崩溃更危险 —— 用户看不到任何异常,只看到"流程跑完了")。
//
// 触发条件(精确边界,不止"凭据形态"):
//
//   1. 路由在**恢复时**重新求值出口条件(而不是回放已选定的分支);
//   2. 该条件读 output(其余条件变量 decision/status/iteration 都是
//      结构化键,原样保留;variables 走 exec.Variables,不经本文件);
//   3. 落库的 output 与实时求值时用的 output **不是同一份**:
//      a. 脱敏改写了它(含凭据形态的 key=value → 值被替换成 ***);
//      b. 或它被**后续节点覆盖**了(output 键的语义是"最近一次成功
//         节点的输出",暂停前跑过的节点会改写它)。
//
// 三条里只要 1+2+3 同时成立,分支就可能与实时不同。
//
// **收口方式(本轮)**:路由一旦选定出口分支,这个选择就是本次执行的
// 既成事实 —— 把它记进**结构化**状态键 route:<决策节点>:branch
// (值是分支下标,枚举,不含自由文本,不落凭据),恢复时**回放**它,
// 不再拿脱敏副本重新求值。实现见 compiler/lower.go 的 selectExitBranch。
//
// 为什么不用别的办法:
//
//   - 把 output 从脱敏键里排除 → 凭据明文落库,违反约定 #8;
//   - 另存一份未脱敏的 output 专供恢复 → 同样是明文落库;
//   - 把 output 从条件变量集合里去掉 → 破坏
//     compiler/conditionenv.go 的唯一定义,用户现有的条件表达式会被
//     校验期判成 "unknown name" 而拒绝保存(约定 #13:校验期与运行期同源)。
//
// 三条路都要么泄漏凭据、要么改掉用户可见的条件变量语义。
// "回放已选定的分支"是唯一不落明文、也不改条件变量集合的路。
//
// **残余**(收口后仍然存在的口径差,边界明确):
//
//   - 路由在**暂停前从未选定过分支**时(例如循环达到上限后暂停,
//     恢复时人的答复合法地改变了 decision → 条件必须重新求值),
//     这次求值用的仍是落库的 output 副本;
//   - 多回边循环里的 switchAgent(选本轮走哪条回边)每轮重新求值,
//     它读到的"上一轮 output"在恢复时是脱敏副本;
//   - 这两处要彻底消除,只能把原文留在盘上(违反约定 #8)或取消 output
//     作为条件变量(违反约定 #13)—— 属于三个硬约束真正互斥的角落。
//
// 规避建议(给写条件表达式的人):需要稳定的分支依据时,用
// decision / status(结构化键,原样保留),别用 output 里的自由文本。

// outcomeJSONKey 判断键是否是"outcome JSON"形态
// (node:<id>:result / node:<id>:answered):值是 compiler.NodeOutcome 的 JSON 文本。
func outcomeJSONKey(key string) bool {
	switch nodeScopedSuffix(key) {
	case nodeSuffixResult, nodeSuffixAnswered:
		return true
	}
	return false
}

// MaskStateData 返回会话状态快照的**脱敏副本**(不修改入参)。
//
// 入参是运行期正在用的 StateData —— 绝不能就地改:运行期还要读它做
// 路由与决策(约定 8 的"读出口只改副本"原则,落库侧同理)。
//
// 结构化键原样保留,其余键脱敏。详见本文件头注释。
func MaskStateData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		if structuredStateKey(k) {
			out[k] = v
			continue
		}
		out[k] = maskStateValue(k, v)
	}
	return out
}

// maskStateValue 脱敏一个自由文本状态值。
//
// 两级判定(规则来自 logx,本文件不复制一份 —— 同源,约定 #13 的精神):
//
//  1. **键名判定**:键名本身是敏感词(api_key / auth_token / password …)
//     → 整值替换为 "***"(强敏感与值的类型无关)。
//     这一级此前缺失:顶层字符串值("api_key":"sk-plain-value-999")只做
//     值形态判定,原样返回 —— 与本文件头"其余键一律脱敏 —— 默认脱敏"的
//     承诺相反(约定 #8)。
//  2. **值形态判定**:值里含 key=value 形态的凭据 → 就地替换。
//
// 唯一的例外是 outcome JSON 键(node:<id>:result / node:<id>:answered):
// 它们的值是一个 JSON 文档,敏感度由**内层键名**决定(解析后判定),
// 不能按外层键名整值替换 —— 外层键名里的节点 ID 是用户自定的标识,
// 不是数据字段名;整值替换会产出非法 JSON,恢复路径解析失败 →
// 静默重跑节点(见 maskStateString)。
func maskStateValue(key string, v any) any {
	if outcomeJSONKey(key) {
		if s, ok := v.(string); ok {
			return maskStateString(key, s)
		}
		// 非字符串的 outcome 值(理论上不该有):按值形态脱敏。
		return logx.MaskValue(v)
	}
	return maskStateValueByKey(key, v)
}

// maskStateValueByKey 按"键名 + 值形态"两级判定脱敏一个状态值。
//
// 实现:把值包进一个**单键 map** 交给 logx.MaskValue —— logx 遍历 map 时
// 对每个键都做键名判定(内部 maskWithKey),键名不敏感时才退回按值形态
// 处理。这样键名规则只有一份来源(logx.keySensitivity),本文件不再各写
// 一份 —— 两份实现各自演化,就会出现"运行期脱敏了但日志侧没脱敏"这类
// 半吊子防护(约定 #13 的同源精神)。
func maskStateValueByKey(key string, v any) any {
	out, ok := logx.MaskValue(map[string]any{key: v}).(map[string]any)
	if !ok {
		return v
	}
	return out[key]
}

// maskStateString 脱敏字符串形态的状态值。
//
// node:<id>:result / node:<id>:answered 走**解析 → 结构化脱敏 → 重新
// 序列化**,而不是对整段文本做正则替换。理由有二:
//
//  1. 恢复路径要 json.Unmarshal 这个值(executor 的 answeredOutcome:
//     解析失败就按"未答复"处理 → 静默重跑节点,用户白等一次)。
//     纯文本正则替换可能产出非法 JSON(转义引号/换行被截断)。
//  2. 结构化脱敏能按**内层键名**判定敏感。形如 {"api_key":"sk-xxx"}
//     的凭据没有 key=value 形态,纯文本正则抓不到;按内层键名判定
//     则整值替换,漏不掉。
//
// 解析失败时退回文本脱敏 —— 不能因为格式异常就把原文整个留下。
//
// 注意:这条路径**不做外层键名判定**(与 maskStateValue 的例外一致):
// 外层键名里的节点 ID 是用户自定的标识而非数据字段名,按数据字段名的
// 规则判敏感会误伤(节点 ID 含 "token" 之类)—— 误伤 = 把 outcome 快照
// 整值替换成 "***" → 恢复路径解析失败 → 静默重跑节点(约定 #14 同族:
// 脱敏不得改写执行语义)。
func maskStateString(key string, s string) string {
	if s == "" {
		return s
	}
	if outcomeJSONKey(key) {
		if out, ok := maskJSONText(s); ok {
			return out
		}
	}
	return logx.Mask(s)
}

// maskJSONText 解析 JSON → 结构化脱敏 → 重新序列化。
//
// 数字经 float64 往返:int64 超过 2^53 时会损失精度(纳秒级时间戳)。
// 这是可接受的:被脱敏的是**展示/审计用**的快照,不是账务数据;
// 而保留数字字面量需要绕开 logx 的值判定(json.Number 会被当成
// fmt.Stringer → 数字变成 JSON 字符串),代价更大。
func maskJSONText(raw string) (string, bool) {
	var obj any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", false
	}
	out, err := json.Marshal(logx.MaskValue(obj))
	if err != nil {
		return "", false
	}
	return string(out), true
}
