package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// nodeFailure 是一次"确凿失败"(非取消)的节点记录。
type nodeFailure struct {
	// NodeID 出问题的节点 ID。
	NodeID string
	// Reason 失败原因(节点 outcome 的 Error 原文)。
	Reason string
	// Cancelled = true 表示这次失败是**取消**造成的(用户取消 / 超时 /
	// 并行区里兄弟分支失败后的级联取消),不是"这个节点坏了"。
	//
	// 取消类记录只用来**排除**:它们不参与失败原因与节点状态的补回 ——
	// 把 "context canceled" 当成失败原因,真因就被顶掉了(约定 #14)。
	Cancelled bool
}

// failureLedger 记录每次执行里**确凿失败**的节点及其原因。
//
// 为什么运行期要自己记一份(而不是只依赖 ADK 会话状态):
//
// 并行区的失败级联(compiler/lower.go 的 parallelCascadeAgent)在任一分支
// 失败后取消共享 ctx,而 ADK 的 parallelagent 在分支 ctx 被取消时会把该
// 分支的**终态事件**一起丢掉(parallelagent.runSubAgent 的 select:
// `<-ctx.Done()` 与 `results <- …` 同时就绪时随机选一个;拿到前者就直接
// return ctx.Err(),那条带 StateDelta 的终态事件永远没到 runner)。
//
// 节点状态的唯一来源是**事件里的 StateDelta**:stateAccess.Set 额外写的
// session.State 是 runner 手里的会话**副本**,引擎收尾时读的是 session
// service 里那份(ADK inMemoryService.Get 返回 stored session 的 state,
// 只有 AppendEvent 的 StateDelta 才会写进去)。于是事件一丢,失败信息
// 整段消失:
//   - 会话状态里没有 `_abort` → 引擎退到 "ADK 报的最后一个错误" 作为失败
//     原因 → 那是**兄弟分支被取消**的 "context canceled",真因(节点 panic
//     的原文)被彻底掩盖(约定 #14:"该失败却成功"与"根因被掩盖"同族);
//   - 会话状态里没有 `_error` → 用户看到的失败原因是一句空错误;
//   - 会话状态里没有 `node:<id>:status` → 节点状态表里根本没有那个节点,
//     用户看到的是"失败,但没有节点失败"。
//
// 引擎这一侧是**唯一**见过节点 outcome 的地方(compiler 只把 outcome 变成
// 事件;事件可能被丢),所以"哪个节点失败了、为什么"必须在这里留底。
//
// 台账只记**确凿失败**:失败时 ctx 未被取消。ctx 已取消 = 这是取消(用户
// 取消 / 超时 / 并行区兄弟分支失败后的级联取消),不是"这个节点坏了" ——
// 把它算进失败原因,真因照样会被 "context canceled" 顶掉。
type failureLedger struct {
	mu     sync.RWMutex
	byExec map[string][]nodeFailure
}

// newFailureLedger 创建台账。
func newFailureLedger() *failureLedger {
	return &failureLedger{byExec: map[string][]nodeFailure{}}
}

