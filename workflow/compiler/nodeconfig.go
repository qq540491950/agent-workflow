package compiler

import (
	"slices"

	"agentworkflow/workflow/model"
)

// 本文件是"节点类型 → 配置契约"的**唯一定义**(AGENTS.md 硬性约定 #13:
// 校验期与运行期必须同源 —— 条件表达式的可用变量集合、节点类型的必填配置
// 结构各只有一份来源,compiler 与 validator 共用)。
//
// 此前有两份实现:
//
//   - 活的一份:workflow/validator 的 validateNodeConfig 内联 switch;
//   - 死的一份:workflow/dsl 里那 7 个零引用的配置结构体(已过期:缺 model
//     与 enabled,多出运行期从不读的 Env —— 还会把凭据引进 DSL 的诱惑)。
//
// 现在活的一份收敛到本文件,validator 直接消费(NodeConfigSpecFor),
// 死的一份已删除(workflow/dsl 不再声明任何节点配置结构体,由
// workflow/dsl/no_config_structs_test.go 守卫:再写一份会立刻失败)。
//
// 本文件描述的是**运行期实际读取**的配置键。运行期实现在
// workflow/runtime/executor.go,两者的同源性由
// nodeconfig_contract_test.go 用 AST 扫描运行期源码做探针校验:
// 运行期新增/删除配置键而本表未同步时,测试立即失败并指出该补哪一行。

// NodeEnabledKey 是节点级开关的配置键。
//
// 运行期语义(workflow/runtime/executor.go 的 dispatchNode):值为布尔 false
// 时该节点被跳过(置 SKIPPED,路由直通),一个配置键都不再读取;值为 true
// 或缺省时节点照常执行。**只认布尔**:非布尔值让运行期 **fail closed**
// (节点失败,不执行任何副作用)—— 不是"被忽略、节点照常执行"。
//
// 校验期对同一取值报 INVALID_NODE_ENABLED(工作流不可保存/不可运行),
// 两边结论一致(约定 #13),回归由 workflow/runtime/enabled_contract_test.go
// 锁住。
const NodeEnabledKey = "enabled"

// NodeEnabledState 描述节点级开关的取值状态。
type NodeEnabledState int

const (
	// NodeEnabledDefault:未配置 enabled(等价于启用)。
	NodeEnabledDefault NodeEnabledState = iota
	// NodeEnabledOn:enabled=true(显式启用)。
	NodeEnabledOn
	// NodeEnabledOff:enabled=false —— 运行期跳过该节点。
	NodeEnabledOff
	// NodeEnabledInvalid:enabled 不是布尔 —— 运行期 fail closed(节点失败,
	// 不执行副作用);校验期报 INVALID_NODE_ENABLED。
	NodeEnabledInvalid
)

// NodeEnabledStateOf 解析节点配置里的 enabled 开关。
//
// ok 语义的展开:返回 NodeEnabledInvalid 表示"配了但类型不对",
// 校验期必须报出来 —— 否则用户以为节点被禁用了,运行期却照常执行。
func NodeEnabledStateOf(cfg map[string]any) NodeEnabledState {
	v, exists := cfg[NodeEnabledKey]
	if !exists {
		return NodeEnabledDefault
	}
	b, isBool := v.(bool)
	if !isBool {
		return NodeEnabledInvalid
	}
	if !b {
		return NodeEnabledOff
	}
	return NodeEnabledOn
}

// NodeConfigSpec 描述一种节点类型的配置契约。
type NodeConfigSpec struct {
	// Required 必填键:缺失(或为空串)即校验失败。
	Required []string
	// Optional 运行期会读取、但允许省略的键。
	Optional []string
	// ValidatorOnly 只有校验期使用、运行期完全不读的键。
	ValidatorOnly []string
	// Deprecated 是**已废弃**的键:运行期与编译器都不读,校验期也不再
	// 要求它、更不会因为它而拒绝保存(只发一条警告)。
	//
	// 目前只有 condition 节点的 expression:路由条件在**边上**
	// (edge.condition),节点上的 expression 从来没有生效过 ——
	// 运行期把 condition 节点当"无副作用的直通节点"
	// (workflow/runtime/executor.go 的 dispatchNode),路由由边条件决定。
	//
	// 为什么保留在契约表里而不是直接删掉:存量 DSL 里已经写了
	// config.expression。删掉会让"这个键不再被识别"变成一次静默的行为
	// 变更;留在表里并标注为废弃,校验期就能明确告诉用户"这个键没有作用,
	// 条件要写在连线上",同时**不再**因为它挡住保存(约定 #13:校验期不得
	// 要求用户填一个没有作用的键)。
	Deprecated []string
	// SubKeys 是 map 型配置键的子键(运行期读取的子键)。
	SubKeys map[string][]string
}

