package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// SaveWorkflow 插入或更新 Workflow 定义(bump 版本并写版本快照)。
// bumpVersion=true 时版本+1 并记录版本历史;false 时仅更新元数据(不产生新版本)。
//
// 版本号以库中现值为准,不采信入参 wf.Version:REST 的请求体可任意伪造
// version(POST /api/workflows 直接反序列化客户端 JSON),伪造 99 会写出
// v100 并留下伪造的版本快照,重复提交还会因 workflow_versions 的唯一
// 约束被 INSERT OR REPLACE 静默覆盖,版本历史失真且无任何告警。
//
// **序列化失败即报错**:落库的每一列都由 marshalJSON 产出,任何一个值
// 不可 JSON 化(NaN/Inf/循环引用)都会让**整次保存失败**,绝不写出
// 半截或空串(见 marshalJSON 的注释)。
func (d *DB) SaveWorkflow(wf *model.Workflow, bumpVersion bool) error {
	now := nowStr()
	wf.UpdatedAt = now

	variables, err := marshalJSON(orEmpty(wf.Variables))
	if err != nil {
		return wrap(err)
	}
	nodes, err := marshalJSON(wf.Nodes)
	if err != nil {
		return wrap(err)
	}
	edges, err := marshalJSON(wf.Edges)
	if err != nil {
		return wrap(err)
	}
	settings, err := marshalJSON(wf.Settings)
	if err != nil {
		return wrap(err)
	}
	permData, err := marshalJSON(orEmpty(wf.Permission))
	if err != nil {
		return wrap(err)
	}
	enabled := 0
	if wf.Enabled {
		enabled = 1
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()

	// 当前版本号(不存在则为 0)。查询错误必须上抛:吞掉后 existing 保持 0
	// 会对已存在的工作流走 INSERT 分支,真实根因被一句主键冲突掩盖。
	var current int
	err = tx.QueryRow(`SELECT COALESCE(MAX(version),0) FROM workflow_versions WHERE workflow_id=?`, wf.ID).Scan(&current)
	if err != nil {
		return wrap(err)
	}
	var existing int
	// 存在性查询失败必须上抛(见 TestSaveWorkflowSurfacesExistenceQueryError):
	// 吞掉后 existing 保持 0,会对**已存在**的工作流走 INSERT 分支,
	// 用户看到的是一句语焉不详的主键冲突,真实根因被完全掩盖。
	if err := d.checkFault("SELECT COUNT(1) FROM workflows WHERE id=?"); err != nil {
		return wrap(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(1) FROM workflows WHERE id=?`, wf.ID).Scan(&existing); err != nil {
		return wrap(err)
	}
	if existing == 0 {
		wf.Version = 1
		wf.CreatedAt = now
		_, err = tx.Exec(`INSERT INTO workflows
			(id,name,description,version,enabled,variables_json,nodes_json,edges_json,settings_json,permission_json,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			wf.ID, wf.Name, wf.Description, wf.Version, enabled,
			string(variables), string(nodes), string(edges), string(settings), string(permData), now, now)
	} else {
		if bumpVersion {
			current++
		}
		if current > 0 {
			// 版本号以库中现值为准:不采信入参(可被 REST 请求体伪造),
			// 也不允许在非 bump 路径被改小(否则快照表撞唯一约束)。
			wf.Version = current
		}
		_, err = tx.Exec(`UPDATE workflows SET name=?,description=?,version=?,enabled=?,variables_json=?,nodes_json=?,edges_json=?,settings_json=?,permission_json=?,updated_at=? WHERE id=?`,
			wf.Name, wf.Description, wf.Version, enabled,
			string(variables), string(nodes), string(edges), string(settings), string(permData), now, wf.ID)
	}
	if err != nil {
		return wrap(err)
	}

	// 版本快照:每次产生新版本时写入 DSL 文本与完整快照。
	// 用普通 INSERT(唯一冲突即错误)而非 INSERT OR REPLACE:同一版本号
	// 被写两次说明版本推进逻辑出了问题,静默覆盖会丢历史。
	if bumpVersion || existing == 0 {
		doc := dsl.FromModel(wf)
		dslText, _ := doc.EncodeYAML()
		snapshot, err := marshalJSON(wf)
		if err != nil {
			return wrap(err)
		}
		_, err = tx.Exec(`INSERT INTO workflow_versions (workflow_id,version,dsl_text,snapshot_json,created_at) VALUES (?,?,?,?,?)`,
			wf.ID, wf.Version, string(dslText), string(snapshot), now)
		if err != nil {
			return wrap(err)
		}
	}
	return tx.Commit()
}

// GetWorkflow 按 ID 读取 Workflow。
func (d *DB) GetWorkflow(id string) (*model.Workflow, error) {
	row := d.sql.QueryRow(`SELECT id,name,description,version,enabled,variables_json,nodes_json,edges_json,settings_json,permission_json,created_at,updated_at FROM workflows WHERE id=?`, id)
	wf, err := scanWorkflow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.NewError(model.KindValidationError, "NOT_FOUND", fmt.Sprintf("工作流 %q 不存在", id))
		}
		return nil, wrap(err)
	}
	return wf, nil
}

