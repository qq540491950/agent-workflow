package persistence

import (
	"path/filepath"
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
