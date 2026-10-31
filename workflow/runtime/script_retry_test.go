package runtime

// script 节点的节点级 retry 支持(与 agent 节点同语义)。
//
// 背景:retry 此前只在 agent 节点的契约表里声明,script 节点写了
// retry 会被校验期判 UNKNOWN_NODE_CONFIG、运行期完全不读 —— 而子进程
// 恰恰是最常见的瞬时失败源(网络抖动、文件未就绪)。现在两类节点
// 共用同一个重试循环(withNodeRetries),契约同源(约定 13)。
//
// 本文件锁住四条行为:
//  1. 首次失败、重试后成功 → 节点 SUCCESS + 恰好一条 node.retry 事件;
//  2. 次次失败 → 节点 FAILED + 事件数 = max_attempts-1(只在重试前发);
//  3. retry 配置非法 → fail closed:命令一次都不执行、零 retry 事件
//     (约定 14:配置错误不能被静默当成"不重试照常成功");
//  4. 门禁拒绝不属于可重试失败:未放行时一次都不执行、零 retry 事件。

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"agentworkflow/event"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// scriptRetryYAML 生成单 script 节点的工作流(固定 max_attempts=2 / fixed 退避)。
// command 里的 __MARKER__ 会被替换成测试专属的标记文件路径。
func scriptRetryYAML(command string) string {
	// 测试里的命令是 POSIX sh 语法;Windows 下显式用 Git Bash 的 sh,
	// 避免默认 cmd.exe 把 test/touch/exit 当成另一种语言。
	shell := ""
	if runtime.GOOS == "windows" {
		shell = "    shell: sh\n"
	}
	return `
version: "1"
workflow:
  id: script-retry
  name: Script Retry
nodes:
  - id: sh
    type: script
` + shell + `    command: ` + command + `
    retry:
      max_attempts: 2
      backoff: fixed
edges: []
`
}

// startScriptEngine 建一个放行 script 的引擎(总线保留历史)。
func startScriptEngine(t *testing.T) *Engine {
	t.Helper()
	eng := newMinimalEngine(t)
	eng.Bus.KeepHistory = true
	eng.AllowScriptNodes = func() bool { return true }
	return eng
}

// runScriptWorkflow 校验并启动,等待执行落定,返回(最终执行, 总线历史)。
func runScriptWorkflow(t *testing.T, eng *Engine, yml string) (*model.Execution, []event.UIEvent) {
	t.Helper()
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionCompleted, model.ExecutionFailed)
	return exec, eng.Bus.History()
}

// retryEventCount 统计 node.retry 事件;任一事件缺 attempt/max_attempts 即失败。
func retryEventCount(t *testing.T, history []event.UIEvent, nodeID string) int {
	t.Helper()
	n := 0
	for _, ev := range history {
		if ev.Type != "node.retry" || ev.NodeID != nodeID {
			continue
		}
		n++
		if !isIntValue(ev.Data["attempt"]) || !isIntValue(ev.Data["max_attempts"]) {
			t.Fatalf("node.retry 事件缺 attempt/max_attempts: %+v", ev.Data)
		}
		if ev.Data["error"] == nil || ev.Data["error"] == "" {
			t.Fatalf("node.retry 事件应带上次失败原因: %+v", ev.Data)
		}
	}
	return n
}

// isIntValue 兼容 int / int64 / float64(JSON 往返后的整数形态)。
func isIntValue(v any) bool {
	switch n := v.(type) {
	case int:
		return true
	case int64:
		return true
	case float64:
		return n == float64(int(n))
	default:
		return false
	}
}

// nodeState 查询执行里某节点的最终状态。
func nodeState(t *testing.T, eng *Engine, execID, nodeID string) model.NodeState {
	t.Helper()
	nodes, err := eng.Repo.ListExecutionNodes(execID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	for _, nd := range nodes {
		if nd.NodeID == nodeID {
			return nd.State
		}
	}
	t.Fatalf("节点 %s 不存在", nodeID)
	return ""
}

// TestScriptNodeRetrySucceedsOnSecondAttempt 首次失败 → 重试成功。
func TestScriptNodeRetrySucceedsOnSecondAttempt(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "flaky marker")
	// 首次:标记不存在 → exit 1 并留下标记;重试:标记存在 → 成功。
	yml := scriptRetryYAML(`test -f "__MARKER__" && echo recovered || { touch "__MARKER__"; exit 1; }`)
	yml = strings.ReplaceAll(yml, "__MARKER__", marker)

	eng := startScriptEngine(t)
	exec, history := runScriptWorkflow(t, eng, yml)
	if exec.State != model.ExecutionCompleted {
		t.Fatalf("执行应 COMPLETED(重试成功),实际 %s(error=%s)", exec.State, exec.Error)
	}
	if st := nodeState(t, eng, exec.ID, "sh"); st != model.NodeSuccess {
		t.Fatalf("节点应 SUCCESS,实际 %s", st)
	}
	if n := retryEventCount(t, history, "sh"); n != 1 {
		t.Fatalf("应恰好 1 条 node.retry 事件,实际 %d", n)
	}
}