// ListWorkflows 返回全部 Workflow。
func (d *DB) ListWorkflows() ([]*model.Workflow, error) {
	rows, err := d.sql.Query(`SELECT id,name,description,version,enabled,variables_json,nodes_json,edges_json,settings_json,permission_json,created_at,updated_at FROM workflows ORDER BY updated_at DESC`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []*model.Workflow{}
	for rows.Next() {
		wf, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

// DeleteWorkflow 删除 Workflow(执行记录保留,审计需要)。
func (d *DB) DeleteWorkflow(id string) error {
	_, err := d.sql.Exec(`DELETE FROM workflows WHERE id=?`, id)
	return wrap(err)
}

// GetWorkflowVersion 读取指定版本的快照(Execution 绑定版本使用)。
func (d *DB) GetWorkflowVersion(workflowID string, version int) (*model.Workflow, error) {
	row := d.sql.QueryRow(`SELECT snapshot_json FROM workflow_versions WHERE workflow_id=? AND version=?`, workflowID, version)
	var snap string
	if err := row.Scan(&snap); err != nil {
		// 版本不存在是正常的"资源缺失",应映射为 NOT_FOUND(404)而非 DB_ERROR
		return nil, wrapNotFound(err, "工作流版本", fmt.Sprintf("%s@%d", workflowID, version))
	}
	wf := &model.Workflow{}
	if err := json.Unmarshal([]byte(snap), wf); err != nil {
		return nil, wrap(err)
	}
	return wf, nil
}

// GetWorkflowVersionDSL 读取某版本的**原始 DSL 文本**。
//
// 为什么要有这个读点:dsl_text 是版本快照里唯一保存"当初那份 YAML 原文"
// 的列 —— snapshot_json 是**模型快照**,再编码成 YAML 得到的是重新
// 序列化的结果(键序、缩进、字段取舍都可能与原文不同)。版本页要展示
// "当时的 DSL",就得读这一列,而不是拿快照重新编码。
//
// 老库同样可读:这一列一直都在(本轮只是给它补上读点,没有删)。
func (d *DB) GetWorkflowVersionDSL(workflowID string, version int) (string, error) {
	var text string
	err := d.sql.QueryRow(`SELECT dsl_text FROM workflow_versions WHERE workflow_id=? AND version=?`,
		workflowID, version).Scan(&text)
	if err != nil {
		// 版本不存在是"资源缺失" → NOT_FOUND(404),与 GetWorkflowVersion 同口径。
		return "", wrapNotFound(err, "工作流版本", fmt.Sprintf("%s@%d", workflowID, version))
	}
	return text, nil
}

// WorkflowVersionMeta 是版本快照的元信息(版本号 + 落库时间)。
type WorkflowVersionMeta struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
}

// ListWorkflowVersionMeta 返回某 Workflow 的版本元信息(版本号降序)。
//
// created_at 此前只写不读 —— 现在有了读点:版本列表可以显示"这个版本
// 是什么时候存的"(与 ListWorkflowVersions 的版本号列表同口径,多一个
// 时间字段)。
func (d *DB) ListWorkflowVersionMeta(workflowID string) ([]WorkflowVersionMeta, error) {
	rows, err := d.sql.Query(`SELECT version, created_at FROM workflow_versions WHERE workflow_id=? ORDER BY version DESC`, workflowID)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []WorkflowVersionMeta{}
	for rows.Next() {
		var m WorkflowVersionMeta
		if err := rows.Scan(&m.Version, &m.CreatedAt); err != nil {
			return nil, wrap(err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListWorkflowVersions 返回某 Workflow 的版本号列表(降序)。
func (d *DB) ListWorkflowVersions(workflowID string) ([]int, error) {
	rows, err := d.sql.Query(`SELECT version FROM workflow_versions WHERE workflow_id=? ORDER BY version DESC`, workflowID)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	// 非 nil 初始化:Go 把 nil slice 编码成 JSON null,前端 asArray 能兜住,
	// 但 REST 契约要求列表端点一律返回数组(见 AGENTS.md 约定 #7)。
	out := []int{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, wrap(err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

// decodeJSONColumn 解码一个 JSON 列。
//
// 空串 = 空值(某些列的默认值就是空串,例如 executions.snapshot_json
// DEFAULT ”),不算损坏。
//
// **空串特别要当"空"而不是"坏"**:上一轮的 bug(序列化失败被吞)往老库里
// 写进了空串的 variables_json / snapshot_json。读出口若把它判成损坏,
// 那些老工作流会**直接打不开**(向后兼容是硬要求)—— 所以这里宽容,
// 由**写**出口保证不再产生空串(见 marshalJSON)。
//
// 非法 JSON → 返回错误,**不**静默退化成零值。
//
// 损坏的后果分两类,决定"失败"还是"降级":
//   - 工作流**定义**列(permission_json / nodes_json / edges_json /
//     variables_json / settings_json):损坏 = 加载出来的**不是用户保存的
//     那个工作流**。其中 permission_json 损坏 = 权限收紧**静默失效**
//     (安全相关:工作流以为自己禁了写权限,实际按全局的宽松策略跑)。
//     → 显式失败(fail closed),由调用方决定。
//   - 执行**运行期状态**列:历史记录,坏了不该让整个列表页打不开
//     → 降级 + WARN(调用点决定)。
func decodeJSONColumn(column, raw string, out any) error {
	if strings.TrimSpace(raw) == "" {
		return nil // 空值(不是损坏)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("%s 列的 JSON 损坏: %w", column, err)
	}
	return nil
}

func scanWorkflow(r rowScanner) (*model.Workflow, error) {
	var wf model.Workflow
	var variables, nodes, edges, settings, permData string
	var enabled int
	if err := r.Scan(&wf.ID, &wf.Name, &wf.Description, &wf.Version, &enabled,
		&variables, &nodes, &edges, &settings, &permData, &wf.CreatedAt, &wf.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err // 由调用方转换为 NOT_FOUND
		}
		return nil, wrap(err)
	}
	wf.Enabled = enabled == 1
	// 定义列损坏 → **显式失败**(此前一律 `_ = json.Unmarshal(...)`,
	// 错误被吞,列静默变成空值):
	//   - permission_json 损坏 = 权限收紧静默失效(安全相关);
	//   - nodes/edges 损坏 = 跑的是另一个工作流(约定 14)。
	// 静默成功比崩溃更危险 —— 宁可让用户看到"这条数据坏了",也不要
	// 让他以为自己的工作流按原样跑。
	for _, col := range []struct {
		name string
		raw  string
		out  any
	}{
		{"variables_json", variables, &wf.Variables},
		{"permission_json", permData, &wf.Permission},
		{"nodes_json", nodes, &wf.Nodes},
		{"edges_json", edges, &wf.Edges},
		{"settings_json", settings, &wf.Settings},
	} {
		if err := decodeJSONColumn(col.name, col.raw, col.out); err != nil {
			return nil, wrap(err)
		}
	}
	// 变量里的整数值在 JSON 往返后退化成 float64(约定 #2):与执行侧
	// 同一套归一,保证工作流保存/读回后类型不变。
	wf.Variables = model.NormalizeStateJSON(wf.Variables)
	if wf.Variables == nil {
		wf.Variables = map[string]any{}
	}
	return &wf, nil
}

// ---- Execution ----

// TouchExecutionNode 只更新执行的"当前节点"定位信息(单列 UPDATE,
// 不触碰其余字段)。用于运行期的轻量检查点:崩溃后 RecoverPending 标
// FAILED 时,RetryNode 靠 current_node_id 定位失败节点 —— 若运行中从不
// 落库,该值会停留在 Start 时的空串,重试恒返回 NO_FAILED_NODE。
func (d *DB) TouchExecutionNode(executionID, nodeID string) error {
	_, err := d.sql.Exec(`UPDATE executions SET current_node_id=? WHERE id=?`, nodeID, executionID)
	return wrap(err)
}

// ExecInsertColumns 是 executions 表的插入列(顺序与 VALUES 占位符一致)。
//
// 显式列名的唯一来源:workflow/runtime 直接写 executions 表(状态列走 CAS),
// 两边的列集合必须一致 —— 持久层改列名而运行期没改,运行期就会写到
// 不存在的列上(整条 SQL 失败,执行记录落不了库)。
// 一致性由 workflow/runtime/persist_contract_test.go 核对。
const ExecInsertColumns = `id,workflow_id,workflow_version,workflow_name,state,task,variables_json,` +
	`current_node_id,iterations_json,created_at,started_at,finished_at,error,node_states_json,` +
	`snapshot_json,state_json`

// ExecUpsertMetaColumns 是 upsert 冲突时更新的**非状态**列。
//
// 不含 state:state 走终态守卫(execStateGuard),裁决权在 CAS 手里。
// 运行期的 execMetaColumns 必须与这份列表逐列一致(同上)。
const ExecUpsertMetaColumns = `current_node_id,iterations_json,started_at,finished_at,error,` +
	`node_states_json,snapshot_json,state_json`

// execColumns 把列清单切成切片(去空白)。
func execColumns(list string) []string {
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if c := strings.TrimSpace(p); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// execUpsertSetClause 生成 "col=excluded.col" 形式的赋值列表。
func execUpsertSetClause() string {
	cols := execColumns(ExecUpsertMetaColumns)
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, c+"=excluded."+c)
	}
	return strings.Join(parts, ", ")
}

// SaveExecution 插入或更新执行状态(实时保存)。
// 单条原子 upsert:不可变字段(workflow 绑定/task/变量/created_at)只在
// 插入时写入,更新时不覆盖。
//
// state 列走**终态守卫**(见 execStateGuardSQL):当 INSERT 撞上一个已存在
// 的执行 ID 时,ON CONFLICT 分支不把 FAILED/CANCELLED/COMPLETED 改走 ——
// 这三个终态只能经 CasExecutionState 离开(显式重试/取消裁决)。
//
// **生产路径当前不依赖这个守卫**(事实,勿夸大):全仓唯一的生产调用点是
// workflow/runtime 的 insertExecution(首次写入,此刻行尚不存在),它插入前
// 先查重、撞 ID 就换一个新 ID,因此 ON CONFLICT 分支在生产中不会命中。
// 运行期的状态迁移一律走 CAS(casStateAndSave),根本不经过本方法。
// 守卫保留在此的理由是 API 契约而非"生产有保护":SaveExecution 是导出
// API,其冲突分支必须有一个确定的、"不推翻既有裁决"的语义,而不是
// "最后写入者赢"。详见 execStateGuardSQL 的注释。
func (d *DB) SaveExecution(e *model.Execution) error {
	variables, err := marshalJSON(orEmpty(e.Variables))
	if err != nil {
		return wrap(err)
	}
	iterations, err := anyMapInt(e.Iterations)
	if err != nil {
		return wrap(err)
	}
	nodeStates, err := anyMapStr(e.NodeStates)
	if err != nil {
		return wrap(err)
	}
	stateData, err := anyMapAny(e.StateData)
	if err != nil {
		return wrap(err)
	}
	var snapshot []byte
	if e.Snapshot != nil {
		snapshot, err = marshalJSON(e.Snapshot)
		if err != nil {
			return wrap(err)
		}
	}
	// 占位符由列清单推导:列数与占位符数不可能再各自漂移。
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(execColumns(ExecInsertColumns))), ",")
	_, err = d.sql.Exec(`INSERT INTO executions (`+ExecInsertColumns+`)
		VALUES (`+placeholders+`)
		ON CONFLICT(id) DO UPDATE SET
			state=`+execStateGuardSQL+`, `+execUpsertSetClause(),
		append([]any{
			e.ID, e.WorkflowID, e.WorkflowVersion, e.WorkflowName, string(e.State), e.Task,
			string(variables), e.CurrentNodeID, string(iterations), e.CreatedAt,
			e.StartedAt, e.FinishedAt, e.Error, string(nodeStates), string(snapshot), string(stateData),
		}, execStateGuardArgs...)...)
	return wrap(err)
}

// execStateGuardSQL / execStateGuardArgs 是 state 列的守卫表达式(参数化)。
//
// 语义:FAILED / CANCELLED / COMPLETED 是**终态** —— 一旦落定,整行 upsert
// (SaveExecution 的 ON CONFLICT 分支)不得把它们改走;否则一份过期副本能
// 把已失败的执行改回"运行中/已完成"(约定 #14 的"该失败却成功"同族问题)。
//
// **作用范围(以下是事实,不是"生产有保护"的承诺)**:
//
//  1. 只作用于 SaveExecution 的 ON CONFLICT(id) 分支 —— 即"拿一个已存在
//     的执行 ID 再插一次"时。INSERT 命中新行时该表达式不参与求值。
//  2. 生产路径当前**不会**命中该分支:唯一的生产调用点是
//     workflow/runtime.insertExecution(首次写入,行尚不存在),它在插入前
//     先查重、撞 ID 就换 ID;运行期其余状态迁移一律走 CAS
//     (casStateAndSave),不经过 SaveExecution。
//  3. 因此本守卫是**导出 API 的契约**,不是运行期状态机的防线。
//     运行期的防线是约定 #4 的 CasExecutionState(由 workflow/runtime
//     自己的用例覆盖)。不要把本守卫当成"过期写进不来"的保证。
//
// 为什么仍然保留:SaveExecution 是导出 API,冲突分支必须有一个确定的、
// 非破坏性的语义。若改成"最后写入者赢",将来任何新调用点都可能用一份
// 过期副本推翻已落定的终态裁决,而且没有任何提示。
//
// 已知边界(未闭合,需调用方配合):
//   - 守卫挡不住"往合法后继方向"的过期写(例如库中 WAITING_USER、过期
//     副本写 RUNNING —— WAITING_USER→RUNNING 本身合法)。彻底堵住需要调用方携带
//     "我基于哪个版本"的令牌(CAS 返回 claim token、保存时校验),
//     属于调用方改动,不在持久化层可解范围内。
var execStateGuardSQL, execStateGuardArgs = buildExecStateGuard()

// execGuardedStates 是受保护的终态清单。
//
// 这是一份**独立维护**的清单,不是从 model.CanTransitionExecution 推导的
// (守卫比状态机更严:连 FAILED→RUNNING 这条"显式重试"也挡,整行 upsert
// 不得替用户做重试裁决)。它与状态机的关系由
// TestExecStateGuardAgreesWithStateMachine 钉死 —— 状态机新增终态状态时
// 这里必须同步跟上。
var execGuardedStates = []model.ExecutionState{
	model.ExecutionFailed,
	model.ExecutionCancelled,
	model.ExecutionCompleted,
}

func buildExecStateGuard() (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(execGuardedStates)), ",")
	args := make([]any, 0, len(execGuardedStates))
	for _, s := range execGuardedStates {
		args = append(args, string(s))
	}
	sql := "CASE WHEN executions.state IN (" + placeholders + ") AND excluded.state<>executions.state" +
		" THEN executions.state ELSE excluded.state END"
	return sql, args
}

// GetExecution 读取执行记录。
func (d *DB) GetExecution(id string) (*model.Execution, error) {
	row := d.sql.QueryRow(`SELECT id,workflow_id,workflow_version,workflow_name,state,task,variables_json,current_node_id,iterations_json,created_at,started_at,finished_at,error,node_states_json,snapshot_json,state_json FROM executions WHERE id=?`, id)
	var e model.Execution
	var variables, iterations, nodeStates, snapshot, stateData string
	if err := row.Scan(&e.ID, &e.WorkflowID, &e.WorkflowVersion, &e.WorkflowName, &e.State, &e.Task,
		&variables, &e.CurrentNodeID, &iterations, &e.CreatedAt, &e.StartedAt, &e.FinishedAt, &e.Error, &nodeStates, &snapshot, &stateData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.NewError(model.KindValidationError, "NOT_FOUND", fmt.Sprintf("执行 %q 不存在", id))
		}
		return nil, wrap(err)
	}
	// 历史遗留的 PAUSED 行归一为 CANCELLED(PAUSED 已从状态机移除,
	// 但老库里仍有这种行)—— 见 NormalizeExecutionState。
	e.State = NormalizeExecutionState(e.State)
	// 读回即归一数值类型:state_json 里的数字经 JSON 解码全是 float64,
	// 而执行期内存里是 int。类型随生命周期变化会让每个读取点都得写
	// 双类型兼容(漏一处就是静默 bug)。在唯一的解码入口钉死一次。
	// 见 model.NormalizeStateJSON。
	//
	// 列损坏 → **显式失败**:读到的执行不是用户跑的那一个(状态为空
	// 时"继续执行"会从头再跑一遍,重复副作用)。历史执行记录坏了一条
	// 就坏这一条,不该被静默当成"空状态"继续用。
	if err := decodeJSONColumn("state_json", stateData, &e.StateData); err != nil {
		return nil, wrap(err)
	}
	e.StateData = model.NormalizeStateJSON(e.StateData)
	// variables_json 同理:变量里的 int 经一次往返也会退化成 float64
	//(约定 #2)。此前只有 state_json 被归一,变量这条路径漏了。
	if err := decodeJSONColumn("variables_json", variables, &e.Variables); err != nil {
		return nil, wrap(err)
	}
	e.Variables = model.NormalizeStateJSON(e.Variables)
	if err := decodeJSONColumn("iterations_json", iterations, &e.Iterations); err != nil {
		return nil, wrap(err)
	}
	if err := decodeJSONColumn("node_states_json", nodeStates, &e.NodeStates); err != nil {
		return nil, wrap(err)
	}
	if snapshot != "" {
		wf := &model.Workflow{}
		if err := json.Unmarshal([]byte(snapshot), wf); err != nil {
			// 快照损坏:运行期已有明确失败路径("执行缺少工作流快照
			// (snapshot 损坏),无法继续"),这里只告警不阻断读取 ——
			// 读到的执行对象本身是好的(状态、节点明细都在)。
			logx.Warn("执行快照 JSON 损坏,已按空快照处理", "execution", id, "error", err.Error())
		} else {
			e.Snapshot = wf
		}
	}
	return &e, nil
}

// ListExecutions 返回执行列表(可按 workflow 过滤,limit<=0 表示全部)。
func (d *DB) ListExecutions(workflowID string, limit int) ([]*model.Execution, error) {
	q := `SELECT id,workflow_id,workflow_version,workflow_name,state,task,variables_json,current_node_id,iterations_json,created_at,started_at,finished_at,error,node_states_json,snapshot_json,state_json FROM executions`
	args := []any{}
	if workflowID != "" {
		q += ` WHERE workflow_id=?`
		args = append(args, workflowID)
	}
	q += ` ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []*model.Execution{}
	for rows.Next() {
		var e model.Execution
		var variables, iterations, nodeStates, snapshot, stateData string
		if err := rows.Scan(&e.ID, &e.WorkflowID, &e.WorkflowVersion, &e.WorkflowName, &e.State, &e.Task,
			&variables, &e.CurrentNodeID, &iterations, &e.CreatedAt, &e.StartedAt, &e.FinishedAt, &e.Error, &nodeStates, &snapshot, &stateData); err != nil {
			return nil, wrap(err)
		}
		// 历史遗留的 PAUSED 行归一为 CANCELLED(与 GetExecution 同口径)。
		e.State = NormalizeExecutionState(e.State)
		// 读回即归一数值类型:state_json 里的数字经 JSON 解码全是 float64,
		// 而执行期内存里是 int。类型随生命周期变化会让每个读取点都得写
		// 双类型兼容(漏一处就是静默 bug)。在唯一的解码入口钉死一次。
		// 见 model.NormalizeStateJSON。
		//
		// 批量列表:列损坏 → **降级 + WARN**,不阻断整张列表
		// (历史执行记录坏了一条,不该让执行列表页整个打不开)。
		// 与 GetExecution 的"显式失败"是刻意的差别:单条读取时损坏的
		// 记录就是**主体**(继续执行会用空状态从头再跑),必须拒绝;
		// 列表里它只是一条历史记录。
		if err := decodeJSONColumn("state_json", stateData, &e.StateData); err != nil {
			logx.Warn("执行记录 state_json 损坏,已按空状态处理",
				"execution", e.ID, "error", err.Error())
		}
		e.StateData = model.NormalizeStateJSON(e.StateData)
		// variables_json 同理(约定 #2):整数值不得在往返后退化成 float64。
		if err := decodeJSONColumn("variables_json", variables, &e.Variables); err != nil {
			logx.Warn("执行记录 variables_json 损坏,已按空变量处理",
				"execution", e.ID, "error", err.Error())
		}
		e.Variables = model.NormalizeStateJSON(e.Variables)
		if err := decodeJSONColumn("iterations_json", iterations, &e.Iterations); err != nil {
			logx.Warn("执行记录 iterations_json 损坏,已按空迭代表处理",
				"execution", e.ID, "error", err.Error())
		}
		if err := decodeJSONColumn("node_states_json", nodeStates, &e.NodeStates); err != nil {
			logx.Warn("执行记录 node_states_json 损坏,已按空节点明细处理",
				"execution", e.ID, "error", err.Error())
		}
		if snapshot != "" {
			wf := &model.Workflow{}
			if err := json.Unmarshal([]byte(snapshot), wf); err != nil {
				logx.Warn("执行记录 snapshot_json 损坏,已按空快照处理",
					"execution", e.ID, "error", err.Error())
			} else {
				e.Snapshot = wf
			}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// legacyPausedState 是历史遗留的执行状态字面量(PAUSED 已从状态机移除,
// 见 workflow/model 的说明)。老库里可能留有 state='PAUSED' 的行。
const legacyPausedState = model.ExecutionState("PAUSED")

// NormalizeExecutionState 归一**历史遗留**的执行状态。
//
// PAUSED 曾是执行状态,但从来不可达:没有任何代码路径会把执行迁移到
// 它。它已从执行状态机里移除,老库里仍可能留有 state='PAUSED' 的行 ——
// 读出来时归一为 CANCELLED:它既不是"还在跑"(不该出现在运行中列表),
// 也不是用户认识的状态(前端没有它的徽章)。
//
// 刻意**不写库**改数据:启动时静默改写用户的历史记录需要理由;
// 读出口归一既够用(用户看到的是自洽的状态),又不碰数据。
func NormalizeExecutionState(s model.ExecutionState) model.ExecutionState {
	if s == legacyPausedState {
		return model.ExecutionCancelled
	}
	return s
}

// ListRunningExecutions 返回未终态的执行(用于崩溃恢复)。
//
// 遗留的 PAUSED 行**不在**这里:它不是"还在跑"的执行(它从来不可达),
// 读出口会把它归一成 CANCELLED(persistence.NormalizeExecutionState)。
func (d *DB) ListRunningExecutions() ([]*model.Execution, error) {
	rows, err := d.sql.Query(`SELECT id FROM executions WHERE state IN ('RUNNING','WAITING_USER','CREATED')`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(err)
		}
		ids = append(ids, id)
	}
	// 必须先关掉 rows 再逐条查询:单连接模式(SetMaxOpenConns(1))下,
	// 持有未关闭的 rows 会占住唯一连接,后面的 GetExecution 将永久等待
	// 自己 → 死锁。(rows.Next 返回 false 时 database/sql 已自动关闭,
	// 这里显式关闭是为了不依赖该隐式行为 —— 将来循环里加个 break 就会踩中。)
	if err := rows.Err(); err != nil {
		return nil, wrap(err)
	}
	if err := rows.Close(); err != nil {
		return nil, wrap(err)
	}
	var out []*model.Execution
	for _, id := range ids {
		e, err := d.GetExecution(id)
		if err != nil {
			// 单条坏记录不能打断整个崩溃恢复循环(其余执行仍要被标记)。
			// 跳过 + 告警:这条执行会一直停在 RUNNING,直到用户处理它。
			logx.Warn("崩溃恢复:跳过读不出的执行记录", "execution", id, "error", err.Error())
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// SaveExecutionNode 插入或更新节点执行明细(原子 upsert;
// execution_id/node_id/node_type/node_name 只在插入时写入)。
func (d *DB) SaveExecutionNode(n *model.ExecutionNode) error {
	_, err := d.sql.Exec(`INSERT INTO execution_nodes
		(id,execution_id,node_id,node_type,node_name,state,attempt,output,result_json,error,started_at,finished_at,duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			state=excluded.state, attempt=excluded.attempt, output=excluded.output,
			result_json=excluded.result_json, error=excluded.error,
			started_at=excluded.started_at, finished_at=excluded.finished_at,
			duration_ms=excluded.duration_ms`,
		n.ID, n.ExecutionID, n.NodeID, string(n.NodeType), n.NodeName, string(n.State), n.Attempt,
		n.Output, n.ResultJSON, n.Error, n.StartedAt, n.FinishedAt, n.DurationMS)
	return wrap(err)
}

// MarkExecutionNodesSkipped 把某次执行里仍处于**非终态**的明细行标成 SKIPPED。
//
// 场景:取消一个等待人工的执行 —— 没有任何协程会再来收尾这些节点,
// 明细行会永远停在"运行中/等待中",而执行已经是"已取消"(状态与事实
// 不符,约定 14 的同族)。
//
// 只更新**已经存在**的行:没跑过的节点本来就没有明细行,凭空造一行
// 等于伪造"执行过一次"(监控页会显示一个从没跑过的节点的记录)。
func (d *DB) MarkExecutionNodesSkipped(executionID string) (int64, error) {
	res, err := d.sql.Exec(`UPDATE execution_nodes SET state=?, finished_at=?
		WHERE execution_id=? AND state IN ('PENDING','RUNNING','WAITING')`,
		string(model.NodeSkipped), nowStr(), executionID)
	if err != nil {
		return 0, wrap(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// ListExecutionNodes 返回某次执行的全部节点明细(按开始时间)。
func (d *DB) ListExecutionNodes(executionID string) ([]*model.ExecutionNode, error) {
	rows, err := d.sql.Query(`SELECT id,execution_id,node_id,node_type,node_name,state,attempt,output,result_json,error,started_at,finished_at,duration_ms FROM execution_nodes WHERE execution_id=? ORDER BY started_at ASC, id ASC`, executionID)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []*model.ExecutionNode{}
	for rows.Next() {
		var n model.ExecutionNode
		if err := rows.Scan(&n.ID, &n.ExecutionID, &n.NodeID, &n.NodeType, &n.NodeName, &n.State, &n.Attempt,
			&n.Output, &n.ResultJSON, &n.Error, &n.StartedAt, &n.FinishedAt, &n.DurationMS); err != nil {
			return nil, wrap(err)
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// SaveEvent 持久化 UI 事件(审计与恢复)。
//
// **载荷不可 JSON 化时(NaN/Inf)即失败**:持久层唯一的出口是 marshalJSON
// (失败即报错,一行都不落库)—— 事件列不例外。
//
// 为什么不"写一条占位载荷"(第二十五轮曾改成那样):占位载荷会让**失败的
// 事件**在审计流里长得像一条正常事件(只是载荷里多了个标记),而这条事件
// 在 SSE / UI 上恰恰**发不出去**(json.Marshal 同样失败)→ 审计流里多一条
// "看起来正常"的记录,实际内容却丢了。失败即报错让运行期走它**既有的**
// 反应链(计数 + ERROR 日志 + 一条 runtime.error 事件),审计流里留下的是
// **显式的失败信号**,而不是一条被改了样的正常事件(约定 14)。
//
// 递归护栏在运行期侧(workflow/runtime/engine.go 的 persistFailGuard):
// runtime.error 自己落库失败时**不再**发一条 runtime.error —— 最多一级
// 嵌套,不会级联。
//
// 向前兼容:老库里已存在的占位载荷行照常可读(它就是一段合法 JSON)。
func (d *DB) SaveEvent(ev event.UIEvent) error {
	data, err := marshalJSON(ev.Data)
	if err != nil {
		return wrap(err)
	}
	_, err = d.sql.Exec(`INSERT INTO events (execution_id,node_id,type,data_json,created_at) VALUES (?,?,?,?,?)`,
		ev.ExecutionID, ev.NodeID, ev.Type, string(data), ev.Timestamp.Format(time.RFC3339Nano))
	return wrap(err)
}

// ListEvents 返回某次执行的事件(升序)。
func (d *DB) ListEvents(executionID string, limit int) ([]map[string]any, error) {
	q := `SELECT seq,execution_id,node_id,type,data_json,created_at FROM events WHERE execution_id=? ORDER BY seq ASC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := d.sql.Query(q, executionID)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var seq int
		var execID, nodeID, typ, dataJSON, createdAt string
		if err := rows.Scan(&seq, &execID, &nodeID, &typ, &dataJSON, &createdAt); err != nil {
			return nil, wrap(err)
		}
		data := map[string]any{}
		// 事件 data 列损坏 → **降级 + WARN**(与执行列表同一口径)。
		//
		// 为什么不是显式失败:事件是**历史审计记录**,一次事件列表/事件流
		// 里坏一条,不该让整个列表页打不开(与 ListExecutions 的批量读取
		// 同理)。但也不能像此前那样 `_ = json.Unmarshal(...)` 静默吞掉 ——
		// 那会让这条事件的载荷凭空变成 {} ,审计线索消失且**没有任何痕迹**。
		// 现在:载荷降级为空对象 + 一条 WARN(可在日志里定位到具体事件)。
		if err := decodeJSONColumn("data_json", dataJSON, &data); err != nil {
			logx.Warn("事件 data_json 损坏,已按空载荷处理",
				"execution", execID, "event", typ, "error", err.Error())
			data = map[string]any{}
		}
		if data == nil {
			data = map[string]any{}
		}
		out = append(out, map[string]any{
			"seq": seq, "execution_id": execID, "node_id": nodeID,
			"type": typ, "data": data, "created_at": createdAt,
		})
	}
	return out, rows.Err()
}

// WorkflowStats 是某工作流的执行统计。
//
// failed 与 cancelled **分列**:此前 failed 用
// `state IN ('FAILED','CANCELLED')` —— "跑失败"与"被用户取消"混在一列,
// 于是成功率(completed/total)把用户主动取消的执行算成"没成功",
// 点一次取消就掉一截成功率;"这次到底是失败还是被取消"在统计页上
// 也无法回答。
type WorkflowStats struct {
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Failed     int    `json:"failed"`
	// Cancelled 被取消的执行数(用户主动取消)。
	Cancelled int `json:"cancelled"`
	Waiting   int `json:"waiting"`
	Running   int `json:"running"`
}

// GetWorkflowStats 按工作流聚合执行状态。
//
// 各列的划分**穷尽**所有执行状态,于是"各列之和 == 总数"恒成立:
//
//	COMPLETED   → completed
//	FAILED      → failed
//	CANCELLED   → cancelled
//	PAUSED      → cancelled(**历史遗留**:PAUSED 已从执行状态机移除,
//	                          老库里仍有这种行;归入 cancelled 而不是
//	                          悄悄消失,否则总数与各列对不上)
//	WAITING_USER→ waiting
//	RUNNING / CREATED → running(都还没终结)
func (d *DB) GetWorkflowStats() ([]WorkflowStats, error) {
	rows, err := d.sql.Query(`
		SELECT w.id, w.name,
			COUNT(e.id) AS total,
			COALESCE(SUM(CASE WHEN e.state='COMPLETED' THEN 1 ELSE 0 END),0) AS completed,
			COALESCE(SUM(CASE WHEN e.state='FAILED' THEN 1 ELSE 0 END),0) AS failed,
			COALESCE(SUM(CASE WHEN e.state IN ('CANCELLED','PAUSED') THEN 1 ELSE 0 END),0) AS cancelled,
			COALESCE(SUM(CASE WHEN e.state='WAITING_USER' THEN 1 ELSE 0 END),0) AS waiting,
			COALESCE(SUM(CASE WHEN e.state IN ('RUNNING','CREATED') THEN 1 ELSE 0 END),0) AS running
		FROM workflows w
		LEFT JOIN executions e ON e.workflow_id = w.id
		GROUP BY w.id, w.name
		ORDER BY total DESC`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []WorkflowStats{}
	for rows.Next() {
		var st WorkflowStats
		if err := rows.Scan(&st.WorkflowID, &st.Name, &st.Total, &st.Completed, &st.Failed,
			&st.Cancelled, &st.Waiting, &st.Running); err != nil {
			return nil, wrap(err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ListAllEvents 返回全局事件流(最新在后),支持按类型过滤与条数限制。
func (d *DB) ListAllEvents(eventType string, limit int) ([]map[string]any, error) {
	q := `SELECT seq,execution_id,node_id,type,data_json,created_at FROM events`
	args := []any{}
	if eventType != "" {
		// 支持前缀过滤(如 "review." 匹配 review.approved/review.rejected)
		if strings.HasSuffix(eventType, ".") {
			q += ` WHERE type LIKE ?`
			args = append(args, eventType+"%")
		} else {
			q += ` WHERE type=?`
			args = append(args, eventType)
		}
	}
	q += ` ORDER BY seq DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var seq int
		var execID, nodeID, typ, dataJSON, createdAt string
		if err := rows.Scan(&seq, &execID, &nodeID, &typ, &dataJSON, &createdAt); err != nil {
			return nil, wrap(err)
		}
		data := map[string]any{}
		// 同 ListEvents:事件 data 列损坏 → 降级 + WARN,不静默吞掉。
		if err := decodeJSONColumn("data_json", dataJSON, &data); err != nil {
			logx.Warn("事件 data_json 损坏,已按空载荷处理",
				"execution", execID, "event", typ, "error", err.Error())
			data = map[string]any{}
		}
		if data == nil {
			data = map[string]any{}
		}
		out = append(out, map[string]any{
			"seq": seq, "execution_id": execID, "node_id": nodeID,
			"type": typ, "data": data, "created_at": createdAt,
		})
	}
	// 反转成时间升序
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// SaveSkillEnabled 保存 Skill 启用状态。
func (d *DB) SaveSkillEnabled(id string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	// 列是拼出来的(见 configUpsert):老库还有 updated_at(只写不读、
	// 已从新库删除),新库没有 → 两种 schema 都要能写。
	return d.configUpsert("skill_configs", []configCol{
		{col: "id", val: id},
		{col: "enabled", val: e},
		{col: "updated_at", val: nowStr()},
	}, []string{"enabled", "updated_at"})
}

// ListDisabledSkills 返回被禁用的 Skill 集合。
func (d *DB) ListDisabledSkills() (map[string]bool, error) {
	rows, err := d.sql.Query(`SELECT id FROM skill_configs WHERE enabled=0`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CasExecutionState 原子比较并交换执行状态。
// 当前状态属于 fromStates 时置为 to 并返回 true;否则返回 false。
// 守卫并发 Resume/Retry:双击"批准"等场景只允许一个调用方成功。
func (d *DB) CasExecutionState(id string, fromStates []model.ExecutionState, toState model.ExecutionState) (bool, error) {
	if len(fromStates) == 0 {
		return false, wrap(fmt.Errorf("persistence: CasExecutionState requires from states"))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(fromStates)), ",")
	args := []any{string(toState), id}
	for _, s := range fromStates {
		args = append(args, string(s))
	}
	res, err := d.sql.Exec(`UPDATE executions SET state=? WHERE id=? AND state IN (`+placeholders+`)`, args...)
	if err != nil {
		return false, wrap(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, wrap(err)
	}
	return n > 0, nil
}

// MarkExecutionFinished 落执行结束时间(只写 finished_at 一列)。
//
// 供取消路径使用:取消的状态迁移走只改状态列的 CAS(状态裁决的唯一入口),
// 结束时间由裁决赢家随后单独补写 —— CAS 成功后行归本次裁决所有,
// 别的写者不会再碰这条行,直接写不引入覆盖。
func (d *DB) MarkExecutionFinished(id, finishedAt string) error {
	_, err := d.sql.Exec(`UPDATE executions SET finished_at=? WHERE id=?`, finishedAt, id)
	if err != nil {
		return wrap(err)
	}
	return nil
}

// DeleteExecution 级联删除执行及其节点/事件/制品。
func (d *DB) DeleteExecution(id string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM execution_nodes WHERE execution_id=?`,
		`DELETE FROM events WHERE execution_id=?`,
		`DELETE FROM artifacts WHERE execution_id=?`,
		`DELETE FROM executions WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return wrap(err)
		}
	}
	return tx.Commit()
}

// SaveArtifact 保存制品。
func (d *DB) SaveArtifact(a *model.Artifact) error {
	_, err := d.sql.Exec(`INSERT OR REPLACE INTO artifacts (id,execution_id,node_id,name,content_type,content,created_at) VALUES (?,?,?,?,?,?,?)`,
		a.ID, a.ExecutionID, a.NodeID, a.Name, a.ContentType, a.Content, a.CreatedAt)
	return wrap(err)
}

// ListArtifacts 列出某次执行的制品。
func (d *DB) ListArtifacts(executionID string) ([]*model.Artifact, error) {
	rows, err := d.sql.Query(`SELECT id,execution_id,node_id,name,content_type,content,created_at FROM artifacts WHERE execution_id=? ORDER BY created_at ASC`, executionID)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []*model.Artifact{}
	for rows.Next() {
		var a model.Artifact
		if err := rows.Scan(&a.ID, &a.ExecutionID, &a.NodeID, &a.Name, &a.ContentType, &a.Content, &a.CreatedAt); err != nil {
			return nil, wrap(err)
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// GetSetting / SaveSetting 简单 KV 设置。
func (d *DB) GetSetting(key string, out any) error {
	var raw string
	err := d.sql.QueryRow(`SELECT value_json FROM settings WHERE key=?`, key).Scan(&raw)
	if err != nil {
		// 设置项不存在是正常初始状态,映射为 NOT_FOUND 让调用方能区分
		// "尚未设置"与"读取失败"(此前一律 DB_ERROR,掩盖真实故障)
		return wrapNotFound(err, "设置项", key)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		// 值损坏也是持久化层的问题:包成结构化错误,别让裸 json 错误
		// 逃到 API 层(那里按 kind/code 映射状态码)。
		return wrap(err)
	}
	return nil
}

func (d *DB) SaveSetting(key string, value any) error {
	raw, err := marshalJSON(value)
	if err != nil {
		return wrap(err)
	}
	stmt := `INSERT OR REPLACE INTO settings (key,value_json) VALUES (?,?)`
	// 注入点:设置项写入失败必须让调用方感知(见
	// TestSetSettingFailureDoesNotFakeSuccess)——上层据此保持内存态不变,
	// 不会出现"界面提示已保存、重启即失效"的假成功。
	if err := d.checkFault(stmt); err != nil {
		return wrap(err)
	}
	_, err = d.sql.Exec(stmt, key, string(raw))
	return wrap(err)
}

// ---- helpers ----

// marshalJSON 把一个值序列化成落库用的 JSON 文本。
//
// **失败即报错,绝不返回空字节** —— 这是本文件最重要的一条约定。
//
// 为什么:json.Marshal 遇到不可 JSON 化的值(NaN / ±Inf / 循环引用)
// 会**报错并返回空字节**(不是部分 JSON)。此前所有调用点都写成
// `_ = json.Marshal(...)`,把错误吞掉 → 列被写成 ""。
//
// 后果(第二轮 P1-1,实测):YAML 里的一个 `.nan` 让整个 variables map
// 与版本快照**静默变空串** —— 旁边合法的 `limit: 10` 一起丢(一个坏值
// 连坐清空整张 map);读回来是空 map;条件变量因此恒假 → 静默走错分支,
// 执行仍 COMPLETED(约定 14:"该失败却成功"等同 P0)。
//
// 现在:序列化失败 → 整次保存失败 → 用户看见明确的错误,而不是一份
// "看起来保存成功、其实内容全没了"的工作流。
//
// **读出口不收紧**:老库里可能已经有空串的 variables_json(上一轮的 bug
// 写进去的)—— 读时把它当**空 map**,不是错误(见 decodeJSONColumn)。
// 写必须失败即报错,读必须宽容 —— 两边口径不同是刻意的。
func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("序列化失败(值不可 JSON 化,常见于 NaN/Inf): %w", err)
	}
	return b, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func anyMapInt(m map[string]int) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := marshalJSON(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func anyMapStr(m map[string]string) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := marshalJSON(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func anyMapAny(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := marshalJSON(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func nowStr() string { return time.Now().Format(time.RFC3339Nano) }

// wrap 把底层错误包成结构化的持久化错误。
//
// 原始错误链必须保留:此前这里直接 new 一个 model.Error,底层错误(如
// sql.ErrNoRows、context.Canceled)被彻底丢弃 —— 调用方无法再用
// errors.Is/As 区分"行不存在"与"磁盘故障",错误语义止步于一句 DB_ERROR。
//
// dbError 同时满足两边:
//   - Error():与此前完全一致的消息(API 响应文案不变);
//   - As():让 errors.As 能命中 *model.Error(API 按 code/kind 映射状态码);
//   - Unwrap():保留底层错误,errors.Is 仍能命中根因。
type dbError struct {
	me    *model.Error
	cause error
}

func (e *dbError) Error() string { return e.me.Error() }

func (e *dbError) Unwrap() error { return e.cause }

func (e *dbError) As(target any) bool {
	// errors.As 传入的是"指向目标类型的指针"(var se *model.Error → **model.Error)。
	p, ok := target.(**model.Error)
	if !ok {
		return false
	}
	*p = e.me
	return true
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	return &dbError{
		me:    model.NewError(model.KindPersistenceError, "DB_ERROR", err.Error()),
		cause: err,
	}
}

// wrapNotFound 将 sql.ErrNoRows 转为友好的结构化 NOT_FOUND 错误。
func wrapNotFound(err error, what, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewError(model.KindValidationError, "NOT_FOUND", fmt.Sprintf("%s %q 不存在", what, id))
	}
	return wrap(err)
}

// ---- AgentConfig ----

// SaveAgentConfig 保存某 Agent 的运行配置(模型/端点/环境变量)。
func (d *DB) SaveAgentConfig(id string, raw []byte) error {
	// 列是拼出来的(见 configUpsert):
	//   - name:老库有(恒等于 id),新库已删 → 老库上照常补齐;
	//   - enabled:老库有(恒为 1、有默认值),新库已删 → 两边都不写;
	//   - updated_at:老库有(NOT NULL 无默认值),新库已删 → 老库上补齐。
	return d.configUpsert("agent_configs", []configCol{
		{col: "id", val: id},
		{col: "name", val: id},
		{col: "config_json", val: string(raw)},
		{col: "updated_at", val: nowStr()},
	}, []string{"config_json", "updated_at"})
}

// configCol 是"一列 + 它的值"(configUpsert 的入参单位)。
type configCol struct {
	col string
	val any
}

// configUpsert 按目标表**实际拥有的列**拼 INSERT ... ON CONFLICT(id)。
//
// 为什么需要:列的删除只改 CREATE TABLE —— 硬要求是**不许**对老库
// ALTER TABLE DROP COLUMN(老库保留多余的列,无害)。于是同一份代码要
// 同时服务两种 schema:
//
//   - 新库:没有 name/enabled/updated_at 这些列;
//   - 老库:有,而且其中几列是 NOT NULL 且**无默认值**(不写就违反约束)。
//
// 写死一份语句必然在另一种 schema 上失败:新库报 "no such column",
// 老库报 "NOT NULL constraint failed"。按实际列清单拼语句则两边都对。
//
// updateCols 是"冲突时需要更新的列":同样只取实际存在的列(老库多一列
// updated_at,更新它不会丢信息;新库没有就不更新)。
func (d *DB) configUpsert(table string, row []configCol, updateCols []string) error {
	have, err := d.tableColumns(table)
	if err != nil {
		return err
	}
	cols := make([]string, 0, len(row))
	vals := make([]any, 0, len(row))
	sets := make([]string, 0, len(row))
	for _, c := range row {
		if !have[c.col] {
			continue
		}
		cols = append(cols, c.col)
		vals = append(vals, c.val)
		if c.col != "id" && slices.Contains(updateCols, c.col) {
			sets = append(sets, c.col+"=excluded."+c.col)
		}
	}
	if len(cols) == 0 {
		return wrap(fmt.Errorf("persistence: 表 %s 没有可写的列", table))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT(id) DO UPDATE SET %s",
		table, strings.Join(cols, ","), placeholders, strings.Join(sets, ","))
	_, err = d.sql.Exec(stmt, vals...)
	return wrap(err)
}

// tableColumns 返回表实际拥有的列名集合(PRAGMA table_info)。
//
// 只用于"按实际列拼语句"的兼容路径(table 参数是本文件里的字面量,
// 不是用户输入)。
func (d *DB) tableColumns(table string) (map[string]bool, error) {
	rows, err := d.sql.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, wrap(err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// GetAgentConfig 读取某 Agent 的配置 JSON(不存在返回 nil)。
func (d *DB) GetAgentConfig(id string) ([]byte, error) {
	var raw string
	err := d.sql.QueryRow(`SELECT config_json FROM agent_configs WHERE id=?`, id).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, wrap(err)
	}
	return []byte(raw), nil
}

// ListAgentConfigs 返回全部 Agent 配置。
func (d *DB) ListAgentConfigs() (map[string]json.RawMessage, error) {
	rows, err := d.sql.Query(`SELECT id, config_json FROM agent_configs`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, wrap(err)
		}
		out[id] = json.RawMessage(raw)
	}
	return out, rows.Err()
}
