package runtime

// 本文件锁住"会话状态快照(state_json)的脱敏规则"(约定 #8)。
//
// 关键不变式有两条,缺一不可:
//
//  1. **自由文本键必须脱敏** —— output / node:<id>:result / _error 等
//     直接来自 Agent 的 stdout/stderr,是凭据的载体。
//  2. **结构化键必须原样保留** —— decision / node:<id>:status / _abort 等
//     被运行期读取,用于路由与决策;脱敏它们 = 改写执行语义。
//
// 第 2 条比看上去微妙:键名判定规则(logx)把"token 结尾"的键判为强敏感,
// 于是节点 ID 里带 token 的状态键(node:my-token:status)在无差别脱敏下
// 会被整值替换成 "***" —— 节点状态直接丢失,恢复时该节点会被重跑。
// 这就是"按键分类"而不是"整个 map 无差别脱敏"的原因。
//
// 本文件后半段是"新增状态键必须被显式分类"的**源码守卫**:
// 落库脱敏按"默认拒绝"处理(未登记 = 自由文本 = 脱敏),所以漏登记一个
// **结构化**键就等于在运行期改写它的值(约定 #14 同族:语义被悄悄改掉)。
// 守卫必须真的看得见所有写入点 —— 否则它只是一条恒真的假保护。

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/logx"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// TestMaskStateDataKeepsStructuredKeys 结构化键必须原样保留。
//
// 这条测试此前**近乎恒真**:它用的值("APPROVED"/"success")不含敏感词,
// logx.Mask 改不动 → 键在不在 structuredStateKeys 里都 PASS。实测删掉
// compiler.KeyDecision 后本测试仍绿,只有分类守卫变红 —— 假保护。
//
// 现在做两件事让它真的能区分:
//  1. 断言 structuredStateKey(k) 为真(直接钉住"登记"这件事);
//  2. 值用**会被 logx.Mask 改动**的凭据形态(key=value):一旦该键被
//     当成自由文本,值就会变成 "token=***",断言立刻变红。
func TestMaskStateDataKeepsStructuredKeys(t *testing.T) {
	// 前置自检:这些值确实会被 logx 改动 —— 否则下面的断言又会恒真。
	sensitive := "token=" + leakSecret
	if got := logx.Mask(sensitive); got == sensitive {
		t.Fatalf("测试前提不成立:logx.Mask 不会改动 %q(无法用它区分结构化键与自由文本键)", sensitive)
	}

	data := map[string]any{
		"decision":          sensitive,
		"status":            sensitive,
		"_abort":            sensitive,
		"_resume_node":      sensitive,
		"_current_node":     sensitive,
		"_loop_count":       sensitive,
		"_loop_exited":      sensitive,
		"_loop_stuck":       sensitive,
		"_loop_decision":    sensitive,
		"node:work:status":  sensitive,
		"node:work:attempt": sensitive,
		// 节点 ID 里带敏感词:键名判定规则会把它整值替换 —— 必须原样保留
		"node:my-token:status": sensitive,
	}
	out := MaskStateData(data)
	for k, want := range data {
		if !structuredStateKey(k) {
			t.Errorf("结构化键 %q 未被登记为结构化键(删掉登记后本测试必须变红)", k)
			continue
		}
		if got := fmt.Sprint(out[k]); got != fmt.Sprint(want) {
			t.Errorf("结构化键 %q 被改动了: got %q want %q", k, got, fmt.Sprint(want))
		}
	}
}

// TestMaskStateDataMasksFreeTextKeys 自由文本键必须脱敏。
func TestMaskStateDataMasksFreeTextKeys(t *testing.T) {
	data := map[string]any{
		"output":              "cli stderr: ANTHROPIC_API_KEY=" + leakSecret,
		"_error":              "boom ANTHROPIC_API_KEY=" + leakSecret,
		"task":                "部署时带上 ANTHROPIC_API_KEY=" + leakSecret,
		"node:work:result":    `{"State":"SUCCESS","Output":"ANTHROPIC_API_KEY=` + leakSecret + `"}`,
		"node:work:answered":  `{"State":"WAITING","Output":"ANTHROPIC_API_KEY=` + leakSecret + `"}`,
		"user_instruction":    "带上 ANTHROPIC_API_KEY=" + leakSecret,
		"git_diff":            "+ANTHROPIC_API_KEY=" + leakSecret,
		"variables":           map[string]any{"api_key": leakSecret},
		"_user_input":         map[string]any{"response": "带上 ANTHROPIC_API_KEY=" + leakSecret},
		"revisit:rev:history": []any{"ANTHROPIC_API_KEY=" + leakSecret},
	}
	out := MaskStateData(data)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("序列化脱敏结果: %v", err)
	}
	if strings.Contains(string(raw), leakSecret) {
		t.Errorf("脱敏后仍含凭据明文: %s", raw)
	}
	// 脱敏不能把内容整体抹掉:键名保留、值替换为 ***(可读性不退化)
	if got := fmt.Sprint(out["output"]); !strings.Contains(got, "ANTHROPIC_API_KEY=***") {
		t.Errorf("output 应保留键名并把值替换为 ***: %q", got)
	}
	// 内层按键名判定:"api_key" 是强敏感键 → 整值替换
	vars, _ := out["variables"].(map[string]any)
	if got := fmt.Sprint(vars["api_key"]); got != "***" {
		t.Errorf("variables.api_key 应整值替换为 ***: %q", got)
	}
}

