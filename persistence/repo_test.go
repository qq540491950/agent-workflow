package persistence

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/event"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

const demoYAML = `
version: "1"
workflow:
  id: wf-demo
  name: Demo
  settings:
    max_iterations: 5
nodes:
  - id: plan
    type: agent
    agent: claude-code
    mode: plan
    x: 10
    y: 20
  - id: review
    type: agent
    agent: claude-code
    mode: review
edges:
  - from: plan
    to: review
`

func TestWorkflowCRUDAndVersions(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()

	// create
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if wf.Version != 1 {
		t.Fatalf("version = %d, want 1", wf.Version)
	}

	// read
	got, err := db.GetWorkflow("wf-demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Demo" || len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.Nodes[0].Position.X != 10 {
		t.Errorf("position lost: %+v", got.Nodes[0])
	}
	if got.Nodes[0].Config["agent"] != "claude-code" {
		t.Errorf("config lost: %+v", got.Nodes[0].Config)
	}

	// update → 版本+1
	got.Description = "updated"
	if err := db.SaveWorkflow(got, true); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	vers, _ := db.ListWorkflowVersions("wf-demo")
	if len(vers) != 2 || vers[0] != 2 {
		t.Fatalf("versions = %v", vers)
	}
	v1, err := db.GetWorkflowVersion("wf-demo", 1)
	if err != nil || v1.Description != "" {
		t.Fatalf("version 1 snapshot: %v %+v", err, v1)
	}
	v2, _ := db.GetWorkflowVersion("wf-demo", 2)
	if v2.Description != "updated" {
		t.Errorf("version 2 description = %q", v2.Description)
	}

	// list & delete
	list, _ := db.ListWorkflows()
	if len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}
	if err := db.DeleteWorkflow("wf-demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list2, _ := db.ListWorkflows(); len(list2) != 0 {
		t.Fatal("expected empty after delete")
	}
}

func TestExecutionPersistenceAndRecovery(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()
	_ = db.SaveWorkflow(wf, false)

	exec := &model.Execution{
		ID: "exec-1", WorkflowID: "wf-demo", WorkflowVersion: 1, WorkflowName: "Demo",
		State: model.ExecutionRunning, Task: "do something",
		CurrentNodeID: "review",
		Iterations:    map[string]int{"review": 2},
		NodeStates:    map[string]string{"plan": "SUCCESS", "review": "RUNNING"},
		CreatedAt:     nowStr(), StartedAt: nowStr(),
		Snapshot: wf,
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("save exec: %v", err)
	}

	// 节点明细
	en := &model.ExecutionNode{
		ID: "en-1", ExecutionID: "exec-1", NodeID: "plan", NodeType: model.NodeTypeAgent,
		NodeName: "plan", State: model.NodeSuccess, Attempt: 1,
		Output: "the plan", ResultJSON: `{"decision":"DONE"}`,
		StartedAt: nowStr(), FinishedAt: nowStr(), DurationMS: 42,
	}
	if err := db.SaveExecutionNode(en); err != nil {
		t.Fatalf("save node: %v", err)
	}
	en.Output = "the plan v2"
	if err := db.SaveExecutionNode(en); err != nil {
		t.Fatalf("update node: %v", err)
	}

	// 事件
	_ = db.SaveEvent(event.New(event.NodeCompleted, "exec-1", "plan", map[string]any{"status": "SUCCESS"}))

	// 模拟崩溃后恢复:重新查询运行中的执行
	running, err := db.ListRunningExecutions()
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	if len(running) != 1 || running[0].ID != "exec-1" {
		t.Fatalf("running = %+v", running)
	}
	rec := running[0]
	if rec.CurrentNodeID != "review" || rec.Iterations["review"] != 2 {
		t.Fatalf("recovery state lost: %+v", rec)
	}
	if rec.Snapshot == nil || len(rec.Snapshot.Nodes) != 2 {
		t.Fatalf("snapshot lost: %+v", rec.Snapshot)
	}

	nodes, _ := db.ListExecutionNodes("exec-1")
	if len(nodes) != 1 || nodes[0].Output != "the plan v2" || nodes[0].DurationMS != 42 {
		t.Fatalf("nodes = %+v", nodes)
	}

	events, _ := db.ListEvents("exec-1", 0)
	if len(events) != 1 || events[0]["type"] != event.NodeCompleted {
		t.Fatalf("events = %+v", events)
	}

	// 完成后不再出现在 running 列表
	rec.State = model.ExecutionCompleted
	_ = db.SaveExecution(rec)
	if r2, _ := db.ListRunningExecutions(); len(r2) != 0 {
		t.Fatal("completed execution should not be running")
	}
}

