package persistence

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/workflow/model"
)

// 本文件锁住"JSON 列损坏"的处理方式(迭代 13)。
//
// 此前 7 处 `json.Unmarshal` 的错误被吞(`_ = json.Unmarshal(...)`):
// 列损坏时字段**静默变成空值**。
//
// 分类与处理(刻意的差别):
//
//   - 工作流**定义**列(permission_json / nodes_json / edges_json /
//     variables_json / settings_json)→ **显式失败**。
//     permission_json 损坏 = 工作流的权限收紧**静默失效**(安全相关:
//     工作流以为自己禁了写权限,实际按全局的宽松策略跑);
//     nodes/edges 损坏 = 加载出来的不是用户保存的那个工作流(约定 14)。
//   - 执行**运行期状态**列(state_json / variables_json / iterations_json /
//     node_states_json)→ 单条读取显式失败(继续执行会用空状态从头再跑,
//     重复副作用);批量列表降级 + WARN(一条坏历史记录不该让列表页打不开)。
//
// 空串**不算损坏**(部分列的默认值就是空串,例如 snapshot_json DEFAULT '')。

// newTestDB 开一个临时库。
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "corrupt.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// corruptColumn 直接改库里的某一列,写成非法 JSON。
func corruptColumn(t *testing.T, db *DB, table, column, id string) {
	t.Helper()
	if _, err := db.SQL().Exec(
		"UPDATE "+table+" SET "+column+"=? WHERE id=?", "{not-json", id); err != nil {
		t.Fatalf("注入坏列: %v", err)
	}
}

// TestGetWorkflowFailsOnCorruptPermissionJSON permission_json 损坏必须
// **显式失败**,不能静默把权限段变成空(安全相关)。
func TestGetWorkflowFailsOnCorruptPermissionJSON(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-corrupt-perm", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	corruptColumn(t, db, "workflows", "permission_json", wf.ID)

	got, err := db.GetWorkflow(wf.ID)
	if err == nil {
		t.Fatalf("permission_json 损坏必须显式失败,实际返回了工作流(Permission=%v)", got.Permission)
	}
	if !strings.Contains(err.Error(), "permission_json") {
		t.Errorf("错误消息必须指明是哪个列坏了,实际: %q", err.Error())
	}
}

// TestGetWorkflowFailsOnCorruptNodesJSON nodes/edges 损坏 = 跑的是另一个
// 工作流 → 必须显式失败(约定 14)。
func TestGetWorkflowFailsOnCorruptNodesJSON(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-corrupt-nodes", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	corruptColumn(t, db, "workflows", "nodes_json", wf.ID)

	if _, err := db.GetWorkflow(wf.ID); err == nil {
		t.Fatal("nodes_json 损坏必须显式失败(不得静默当成空节点图)")
	}
	corruptColumn(t, db, "workflows", "nodes_json", wf.ID)
	corruptColumn(t, db, "workflows", "edges_json", wf.ID)
	if _, err := db.GetWorkflow(wf.ID); err == nil {
		t.Fatal("edges_json 损坏必须显式失败(不得静默当成空连线表)")
	}
}

// TestListWorkflowsFailsOnCorruptRow 列表也必须显式失败 —— 静默把坏的
// 工作流从列表里抹掉,用户只会看到"我的工作流不见了"。
func TestListWorkflowsFailsOnCorruptRow(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-corrupt-list", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	corruptColumn(t, db, "workflows", "nodes_json", wf.ID)
	if _, err := db.ListWorkflows(); err == nil {
		t.Fatal("列表里有坏行时必须显式失败,不得静默丢弃")
	}
}

// TestGetWorkflowToleratesEmptyColumns 空串不是损坏(默认值就是空串/空数组)。
func TestGetWorkflowToleratesEmptyColumns(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-empty-cols", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := db.SQL().Exec(
		"UPDATE workflows SET permission_json='', nodes_json='' WHERE id=?", wf.ID); err != nil {
		t.Fatalf("清空列: %v", err)
	}
	got, err := db.GetWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("空串不是损坏,不得报错: %v", err)
	}
	if len(got.Permission) != 0 {
		t.Errorf("空 permission_json 应读成空权限段,实际: %v", got.Permission)
	}
}

