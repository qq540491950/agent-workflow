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

// structuredStateKey 判定键是否为"结构化键"(值必须原样保留)。
func structuredStateKey(key string) bool {
	if structuredStateKeys[key] {
		return true
	}
	switch nodeScopedSuffix(key) {
	case nodeSuffixStatus, nodeSuffixAttempt:
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
func maskStateValue(key string, v any) any {
	if s, ok := v.(string); ok {
		return maskStateString(key, s)
	}
	// map/slice 等容器:交给 logx 按键名+值形态递归脱敏
	// (返回新容器,不改原对象)。
	return logx.MaskValue(v)
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