// nodeConfigSpecs 是"节点类型 → 配置契约"的唯一定义。
//
// 每一条都必须与运行期实际读取的键一致(nodeconfig_contract_test.go 探针校验)。
var nodeConfigSpecs = map[model.NodeType]NodeConfigSpec{
	model.NodeTypeAgent: {
		Required: []string{"agent", "mode"},
		Optional: []string{
			"prompt", "instructions", "model", "working_dir",
			"timeout_seconds", "retry",
		},
		SubKeys: map[string][]string{
			// prompt 有两种写法:纯字符串,或 {template: ...}(运行期读 map 里的 template)。
			"prompt": {"template"},
			"retry":  {"max_attempts", "backoff"},
		},
	},
	model.NodeTypeSkill: {
		Required: []string{"skill"},
		Optional: []string{"agent", "args"},
	},
	model.NodeTypeCondition: {
		// expression 已废弃:路由条件在边上(edge.condition)。
		Deprecated: []string{"expression"},
	},
	model.NodeTypeParallel: {},
	model.NodeTypeMerge:    {},
	model.NodeTypeHuman: {
		Optional: []string{"prompt", "responses"},
	},
	model.NodeTypeScript: {
		Required: []string{"command"},
		Optional: []string{"shell", "timeout_seconds", "working_dir"},
	},
	model.NodeTypeGit: {
		Required: []string{"operation"},
		Optional: []string{"args"},
	},
	model.NodeTypeSubWorkflow: {
		Required: []string{"workflow_id"},
		Optional: []string{"input"},
	},
}

// NodeConfigSpecFor 返回指定节点类型的配置契约(副本,调用方改不动内部定义)。
//
// 第二个返回值为 false 表示该节点类型未注册 —— 节点类型清单的唯一来源是
// model.AllNodeTypes(),新增类型时必须同时在这里补一条契约。
func NodeConfigSpecFor(t model.NodeType) (NodeConfigSpec, bool) {
	spec, ok := nodeConfigSpecs[t]
	if !ok {
		return NodeConfigSpec{}, false
	}
	out := NodeConfigSpec{
		Required:      append([]string(nil), spec.Required...),
		Optional:      append([]string(nil), spec.Optional...),
		ValidatorOnly: append([]string(nil), spec.ValidatorOnly...),
		Deprecated:    append([]string(nil), spec.Deprecated...),
	}
	if len(spec.SubKeys) > 0 {
		out.SubKeys = make(map[string][]string, len(spec.SubKeys))
		for k, v := range spec.SubKeys {
			out.SubKeys[k] = append([]string(nil), v...)
		}
	}
	return out, true
}

// RequiredConfigKeys 返回该节点类型的必填配置键(副本)。
func RequiredConfigKeys(t model.NodeType) []string {
	spec, ok := NodeConfigSpecFor(t)
	if !ok {
		return nil
	}
	return spec.Required
}

// OptionalConfigKeys 返回该节点类型的可选配置键(副本)。
func OptionalConfigKeys(t model.NodeType) []string {
	spec, ok := NodeConfigSpecFor(t)
	if !ok {
		return nil
	}
	return spec.Optional
}

// KnownConfigKeys 返回该节点类型运行期会读取的全部配置键(必填 + 可选)。
func KnownConfigKeys(t model.NodeType) []string {
	spec, ok := NodeConfigSpecFor(t)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(spec.Required)+len(spec.Optional))
	out = append(out, spec.Required...)
	out = append(out, spec.Optional...)
	return out
}

