package compiler

// 本文件是条件表达式"可用变量集合"的**唯一定义**(AGENTS.md 硬性约定 #13:
// 校验期与运行期必须同源)。
//
// 此前两侧各写了一份 env 字面量:
//
//	运行期(lower.go):  {decision, status, output, iteration, variables}
//	校验期(validator):  {decision, status, output, iteration, variables}
//
// 靠人工保持一致 —— 一旦某侧新增变量,另一侧就会把用户合法的表达式判成
// "unknown name" 而拒绝保存(用户被自己的校验器挡住),或者反过来:
// 校验期放行一个运行期没有的变量,条件恒假。
//
// 现在两侧都从本文件取:
//   - 运行期:buildConditionEnv(从会话状态取值)
//   - 校验期:NewConditionEnv(同形状的静态环境,用于 expr 编译)

// conditionEnvKind 区分变量的取值方式。
type conditionEnvKind int

const (
	// condEnvString 从状态键读字符串。
	condEnvString conditionEnvKind = iota
	// condEnvInt 从状态键读整数。
	condEnvInt
	// condEnvVars 运行期变量 map(任意键值)。
	condEnvVars
)

// conditionEnvVar 描述一个可用的条件表达式变量。
type conditionEnvVar struct {
	name string
	kind conditionEnvKind
	// stateKey 是取值用的会话状态键(condEnvString / condEnvInt)。
	stateKey string
	// overridable:该变量可被调用方覆盖(decision 用循环体内的决策快照)。
	overridable bool
}

// conditionEnvVars 是条件表达式可用变量的唯一定义。
var conditionEnvVars = []conditionEnvVar{
	{name: "decision", kind: condEnvString, stateKey: KeyDecision, overridable: true},
	{name: "status", kind: condEnvString, stateKey: KeyStatus},
	{name: "output", kind: condEnvString, stateKey: KeyOutput},
	{name: "iteration", kind: condEnvInt, stateKey: KeyLoopCount},
	{name: "variables", kind: condEnvVars},
}

// ConditionEnvKeys 返回条件表达式可用的顶层变量名(副本)。
func ConditionEnvKeys() []string {
	out := make([]string, 0, len(conditionEnvVars))
	for _, v := range conditionEnvVars {
		out = append(out, v.name)
	}
	return out
}

// expressionEnv 是条件表达式的求值环境(map 形状:variables 为任意键值)。
//
// 必须是 map **别名**而非具名类型:expr 的虚拟机对 env 做类型断言
// (vm/vm.go:`env.(map[string]any)`),具名 map 会 panic
// ("interface conversion: ... not map[string]interface {}")。
type expressionEnv = map[string]any

// NewConditionEnv 构造与运行期同形状的环境,供校验期编译表达式用。
//
// vars 为 nil 时归一成空 map —— 与运行期完全一致(运行期也归一),
// 两侧的 env 形状因此逐键相同,不会一边是 nil map 一边是空 map。
func NewConditionEnv(vars map[string]any) expressionEnv {
	env := make(expressionEnv, len(conditionEnvVars))
	for _, v := range conditionEnvVars {
		switch v.kind {
		case condEnvString:
			env[v.name] = ""
		case condEnvInt:
			env[v.name] = 0
		case condEnvVars:
			env[v.name] = normalizeVars(vars)
		}
	}
	return env
}

// buildConditionEnv 构造运行期环境(从会话状态取值)。
//
// override 非空时覆盖对应的可覆盖变量(decision 用循环体内的决策快照
// KeyLoopDecision:循环体每轮开头会清空 KeyDecision,多回边分支必须据此
// 判断本轮走哪条回边)。
func buildConditionEnv(st StateAccess, vars map[string]any, override string) expressionEnv {
	env := make(expressionEnv, len(conditionEnvVars))
	for _, v := range conditionEnvVars {
		switch v.kind {
		case condEnvString:
			val := stringOf(st, v.stateKey)
			if v.overridable && override != "" {
				val = override
			}
			env[v.name] = val
		case condEnvInt:
			env[v.name] = intOf(st, v.stateKey)
		case condEnvVars:
			env[v.name] = normalizeVars(vars)
		}
	}
	return env
}

// normalizeVars 把 nil 变量表归一成空 map(两侧形状一致)。
func normalizeVars(vars map[string]any) map[string]any {
	if vars == nil {
		return map[string]any{}
	}
	return vars
}