// TestGetExecutionFailsOnCorruptStateJSON 单条读取:state_json 损坏必须
// 显式失败 —— 否则"继续执行"会拿着空状态从头再跑一遍(重复副作用)。
func TestGetExecutionFailsOnCorruptStateJSON(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-exec", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	exec := &model.Execution{
		ID: "exec-corrupt", WorkflowID: wf.ID, WorkflowVersion: 1,
		State: model.ExecutionCompleted, Task: "t",
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("save exec: %v", err)
	}
	corruptColumn(t, db, "executions", "state_json", exec.ID)

	if _, err := db.GetExecution(exec.ID); err == nil {
		t.Fatal("state_json 损坏必须显式失败(不得静默当成空状态)")
	}
}

// TestListExecutionsWarnsOnCorruptRow 批量列表:降级 + WARN,列表仍可读。
func TestListExecutionsWarnsOnCorruptRow(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-exec2", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	exec := &model.Execution{
		ID: "exec-list-corrupt", WorkflowID: wf.ID, WorkflowVersion: 1,
		State: model.ExecutionCompleted, Task: "t",
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("save exec: %v", err)
	}
	corruptColumn(t, db, "executions", "state_json", exec.ID)

	var buf bytes.Buffer
	restore := logx.SetOutput(&buf)
	list, err := db.ListExecutions("", 0)
	restore()
	if err != nil {
		t.Fatalf("批量列表遇到一条坏行不得整体失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("列表应仍返回 1 条,实际 %d 条", len(list))
	}
	if !strings.Contains(buf.String(), "state_json") {
		t.Errorf("降级必须记 WARN(指明坏的是哪一列),日志: %s", buf.String())
	}
}

// TestListRunningExecutionsSkipsCorruptRow 崩溃恢复:一条坏记录不能打断
// 整个恢复循环(其余执行仍要被标记成 FAILED)。
func TestListRunningExecutionsSkipsCorruptRow(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-exec3", Name: "n"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	bad := &model.Execution{
		ID: "exec-bad-running", WorkflowID: wf.ID, WorkflowVersion: 1,
		State: model.ExecutionRunning, Task: "t",
	}
	good := &model.Execution{
		ID: "exec-good-running", WorkflowID: wf.ID, WorkflowVersion: 1,
		State: model.ExecutionRunning, Task: "t",
	}
	for _, e := range []*model.Execution{bad, good} {
		if err := db.SaveExecution(e); err != nil {
			t.Fatalf("save exec %s: %v", e.ID, err)
		}
	}
	corruptColumn(t, db, "executions", "state_json", bad.ID)

	var buf bytes.Buffer
	restore := logx.SetOutput(&buf)
	list, err := db.ListRunningExecutions()
	restore()
	if err != nil {
		t.Fatalf("崩溃恢复不得因一条坏记录整体失败: %v", err)
	}
	if len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("应跳过坏记录、保留好记录,实际: %+v", list)
	}
	if !strings.Contains(buf.String(), "exec-bad-running") {
		t.Errorf("跳过坏记录必须留痕(告警里要有执行 ID),日志: %s", buf.String())
	}
}

// ---- 事件 data 列(迭代 14B 补) ------------------------------------------
//
// 事件是**历史审计记录**,口径与批量列表一致:一条坏记录不该让整个
// 事件列表/事件流打不开(那会让审计页整页空白);但也不能静默吞掉 ——
// 此前两处 `_ = json.Unmarshal(dataJSON, &data)` 让这条事件的载荷凭空
// 变成 {},审计线索消失且**没有任何痕迹**。
//
// 现在:载荷降级为空对象 + 一条 WARN(指明坏的是哪一列、哪次执行、哪条
// 事件),与 ListExecutions / ListRunningExecutions 同一口径。
//
// 注意 events 表没有 id 列(主键是自增的 seq),所以不能用 corruptColumn。

// corruptEventData 直接把某次执行的**全部**事件 data 列写成非法 JSON。
func corruptEventData(t *testing.T, db *DB, executionID string) {
	t.Helper()
	if _, err := db.SQL().Exec(
		"UPDATE events SET data_json=? WHERE execution_id=?", "{not-json", executionID); err != nil {
		t.Fatalf("注入坏列: %v", err)
	}
}

