package persistence

import (
	"testing"

	"agentworkflow/workflow/model"
)

// 本文件锁住"执行统计把 failed 与 cancelled 分开,且各列之和 == 总数"。
//
// 缺口:此前 failed 列的 SQL 是 `state IN ('FAILED','CANCELLED')` ——
// "被取消"与"跑失败"混在一列里。后果:
//   - Dashboard 的成功率 = completed / total,把**用户主动取消**的
//     执行算成"没成功" → 用户点一次取消,成功率就掉一截;
//   - "这次执行是失败还是被取消"这个问题在统计页上**无法回答**
//     (只能逐条翻执行记录)。
//
// 修复:failed 只算 FAILED,cancelled 单独一列;成功率的分母排除
// CANCELLED(被取消的执行不算"没做成")。
//
// 同时锁住"总数 == 各列之和":列的划分必须**穷尽**所有执行状态,
// 否则总数与各列对不上(表格里的数字加不起来 = 用户不信任这张表)。
// 穷尽的集合:COMPLETED / FAILED / CANCELLED / WAITING_USER /
// RUNNING / CREATED,加上**历史遗留**的 PAUSED(老库里的行,已归入
// cancelled —— 见 workflow/model 的执行状态机:PAUSED 已从状态机移除)。

// seedExecutions 造一批指定状态的执行记录(直接写库,绕开运行期)。
func seedExecutions(t *testing.T, db *DB, workflowID string, states ...model.ExecutionState) {
	t.Helper()
	for i, st := range states {
		id := workflowID + "-exec-" + string(st) + "-" + string(rune('a'+i))
		_, err := db.sql.Exec(`INSERT INTO executions
			(id,workflow_id,workflow_version,workflow_name,state,task,created_at)
			VALUES (?,?,1,?,?,?,?)`,
			id, workflowID, workflowID, string(st), "t", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatalf("insert exec %s: %v", id, err)
		}
	}
}

// statsOf 取某个工作流的统计行。
func statsOf(t *testing.T, db *DB, workflowID string) WorkflowStats {
	t.Helper()
	all, err := db.GetWorkflowStats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	for _, s := range all {
		if s.WorkflowID == workflowID {
			return s
		}
	}
	t.Fatalf("统计里找不到工作流 %q", workflowID)
	return WorkflowStats{}
}

// TestWorkflowStatsSplitFailedAndCancelled failed 与 cancelled 必须分列,
// 且各列之和 == 总数。
func TestWorkflowStatsSplitFailedAndCancelled(t *testing.T) {
	db := openTestDB(t)
	// 直接建一个工作流行(统计按 workflows 左连接 executions 聚合)。
	if _, err := db.sql.Exec(`INSERT INTO workflows (id,name,created_at,updated_at) VALUES (?,?,?,?)`,
		"wf-stats", "Stats", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	seedExecutions(t, db, "wf-stats",
		model.ExecutionCompleted,
		model.ExecutionCompleted,
		model.ExecutionFailed,
		model.ExecutionCancelled,
		model.ExecutionWaitingUser,
		model.ExecutionRunning,
		model.ExecutionCreated,
	)

	st := statsOf(t, db, "wf-stats")
	if st.Completed != 2 {
		t.Errorf("completed = %d, want 2", st.Completed)
	}
	if st.Failed != 1 {
		t.Errorf("failed = %d, want 1(只算 FAILED)", st.Failed)
	}
	if st.Cancelled != 1 {
		t.Errorf("cancelled = %d, want 1(CANCELLED 必须单列)", st.Cancelled)
	}
	if st.Waiting != 1 {
		t.Errorf("waiting = %d, want 1", st.Waiting)
	}
	if st.Running != 2 {
		t.Errorf("running = %d, want 2(RUNNING + CREATED 都算未终结)", st.Running)
	}
	if st.Total != 7 {
		t.Errorf("total = %d, want 7", st.Total)
	}
	sum := st.Completed + st.Failed + st.Cancelled + st.Waiting + st.Running
	if sum != st.Total {
		t.Errorf("各列之和 %d != 总数 %d(列的划分必须穷尽所有执行状态)", sum, st.Total)
	}
}

// TestWorkflowStatsCountsLegacyPausedAsCancelled 历史遗留的 PAUSED 行
// (PAUSED 已从执行状态机移除,老库里仍有)必须被归入 cancelled ——
// 否则它既不在任何一列里,又计入总数,表格里的数字加不起来。
func TestWorkflowStatsCountsLegacyPausedAsCancelled(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.sql.Exec(`INSERT INTO workflows (id,name,created_at,updated_at) VALUES (?,?,?,?)`,
		"wf-legacy", "Legacy", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	seedExecutions(t, db, "wf-legacy", model.ExecutionCompleted)
	// 老库遗留:状态列里是 PAUSED(新代码不再产生这个状态)。
	if _, err := db.sql.Exec(`INSERT INTO executions
		(id,workflow_id,workflow_version,workflow_name,state,task,created_at)
		VALUES (?,?,1,?,?,?,?)`,
		"wf-legacy-exec-paused", "wf-legacy", "wf-legacy", "PAUSED", "t", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert legacy exec: %v", err)
	}

	st := statsOf(t, db, "wf-legacy")
	if st.Cancelled != 1 {
		t.Errorf("cancelled = %d, want 1(遗留 PAUSED 归入 cancelled)", st.Cancelled)
	}
	sum := st.Completed + st.Failed + st.Cancelled + st.Waiting + st.Running
	if sum != st.Total {
		t.Errorf("各列之和 %d != 总数 %d(遗留 PAUSED 必须被归类)", sum, st.Total)
	}
}

// TestOpenKeepsLegacySchemaCompatible 见 dead_columns_test.go 的
// TestLegacySchemaStillWritable(老库 schema 的兼容策略在那里统一覆盖)。