func TestArtifactsAndSettings(t *testing.T) {
	db := openTestDB(t)
	a := &model.Artifact{
		ID: "a-1", ExecutionID: "exec-9", NodeID: "plan", Name: "plan.md",
		ContentType: "text/markdown", Content: "# Plan", CreatedAt: nowStr(),
	}
	if err := db.SaveArtifact(a); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	list, _ := db.ListArtifacts("exec-9")
	if len(list) != 1 || list[0].Content != "# Plan" {
		t.Fatalf("artifacts = %+v", list)
	}

	var conf map[string]any
	if err := db.SaveSetting("app", map[string]any{"theme": "dark"}); err != nil {
		t.Fatalf("save setting: %v", err)
	}
	if err := db.GetSetting("app", &conf); err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if conf["theme"] != "dark" {
		t.Fatalf("setting = %+v", conf)
	}
}

// ---- 版本号不可由调用方伪造(轮次 27)----

// TestWorkflowVersionNotForged 验证版本号以库中现值为准。
// REST 的 POST /api/workflows 直接反序列化客户端 JSON,model.Workflow.Version
// 带 json tag,伪造 version:99 此前会被 ++ 后落库成 v100 并留下伪造快照;
// 重复提交还会因 workflow_versions 唯一约束被 INSERT OR REPLACE 静默覆盖,
// 版本历史失真且无任何告警。
func TestWorkflowVersionNotForged(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 伪造版本号 99
	got, err := db.GetWorkflow("wf-demo")
	if err != nil {
		t.Fatal(err)
	}
	got.Version = 99
	got.Description = "forged"
	if err := db.SaveWorkflow(got, true); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("版本应为 2(库中现值 +1),实际 %d(入参 99 被采信了)", got.Version)
	}
	vers, _ := db.ListWorkflowVersions("wf-demo")
	if len(vers) != 2 || vers[0] != 2 {
		t.Fatalf("版本历史应为 [1 2],实际 %v(伪造版本污染了历史)", vers)
	}

	// 不 bump 的路径(启用/禁用/恢复备份)也不得把版本改小
	got.Version = 0
	if err := db.SaveWorkflow(got, false); err != nil {
		t.Fatalf("save no-bump: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("非 bump 保存不应改变版本,实际 %d", got.Version)
	}
}

// TestWorkflowVersionHistoryNotSilentlyOverwritten 验证同一版本号不会被
// 静默覆盖(唯一冲突必须显式报错),否则版本历史会悄悄丢记录。
func TestWorkflowVersionHistoryNotSilentlyOverwritten(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 直接写一条重复版本快照,应报错而非静默覆盖
	_, err := db.sql.Exec(`INSERT INTO workflow_versions (workflow_id,version,dsl_text,snapshot_json,created_at) VALUES (?,?,?,?,?)`,
		"wf-demo", 1, "dup", "{}", "now")
	if err == nil {
		t.Error("重复版本快照应报错(静默覆盖会丢版本历史)")
	}
	// 原记录未被破坏
	v, err := db.GetWorkflowVersion("wf-demo", 1)
	if err != nil || v == nil {
		t.Fatalf("原版本快照丢失: %v %+v", err, v)
	}
}

// TestSaveWorkflowSurfacesExistenceQueryError 故障注入(C4):
// 存在性查询失败必须上抛。此前 `existing` 查询的错误被 `_ =` 丢弃,
// existing 保持 0 → 对**已存在**的工作流执行 INSERT → 用户看到的是
// 一句主键冲突,真实根因(查询失败)被彻底掩盖;若判定反了则是静默丢数据。
// 正常内存 SQLite 无法构造查询失败,只能注入。
func TestSaveWorkflowSurfacesExistenceQueryError(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("create: %v", err)
	}

	injected := errors.New("injected: disk I/O error")
	db.SetQueryFault(func(stmt string) error {
		if strings.Contains(stmt, "SELECT COUNT(1) FROM workflows") {
			return injected
		}
		return nil
	})
	t.Cleanup(func() { db.SetQueryFault(nil) })

	err := db.SaveWorkflow(wf, true)
	if err == nil {
		t.Fatal("存在性查询失败时 SaveWorkflow 必须报错")
	}
	if !strings.Contains(err.Error(), "injected") {
		t.Errorf("错误应保留真实根因(而非被主键冲突掩盖),实际 %v", err)
	}
	// 取消注入后仍能正常保存
	db.SetQueryFault(nil)
	got, err := db.GetWorkflow("wf-demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := db.SaveWorkflow(got, true); err != nil {
		t.Errorf("取消注入后应恢复正常保存,实际 %v", err)
	}
}

