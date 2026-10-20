package persistence

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// seedExec 落一条执行记录(状态可控)。
func seedExec(t *testing.T, db *DB, id string, state model.ExecutionState) *model.Execution {
	t.Helper()
	exec := &model.Execution{
		ID: "exec-" + id, WorkflowID: "wf-demo", WorkflowVersion: 1, WorkflowName: "Demo",
		State: state, Task: "t", CreatedAt: nowStr(),
		Variables: map[string]any{}, NodeStates: map[string]string{},
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return exec
}

func execState(t *testing.T, db *DB, id string) model.ExecutionState {
	t.Helper()
	e, err := db.GetExecution(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return e.State
}

// ---- 约定 #4:执行状态转换必须走 CAS,整行 upsert 不得绕过它 ----
//
// **本组用例的定位(别误读)**:它们直接调用 SaveExecution(导出 API),
// 验证的是"ON CONFLICT 分支不改走终态"这条 **API 契约**。
// 当前**生产路径不经过这个分支** —— 唯一的生产调用点
// workflow/runtime.insertExecution 只做首次写入(行尚不存在,插入前先查重、
// 撞 ID 就换 ID)。所以这些用例证明的是"SaveExecution 的语义",
// 而不是"运行期状态机有保护"。运行期状态机的防线是约定 #4 的
// CasExecutionState,由 workflow/runtime 自己的用例覆盖。
//
// 保留这些用例的意义:SaveExecution 是导出 API,契约不能只靠注释声明 ——
// 改坏守卫时必须有测试变红(而不是靠"反正生产不走这里"放行)。

// TestSaveExecutionDoesNotRevertCastedNegativeTerminal 锁定 CAS 落定的
// 负面终态(FAILED/CANCELLED)。
//
// 背景:SaveExecution 是整行 upsert,若冲突分支无条件覆盖 state,
// 一个拿着过期副本的写者就能把刚由 CAS 落定的状态改回去。"失败/取消"的
// 裁决被改回"运行中/已完成"尤其致命:用户明明看到 FAILED,执行却复活成
// 成功(约定 #14 的"该失败却成功"同族问题)。
//
// 期望:FAILED/CANCELLED 只能由 CasExecutionState 推翻(显式重试),整行
// upsert 不得改走它们。
func TestSaveExecutionDoesNotRevertCastedNegativeTerminal(t *testing.T) {
	for _, tc := range []struct {
		name string
		to   model.ExecutionState
	}{
		{"failed", model.ExecutionFailed},
		{"cancelled", model.ExecutionCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			seedExec(t, db, tc.name, model.ExecutionRunning)

			ok, err := db.CasExecutionState("exec-"+tc.name,
				[]model.ExecutionState{model.ExecutionRunning}, tc.to)
			if err != nil || !ok {
				t.Fatalf("cas: ok=%v err=%v", ok, err)
			}

			// 过期副本(状态仍是 CAS 之前的 RUNNING)随后落库。
			stale := &model.Execution{
				ID: "exec-" + tc.name, WorkflowID: "wf-demo", State: model.ExecutionRunning,
				CreatedAt: nowStr(), Variables: map[string]any{}, NodeStates: map[string]string{},
			}
			if err := db.SaveExecution(stale); err != nil {
				t.Fatalf("save stale: %v", err)
			}

			if got := execState(t, db, "exec-"+tc.name); got != tc.to {
				t.Fatalf("CAS 落定的 %s 被过期副本改回: %s", tc.to, got)
			}
			// 负面终态不得再出现在"未终态"列表里(否则崩溃恢复会改写它)。
			running, err := db.ListRunningExecutions()
			if err != nil {
				t.Fatalf("list running: %v", err)
			}
			for _, r := range running {
				if r.ID == "exec-"+tc.name {
					t.Fatal("负面终态出现在未终态列表里")
				}
			}
		})
	}
}

// TestSaveExecutionRejectsIllegalStateJump 锁定"负面终态不得被改走"。
//
// 事实澄清:守卫(execGuardedStates)是持久层自己的一份**独立清单**,
// 并**不**由 model.CanTransitionExecution 推导 —— 守卫比状态机更严:
// 状态机允许 FAILED→RUNNING(用户显式重试),守卫连这条也挡(整行 upsert
// 不得替用户做重试裁决)。
//
// 两份清单的关系由 TestExecStateGuardAgreesWithStateMachine 钉死
// (persistence/exec_state_guard_test.go):状态机新增终态状态时,守卫必须
// 同步跟上,否则那里会红。
func TestSaveExecutionRejectsIllegalStateJump(t *testing.T) {
	db := openTestDB(t)
	seedExec(t, db, "jump", model.ExecutionFailed)

	stale := &model.Execution{
		ID: "exec-jump", WorkflowID: "wf-demo", State: model.ExecutionCreated,
		CreatedAt: nowStr(), Variables: map[string]any{}, NodeStates: map[string]string{},
	}
	if err := db.SaveExecution(stale); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := execState(t, db, "exec-jump"); got != model.ExecutionFailed {
		t.Fatalf("负面终态被过期副本改写: %s(want FAILED)", got)
	}
}

// TestSaveExecutionAppliesLegalTransition 回归:守卫不得拦掉合法转换
// (RUNNING→COMPLETED 是引擎收尾的正常路径)。
func TestSaveExecutionAppliesLegalTransition(t *testing.T) {
	db := openTestDB(t)
	seedExec(t, db, "legal", model.ExecutionRunning)

	done := &model.Execution{
		ID: "exec-legal", WorkflowID: "wf-demo", State: model.ExecutionCompleted,
		CreatedAt: nowStr(), Variables: map[string]any{}, NodeStates: map[string]string{},
	}
	if err := db.SaveExecution(done); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := execState(t, db, "exec-legal"); got != model.ExecutionCompleted {
		t.Fatalf("合法转换未生效: %s(want COMPLETED)", got)
	}
}

// TestSaveExecutionAppliesWaitingUserTransition 回归:RUNNING→WAITING_USER
// 是 HITL 暂停的正常路径(守卫若误伤,人工审批将永远等不到)。
func TestSaveExecutionAppliesWaitingUserTransition(t *testing.T) {
	db := openTestDB(t)
	seedExec(t, db, "wait", model.ExecutionRunning)

	paused := &model.Execution{
		ID: "exec-wait", WorkflowID: "wf-demo", State: model.ExecutionWaitingUser,
		CreatedAt: nowStr(), Variables: map[string]any{}, NodeStates: map[string]string{},
	}
	if err := db.SaveExecution(paused); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := execState(t, db, "exec-wait"); got != model.ExecutionWaitingUser {
		t.Fatalf("RUNNING→WAITING_USER 未生效: %s", got)
	}
}

// TestCasExecutionStateSingleWinner 锁定 CAS 的原子性:并发 CAS 同一执行,
// 恰好一路成功(双击"批准"只允许一路重放)。
func TestCasExecutionStateSingleWinner(t *testing.T) {
	db := openTestDB(t)
	seedExec(t, db, "race", model.ExecutionWaitingUser)

	const n = 8
	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := db.CasExecutionState("exec-race",
				[]model.ExecutionState{model.ExecutionWaitingUser}, model.ExecutionRunning)
			if err != nil {
				t.Errorf("cas: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&wins); got != 1 {
		t.Fatalf("CAS 赢家 = %d, want 1", got)
	}
	if got := execState(t, db, "exec-race"); got != model.ExecutionRunning {
		t.Fatalf("最终状态 = %s, want RUNNING", got)
	}
}

// ---- 迁移:升级失败不得静默 ----

// TestMigrateColumnsSurfacesRealFailure 锁定"迁移失败必须上抛"。
// 背景:migrate() 里 ALTER TABLE 的错误此前被 `_, _ =` 吞掉 —— 升级失败的
// 应用带着半残 schema 启动,后续查询报出的是莫名其妙的"no such column",
// 真正的原因(迁移失败)被彻底掩盖。
func TestMigrateColumnsSurfacesRealFailure(t *testing.T) {
	db := openTestDB(t)
	// 把 executions 换成视图:ALTER TABLE 对视图必失败,且不是"列已存在"。
	if _, err := db.sql.Exec(`DROP TABLE executions`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.sql.Exec(`CREATE VIEW executions AS SELECT 'x' AS id`); err != nil {
		t.Fatalf("create view: %v", err)
	}
	err := db.migrateColumns()
	if err == nil {
		t.Fatal("迁移失败必须上抛(静默忽略 = 应用带半残 schema 启动)")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("错误应指明是迁移失败: %v", err)
	}
}

// TestMigrateIsIdempotent 回归:重复打开同一个库文件时 ALTER 会命中
// "列已存在",这一路径必须被容忍(否则升级路径直接打不开旧库)。
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("首次打开: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("二次打开(列已存在必须容忍): %v", err)
	}
	t.Cleanup(func() { db2.Close() })
}

// ---- 错误语义:哨兵错误不得被包装到失效 ----

// TestWrappedErrorKeepsCause 锁定"包装后仍能 errors.Is 命中根因"。
// 背景:wrap() 把底层错误换成新的结构化错误,原始错误链彻底丢失 ——
// 调用方无法再区分"行不存在(sql.ErrNoRows)"与"磁盘故障",也无法识别
// context.Canceled。
func TestWrappedErrorKeepsCause(t *testing.T) {
	db := openTestDB(t)
	injected := errors.New("injected: disk I/O error")
	db.SetQueryFault(func(stmt string) error {
		if strings.Contains(stmt, "settings") {
			return injected
		}
		return nil
	})
	t.Cleanup(func() { db.SetQueryFault(nil) })

	err := db.SaveSetting("k", "v")
	if err == nil {
		t.Fatal("写入失败必须报错")
	}
	if !errors.Is(err, injected) {
		t.Errorf("错误链丢失根因,errors.Is 失效: %v", err)
	}
	if !model.IsKind(err, model.KindPersistenceError) {
		t.Errorf("仍应是结构化持久化错误(API 按 kind/code 映射状态码): %v", err)
	}
}

// TestWrappedErrorMessageUnchanged 回归:包装后的错误文案与此前完全一致
// (API 响应体文案不变),只是多了错误链。
func TestWrappedErrorMessageUnchanged(t *testing.T) {
	err := wrap(errors.New("boom"))
	if err.Error() != "PersistenceError(DB_ERROR): boom" {
		t.Errorf("错误文案变了: %q", err.Error())
	}
}

// ---- 约定 #2:JSON 往返后数值类型不得退化 ----

// TestVariablesKeepIntTypeAfterRoundTrip 锁定 variables 的类型稳定性。
// 背景:state_json 已在解码入口归一(见 model.NormalizeStateJSON),
// 但 variables_json 没有 —— int 变量经一次 SQLite 往返就变成 float64,
// 同一字段的类型随生命周期变化(约定 #2)。
func TestVariablesKeepIntTypeAfterRoundTrip(t *testing.T) {
	db := openTestDB(t)
	exec := &model.Execution{
		ID: "exec-var", WorkflowID: "wf-demo", State: model.ExecutionRunning,
		Task: "t", CreatedAt: nowStr(), NodeStates: map[string]string{},
		Variables: map[string]any{"count": 3, "ratio": 1.5, "nested": map[string]any{"n": 7}},
	}
	if err := db.SaveExecution(exec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := db.GetExecution("exec-var")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := got.Variables["count"].(int); !ok {
		t.Errorf("count 应为 int,实际 %T(%v)", got.Variables["count"], got.Variables["count"])
	}
	if r, ok := got.Variables["ratio"].(float64); !ok || r != 1.5 {
		t.Errorf("1.5 应保持 float64,实际 %T(%v)", got.Variables["ratio"], got.Variables["ratio"])
	}
	nested, _ := got.Variables["nested"].(map[string]any)
	if _, ok := nested["n"].(int); !ok {
		t.Errorf("嵌套 map 的数字应为 int,实际 %T", nested["n"])
	}
}

// TestListExecutionsAlsoNormalizesVariables 列表路径同样必须归一。
func TestListExecutionsAlsoNormalizesVariables(t *testing.T) {
	db := openTestDB(t)
	exec := &model.Execution{
		ID: "exec-var2", WorkflowID: "wf-demo", State: model.ExecutionCompleted,
		Task: "t", CreatedAt: nowStr(), NodeStates: map[string]string{},
		Variables: map[string]any{"count": 5},
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
	if _, ok := list[0].Variables["count"].(int); !ok {
		t.Errorf("列表路径也应归一为 int,实际 %T", list[0].Variables["count"])
	}
}

// TestWorkflowVariablesKeepIntTypeAfterRoundTrip 锁定工作流侧变量的类型稳定性。
// DSL 变量会被条件表达式(variables.xxx)与模板消费,类型随生命周期变化
// 会让同一份 DSL 在保存前后表现不同。
func TestWorkflowVariablesKeepIntTypeAfterRoundTrip(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()
	wf.Variables = map[string]any{"count": 3, "ratio": 1.5}
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := db.GetWorkflow("wf-demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := got.Variables["count"].(int); !ok {
		t.Errorf("count 应为 int,实际 %T(%v)", got.Variables["count"], got.Variables["count"])
	}
	if r, ok := got.Variables["ratio"].(float64); !ok || r != 1.5 {
		t.Errorf("1.5 应保持 float64,实际 %T(%v)", got.Variables["ratio"], got.Variables["ratio"])
	}
}

// TestSaveExecutionDoesNotRevertCompleted 锁定"已完成的执行不得被整行
// upsert 改回运行中"。
//
// 背景:终态守卫此前只含 FAILED/CANCELLED(负面终态)。COMPLETED 不在守卫里,
// 于是任何"用已存在的执行 ID 再插一次"的调用(直接调 SaveExecution 的代码)
// 可以把一条已完成的执行改回 RUNNING —— 用户在列表里看到"运行中",
// 实际上它早就跑完了(约定 #14 的"该失败却成功"同族问题:状态与事实不符)。
//
// COMPLETED 纳入守卫后:离开它只能走 CasExecutionState(与 FAILED/CANCELLED
// 同一套裁决机制)。
//
// 注意:这是 SaveExecution 这个导出 API 的契约(生产路径当前不经过该分支,
// 见本文件开头的说明),不是"运行期有保护"的证据。
//
// 已知边界:守卫挡不住"往合法后继方向"的过期写(例如库中 PAUSED、过期副本
// 写 RUNNING —— PAUSED→RUNNING 本身合法)。彻底堵住需要调用方携带
// "我基于哪个版本"的令牌,不在持久化层可解范围内(见 execStateGuardSQL 注释)。
func TestSaveExecutionDoesNotRevertCompleted(t *testing.T) {
	db := openTestDB(t)
	seedExec(t, db, "completed", model.ExecutionRunning)

	ok, err := db.CasExecutionState("exec-completed",
		[]model.ExecutionState{model.ExecutionRunning}, model.ExecutionCompleted)
	if err != nil || !ok {
		t.Fatalf("cas: ok=%v err=%v", ok, err)
	}

	// 过期副本(状态仍是 CAS 之前的 RUNNING)随后落库。
	stale := &model.Execution{
		ID: "exec-completed", WorkflowID: "wf-demo", State: model.ExecutionRunning,
		CreatedAt: nowStr(), Variables: map[string]any{}, NodeStates: map[string]string{},
	}
	if err := db.SaveExecution(stale); err != nil {
		t.Fatalf("save stale: %v", err)
	}
	if got := execState(t, db, "exec-completed"); got != model.ExecutionCompleted {
		t.Fatalf("已完成的执行被过期副本改回: %s(want COMPLETED)", got)
	}
}

// TestSaveExecutionKeepsCompletedOnSameState 回归:守卫不得拦掉"状态不变"
// 的整行 upsert(COMPLETED → COMPLETED 是正常的元信息落库路径)。
func TestSaveExecutionKeepsCompletedOnSameState(t *testing.T) {
	db := openTestDB(t)
	seedExec(t, db, "same", model.ExecutionRunning)
	ok, err := db.CasExecutionState("exec-same",
		[]model.ExecutionState{model.ExecutionRunning}, model.ExecutionCompleted)
	if err != nil || !ok {
		t.Fatalf("cas: ok=%v err=%v", ok, err)
	}
	same := &model.Execution{
		ID: "exec-same", WorkflowID: "wf-demo", State: model.ExecutionCompleted,
		CurrentNodeID: "n2", CreatedAt: nowStr(), Variables: map[string]any{}, NodeStates: map[string]string{},
	}
	if err := db.SaveExecution(same); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := execState(t, db, "exec-same"); got != model.ExecutionCompleted {
		t.Fatalf("同状态写入后状态变了: %s(want COMPLETED)", got)
	}
	e, err := db.GetExecution("exec-same")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 同状态写入时,元信息列必须正常更新(守卫只挡状态,不是挡整行)。
	if e.CurrentNodeID != "n2" {
		t.Errorf("同状态写入的元信息列应正常更新: current_node_id = %q, want n2", e.CurrentNodeID)
	}
}