// TestMaskStateDataKeepsOutcomeJSONValid outcome JSON 脱敏后必须仍是合法 JSON。
//
// 恢复路径要 json.Unmarshal 这个值(executor 的 answeredOutcome):
// 解析失败就按"未答复"处理 → 静默重跑节点,用户白等一次。
func TestMaskStateDataKeepsOutcomeJSONValid(t *testing.T) {
	raw := `{"State":"SUCCESS","Decision":"APPROVED","Summary":"ok",` +
		`"Output":"cli stderr: ANTHROPIC_API_KEY=` + leakSecret + `",` +
		`"Data":{"count":3},"Error":""}`
	out := MaskStateData(map[string]any{"node:work:result": raw})
	got := fmt.Sprint(out["node:work:result"])

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("脱敏后不再是合法 JSON(恢复路径会解析失败): %v (%s)", err, got)
	}
	if parsed["State"] != "SUCCESS" {
		t.Errorf("结构化字段 State 被改动: %v", parsed["State"])
	}
	if parsed["Decision"] != "APPROVED" {
		t.Errorf("结构化字段 Decision 被改动: %v", parsed["Decision"])
	}
	if strings.Contains(got, leakSecret) {
		t.Errorf("脱敏后仍含凭据明文: %s", got)
	}
	// 数字字面量不能被 float64 往返改写(脱敏不该顺手改数据的数值)
	if !strings.Contains(got, `"count":3`) {
		t.Errorf("Data 里的数字被改写了: %s", got)
	}
}

// TestMaskStateDataMasksSensitiveKeyNames 顶层键的**键名**也必须参与敏感判定。
//
// 回归:此前顶层字符串值只做"值形态"脱敏(logx.Mask),不做键名判定 ——
// {"api_key":"sk-plain-value-999"} 原样返回,与 state_mask.go 头注释
// "其余键一律脱敏 —— 默认脱敏"的承诺相反(约定 #8)。同一份输入经
// logx.MaskValue 则会被判为 "***"(logx 内部对 map 的键做键名判定)。
//
// 下面的断言必须**只能**由键名判定满足:取值刻意不含 key=value 形态
// (logx.Mask 改不动它),否则测试会退化成恒真。
//
// **取值变更说明(迭代 12)**:原取值是 "sk-plain-value-999" —— 它带已知的
// 凭据前缀 "sk-",在迭代 12 把"值形态"规则收敛进 logx.Mask 之后,logx.Mask
// 会改动它,于是下面那条"前置自检"恒红(测试前提不再成立)。
//
// 断言本身**没有**改动(仍然是"顶层敏感键的值必须变成 ***"),改的只是用来
// 探测键名判定的**探针值**:换成既不含已知凭据前缀、也不构成高熵长串的
// "plain-value-999"。这样"前置自检"重新成立,断言依然只由键名判定满足 ——
// 测试的分辨力不变(它仍能区分"键名判定"与"值形态判定")。
func TestMaskStateDataMasksSensitiveKeyNames(t *testing.T) {
	// 前置自检:这些值不会被"值形态"脱敏改动 —— 否则下面的断言测不到键名判定。
	const plain = "plain-value-999"
	if got := logx.Mask(plain); got != plain {
		t.Fatalf("测试前提不成立:logx.Mask 会改动 %q(无法用它区分键名判定与值形态判定)", plain)
	}

	for _, key := range []string{
		"api_key", "auth_token", "password", "client_secret", "user_token",
	} {
		out := MaskStateData(map[string]any{key: plain})
		got, ok := out[key].(string)
		if !ok {
			t.Errorf("顶层键 %q 的值类型被改动了: %T", key, out[key])
			continue
		}
		if got != "***" {
			t.Errorf("顶层敏感键名 %q 未被脱敏: got %q want %q", key, got, "***")
		}
	}
}