// record 登记一次失败。同一节点重复登记只保留第一次。
//
// 重复登记只发生在一个节点被重试的场合,而节点级重试在 runAgentNode
// 内部完成(引擎只看到最终 outcome),所以这里"第一次即最终"。
//
// 原因不允许为空:失败必须给出可读原因(约定 #14),空原因会让用户只看到
// "失败"两个字,无从下手。
func (l *failureLedger) record(execID, nodeID, reason string, cancelled bool) {
	if l == nil || execID == "" || nodeID == "" {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "节点 " + nodeID + " 失败"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byExec == nil {
		l.byExec = map[string][]nodeFailure{}
	}
	for _, f := range l.byExec[execID] {
		if f.NodeID == nodeID {
			return
		}
	}
	l.byExec[execID] = append(l.byExec[execID], nodeFailure{
		NodeID:    nodeID,
		Reason:    reason,
		Cancelled: cancelled,
	})
}

// list 返回该执行的**确凿失败**列表(排除取消类),**按节点 ID 排序**。
//
// 排序是为了确定性:并行分支可能同时失败,收集到哪一个不该取决于调度
// 顺序(否则同一个工作流两次运行给出的失败原因不同,没法比对、没法复现)。
func (l *failureLedger) list(execID string) []nodeFailure {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	items := l.byExec[execID]
	if len(items) == 0 {
		return nil
	}
	out := make([]nodeFailure, 0, len(items))
	for _, f := range items {
		if f.Cancelled {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// reset 清空某次执行的台账(每次 execute 开始 / 显式重试时调用)。
func (l *failureLedger) reset(execID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byExec, execID)
}

// failureReason 把失败列表拼成一条可读的失败原因。
//
// 单个失败时直接用它的原因原文(节点 outcome 的 Error 原文,已含节点 ID);
// 多个失败时逐条带上节点 ID,按节点 ID 排序(确定性)。
func failureReason(fails []nodeFailure) string {
	switch len(fails) {
	case 0:
		return ""
	case 1:
		return fails[0].Reason
	}
	parts := make([]string, 0, len(fails))
	for _, f := range fails {
		parts = append(parts, fmt.Sprintf("节点 %s: %s", f.NodeID, f.Reason))
	}
	return strings.Join(parts, "; ")
}

// abortNamesFailure 判断某个中断原因是否指向"台账认定的真失败节点"。
//
// 口径:中断原因形如 "FAILED:<node>"。只有当 <node> 确实是台账里的
// **确凿失败**节点时才算数 —— 指向被取消的兄弟分支("context canceled")
// 不算,那正是要纠正的对象。
func abortNamesFailure(abort string, fails []nodeFailure) bool {
	if !strings.HasPrefix(abort, "FAILED:") {
		return false
	}
	node := strings.TrimPrefix(abort, "FAILED:")
	if node == "" {
		return false
	}
	for _, f := range fails {
		if f.NodeID == node {
			return true
		}
	}
	return false
}

// repairFailureState 用台账纠正会话状态里丢失或串位的失败信息。
//
// 会话状态(ADK 事件送达时写入)有三种"坏"形态,都要纠正:
//  1. **缺** —— 失败分支的终态事件被并行区的级联取消丢掉,于是
//     `_abort` / `_error` / `node:<id>:status` 整段没有;
//  2. **串位** —— 被取消的兄弟分支的 escalate 事件**先落地**,把
//     `_abort` 写成了 "FAILED:<被取消的兄弟>"、`_error` 写成了
//     "context canceled" 这类派生错误;真因(出问题的那个节点)整个
//     不见了,用户看到的失败原因指向一个根本没坏的节点;
//  3. **被别的中断盖住** —— 同一并行区里另一个分支在等人工
//     (WAIT_USER),它的 escalate 事件落地后 `_abort` 变成 "WAIT_USER",
//     而真正失败的那个分支的事件丢了 → 执行停在"等用户输入"上,
//     明明已经失败却没人被告知(约定 #14)。
//
// 纠正口径:**台账说了算**。台账记的是引擎亲眼见到的节点 outcome,
// 会话状态只是事件的投影,而事件可能丢、可能乱序落地。
//
// 只在"台账里确有失败"时纠正:
//   - `node:<id>:status`:会话状态已有终态就以它为准(它可能比台账更晚,
//     例如节点重试后成功);没有终态才补 FAILED。
//   - `_abort`:会话状态已经指向同一个真失败节点时保留(不制造无谓差异);
//     缺失 / 指向被取消的兄弟 / 被 WAIT_USER 盖住 → 一律纠正成台账认定的
//     失败节点。
//   - `_error`:一律以台账为准 —— 同一节点的 outcome 引擎见过原件,
//     会话状态里的那份可能是**别的分支**写进来的派生错误("context
//     canceled"),也可能整段缺失。
func (e *Engine) repairFailureState(exec *model.Execution) {
	if exec == nil || exec.StateData == nil {
		return
	}
	fails := e.failures.list(exec.ID)
	if len(fails) == 0 {
		return
	}
	// 1. 节点状态:会话状态里没有终态的失败节点补 FAILED。
	//    否则节点状态表里根本没有那个节点(用户看到"失败,但没有节点失败")。
	for _, f := range fails {
		key := compiler.StatusKey(f.NodeID)
		if cur, ok := exec.StateData[key]; ok && cur != nil {
			if s := fmt.Sprint(cur); s != "" && s != string(model.NodeRunning) {
				continue // 已有终态:以会话状态为准
			}
		}
		exec.StateData[key] = string(model.NodeFailed)
	}
	// 2. 失败原因:以台账为准(确定性:按节点 ID 排序,与事件落地顺序无关)。
	reason := failureReason(fails)
	// 3. 中断原因:会话状态已经指向同一个真失败节点就保留;
	//    缺失 / 指向被取消的兄弟 / 被 WAIT_USER 盖住 → 一律纠正。
	cur := fmt.Sprint(exec.StateData[compiler.KeyAbort])
	if !abortNamesFailure(cur, fails) {
		exec.StateData[compiler.KeyAbort] = "FAILED:" + fails[0].NodeID
	}
	exec.StateData["_error"] = reason
}

// secondaryFailure 返回**派生**错误的文本;没有派生错误时返回空串。
//
// 派生错误 = 由失败/取消**导致**的错误,而不是失败的原因本身。典型:
// 并行区里一个分支失败 → 级联取消兄弟分支 → ADK 报
// "failed to run sub-agent …: context canceled"。
//
// 它不是"为什么失败",只是"失败之后又发生了什么"。写进失败原因会让用户
// 看到一句没有信息量的 "context canceled",还把真因挤到后面(约定 #14:
// 失败原因必须指向真正出问题的节点)。
//
// 判定用 errors.Is 而不是字符串匹配:ADK 用 %w 包了一层
// (fmt.Errorf("failed to run sub-agent %q: %w", …)),errors.Is 能穿透。
func secondaryFailure(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ""
	}
	return err.Error()
}
