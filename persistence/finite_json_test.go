package persistence

import (
	"math"
	"strings"
	"testing"

	"agentworkflow/event"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
	"agentworkflow/workflow/validator"
)

// 本文件锁住"**序列化失败即报错**"这条底线(第二轮 P1-1)。
//
// 实测缺陷(隔离副本,基线 73c02eb):YAML 里的一个 `.nan` 让
// `json.Marshal` 报错并返回**空字节**(不是部分 JSON),而调用点一律
// `_ = json.Marshal(...)` 把错误吞掉 → 列写成 ""。
//
// 后果比"这一个值丢了"严重得多:
//
//   - 一个坏值**连坐清空整张 map**(旁边合法的 `limit: 10` 一起丢);
//   - workflow_versions.snapshot_json 同样变空 → 版本快照丢失;
//   - 导入成功、无错误无警告 → 运行期 variables 为空 → 模板变量渲染
//     空串(有 node.warning),**条件变量则零告警** → 条件恒假 → 静默
//     走错分支,执行仍 COMPLETED(约定 14:"该失败却成功"等同 P0)。
//
// 修法三层:持久层(失败即报错)+ 校验期(数值必须有限)+ 导入期预检。
// 本文件锁住**持久层**这一层;校验期见 workflow/validator/finite_test.go。

// TestSaveWorkflowRejectsNonFiniteVariables 含 NaN/Inf 的 variables 必须
// 让保存**失败**,且**不得**把 variables_json 写成空串。
func TestSaveWorkflowRejectsNonFiniteVariables(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{
		ID:   "wf-nan",
		Name: "含 NaN 的工作流",
		Variables: map[string]any{
			"env":   math.NaN(),
			"limit": 10, // 合法值:一个坏值不得连坐清空整张 map
		},
	}
	err := db.SaveWorkflow(wf, true)
	if err == nil {
		t.Fatal("含 NaN 的 variables 必须让 SaveWorkflow 失败(否则列会被写成空串,整张 map 连坐清空)")
	}
	// 不得落库(哪怕是空串):工作流根本没保存成功。
	var n int
	if err := db.SQL().QueryRow(`SELECT COUNT(1) FROM workflows WHERE id=?`, wf.ID).Scan(&n); err != nil {
		t.Fatalf("查行数: %v", err)
	}
	if n != 0 {
		t.Errorf("保存失败后不得留下工作流行,实际有 %d 行", n)
	}
	var versions int
	if err := db.SQL().QueryRow(`SELECT COUNT(1) FROM workflow_versions WHERE workflow_id=?`, wf.ID).Scan(&versions); err != nil {
		t.Fatalf("查版本数: %v", err)
	}
	if versions != 0 {
		t.Errorf("保存失败后不得留下版本快照行,实际有 %d 行", versions)
	}
}

// TestSaveWorkflowKeepsValidVariablesWhenOneValueIsBad 旁边合法的值必须
// 保住:坏值让**整次保存**失败,而不是"把整张 map 变成空"。
func TestSaveWorkflowKeepsValidVariablesWhenOneValueIsBad(t *testing.T) {
	db := newTestDB(t)
	good := &model.Workflow{
		ID:        "wf-good",
		Name:      "正常工作流",
		Variables: map[string]any{"env": "prod", "limit": 10},
	}
	if err := db.SaveWorkflow(good, true); err != nil {
		t.Fatalf("保存正常工作流失败: %v", err)
	}
	var raw string
	if err := db.SQL().QueryRow(`SELECT variables_json FROM workflows WHERE id=?`, good.ID).Scan(&raw); err != nil {
		t.Fatalf("读 variables_json: %v", err)
	}
	if raw == "" {
		t.Fatal("正常工作流的 variables_json 不得是空串")
	}
	if !strings.Contains(raw, "prod") || !strings.Contains(raw, "10") {
		t.Errorf("variables_json 丢了合法值: %s", raw)
	}
}

// TestSaveWorkflowRejectsNonFiniteNodeConfig 节点配置里的 NaN 同样会
// **清空整个 nodes_json**(跑的是另一个工作流,约定 14)—— 必须失败。
func TestSaveWorkflowRejectsNonFiniteNodeConfig(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{
		ID:   "wf-node-nan",
		Name: "节点含 NaN",
		Nodes: []model.Node{{
			ID:   "n1",
			Type: model.NodeTypeAgent,
			Config: map[string]any{
				"agent":           "claude",
				"mode":            "plan",
				"timeout_seconds": math.Inf(1),
			},
		}},
	}
	if err := db.SaveWorkflow(wf, true); err == nil {
		t.Fatal("节点配置含 Inf 时必须让 SaveWorkflow 失败(否则整个 nodes_json 变空串)")
	}
}

// TestSaveExecutionRejectsNonFiniteVariables 执行记录里的 variables 同样
// 不得被静默写成空串(执行页会显示"没有变量",且状态快照丢失)。
func TestSaveExecutionRejectsNonFiniteVariables(t *testing.T) {
	db := newTestDB(t)
	e := &model.Execution{
		ID:        "ex-nan",
		State:     model.ExecutionRunning,
		Task:      "t",
		Variables: map[string]any{"score": math.NaN()},
	}
	if err := db.SaveExecution(e); err == nil {
		t.Fatal("含 NaN 的执行变量必须让 SaveExecution 失败(不得静默写空串)")
	}
	var n int
	if err := db.SQL().QueryRow(`SELECT COUNT(1) FROM executions WHERE id=?`, e.ID).Scan(&n); err != nil {
		t.Fatalf("查行数: %v", err)
	}
	if n != 0 {
		t.Errorf("保存失败后不得留下执行行,实际有 %d 行", n)
	}
}