// TestMaskStateDataKeyNameJudgmentDoesNotTouchStructuredKeys 键名判定
// 不得越过"结构化键免脱敏"这条线。
//
// logx 的键名规则把"token 结尾"判为强敏感:节点 ID 里带 token 的状态键
// (node:my-token:status)会被整值替换成 "***" —— 节点状态直接丢失,恢复时
// 该节点会被重跑(约定 #14 同族:脱敏不得改写执行语义)。
func TestMaskStateDataKeyNameJudgmentDoesNotTouchStructuredKeys(t *testing.T) {
	// 取值**刻意**是凭据形态("sk-" 前缀):迭代 12 之后 logx.Mask 会改动
	// 它 —— 于是这条断言的分辨力不降反升:结构化键连"凭据形态的值"都
	// 必须原样保留(一旦豁免失效,这里立刻变红)。
	const plain = "sk-plain-value-999"
	for _, key := range []string{
		"node:my-token:status",
		"node:my-api-key:status",
		"node:client-secret:attempt",
	} {
		out := MaskStateData(map[string]any{key: plain})
		if got := fmt.Sprint(out[key]); got != plain {
			t.Errorf("结构化键 %q 被键名判定改动了: got %q want %q", key, got, plain)
		}
		if !structuredStateKey(key) {
			t.Errorf("键 %q 未被登记为结构化键(删掉登记后本测试必须变红)", key)
		}
	}
}

// TestMaskStateDataKeyNameJudgmentKeepsOutcomeJSON 键名判定不得破坏
// outcome JSON 快照。
//
// node:<id>:result / node:<id>:answered 的外层键名里嵌着用户自定的节点
// ID(含 "token" 之类时会被 logx 判为弱敏感)。若按外层键名整值替换,快照
// 就变成 "***" —— 恢复路径 json.Unmarshal 失败 → 按"未答复"处理 → 静默
// 重跑节点(用户白等一次)。
func TestMaskStateDataKeyNameJudgmentKeepsOutcomeJSON(t *testing.T) {
	raw := `{"State":"SUCCESS","Decision":"APPROVED","Output":"ok"}`
	for _, key := range []string{
		"node:my-token:result",
		"node:my-token:answered",
	} {
		out := MaskStateData(map[string]any{key: raw})
		got := fmt.Sprint(out[key])
		var parsed map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Errorf("%s 被键名判定破坏成非法 JSON(恢复路径会解析失败): %v (%s)", key, err, got)
			continue
		}
		if parsed["Decision"] != "APPROVED" {
			t.Errorf("%s 的结构化字段被改动: %v", key, parsed["Decision"])
		}
	}
}

// TestMaskStateDataDoesNotMutateInput 脱敏不得就地改运行期状态。
//
// 入参是运行期正在用的 StateData(还要读它做路由与决策)。
func TestMaskStateDataDoesNotMutateInput(t *testing.T) {
	data := map[string]any{"output": "ANTHROPIC_API_KEY=" + leakSecret}
	out := MaskStateData(data)
	if !strings.Contains(fmt.Sprint(data["output"]), leakSecret) {
		t.Errorf("脱敏就地改了入参(运行期会读到被遮蔽的值): %v", data["output"])
	}
	if strings.Contains(fmt.Sprint(out["output"]), leakSecret) {
		t.Errorf("返回的副本仍含凭据明文: %v", out["output"])
	}
}

