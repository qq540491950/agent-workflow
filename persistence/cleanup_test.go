package persistence

// Cleanup(保留策略清理)的回归:票 15 的验收 —— dry_run 只统计不删、
// 只处理终态执行、保留期 0 不动任何数据、重复清理幂等,以及
// estimated_bytes 与将删内容的量级一致。
import (
	"strings"
	"testing"
	"time"

	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

func seedCleanupData(t *testing.T, d *DB) {
	t.Helper()
	now := time.Now()
	mk := func(id, state string, age time.Duration) *model.Execution {
		return &model.Execution{
			ID: id, WorkflowID: "wf-c", WorkflowVersion: 1, WorkflowName: "清理",
			State: model.ExecutionState(state), Task: "task " + id,
			CreatedAt: now.Add(-age).Format(time.RFC3339Nano),
			Snapshot:  &model.Workflow{ID: "wf-c"},
		}
	}
	for _, e := range []*model.Execution{
		mk("cleanup-old-done", "COMPLETED", 48*time.Hour),
		mk("cleanup-old-failed", "FAILED", 48*time.Hour),
		mk("cleanup-old-waiting", "WAITING_USER", 48*time.Hour),
		mk("cleanup-new-done", "COMPLETED", time.Hour),
	} {
		if err := d.SaveExecution(e); err != nil {
			t.Fatalf("seed exec %s: %v", e.ID, err)
		}
		// 事件时间戳跟随执行年龄:Cleanup 按 created_at 判保留期。
		age := 48 * time.Hour
		if e.State == model.ExecutionCompleted && e.ID == "cleanup-new-done" {
			age = time.Hour
		}
		for j := 0; j < 3; j++ {
			if err := d.SaveEvent(event.UIEvent{
				Type: "node.started", ExecutionID: e.ID,
				Timestamp: time.Now().Add(-age),
				Data:      map[string]any{"k": strings.Repeat("x", 100)},
			}); err != nil {
				t.Fatalf("seed event: %v", err)
			}
		}
	}
}

func TestCleanupDryRunCountsOnly(t *testing.T) {
	d := openTestDB(t)
	seedCleanupData(t, d)

	stats, err := d.Cleanup(true, 0, 1, 0) // 执行保留 1 天(事件随执行删)
	if err != nil {
		t.Fatalf("Cleanup dry_run: %v", err)
	}
	// 旧终态执行 2 条,每条 3 个事件 → 6 个事件;WAITING_USER 与新执行不动。
	if stats.Executions != 2 || stats.Events != 6 {
		t.Fatalf("dry_run stats = %+v, want 2 executions / 6 events", stats)
	}
	if stats.EstimatedBytes <= 0 {
		t.Errorf("dry_run 必须给出估算字节数,实际 %d", stats.EstimatedBytes)
	}
	// dry_run 不得真删:执行还在。
	if _, err := d.GetExecution("cleanup-old-done"); err != nil {
		t.Errorf("dry_run 后执行仍应存在: %v", err)
	}
}

func TestCleanupOnlyTerminalStates(t *testing.T) {
	d := openTestDB(t)
	seedCleanupData(t, d)

	if _, err := d.Cleanup(false, 0, 1, 0); err != nil { // 执行保留 1 天
		t.Fatalf("Cleanup: %v", err)
	}
	// 旧终态执行已删;WAITING_USER 永不清理(票 15)。
	if _, err := d.GetExecution("cleanup-old-done"); err == nil {
		t.Error("旧 COMPLETED 执行应被删除")
	}
	if _, err := d.GetExecution("cleanup-old-waiting"); err != nil {
		t.Error("WAITING_USER 执行不得被清理")
	}
	if _, err := d.GetExecution("cleanup-new-done"); err != nil {
		t.Error("保留期内执行不得被清理")
	}
}

func TestCleanupRetentionZeroIsNoop(t *testing.T) {
	d := openTestDB(t)
	seedCleanupData(t, d)

	stats, err := d.Cleanup(false, 0, 0, 0)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if stats.Events != 0 || stats.Executions != 0 {
		t.Fatalf("保留期 0 = 不自动删除,实际 %+v", stats)
	}
	if _, err := d.GetExecution("cleanup-old-done"); err != nil {
		t.Error("保留期 0 不得删除执行")
	}
}

func TestCleanupIdempotent(t *testing.T) {
	d := openTestDB(t)
	seedCleanupData(t, d)

	if _, err := d.Cleanup(false, 1, 1, 0); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	second, err := d.Cleanup(false, 1, 1, 0)
	if err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if second.Events != 0 || second.Executions != 0 {
		t.Fatalf("重复清理应全 0,实际 %+v", second)
	}
}