// TestSetSettingFailureDoesNotFakeSuccess 故障注入(C4):设置项写入失败
// 必须上抛,调用方据此保持内存态不变(否则界面提示"已保存"但重启即失效)。
func TestSetSettingFailureDoesNotFakeSuccess(t *testing.T) {
	db := openTestDB(t)
	if err := db.SaveSetting("k", "v1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	injected := errors.New("injected: database is locked")
	db.SetQueryFault(func(stmt string) error {
		if strings.Contains(stmt, "settings") {
			return injected
		}
		return nil
	})
	t.Cleanup(func() { db.SetQueryFault(nil) })

	if err := db.SaveSetting("k", "v2"); err == nil {
		t.Fatal("写入失败必须报错,不能静默成功")
	}
}

// TestStateJSONNumbersAreIntAfterRead 轮次 29 第 1 项:
// state_json 读回后数值必须是 int,而不是 float64。
//
// 背景:StateData 执行期来自 ADK 内存(int),经 SQLite JSON 往返后
// encoding/json 把数字全解成 float64 —— 同一字段的类型随生命周期变化,
// 于是每个读取点都得写双类型兼容(compiler.IntOfOk 就是这么来的),
// 漏掉一处就是静默 bug(轮次 10/24/25/27 都踩过循环计数回落)。
// 现在在解码入口统一归一,类型在整个生命周期内稳定。
func TestStateJSONNumbersAreIntAfterRead(t *testing.T) {
	db := openTestDB(t)
	exec := &model.Execution{
		ID: "exec-1", WorkflowID: "wf", State: model.ExecutionRunning,
		Task: "t", CreatedAt: "now",
		Iterations: map[string]int{"loop": 1},
		NodeStates: map[string]string{},
		StateData: map[string]any{
			"_loop_count":         3,
			"node:review:attempt": 2,
			"guard:history":       []any{"a", "b"},
			"nested":              map[string]any{"n": 7},
			"ratio":               1.5, // 真浮点应保持 float64
			"text":                "hello",
		},
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := db.GetExecution("exec-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := got.StateData["_loop_count"].(int); !ok {
		t.Errorf("_loop_count 应为 int,实际 %T(%v)",
			got.StateData["_loop_count"], got.StateData["_loop_count"])
	}
	if _, ok := got.StateData["node:review:attempt"].(int); !ok {
		t.Errorf("attempt 应为 int,实际 %T", got.StateData["node:review:attempt"])
	}
	nested, _ := got.StateData["nested"].(map[string]any)
	if _, ok := nested["n"].(int); !ok {
		t.Errorf("嵌套 map 的数字应为 int,实际 %T", nested["n"])
	}
	// 真正的小数保持 float64 —— 归一不能误伤浮点语义
	if r, ok := got.StateData["ratio"].(float64); !ok || r != 1.5 {
		t.Errorf("1.5 应保持 float64,实际 %T(%v)", got.StateData["ratio"], got.StateData["ratio"])
	}
	if got.StateData["text"] != "hello" {
		t.Errorf("字符串不应被改动: %v", got.StateData["text"])
	}
}

// TestListExecutionsAlsoNormalizes 列表路径(Executions 页/Dashboard 走它)
// 必须同样归一,不能只有 GetExecution 生效。
func TestListExecutionsAlsoNormalizes(t *testing.T) {
	db := openTestDB(t)
	exec := &model.Execution{
		ID: "exec-2", WorkflowID: "wf", State: model.ExecutionCompleted,
		Task: "t", CreatedAt: "now", NodeStates: map[string]string{},
		StateData: map[string]any{"_loop_count": 5},
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("save: %v", err)
	}
	list, err := db.ListExecutions("", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	if _, ok := list[0].StateData["_loop_count"].(int); !ok {
		t.Errorf("列表路径也应归一为 int,实际 %T", list[0].StateData["_loop_count"])
	}
}