// TestInsertExecutionStoresMaskedCopy insert 路径必须落**脱敏副本**。
//
// SaveExecution 是整行 upsert:state_json / error 原样写;而更新路径
// (saveMeta / casStateAndSave)走 execMetaArgs,那里的 error 与 state_json
// 都已脱敏。两条路径口径不一致 → "落库副本已脱敏"这条不变式只在一条
// 路径上成立:今天 insert 时 StateData 为 nil 所以不漏,但只要有人复用
// 这条路径(带状态的插入、备份恢复),明文就直接进库。
//
// 本测试直接往 insert 路径塞一份带凭据的状态与错误,读回**原始列**断言
// 明文不在(读出的 Execution 已经过读出口的还原,只能证明"没被写进去"
// 证明不了"写进去的是明文")。
func TestInsertExecutionStoresMaskedCopy(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	exec := &model.Execution{
		ID:    "exec-insert-mask",
		State: model.ExecutionRunning,
		Task:  "t",
		Error: "boom ANTHROPIC_API_KEY=" + leakSecret,
		StateData: map[string]any{
			"output":           "cli stderr: ANTHROPIC_API_KEY=" + leakSecret,
			"node:work:result": `{"Output":"ANTHROPIC_API_KEY=` + leakSecret + `"}`,
			"node:work:status": "SUCCESS",
			"decision":         "APPROVED",
		},
	}
	if err := eng.insertExecution(exec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var stateJSON, errText string
	if err := repo.SQL().QueryRow(
		`SELECT state_json, error FROM executions WHERE id=?`, exec.ID).
		Scan(&stateJSON, &errText); err != nil {
		t.Fatalf("读回原始列: %v", err)
	}
	if strings.Contains(stateJSON, leakSecret) {
		t.Errorf("insert 路径落库的 state_json 含凭据明文: %s", stateJSON)
	}
	if strings.Contains(errText, leakSecret) {
		t.Errorf("insert 路径落库的 error 含凭据明文: %s", errText)
	}
	// 脱敏不得顺手改写结构化键(它们是路由依据)。
	if !strings.Contains(stateJSON, `"decision":"APPROVED"`) {
		t.Errorf("结构化键 decision 被改动了: %s", stateJSON)
	}
	// 脱敏只作用于副本:内存里的执行记录必须保持原样(运行期还要读它)。
	if !strings.Contains(exec.Error, leakSecret) {
		t.Errorf("脱敏就地改了内存里的 exec.Error: %q", exec.Error)
	}
	if !strings.Contains(fmt.Sprint(exec.StateData["output"]), leakSecret) {
		t.Errorf("脱敏就地改了内存里的 exec.StateData: %v", exec.StateData["output"])
	}
}

// TestStateWriteEntryHandlesNilStateMap 状态写入的统一入口必须能处理
// **nil 状态快照**。
//
// 内存里新建的执行记录(Start 路径、备份恢复、测试构造)StateData 是 nil:
// 直接写 exec.StateData[k] = v 会 panic("assignment to entry in nil map"),
// 而 RetryNode 跑在**调用方** goroutine 里(不在 execute 的兜底 recover
// 覆盖范围内)→ panic 一路上抛(约定 #12:新增 panic 风险路径不得依赖上层保护)。
//
// (从库里读回的记录不会是 nil:json.Unmarshal 对 "{}" 也会建一个空 map。
// 但统一入口不能依赖"调用方恰好先读过一遍库"。)
func TestStateWriteEntryHandlesNilStateMap(t *testing.T) {
	exec := &model.Execution{}
	setExecState(exec, "k", "v")
	if got := fmt.Sprint(exec.StateData["k"]); got != "v" {
		t.Errorf("统一写入口没写进去: %v", exec.StateData)
	}
}

// TestRetryNodeWritesNodeStatusViaStateEntry 重试跳过节点时,节点状态
// 必须经统一写入口落到状态快照里。
func TestRetryNodeWritesNodeStatusViaStateEntry(t *testing.T) {
	eng, repo, _ := newEngineWithAgent(t, mock.New(mock.Options{ID: "mock-a"}))
	exec := &model.Execution{
		ID: "exec-retry-state", WorkflowID: "simple", State: model.ExecutionFailed,
		CurrentNodeID: "work", Task: "t",
	}
	if err := repo.SaveExecution(exec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := eng.RetryNode(context.Background(), exec.ID, true); err != nil {
		t.Fatalf("retry: %v", err)
	}
	after, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if got := fmt.Sprint(after.StateData[compiler.StatusKey("work")]); got != string(model.NodeSkipped) {
		t.Errorf("重试后节点状态 = %q, want %q", got, string(model.NodeSkipped))
	}
}

// ---- 键分类的同源守卫 ----

// stateKeyFuncs 把"生成状态键的函数"映射到键形态。
//
// 形态里的 * 表示该段由运行期变量决定(节点 ID 等)。
var stateKeyFuncs = map[string]string{
	"StatusKey":      "node:*:status",
	"ResultKey":      "node:*:result",
	"AnsweredKey":    "node:*:answered",
	"attemptKey":     "node:*:attempt",
	"RouteBranchKey": "route:*:branch",
}

// expectedStateKeyClass 是每个状态键的期望分类。
//
// 与实现对照:分类错了要么改写执行语义(结构化键被脱敏),
// 要么漏凭据(自由文本键没脱敏)。新增状态键必须在这里登记 ——
// 凭印象维护清单必然漏项(约定 #13 的同源精神)。
var expectedStateKeyClass = map[string]string{
	// 结构化:路由/决策依据,必须原样保留
	"decision":       "structured",
	"status":         "structured",
	"_abort":         "structured",
	"_resume_node":   "structured",
	"_current_node":  "structured",
	"_loop_count":    "structured",
	"_loop_exited":   "structured",
	"_loop_stuck":    "structured",
	"_loop_decision": "structured",
	"node:*:status":  "structured",
	"node:*:attempt": "structured",
	"route:*:branch": "structured",
	// 自由文本:凭据的载体,必须脱敏
	"output":            "free",
	"task":              "free",
	"_user_input":       "free",
	"_error":            "free",
	"user_instruction":  "free",
	"git_diff":          "free",
	"node:*:result":     "free",
	"node:*:answered":   "free",
	"revisit:*:history": "free",
}

// seedOnlyStateKeys 是"只被种进初始会话状态、此后没有写入点"的键。
//
// 它们同样必须登记分类(落库脱敏按默认拒绝处理),但源码里找不到对应的
// 写入点 —— 守卫不要求"每个登记项都被扫到",只要求"每个扫到的写入点
// 都被登记"。
var seedOnlyStateKeys = map[string]string{
	// task:引擎把任务文本种进会话初始状态(engine.go 的 initialState),
	// 之后不再被 Set 覆盖。
	"task": "free",
}

// minScannedStateKeys 是"扫到的状态键形态数"下限。
//
// 它的作用是**防空转**:扫描器的识别逻辑一旦退化(变量改名、访问器类型
// 改名、扫错目录),扫描结果会变成空集或极小集,而"没有未登记的键"
// 这条断言仍然成立 → 测试恒真通过(虚假安全感)。下限把这种退化变成红灯。
//
// 下限取自当前实现的实际扫描结果(19 个形态),留了一点余量。
const minScannedStateKeys = 15

// TestStateWritesAreClassified 扫描状态写入点,确保每个键都已分类。
//
// 这条守卫的作用是"新增状态键必须被显式分类":编译器或运行期新增一个
// 状态键时,若不登记,落库脱敏会按默认规则把它当自由文本处理 ——
// 结构化键(路由依据)会被改写,执行语义悄悄变掉。
//
// 守卫必须真的看得见所有写入点。历史教训(本轮实测):
//
//   - 只扫两个文件 → 别的文件里新增的键看不见;
//   - 只认 `.Set(...)` 方法调用 → `exec.StateData[k] = v` 这种**直接
//     map 写**完全看不见(engine.go 里真的有一处);
//   - 没有"至少扫到 N 个键"的断言 → 扫描器失效时扫描结果为空集,
//     测试**空转恒过**。
//
// 三条现在都由下面的实现保证:扫描范围是 compiler + runtime 的全部
// 非测试文件;识别依据是类型/字段名(直接 map 写与 .Set 都认);扫描到的
// 形态数有下限断言。
func TestStateWritesAreClassified(t *testing.T) {
	g := newStateGuard()
	if err := g.scan([]string{filepath.Join("..", "compiler"), "."}); err != nil {
		t.Fatalf("扫描状态写入点: %v", err)
	}

	// 防空转(1):一个写入点都扫不到 → 扫描器已失效。
	if len(g.writes) == 0 {
		t.Fatal("扫描不到任何状态写入点:扫描范围或识别逻辑已退化,本测试会恒真通过")
	}

	distinct := map[string]bool{}
	for _, w := range g.writes {
		if w.key == "" {
			t.Errorf("%s: 状态键写法 %q 无法解析 —— 必须在 expectedStateKeyClass 里登记分类", w.file, w.expr)
			continue
		}
		distinct[w.key] = true
		want, ok := expectedStateKeyClass[w.key]
		if !ok {
			t.Errorf("%s: 新增的状态键 %q 未登记分类(漏登记 = 落库明文或执行语义被改写)", w.file, w.key)
			continue
		}
		got := "free"
		if structuredStateKey(w.key) {
			got = "structured"
		}
		if got != want {
			t.Errorf("状态键 %q 分类错误: 实现=%s 期望=%s", w.key, got, want)
		}
	}

	// 防空转(2):扫到的形态太少 → 扫描范围或识别逻辑退化了(例如只扫到
	// 一个文件、或只认一种写法)。没有这条,退化后的扫描结果仍然是
	// "没有未登记的键",测试照常通过。
	if len(distinct) < minScannedStateKeys {
		t.Fatalf("只扫到 %d 个状态键形态(下限 %d):扫描范围或识别逻辑退化,本测试会恒真通过",
			len(distinct), minScannedStateKeys)
	}

	// 登记不许陈旧:源码里已经找不到写入点的登记项,说明登记与实现漂移了
	// (键被改名/删除,或写入点被搬到了扫描范围之外)。
	for k := range expectedStateKeyClass {
		if distinct[k] {
			continue
		}
		if _, ok := seedOnlyStateKeys[k]; ok {
			continue
		}
		t.Errorf("登记的状态键 %q 在源码里已找不到写入点(登记与实现漂移:该键是否已改名/删除/搬出了扫描范围?)", k)
	}
}

// ---- 状态写入点扫描 ----
//
// 识别依据是**类型名与字段名**,不是变量名:变量名(st/state/exec/snapshot)
// 随时会改,类型名(StateAccess / stateAccess)与字段名(StateData /
// StateDelta)是结构性的,改变量名不影响识别。
//
// 识别的写入形态:
//
//  1. <访问器>.Set(键, 值) —— 访问器 = 带 Set 方法的状态读写接口;
//  2. <状态map>[键] = 值    —— 直接下标赋值(Set 之外的写法,历史上真的有);
//  3. delete(<状态map>, 键) —— 删除也是对状态键的引用(拼错的键同样是 bug)。
//
// 键形态的解析:字面量原样;包级常量取其值;"字面量+变量"拼接得到
// "node:*:status" 这样的形态(变量段记 *);键构造函数取其形态;局部变量
// 取其赋值表达式解析出的形态。

// stateMapFields 是"承载会话状态的 map 字段名"。
//
// 字段名是结构性的:model.Execution.StateData 与 ADK EventActions.StateDelta
// 是仓库里仅有的两个会话状态 map。
//
// 耦合提示:model.Execution.StateData 若被改名,这里必须同步 —— 改完若
// 守卫报"扫描不到任何状态写入点",多半就是这里漏了(而不是扫描器坏了)。
var stateMapFields = map[string]bool{
	"StateData":  true,
	"StateDelta": true,
}

// stateWrite 是一处状态写入。
type stateWrite struct {
	file string
	// expr 键表达式的源码文本(报错信息用)。
	expr string
	// key 解析出的键形态;空串 = 解析不出来(必须人工登记分类)。
	key string
}

// guardPhase 是扫描的两个阶段。
type guardPhase int

const (
	// phaseCollect 收集写入点,并登记"转发键参数"的函数。
	phaseCollect guardPhase = iota
	// phaseResolve 解析转发函数的调用点(键在调用方给出)。
	phaseResolve
)

// guardEnv 是单个函数内的静态环境。
type guardEnv struct {
	// pkg 当前文件所属的包名。
	pkg string
	// imports import 别名 → 包名。
	imports map[string]string
	// locals 局部变量 → 键形态(仅当该变量的值是键时才有)。
	locals map[string]string
	// varTypes 局部变量/参数 → 类型名。
	varTypes map[string]string
	// varTypeExprs 局部变量/参数 → 类型表达式(用于识别匿名接口)。
	varTypeExprs map[string]ast.Expr
	// params 参数名 → 参数序号(不含接收者)。
	params map[string]int
	// aliases 是状态 map 别名的局部变量。
	aliases map[string]bool
}

// stateGuard 扫描状态写入点。
type stateGuard struct {
	fset *token.FileSet
	// pkgConsts 包名 → 包级字符串常量。
	pkgConsts map[string]map[string]string
	// accessors 状态访问器的类型名(小写)。
	accessors map[string]bool
	// forwarders 转发键参数的函数:函数标识 → 键参数序号。
	forwarders map[string]int
	// writes 扫到的写入点。
	writes []stateWrite
	// filePkgs 文件路径 → 所属包名。
	filePkgs map[string]string
	// fileImports 文件路径 → import 别名到包名的映射。
	fileImports map[string]map[string]string
}

func newStateGuard() *stateGuard {
	return &stateGuard{
		fset:        token.NewFileSet(),
		pkgConsts:   map[string]map[string]string{},
		accessors:   map[string]bool{},
		forwarders:  map[string]int{},
		filePkgs:    map[string]string{},
		fileImports: map[string]map[string]string{},
	}
}

// scan 扫描给定目录下的全部非测试 .go 文件。
func (g *stateGuard) scan(roots []string) error {
	paths, err := guardGoFiles(roots)
	if err != nil {
		return err
	}
	type parsed struct {
		path string
		file *ast.File
	}
	var files []parsed
	for _, p := range paths {
		f, err := parser.ParseFile(g.fset, p, nil, 0)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", p, err)
		}
		files = append(files, parsed{p, f})
		g.filePkgs[p] = f.Name.Name
		g.fileImports[p] = importAliases(f)
	}
	// 第一遍:包级常量 + 状态访问器类型名(跨文件可见)。
	for _, p := range files {
		g.collectScope(p.file)
	}
	// 第二遍:写入点(含转发函数的登记)。
	for _, p := range files {
		for _, decl := range p.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			g.scanFunc(p.path, fd, phaseCollect)
		}
	}
	// 第三遍:转发函数的调用点(键在调用方解析)。
	for _, p := range files {
		for _, decl := range p.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			g.scanFunc(p.path, fd, phaseResolve)
		}
	}
	return nil
}

