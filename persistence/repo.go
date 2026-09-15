package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"agentworkflow/event"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// SaveWorkflow 插入或更新 Workflow 定义(bump 版本并写版本快照)。
// bumpVersion=true 时版本+1 并记录版本历史;false 时仅更新元数据(不产生新版本)。
func (d *DB) SaveWorkflow(wf *model.Workflow, bumpVersion bool) error {
	now := nowStr()
	if bumpVersion {
		wf.Version++
	}
	wf.UpdatedAt = now

	variables, _ := json.Marshal(orEmpty(wf.Variables))
	nodes, _ := json.Marshal(wf.Nodes)
	edges, _ := json.Marshal(wf.Edges)
	settings, _ := json.Marshal(wf.Settings)
	enabled := 0
	if wf.Enabled {
		enabled = 1
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()

	var existing int
	_ = tx.QueryRow(`SELECT COUNT(1) FROM workflows WHERE id=?`, wf.ID).Scan(&existing)
	if existing == 0 {
		wf.Version = 1
		wf.CreatedAt = now
		_, err = tx.Exec(`INSERT INTO workflows
			(id,name,description,version,enabled,variables_json,nodes_json,edges_json,settings_json,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			wf.ID, wf.Name, wf.Description, wf.Version, enabled,
			string(variables), string(nodes), string(edges), string(settings), now, now)
	} else {
		_, err = tx.Exec(`UPDATE workflows SET name=?,description=?,version=?,enabled=?,variables_json=?,nodes_json=?,edges_json=?,settings_json=?,updated_at=? WHERE id=?`,
			wf.Name, wf.Description, wf.Version, enabled,
			string(variables), string(nodes), string(edges), string(settings), now, wf.ID)
	}
	if err != nil {
		return wrap(err)
	}

	// 版本快照:每次产生新版本时写入 DSL 文本与完整快照
	if bumpVersion || existing == 0 {
		doc := dsl.FromModel(wf)
		dslText, _ := doc.EncodeYAML()
		snapshot, _ := json.Marshal(wf)
		_, err = tx.Exec(`INSERT OR REPLACE INTO workflow_versions (workflow_id,version,dsl_text,snapshot_json,created_at) VALUES (?,?,?,?,?)`,
			wf.ID, wf.Version, string(dslText), string(snapshot), now)
		if err != nil {
			return wrap(err)
		}
	}
	return tx.Commit()
}

// GetWorkflow 按 ID 读取 Workflow。
func (d *DB) GetWorkflow(id string) (*model.Workflow, error) {
	row := d.sql.QueryRow(`SELECT id,name,description,version,enabled,variables_json,nodes_json,edges_json,settings_json,created_at,updated_at FROM workflows WHERE id=?`, id)
	return scanWorkflow(row)
}

// ListWorkflows 返回全部 Workflow。
func (d *DB) ListWorkflows() ([]*model.Workflow, error) {
	rows, err := d.sql.Query(`SELECT id,name,description,version,enabled,variables_json,nodes_json,edges_json,settings_json,created_at,updated_at FROM workflows ORDER BY updated_at DESC`)
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
		return nil, wrap(err)
	}
	wf := &model.Workflow{}
	if err := json.Unmarshal([]byte(snap), wf); err != nil {
		return nil, wrap(err)
	}
	return wf, nil
}

// ListWorkflowVersions 返回某 Workflow 的版本号列表(降序)。
func (d *DB) ListWorkflowVersions(workflowID string) ([]int, error) {
	rows, err := d.sql.Query(`SELECT version FROM workflow_versions WHERE workflow_id=? ORDER BY version DESC`, workflowID)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	var out []int
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

func scanWorkflow(r rowScanner) (*model.Workflow, error) {
	var wf model.Workflow
	var variables, nodes, edges, settings string
	var enabled int
	if err := r.Scan(&wf.ID, &wf.Name, &wf.Description, &wf.Version, &enabled,
		&variables, &nodes, &edges, &settings, &wf.CreatedAt, &wf.UpdatedAt); err != nil {
		return nil, wrap(err)
	}
	wf.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(variables), &wf.Variables)
	_ = json.Unmarshal([]byte(nodes), &wf.Nodes)
	_ = json.Unmarshal([]byte(edges), &wf.Edges)
	_ = json.Unmarshal([]byte(settings), &wf.Settings)
	if wf.Variables == nil {
		wf.Variables = map[string]any{}
	}
	return &wf, nil
}

// ---- Execution ----

// SaveExecution 插入或更新执行状态(实时保存)。
func (d *DB) SaveExecution(e *model.Execution) error {
	variables, _ := json.Marshal(orEmpty(e.Variables))
	iterations := anyMapInt(e.Iterations)
	nodeStates := anyMapStr(e.NodeStates)
	stateData := anyMapAny(e.StateData)
	var snapshot []byte
	if e.Snapshot != nil {
		snapshot, _ = json.Marshal(e.Snapshot)
	}
	var existing int
	_ = d.sql.QueryRow(`SELECT COUNT(1) FROM executions WHERE id=?`, e.ID).Scan(&existing)
	if existing == 0 {
		_, err := d.sql.Exec(`INSERT INTO executions
			(id,workflow_id,workflow_version,workflow_name,state,task,variables_json,current_node_id,iterations_json,created_at,started_at,finished_at,error,node_states_json,snapshot_json,state_json)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.ID, e.WorkflowID, e.WorkflowVersion, e.WorkflowName, string(e.State), e.Task,
			string(variables), e.CurrentNodeID, string(iterations), e.CreatedAt,
			e.StartedAt, e.FinishedAt, e.Error, string(nodeStates), string(snapshot), string(stateData))
		return wrap(err)
	}
	_, err := d.sql.Exec(`UPDATE executions SET state=?,current_node_id=?,iterations_json=?,started_at=?,finished_at=?,error=?,node_states_json=?,snapshot_json=?,state_json=? WHERE id=?`,
		string(e.State), e.CurrentNodeID, string(iterations), e.StartedAt, e.FinishedAt, e.Error, string(nodeStates), string(snapshot), string(stateData), e.ID)
	return wrap(err)
}

// GetExecution 读取执行记录。
func (d *DB) GetExecution(id string) (*model.Execution, error) {
	row := d.sql.QueryRow(`SELECT id,workflow_id,workflow_version,workflow_name,state,task,variables_json,current_node_id,iterations_json,created_at,started_at,finished_at,error,node_states_json,snapshot_json,state_json FROM executions WHERE id=?`, id)
	var e model.Execution
	var variables, iterations, nodeStates, snapshot, stateData string
	if err := row.Scan(&e.ID, &e.WorkflowID, &e.WorkflowVersion, &e.WorkflowName, &e.State, &e.Task,
		&variables, &e.CurrentNodeID, &iterations, &e.CreatedAt, &e.StartedAt, &e.FinishedAt, &e.Error, &nodeStates, &snapshot, &stateData); err != nil {
		return nil, wrap(err)
	}
	_ = json.Unmarshal([]byte(stateData), &e.StateData)
	_ = json.Unmarshal([]byte(variables), &e.Variables)
	_ = json.Unmarshal([]byte(iterations), &e.Iterations)
	_ = json.Unmarshal([]byte(nodeStates), &e.NodeStates)
	if snapshot != "" {
		wf := &model.Workflow{}
		if err := json.Unmarshal([]byte(snapshot), wf); err == nil {
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
		_ = json.Unmarshal([]byte(stateData), &e.StateData)
		_ = json.Unmarshal([]byte(variables), &e.Variables)
		_ = json.Unmarshal([]byte(iterations), &e.Iterations)
		_ = json.Unmarshal([]byte(nodeStates), &e.NodeStates)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ListRunningExecutions 返回未终态的执行(用于崩溃恢复)。
func (d *DB) ListRunningExecutions() ([]*model.Execution, error) {
	rows, err := d.sql.Query(`SELECT id FROM executions WHERE state IN ('RUNNING','WAITING_USER','PAUSED','CREATED')`)
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
	var out []*model.Execution
	for _, id := range ids {
		e, err := d.GetExecution(id)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// SaveExecutionNode 插入或更新节点执行明细。
func (d *DB) SaveExecutionNode(n *model.ExecutionNode) error {
	var existing int
	_ = d.sql.QueryRow(`SELECT COUNT(1) FROM execution_nodes WHERE id=?`, n.ID).Scan(&existing)
	if existing == 0 {
		_, err := d.sql.Exec(`INSERT INTO execution_nodes
			(id,execution_id,node_id,node_type,node_name,state,attempt,output,result_json,error,started_at,finished_at,duration_ms)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.ExecutionID, n.NodeID, string(n.NodeType), n.NodeName, string(n.State), n.Attempt,
			n.Output, n.ResultJSON, n.Error, n.StartedAt, n.FinishedAt, n.DurationMS)
		return wrap(err)
	}
	_, err := d.sql.Exec(`UPDATE execution_nodes SET state=?,attempt=?,output=?,result_json=?,error=?,started_at=?,finished_at=?,duration_ms=? WHERE id=?`,
		string(n.State), n.Attempt, n.Output, n.ResultJSON, n.Error, n.StartedAt, n.FinishedAt, n.DurationMS, n.ID)
	return wrap(err)
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
func (d *DB) SaveEvent(ev event.UIEvent) error {
	data, _ := json.Marshal(ev.Data)
	_, err := d.sql.Exec(`INSERT INTO events (execution_id,node_id,type,data_json,created_at) VALUES (?,?,?,?,?)`,
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
		_ = json.Unmarshal([]byte(dataJSON), &data)
		out = append(out, map[string]any{
			"seq": seq, "execution_id": execID, "node_id": nodeID,
			"type": typ, "data": data, "created_at": createdAt,
		})
	}
	return out, rows.Err()
}

// WorkflowStats 是某工作流的执行统计。
type WorkflowStats struct {
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Failed     int    `json:"failed"`
	Waiting    int    `json:"waiting"`
	Running    int    `json:"running"`
}

// GetWorkflowStats 按工作流聚合执行状态。
func (d *DB) GetWorkflowStats() ([]WorkflowStats, error) {
	rows, err := d.sql.Query(`
		SELECT w.id, w.name,
			COUNT(e.id) AS total,
			COALESCE(SUM(CASE WHEN e.state='COMPLETED' THEN 1 ELSE 0 END),0) AS completed,
			COALESCE(SUM(CASE WHEN e.state IN ('FAILED','CANCELLED') THEN 1 ELSE 0 END),0) AS failed,
			COALESCE(SUM(CASE WHEN e.state='WAITING_USER' THEN 1 ELSE 0 END),0) AS waiting,
			COALESCE(SUM(CASE WHEN e.state='RUNNING' THEN 1 ELSE 0 END),0) AS running
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
		if err := rows.Scan(&st.WorkflowID, &st.Name, &st.Total, &st.Completed, &st.Failed, &st.Waiting, &st.Running); err != nil {
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
		q += ` WHERE type=?`
		args = append(args, eventType)
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
		_ = json.Unmarshal([]byte(dataJSON), &data)
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
		return wrap(err)
	}
	return json.Unmarshal([]byte(raw), out)
}

func (d *DB) SaveSetting(key string, value any) error {
	raw, _ := json.Marshal(value)
	_, err := d.sql.Exec(`INSERT OR REPLACE INTO settings (key,value_json) VALUES (?,?)`, key, string(raw))
	return wrap(err)
}

// ---- helpers ----

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func anyMapInt(m map[string]int) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func anyMapStr(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func anyMapAny(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func nowStr() string { return time.Now().Format(time.RFC3339Nano) }

func wrap(err error) error {
	if err == nil {
		return nil
	}
	return model.NewError(model.KindPersistenceError, "DB_ERROR", err.Error())
}

// ---- AgentConfig ----

// SaveAgentConfig 保存某 Agent 的运行配置(模型/端点/环境变量)。
func (d *DB) SaveAgentConfig(id string, raw []byte) error {
	_, err := d.sql.Exec(`INSERT INTO agent_configs (id,name,enabled,config_json,updated_at)
		VALUES (?,?,1,?,?)
		ON CONFLICT(id) DO UPDATE SET config_json=excluded.config_json, updated_at=excluded.updated_at`,
		id, id, string(raw), nowStr())
	return wrap(err)
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