// TestListEventsWarnsOnCorruptDataJSON 单次执行的事件流:坏 data 列降级 +
// WARN,列表仍可读。
func TestListEventsWarnsOnCorruptDataJSON(t *testing.T) {
	db := newTestDB(t)
	if err := db.SaveEvent(event.UIEvent{
		ExecutionID: "exec-ev", NodeID: "n1", Type: event.AgentOutput,
		Data: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("save event: %v", err)
	}
	corruptEventData(t, db, "exec-ev")

	var buf bytes.Buffer
	restore := logx.SetOutput(&buf)
	list, err := db.ListEvents("exec-ev", 0)
	restore()
	if err != nil {
		t.Fatalf("事件列表遇到一条坏记录不得整体失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("列表应仍返回 1 条,实际 %d 条", len(list))
	}
	data, ok := list[0]["data"].(map[string]any)
	if !ok || len(data) != 0 {
		t.Errorf("坏事件的载荷应降级为空对象,实际: %#v", list[0]["data"])
	}
	if !strings.Contains(buf.String(), "data_json") {
		t.Errorf("降级必须记 WARN(指明坏的是哪一列),日志: %s", buf.String())
	}
}

// TestListAllEventsWarnsOnCorruptDataJSON 全局事件流:同一口径。
func TestListAllEventsWarnsOnCorruptDataJSON(t *testing.T) {
	db := newTestDB(t)
	if err := db.SaveEvent(event.UIEvent{
		ExecutionID: "exec-ev2", NodeID: "n1", Type: event.AgentOutput,
		Data: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("save event: %v", err)
	}
	corruptEventData(t, db, "exec-ev2")

	var buf bytes.Buffer
	restore := logx.SetOutput(&buf)
	list, err := db.ListAllEvents("", 0)
	restore()
	if err != nil {
		t.Fatalf("全局事件流遇到一条坏记录不得整体失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("列表应仍返回 1 条,实际 %d 条", len(list))
	}
	data, ok := list[0]["data"].(map[string]any)
	if !ok || len(data) != 0 {
		t.Errorf("坏事件的载荷应降级为空对象,实际: %#v", list[0]["data"])
	}
	if !strings.Contains(buf.String(), "data_json") {
		t.Errorf("降级必须记 WARN(指明坏的是哪一列),日志: %s", buf.String())
	}
}

// TestListEventsKeepsHealthyRows 可读性:一条坏事件不许把**同一批**里
// 的好事件一起吞掉(降级是逐行的)。
func TestListEventsKeepsHealthyRows(t *testing.T) {
	db := newTestDB(t)
	if err := db.SaveEvent(event.UIEvent{
		ExecutionID: "exec-ev3", NodeID: "n1", Type: event.AgentOutput,
		Data: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("save event: %v", err)
	}
	if err := db.SaveEvent(event.UIEvent{
		ExecutionID: "exec-ev3", NodeID: "n2", Type: event.NodeFailed,
		Data: map[string]any{"error": "boom"},
	}); err != nil {
		t.Fatalf("save event: %v", err)
	}
	// 只把第一条写坏
	if _, err := db.SQL().Exec(
		"UPDATE events SET data_json=? WHERE execution_id=? AND node_id=?",
		"{not-json", "exec-ev3", "n1"); err != nil {
		t.Fatalf("注入坏列: %v", err)
	}

	list, err := db.ListEvents("exec-ev3", 0)
	if err != nil {
		t.Fatalf("事件列表遇到一条坏记录不得整体失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应仍返回 2 条,实际 %d 条", len(list))
	}
	// 第二条(好行)的载荷必须完好
	second, ok := list[1]["data"].(map[string]any)
	if !ok || second["error"] != "boom" {
		t.Errorf("好事件的载荷不应受影响,实际: %#v", list[1]["data"])
	}
}

// TestListEventsToleratesEmptyDataColumn 空串/null 不是损坏(默认列值
// 就是 '{}',历史数据里也可能有空串)。
func TestListEventsToleratesEmptyDataColumn(t *testing.T) {
	db := newTestDB(t)
	if err := db.SaveEvent(event.UIEvent{
		ExecutionID: "exec-ev4", NodeID: "n1", Type: event.AgentOutput,
		Data: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("save event: %v", err)
	}
	for _, raw := range []string{"", "null"} {
		if _, err := db.SQL().Exec(
			"UPDATE events SET data_json=? WHERE execution_id=?", raw, "exec-ev4"); err != nil {
			t.Fatalf("清空列: %v", err)
		}
		list, err := db.ListEvents("exec-ev4", 0)
		if err != nil {
			t.Fatalf("data_json=%q 不是损坏,不得报错: %v", raw, err)
		}
		if len(list) != 1 {
			t.Fatalf("data_json=%q 时列表应仍返回 1 条,实际 %d 条", raw, len(list))
		}
		data, ok := list[0]["data"].(map[string]any)
		if !ok {
			t.Errorf("data_json=%q 时载荷应是空对象(不是 nil),实际: %#v", raw, list[0]["data"])
			continue
		}
		if len(data) != 0 {
			t.Errorf("data_json=%q 时载荷应为空对象,实际: %#v", raw, data)
		}
	}
}