// importAliases 收集文件的 import 别名 → **包名**。
//
// 值取路径末段(本仓库的导入路径末段与包名一致):跨包常量(如
// compiler.KeyDecision)靠它定位到"被扫描包"的常量表。
func importAliases(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		alias := path.Base(p)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		out[alias] = path.Base(p)
	}
	return out
}

// guardGoFiles 列出目录下的全部非测试 .go 文件(递归)。
func guardGoFiles(roots []string) ([]string, error) {
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("遍历 %s: %w", root, err)
		}
	}
	sort.Strings(out)
	return out, nil
}

// collectScope 收集包级常量与状态访问器类型名。
func (g *stateGuard) collectScope(f *ast.File) {
	pkg := f.Name.Name
	if g.pkgConsts[pkg] == nil {
		g.pkgConsts[pkg] = map[string]string{}
	}
	for name, val := range constStringValues(f) {
		g.pkgConsts[pkg][name] = val
	}
	// 状态访问器:带 Set(key string, val any) error 方法的类型
	// (接口 StateAccess 与实现 stateAccess)。
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if it, ok := ts.Type.(*ast.InterfaceType); ok && interfaceHasSet(it) {
					g.accessors[strings.ToLower(ts.Name.Name)] = true
				}
			}
		case *ast.FuncDecl:
			if d.Recv == nil || d.Name.Name != "Set" {
				continue
			}
			if !methodLooksLikeStateSet(d) {
				continue
			}
			if t := recvTypeName(d.Recv); t != "" {
				g.accessors[strings.ToLower(t)] = true
			}
		}
	}
}

