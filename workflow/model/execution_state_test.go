package model

import "testing"

// 本文件锁住"PAUSED 已从执行状态机里移除"。
//
// PAUSED 曾经是一个执行状态:model 里有常量、状态机里有转换分支、
// 运行期的 Resume/Cancel 接受它、崩溃恢复把它当"可恢复"、持久层的
// "运行中列表"查它、前端还有一枚专属徽章。
//
// 但**没有任何代码路径**会把执行迁移到 PAUSED —— 它从来不可达。
// 于是这份"能力"全是死代码:用户在前端看到一个"Paused"徽章,却永远
// 不会有执行进入这个状态;运行期的三个分支(Resume 接受、Cancel 接受、
// 崩溃恢复保持)也永远走不到。
//
// 移除它(而不是留着"以后也许用得上"):不可达的状态 + 不可达的分支
// = 不可达的 bug —— 将来有人照着 PAUSED 的分支写代码,照的是一个
// 永远不成立的前提。
//
// 遗留数据的兼容:老库里可能留有 state='PAUSED' 的行。读出口把它
// 归一为 CANCELLED(见 persistence.NormalizeExecutionState),既不是
// "还在跑",也不是用户认识的状态。
func TestPausedIsNotAReachableExecutionState(t *testing.T) {
	const paused = ExecutionState("PAUSED")

	if CanTransitionExecution(ExecutionRunning, paused) {
		t.Error("不应允许迁移到 PAUSED:该状态不可达(没有任何路径会迁移到它)")
	}
	if CanTransitionExecution(paused, ExecutionRunning) {
		t.Error("不应允许从 PAUSED 迁移:该状态不可达")
	}
	if CanTransitionExecution(paused, ExecutionCancelled) {
		t.Error("不应允许从 PAUSED 迁移:该状态不可达")
	}
	if CanTransitionExecution(paused, ExecutionFailed) {
		t.Error("不应允许从 PAUSED 迁移:该状态不可达")
	}
	if CanTransitionExecution(ExecutionCreated, paused) {
		t.Error("不应允许迁移到 PAUSED:该状态不可达")
	}
}
