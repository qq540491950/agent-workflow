package persistence

import (
	"testing"

	"agentworkflow/workflow/model"
)

// execStateGuardAgreesWithStateMachine 锁定"终态守卫"与执行状态机之间
// 的关系。
//
// 背景:守卫清单(execGuardedStates)是持久层自己维护的一份**独立清单**,
// 不是从 model.CanTransitionExecution 推导出来的。两份清单各自演化,
// 就会出现"状态机认得、守卫不认"(或反之)的漂移 —— 约定 13 的同族问题:
// 校验期与运行期必须同源,否则用户会被自己的守卫挡住。
//
// 本用例把两者的关系钉死,三条规则:
//
//  1. **终态覆盖**:状态机里"没有任何合法后继"的状态(COMPLETED、
//     CANCELLED)必须纳入守卫。状态机新增一个终态而守卫忘了加 → 这里变红。
//  2. **不得过宽**:被守卫的状态,其合法后继只能是 RUNNING —— 守卫只能
//     拦"显式重试"这条路,不能拦 WAITING_USER→CANCELLED 这类正常收尾。
//     守卫清单被扩成"把非终态也锁死" → 这里变红。
//  3. **FAILED 必须在内**:状态机允许 FAILED→RUNNING,但那仅代表"用户
//     显式重试"(引擎走 CAS)。整行 upsert 不得替用户做重试裁决,
//     所以 FAILED 必须在守卫里。守卫把 FAILED 漏掉 → 这里变红。
//
// 注意:这组用例守护的是**清单之间的关系**,不改变守卫的行为
// (守卫行为由 repo_cas_test.go 的四条用例覆盖)。
func TestExecStateGuardAgreesWithStateMachine(t *testing.T) {
	allStates := []model.ExecutionState{
		model.ExecutionCreated,
		model.ExecutionRunning,
		model.ExecutionWaitingUser,
		model.ExecutionCompleted,
		model.ExecutionFailed,
		model.ExecutionCancelled,
	}

	guarded := make(map[model.ExecutionState]bool, len(execGuardedStates))
	for _, s := range execGuardedStates {
		guarded[s] = true
	}

	// successors 返回状态机认定的合法后继集合。
	successors := func(from model.ExecutionState) []model.ExecutionState {
		var out []model.ExecutionState
		for _, to := range allStates {
			if model.CanTransitionExecution(from, to) {
				out = append(out, to)
			}
		}
		return out
	}

	// 规则 1:状态机里的终态(无合法后继)必须被守卫覆盖。
	for _, s := range allStates {
		if len(successors(s)) == 0 && !guarded[s] {
			t.Errorf("状态 %s 在状态机里没有合法后继(终态),必须纳入终态守卫 execGuardedStates", s)
		}
	}

	// 规则 2:守卫不得过宽 —— 被守卫的状态的合法后继只能是 RUNNING。
	for _, s := range execGuardedStates {
		succ := successors(s)
		if len(succ) > 1 || (len(succ) == 1 && succ[0] != model.ExecutionRunning) {
			t.Errorf("守卫状态 %s 的合法后继是 %v:守卫只能拦 RUNNING 这条显式重试路径,不能拦 %v",
				s, succ, succ)
		}
	}

	// 规则 3:FAILED 必须被守卫(整行 upsert 不得替用户做显式重试)。
	if !guarded[model.ExecutionFailed] {
		t.Error("FAILED 必须在终态守卫里:状态机的 FAILED→RUNNING 仅代表用户显式重试(走 CAS),整行 upsert 不得越权改写")
	}
}