// interfaceHasSet 接口里是否有 Set 方法。
func interfaceHasSet(it *ast.InterfaceType) bool {
	for _, m := range it.Methods.List {
		for _, n := range m.Names {
			if n.Name == "Set" {
				return true
			}
		}
	}
	return false
}

// methodLooksLikeStateSet 方法签名是否是 Set(key string, val any) error。
func methodLooksLikeStateSet(fd *ast.FuncDecl) bool {
	if fd.Type.Params == nil || len(fd.Type.Params.List) != 2 {
		return false
	}
	if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
		return false
	}
	return true
}

// recvTypeName 取接收者的类型名(去掉指针与泛型参数)。
func recvTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return typeNameOf(recv.List[0].Type)
}

// typeNameOf 取表达式的类型名(去掉指针/取址/复合字面量/泛型参数)。
func typeNameOf(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeNameOf(t.X)
	case *ast.UnaryExpr:
		if t.Op == token.AND {
			return typeNameOf(t.X)
		}
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.CompositeLit:
		return typeNameOf(t.Type)
	case *ast.IndexExpr:
		return typeNameOf(t.X)
	case *ast.IndexListExpr:
		return typeNameOf(t.X)
	}
	return ""
}

// scanFunc 扫描一个函数。
//
// phaseCollect:收集写入点,并登记"转发键参数"的函数;
// phaseResolve:解析转发函数的调用点(键在调用方给出)。
func (g *stateGuard) scanFunc(path string, fd *ast.FuncDecl, phase guardPhase) {
	env := &guardEnv{
		imports:      map[string]string{},
		locals:       map[string]string{},
		varTypes:     map[string]string{},
		varTypeExprs: map[string]ast.Expr{},
		params:       map[string]int{},
		aliases:      map[string]bool{},
	}
	// 包名与 import 别名:取自文件级信息(scan 时记录)。
	env.pkg = g.filePkgs[path]
	env.imports = g.fileImports[path]

	// 参数:类型名 + 序号(接收者不计入)。
	idx := 0
	if fd.Type.Params != nil {
		for _, field := range fd.Type.Params.List {
			tn := typeNameOf(field.Type)
			for _, name := range field.Names {
				env.varTypes[name.Name] = tn
				env.varTypeExprs[name.Name] = field.Type
				env.params[name.Name] = idx
				idx++
			}
		}
	}
	// 局部变量声明(var x T)。
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if ds, ok := n.(*ast.DeclStmt); ok {
			gd, ok := ds.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Type == nil {
					continue
				}
				tn := typeNameOf(vs.Type)
				for _, name := range vs.Names {
					env.varTypes[name.Name] = tn
					env.varTypeExprs[name.Name] = vs.Type
				}
			}
		}
		return true
	})

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			g.handleAssign(path, node, env, fd, phase)
		case *ast.CallExpr:
			g.handleCall(path, node, env, fd, phase)
		}
		return true
	})
}