// TestScriptNodeRetryExhaustedFails 次次失败 → 节点 FAILED,执行 FAILED。
func TestScriptNodeRetryExhaustedFails(t *testing.T) {
	eng := startScriptEngine(t)
	exec, history := runScriptWorkflow(t, eng, scriptRetryYAML(`echo "boom" >&2; exit 7`))
	if exec.State != model.ExecutionFailed {
		t.Fatalf("执行应 FAILED,实际 %s", exec.State)
	}
	if st := nodeState(t, eng, exec.ID, "sh"); st != model.NodeFailed {
		t.Fatalf("节点应 FAILED,实际 %s", st)
	}
	if n := retryEventCount(t, history, "sh"); n != 1 {
		t.Fatalf("max_attempts=2 时应恰好 1 条 node.retry(只在重试前发),实际 %d", n)
	}
}

// TestScriptNodeRetryInvalidConfigFailsClosed retry 配置非法 → fail closed。
//
// 校验期已经用同一份契约拦住非法 retry(见
// workflow/validator/retry_config_test.go 同族用例),正常路径到不了这里;
// 本用例直接调 ExecuteNode,锁住运行期兜底分支:错误可读、命令一次不跑、
// 零 retry 事件(约定 14:配置错误不能被静默当成"不重试照常成功")。
func TestScriptNodeRetryInvalidConfigFailsClosed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "never")
	eng := startScriptEngine(t)
	node := &model.Node{ID: "sh", Type: model.NodeTypeScript, Config: map[string]any{
		"command": `touch "` + marker + `"`,
		"retry":   map[string]any{"max_attempts": "2", "backoff": "fixed"},
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-script-retry-bad"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	env.Bus.KeepHistory = true
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("配置非法应 fail closed(节点失败),实际 %s", out.State)
	}
	if !strings.Contains(out.Error, "retry 配置非法") {
		t.Fatalf("错误应说明 retry 配置非法,实际:%s", out.Error)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("命令不应被执行(标记文件不应存在): %s", marker)
	}
	if n := retryEventCount(t, env.Bus.History(), "sh"); n != 0 {
		t.Fatalf("不应有任何 node.retry 事件,实际 %d", n)
	}
}

// TestScriptNodeGateFailureNotRetried 门禁拒绝不属于可重试失败:不放行时
// 即便配了 retry 也一次都不执行(重试只会重复同样的策略拒绝)。
func TestScriptNodeGateFailureNotRetried(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.Bus.KeepHistory = true
	// 故意不注入 AllowScriptNodes(默认拒绝)
	exec, history := runScriptWorkflow(t, eng, scriptRetryYAML(`echo hi`))
	if exec.State != model.ExecutionFailed {
		t.Fatalf("执行应 FAILED,实际 %s", exec.State)
	}
	if !strings.Contains(exec.Error, "未放行") {
		t.Fatalf("错误应说明门禁未放行,实际:%s", exec.Error)
	}
	if n := retryEventCount(t, history, "sh"); n != 0 {
		t.Fatalf("门禁拒绝不应产生 node.retry 事件,实际 %d", n)
	}
}

// TestScriptNodeCancelDuringBackoff 退避等待期间取消 → 立即生效(不吞取消)。
//
// initial_backoff_seconds=30 让"第二次尝试前的等待"足够长:取消必然落在
// 退避窗口内,时序确定 —— withNodeRetries 的 select 必须先于 30s 响应取消。
func TestScriptNodeCancelDuringBackoff(t *testing.T) {
	eng := startScriptEngine(t)
	yml := `
version: "1"
workflow:
  id: script-cancel-backoff
  name: Script Cancel Backoff
nodes:
  - id: sh
    type: script
    command: exit 1
    retry:
      max_attempts: 3
      backoff: fixed
      initial_backoff_seconds: 30
edges: []
`
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 等节点真的跑起来(第一次尝试快速失败,随后进入 30s 退避 ——
	// 重试期间节点状态保持 RUNNING,结果要等循环结束才落,所以等事件;
	// startScriptEngine 已开总线历史)
	deadline := time.Now().Add(3 * time.Second)
	started := false
	for time.Now().Before(deadline) && !started {
		for _, ev := range eng.Bus.History() {
			if ev.Type == "node.started" && ev.NodeID == "sh" {
				started = true
				break
			}
		}
		if !started {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !started {
		t.Fatal("3s 内未等到 node.started(第一次尝试未执行)")
	}
	if err := eng.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionCancelled)
}