// TestGetWorkflowTreatsEmptyVariablesJSONAsEmptyMap **向后兼容**:老库里
// 可能已经存在空串的 variables_json(上一轮的 bug 写进去的)。
// 读出口必须把它**当成空 map**,不是错误 —— 否则老工作流直接打不开。
func TestGetWorkflowTreatsEmptyVariablesJSONAsEmptyMap(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-legacy", Name: "老库工作流"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("保存: %v", err)
	}
	// 模拟上一轮 bug 写进去的空串。
	if _, err := db.SQL().Exec(`UPDATE workflows SET variables_json='' WHERE id=?`, wf.ID); err != nil {
		t.Fatalf("注入空串: %v", err)
	}
	got, err := db.GetWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("读老库工作流必须成功(空串 variables_json 视为空 map): %v", err)
	}
	if got.Variables == nil {
		t.Fatal("空串 variables_json 必须读成空 map,不得是 nil")
	}
	if len(got.Variables) != 0 {
		t.Errorf("空串 variables_json 必须读成空 map,实际 %v", got.Variables)
	}
}

// TestListWorkflowsTreatsEmptyVariablesJSONAsEmptyMap 列表路径同样兼容。
func TestListWorkflowsTreatsEmptyVariablesJSONAsEmptyMap(t *testing.T) {
	db := newTestDB(t)
	wf := &model.Workflow{ID: "wf-legacy-list", Name: "老库工作流"}
	if err := db.SaveWorkflow(wf, true); err != nil {
		t.Fatalf("保存: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE workflows SET variables_json='' WHERE id=?`, wf.ID); err != nil {
		t.Fatalf("注入空串: %v", err)
	}
	list, err := db.ListWorkflows()
	if err != nil {
		t.Fatalf("列老库工作流必须成功: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应有 1 条,实际 %d", len(list))
	}
	if list[0].Variables == nil || len(list[0].Variables) != 0 {
		t.Errorf("空串 variables_json 必须读成空 map,实际 %v", list[0].Variables)
	}
}

// TestSaveEventKeepsAuditRecordWhenPayloadUnserializable 事件载荷含 NaN 时,
// 审计记录**不得**变成空载荷 —— 事件本身必须还在表里,且"载荷丢了"这件事
// 在 data_json 里看得见(见 SaveEvent 的注释:失败会改变 SSE 契约,故走
// 可见降级而不是"整条事件不落库")。
func TestSaveEventKeepsAuditRecordWhenPayloadUnserializable(t *testing.T) {
	db := newTestDB(t)
	ev := event.New(event.NodeStarted, "exec-nan", "n1", map[string]any{"ratio": math.NaN()})
	if err := db.SaveEvent(ev); err != nil {
		t.Fatalf("SaveEvent 不得失败(事件必须留在审计流里): %v", err)
	}
	var raw string
	if err := db.SQL().QueryRow(`SELECT data_json FROM events WHERE execution_id=?`, "exec-nan").Scan(&raw); err != nil {
		t.Fatalf("读事件: %v", err)
	}
	if raw == "" {
		t.Fatal("事件载荷不得写成空串(与'载荷本来就是空的'无法区分)")
	}
	if !strings.Contains(raw, "__unserializable__") {
		t.Errorf("占位载荷必须写明'原始载荷未保存',实际: %s", raw)
	}
}

// TestImportDSLWithNaNIsRejectedByValidator 端到端:导入含 `.nan` 的 DSL
// 必须被**校验期**拒绝(而不是 200 + 空表)。
//
// 三层防线里,校验期是用户能看见的那一层:导入返回明确的校验错误,
// 而不是"导入成功但变量全没了"。
func TestImportDSLWithNaNIsRejectedByValidator(t *testing.T) {
	src := `
version: "1"
workflow:
  id: wf-nan-import
  name: 含 NaN 的导入
variables:
  env: .nan
  limit: 10
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
`
	doc, err := dsl.Parse([]byte(src))
	if err != nil {
		t.Fatalf("YAML 解析失败(.nan 是 YAML 1.1 的合法字面量,解析器不该报错): %v", err)
	}
	wf := doc.ToModel()
	// 解析器确实把 .nan 读成了 NaN —— 前提不成立时下面的断言没有意义。
	if _, ok := wf.Variables["env"].(float64); !ok {
		t.Fatalf("测试前提不成立:.nan 应被解析成 float64,实际 %T", wf.Variables["env"])
	}
	res := validator.Validate(wf, nil, nil)
	if res.Valid {
		t.Fatal("含 .nan 的工作流必须校验失败(否则它会被静默存成空 variables)")
	}
	found := false
	for _, e := range res.Errors {
		if e.Code == "INVALID_NODE" {
			found = true
		}
	}
	if !found {
		t.Errorf("校验错误里必须有 INVALID_NODE(前端 CODE_HINTS 已有该码),实际: %+v", res.Errors)
	}
}