// handleAssign 处理赋值语句。
func (g *stateGuard) handleAssign(path string, st *ast.AssignStmt, env *guardEnv, fd *ast.FuncDecl, phase guardPhase) {
	// 先记录局部变量的类型/形态/别名(同一条语句里先赋值后使用)。
	for i, lhs := range st.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || i >= len(st.Rhs) {
			continue
		}
		rhs := st.Rhs[i]
		if tn := typeNameOf(rhs); tn != "" {
			env.varTypes[id.Name] = tn
			env.varTypeExprs[id.Name] = rhs
		}
		if g.isStateMapExpr(rhs, env) {
			env.aliases[id.Name] = true
		}
		if shape, ok := g.resolveShape(rhs, env); ok {
			env.locals[id.Name] = shape
		}
	}
	// 再看有没有"状态 map 的下标赋值"。
	if phase != phaseCollect {
		return
	}
	for _, lhs := range st.Lhs {
		idx, ok := lhs.(*ast.IndexExpr)
		if !ok {
			continue
		}
		if !g.isStateMapExpr(idx.X, env) {
			continue
		}
		g.addWrite(path, idx.Index, env, fd, phase)
	}
}

// handleDelete 处理 delete(map, key)(go/ast 里 delete 是普通调用)。
func (g *stateGuard) handleDelete(path string, call *ast.CallExpr, env *guardEnv, fd *ast.FuncDecl, phase guardPhase) {
	if len(call.Args) != 2 || phase != phaseCollect {
		return
	}
	if !g.isStateMapExpr(call.Args[0], env) {
		return
	}
	g.addWrite(path, call.Args[1], env, fd, phase)
}

// handleCall 处理 <访问器>.Set(键, 值) 与转发函数的调用点。
func (g *stateGuard) handleCall(path string, call *ast.CallExpr, env *guardEnv, fd *ast.FuncDecl, phase guardPhase) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		// 普通函数调用:delete(map, key) 或转发函数的调用点(键在调用方)。
		if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "delete" {
			g.handleDelete(path, call, env, fd, phase)
			return
		}
		if phase == phaseResolve {
			g.resolveForwarderCall(path, call, env)
		}
		return
	}
	if sel.Sel.Name == "Set" {
		if phase != phaseCollect {
			return
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		if !g.isAccessorVar(recv.Name, env) {
			return
		}
		if len(call.Args) == 0 {
			return
		}
		g.addWrite(path, call.Args[0], env, fd, phase)
		return
	}
	// 方法调用:可能是转发方法(如状态访问器的 Set/Delete)的调用点。
	if phase == phaseResolve {
		g.resolveForwarderCall(path, call, env)
	}
}

