package runtime

// ADK 的 Escalate 事件在本项目里编码了多种语义(见 compiler/lower.go 的
// escalateEvent):循环出口条件成立(正常)、HITL 暂停(正常)、节点失败/
// 循环上限/循环停滞(异常)。此前引擎把**所有** Escalate 一律记 WARN,
// 于是"任何含循环的工作流正常跑通"都会产出 WARN 告警 —— 噪声掩盖真正
// 的异常,运维与用户都会对告警脱敏。
//
// 这里按事件携带的状态增量分类(状态键与 compiler 同源,约定 #13):
// 分类依据全部是 compiler 写入的结构化状态键,不猜字符串。

import (
	"fmt"
	"strings"

	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
	"google.golang.org/adk/session"
)

// escalateKind 是 Escalate 事件的分类。
type escalateKind string

const (
	// escalateExpected:预期内的 Escalate(循环正常出口 / HITL 暂停)。
	escalateExpected escalateKind = "expected"
	// escalateUnexpected:真正的异常 Escalate(失败 / 循环上限 / 停滞)。
	escalateUnexpected escalateKind = "unexpected"
)

// onEscalate 处理 ADK 的 Escalate 事件:分类 → 记日志 → 发事件。
//
// 事件类型 runtime.escalate 让"为什么循环在这里结束了"可审计:
// kind=expected 表示正常路径,kind=unexpected 表示需要人看一眼。
func (e *Engine) onEscalate(exec *model.Execution, ev *session.Event) {
	kind, reason := classifyEscalate(ev.Actions.StateDelta)
	if kind == escalateExpected {
		e.escalateExpected.Add(1)
		logx.Info("adk escalate(预期内)", "execution", exec.ID,
			"author", ev.Author, "reason", reason)
	} else {
		e.escalateUnexpected.Add(1)
		logx.Warn("adk escalate(异常)", "execution", exec.ID,
			"author", ev.Author, "reason", reason)
	}
	e.Bus.Emit(event.New("runtime.escalate", exec.ID, "", map[string]any{
		"kind":   string(kind),
		"reason": reason,
		"author": ev.Author,
	}))
}

// classifyEscalate 按状态增量判定 Escalate 是预期内还是异常。
//
// 判定依据(全部为 compiler 写入的状态键):
//   - _abort=WAIT_USER    → HITL 暂停,预期内
//   - _abort=LOOP_LIMIT:x → 循环达到上限:工作流没有自行收敛,异常
//   - _abort=FAILED:x     → 节点失败中断,异常
//   - _loop_exited=true   → 循环出口条件成立,预期内
//   - _loop_stuck=true    → 循环停滞(连续相同结果),异常
//   - 其余/无线索         → 异常(保守:宁可多一条告警,不可漏报)
func classifyEscalate(delta map[string]any) (escalateKind, string) {
	if v, ok := delta[compiler.KeyAbort]; ok && v != nil {
		if s := fmt.Sprint(v); s != "" {
			switch {
			case s == "WAIT_USER":
				return escalateExpected, "等待人工输入(HITL 暂停)"
			case strings.HasPrefix(s, "LOOP_LIMIT:"):
				return escalateUnexpected, "循环达到上限:" + s
			default:
				return escalateUnexpected, "执行中断:" + s
			}
		}
	}
	if isTrue(delta[compiler.KeyLoopExited]) {
		return escalateExpected, "循环出口条件成立"
	}
	if isTrue(delta[compiler.KeyLoopStuck]) {
		return escalateUnexpected, "循环停滞(连续相同结果)"
	}
	return escalateUnexpected, "未识别的 Escalate(无状态线索)"
}

// isTrue 判定状态值是否为真(bool / 数值 / "true" 字符串)。
func isTrue(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case uint64:
		return x != 0
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	}
	return false
}
