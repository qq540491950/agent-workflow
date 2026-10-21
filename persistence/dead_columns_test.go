package persistence

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// 本文件锁住"写入却从不读取的列"的处置结果。
//
// 逐列结论(理由见 docs/OPTIMIZATION.md 的轮次 84 一节):
//
//	workflow_versions.dsl_text      → 保留 + 加读点(GetWorkflowVersionDSL)
//	workflow_versions.created_at    → 保留 + 加读点(ListWorkflowVersionMeta)
//	workflow_versions.seq           → 删除(只改 CREATE TABLE)
//	agent_configs.name              → 删除(只改 CREATE TABLE)
//	agent_configs.enabled           → 删除(只改 CREATE TABLE)
//	agent_configs.updated_at        → 删除(只改 CREATE TABLE)
//	skill_configs.updated_at        → 删除(只改 CREATE TABLE)
//	skill_configs.enabled           → 保留(有读点:ListDisabledSkills)
//
// 删除列的硬约束:**只改 CREATE TABLE**,绝不对老库 ALTER TABLE DROP
// COLUMN —— 老库保留多余的列(无害),但代码必须能在两种 schema 上都跑。
// 于是写库语句按"表实际拥有的列"拼(见 configUpsert)。

// tableColsOf 返回表实际拥有的列名(PRAGMA table_info)。
func tableColsOf(t *testing.T, db *DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.sql.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("pragma %s: %v", table, err)
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
			t.Fatalf("scan: %v", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestNewSchemaDropsDeadColumns 新建库里不得再有"只写不读"的列。
func TestNewSchemaDropsDeadColumns(t *testing.T) {
	db := openTestDB(t)

	cases := []struct {
		table string
		dead  []string
	}{
		{"agent_configs", []string{"name", "enabled", "updated_at"}},
		{"skill_configs", []string{"updated_at"}},
		{"workflow_versions", []string{"seq"}},
	}
	for _, c := range cases {
		cols := tableColsOf(t, db, c.table)
		for _, dead := range c.dead {
			if cols[dead] {
				t.Errorf("%s.%s 是只写不读的列,新库不该再建它", c.table, dead)
			}
		}
	}
	// 保留下来的列必须还在(别把有用的列一起删了)。
	agentCols := tableColsOf(t, db, "agent_configs")
	for _, keep := range []string{"id", "config_json"} {
		if !agentCols[keep] {
			t.Errorf("agent_configs.%s 必须保留", keep)
		}
	}
	skillCols := tableColsOf(t, db, "skill_configs")
	for _, keep := range []string{"id", "enabled"} {
		if !skillCols[keep] {
			t.Errorf("skill_configs.%s 必须保留(有读点)", keep)
		}
	}
	verCols := tableColsOf(t, db, "workflow_versions")
	for _, keep := range []string{"workflow_id", "version", "dsl_text", "snapshot_json", "created_at"} {
		if !verCols[keep] {
			t.Errorf("workflow_versions.%s 必须保留", keep)
		}
	}
}

// TestLegacySchemaStillWritable 老库(本轮之前的 schema)必须照常能写:
// 老库里 agent_configs.name / updated_at 是 NOT NULL 且**无默认值**
// (不写就违反约束),skill_configs.updated_at 同理。
//
// 这就是"按实际列清单拼 INSERT"的理由:写死一份语句必然在另一种
// schema 上失败(新库报 no such column,老库报 NOT NULL constraint failed)。
func TestLegacySchemaStillWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// 造一个"老库"形态的 agent_configs / skill_configs。
	for _, stmt := range []string{
		`DROP TABLE agent_configs`,
		`CREATE TABLE agent_configs (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
			config_json TEXT DEFAULT '{}', updated_at TEXT NOT NULL)`,
		`DROP TABLE skill_configs`,
		`CREATE TABLE skill_configs (
			id TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL)`,
	} {
		if _, err := db.sql.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	// 写:两条路径都要在老库上成功。
	if err := db.SaveAgentConfig("legacy-agent", []byte(`{"model":"m"}`)); err != nil {
		t.Fatalf("老库里写 Agent 配置失败: %v", err)
	}
	got, err := db.GetAgentConfig("legacy-agent")
	if err != nil {
		t.Fatalf("get agent config: %v", err)
	}
	if string(got) != `{"model":"m"}` {
		t.Errorf("老库往返后配置不对: %s", got)
	}
	if err := db.SaveSkillEnabled("legacy-skill", false); err != nil {
		t.Fatalf("老库里写 Skill 启用状态失败: %v", err)
	}
	disabled, err := db.ListDisabledSkills()
	if err != nil {
		t.Fatalf("list disabled: %v", err)
	}
	if !disabled["legacy-skill"] {
		t.Error("老库里写入的 Skill 禁用状态没读回来")
	}
}

// TestNewSchemaAlsoWritable 新库上的写入路径同样要通(列少了一半)。
func TestNewSchemaAlsoWritable(t *testing.T) {
	db := openTestDB(t)
	if err := db.SaveAgentConfig("a1", []byte(`{"model":"m"}`)); err != nil {
		t.Fatalf("写 Agent 配置失败: %v", err)
	}
	if err := db.SaveAgentConfig("a1", []byte(`{"model":"m2"}`)); err != nil {
		t.Fatalf("重复写 Agent 配置失败: %v", err)
	}
	got, err := db.GetAgentConfig("a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != `{"model":"m2"}` {
		t.Errorf("重复写入后配置不对: %s", got)
	}
	if err := db.SaveSkillEnabled("s1", true); err != nil {
		t.Fatalf("写 Skill 启用状态失败: %v", err)
	}
	if err := db.SaveSkillEnabled("s1", false); err != nil {
		t.Fatalf("重复写 Skill 启用状态失败: %v", err)
	}
	disabled, err := db.ListDisabledSkills()
	if err != nil {
		t.Fatalf("list disabled: %v", err)
	}
	if !disabled["s1"] {
		t.Error("重复写入后 Skill 禁用状态不对")
	}
}

// TestWorkflowVersionReadPoints dsl_text 与 created_at 这两列此前
// "只写不读" —— 现在各自有了读点:
//
//   - dsl_text:唯一保存"当初那份 YAML 原文"的列(snapshot_json 是模型
//     快照,重新编码得到的 YAML 与原文未必一致);
//   - created_at:版本落库时间(版本列表可以显示"这个版本什么时候存的")。
func TestWorkflowVersionReadPoints(t *testing.T) {
	db := openTestDB(t)
	doc, err := dsl.Parse([]byte(demoYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatalf("save: %v", err)
	}

	text, err := db.GetWorkflowVersionDSL(wf.ID, wf.Version)
	if err != nil {
		t.Fatalf("get dsl: %v", err)
	}
	if !strings.Contains(text, "wf-demo") {
		t.Errorf("读回来的 DSL 文本不像是原文: %q", text)
	}

	metas, err := db.ListWorkflowVersionMeta(wf.ID)
	if err != nil {
		t.Fatalf("list meta: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("版本元信息条数 = %d, want 1", len(metas))
	}
	if metas[0].Version != wf.Version {
		t.Errorf("版本号 = %d, want %d", metas[0].Version, wf.Version)
	}
	if metas[0].CreatedAt == "" {
		t.Error("created_at 读点必须返回落库时间(此前这一列只写不读)")
	}
}

// TestGetWorkflowVersionDSLNotFound 版本不存在 → NOT_FOUND(404 口径)。
func TestGetWorkflowVersionDSLNotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := db.GetWorkflowVersionDSL("no-such-wf", 99)
	if err == nil {
		t.Fatal("版本不存在应报错")
	}
	var me *model.Error
	if !errors.As(err, &me) || me.Code != "NOT_FOUND" {
		t.Errorf("错误码 = %v, want NOT_FOUND", err)
	}
}