// resolveForwarderCall 解析"转发函数"的调用点:键在调用方给出。
func (g *stateGuard) resolveForwarderCall(path string, call *ast.CallExpr, env *guardEnv) {
	idx, ok := g.forwarders[g.calleeIdent(call, env)]
	if !ok {
		return
	}
	if idx >= len(call.Args) {
		return
	}
	g.recordWrite(path, call.Args[idx], env)
}

// recordWrite 解析键形态并记录一处写入。
func (g *stateGuard) recordWrite(path string, keyExpr ast.Expr, env *guardEnv) {
	shape, ok := g.resolveShape(keyExpr, env)
	w := stateWrite{file: path, expr: exprString(g.fset, keyExpr)}
	if ok {
		w.key = shape
	}
	g.writes = append(g.writes, w)
}

// calleeIdent 取被调用者的标识:普通函数用函数名,方法用 "类型.方法"。
func (g *stateGuard) calleeIdent(call *ast.CallExpr, env *guardEnv) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			if t, ok := env.varTypes[x.Name]; ok && t != "" {
				return t + "." + fun.Sel.Name
			}
		}
	}
	return ""
}

// addWrite 登记一处状态写入(或登记一个转发函数)。
//
// phaseCollect:键来自参数 → 这是个转发函数(真正的键在调用方),登记后
// 跳过本处;否则解析出键形态并记录。
func (g *stateGuard) addWrite(path string, keyExpr ast.Expr, env *guardEnv, fd *ast.FuncDecl, phase guardPhase) {
	if phase == phaseCollect {
		if id, ok := keyExpr.(*ast.Ident); ok {
			if idx, ok := env.params[id.Name]; ok {
				g.forwarders[funcIdent(fd)] = idx
				return
			}
		}
	}
	g.recordWrite(path, keyExpr, env)
}

// resolveShape 解析键表达式的形态。ok=false 表示解析不出来。
func (g *stateGuard) resolveShape(expr ast.Expr, env *guardEnv) (string, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return g.resolveShape(e.X, env)
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	case *ast.Ident:
		if v, ok := env.locals[e.Name]; ok {
			return v, true
		}
		if v, ok := g.pkgConsts[env.pkg][e.Name]; ok {
			return v, true
		}
		return "", false
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			if pkg, ok := env.imports[x.Name]; ok {
				if v, ok := g.pkgConsts[pkg][e.Sel.Name]; ok {
					return v, true
				}
			}
		}
		return "", false
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, lok := g.resolveShape(e.X, env)
		r, rok := g.resolveShape(e.Y, env)
		if !lok {
			l = "*"
		}
		if !rok {
			r = "*"
		}
		return l + r, true
	case *ast.CallExpr:
		if ident, ok := e.Fun.(*ast.Ident); ok {
			if shape, ok := stateKeyFuncs[ident.Name]; ok {
				return shape, true
			}
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if shape, ok := stateKeyFuncs[sel.Sel.Name]; ok {
				return shape, true
			}
		}
		return "", false
	}
	return "", false
}

// isStateMapExpr 判断表达式是不是"承载会话状态的 map"。
func (g *stateGuard) isStateMapExpr(e ast.Expr, env *guardEnv) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		return stateMapFields[x.Sel.Name]
	case *ast.Ident:
		return env.aliases[x.Name]
	}
	return false
}

// isAccessorVar 判断变量是不是状态访问器(按**类型**判断,不靠变量名)。
//
// 判据:类型的名字在"带 Set 方法的类型"集合里(状态访问器接口与实现),
// 或类型是**匿名接口**且带 Set 方法。
func (g *stateGuard) isAccessorVar(name string, env *guardEnv) bool {
	expr, ok := env.varTypeExprs[name]
	if !ok {
		return false
	}
	if it, ok := expr.(*ast.InterfaceType); ok {
		return interfaceHasSet(it)
	}
	t, ok := env.varTypes[name]
	if !ok || t == "" {
		return false
	}
	return g.accessors[strings.ToLower(t)]
}

// funcIdent 函数的标识:方法用 "类型.方法",普通函数用函数名。
func funcIdent(fd *ast.FuncDecl) string {
	if fd.Recv != nil {
		if t := recvTypeName(fd.Recv); t != "" {
			return t + "." + fd.Name.Name
		}
	}
	return fd.Name.Name
}

// constStringValues 收集文件里 const 块的字符串常量(name → 值)。
func constStringValues(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if v, err := strconv.Unquote(lit.Value); err == nil {
					out[name.Name] = v
				}
			}
		}
	}
	return out
}

// exprString 把表达式还原成源码文本(报错信息用)。
func exprString(fset *token.FileSet, expr ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, expr)
	return b.String()
}
