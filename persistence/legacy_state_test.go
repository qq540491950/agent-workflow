package persistence

import (
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"历史遗留的 PAUSED 执行行怎么读"。
//
// 背景:PAUSED 曾是执行状态(有转换分支、有 UI 徽章),但从来不可达 —
// 没有任何代码路径会把执行迁移到它。它已从执行状态机里移除,老库里
// 仍可能留有 state='PAUSED' 的行。
//
// 读法(不写库、不改数据):
//   - 归一为 CANCELLED:既不是"还在跑",也不是用户认识的状态;
//   - 不出现在"运行中列表"里(崩溃恢复只处理真的还在跑的)。
//
// 为什么不写库改数据:启动时静默改写用户的历史记录是件需要理由的事;
// 读出口归一既够用(用户看到的是自洽的状态),又不碰数据。

// seedLegacyPaused 造一条 state='PAUSED' 的历史执行行。
func seedLegacyPaused(t *testing.T, db *DB, id string) {
	t.Helper()
	if _, err := db.sql.Exec(`INSERT INTO executions
		(id,workflow_id,workflow_version,workflow_name,state,task,created_at)
		VALUES (?,?,1,?,?,?,?)`,
		id, "wf-legacy", "wf-legacy", "PAUSED", "t", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert legacy exec: %v", err)
	}
}

// TestLegacyPausedExecutionIsNormalized 读出来必须是 CANCELLED。
func TestLegacyPausedExecutionIsNormalized(t *testing.T) {
	db := openTestDB(t)
	seedLegacyPaused(t, db, "exec-legacy-paused")

	exec, err := db.GetExecution("exec-legacy-paused")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if exec.State != model.ExecutionCancelled {
		t.Errorf("遗留 PAUSED 行读出来应为 CANCELLED,实际 %q", exec.State)
	}
}

// TestLegacyPausedNotInRunningList 遗留 PAUSED 行不得出现在"运行中"列表里
// (它不是还在跑的执行 —— 崩溃恢复不该去动它)。
func TestLegacyPausedNotInRunningList(t *testing.T) {
	db := openTestDB(t)
	seedLegacyPaused(t, db, "exec-legacy-paused")

	running, err := db.ListRunningExecutions()
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	for _, e := range running {
		if e.ID == "exec-legacy-paused" {
			t.Error("遗留 PAUSED 行不得出现在运行中列表里(它不是还在跑的执行)")
		}
	}
}

// TestNormalizeExecutionState 归一函数本身的契约。
func TestNormalizeExecutionState(t *testing.T) {
	cases := map[model.ExecutionState]model.ExecutionState{
		"PAUSED":         model.ExecutionCancelled,
		"RUNNING":        model.ExecutionRunning,
		"WAITING_USER":   model.ExecutionWaitingUser,
		"COMPLETED":      model.ExecutionCompleted,
		"FAILED":         model.ExecutionFailed,
		"CANCELLED":      model.ExecutionCancelled,
		"CREATED":        model.ExecutionCreated,
		"":               model.ExecutionState(""),
		"PAUSED_UNKNOWN": model.ExecutionState("PAUSED_UNKNOWN"),
	}
	for in, want := range cases {
		if got := NormalizeExecutionState(in); got != want {
			t.Errorf("NormalizeExecutionState(%q) = %q, want %q", in, got, want)
		}
	}
}