// ValidatorOnlyConfigKeys 返回只有校验期使用、运行期不读的键(副本)。
func ValidatorOnlyConfigKeys(t model.NodeType) []string {
	spec, ok := NodeConfigSpecFor(t)
	if !ok {
		return nil
	}
	return spec.ValidatorOnly
}

// DeprecatedConfigKeys 返回已废弃的键(副本):运行期与编译器都不读,
// 校验期只发警告,不会因为它们拒绝保存工作流。
func DeprecatedConfigKeys(t model.NodeType) []string {
	spec, ok := NodeConfigSpecFor(t)
	if !ok {
		return nil
	}
	return spec.Deprecated
}

// globalConfigKeys 是所有节点类型都支持的全局配置键。
var globalConfigKeys = []string{NodeEnabledKey}

// GlobalConfigKeys 返回全局配置键(副本)。
func GlobalConfigKeys() []string { return append([]string(nil), globalConfigKeys...) }

// TimeoutSecondsKey 是节点级超时配置键(agent / script 节点)。
const TimeoutSecondsKey = "timeout_seconds"

// timeout_seconds 的取值范围(**唯一来源**,约定 #13:校验期与运行期同源)。
//
// 上界的作用是**防止 time.Duration 溢出**。运行期把秒数换算成
// time.Duration(raw)*time.Second(raw 是 int,乘数是 1e9):
//
//   - raw=10000000000        → 换算后是 -2346317h47m53.7s(负数)
//     → context.WithTimeout 拿到负的 deadline → **立即超时**;
//   - raw=4611686018427387904 → 换算后是 0s → deadline = 现在 → **立即超时**;
//   - raw=1e20(float)       → int(1e20) 溢出成负 → 运行期的
//     `req.Timeout > 0` 判假 → **静默改用适配器默认 600s**
//     (用户配的值根本没生效,且界面与日志里看不出任何异常)。
//
// 三条路径的共同点:用户配的超时**没有按自己配的值生效**。上界取
// 86400(24h)让换算结果恒为正、且远小于 int64 上限(86400*1e9 = 8.64e13)。
//
// 下界取 **0**:0 的语义是"**不设节点超时**"(运行期按 `req.Timeout > 0`
// 判断,走适配器默认超时)—— 与运行期口径一致。把它判成非法就是
// "用户被自己的校验器挡住"(约定 13 的反例),存量工作流里显式写 0 的
// 会直接存不下去。
const (
	// TimeoutSecondsMin 是 timeout_seconds 的最小值(0 = 不设节点超时)。
	TimeoutSecondsMin = 0
	// TimeoutSecondsMax 是 timeout_seconds 的最大值(24 小时)。
	TimeoutSecondsMax = 86400
)

// TimeoutSecondsRange 返回 timeout_seconds 的取值范围(min, max)。
func TimeoutSecondsRange() (int, int) { return TimeoutSecondsMin, TimeoutSecondsMax }

// HasTimeoutSecondsKey 报告该节点类型的契约里是否声明了 timeout_seconds。
//
// 唯一来源:nodeConfigSpecs(约定 #13)。校验期只对**声明了这个键**的节点
// 类型查取值范围 —— 别处的 timeout_seconds 是"未识别的键",由既有的
// UNKNOWN_NODE_CONFIG 警告处理(不重复报)。
func HasTimeoutSecondsKey(t model.NodeType) bool {
	spec, ok := NodeConfigSpecFor(t)
	if !ok {
		return false
	}
	return slices.Contains(spec.Required, TimeoutSecondsKey) ||
		slices.Contains(spec.Optional, TimeoutSecondsKey)
}

// gitOperations 是 git 节点支持的 operation 取值。
//
// 与运行期 runGitNode 的 switch 同源:运行期只认这一组,校验期也只认这一组
// —— 两边各写一份必然漂移(运行期加了 stash,校验期却说非法 → 用户被自己的
// 校验器挡住)。
var gitOperations = []string{"status", "diff", "log", "branch", "checkout", "commit"}

// GitOperations 返回 git 节点支持的 operation 取值(副本)。
func GitOperations() []string { return append([]string(nil), gitOperations...) }
