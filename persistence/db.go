// Package persistence 提供基于 SQLite 的持久化。
// 保存 Workflow、WorkflowVersion、Execution、ExecutionNode、Event、
// Artifact、Agent/Skill 配置与 Settings。执行过程中实时写库,
// 应用崩溃后重启可据此恢复。
package persistence

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// DB 封装 SQLite 连接与迁移。
type DB struct {
	sql *sql.DB
	// queryFault 仅测试使用:非 nil 时,在执行敏感语句前先问一次,
	// 返回非 nil 即让该语句失败。正常内存 SQLite 无法构造
	// IO/锁/查询失败,而"查询失败被吞 → 走错分支"这类缺陷只有
	// 注入才能覆盖(见 TestSaveWorkflowSurfacesExistenceQueryError)。
	// 生产代码不设置该字段。
	queryFault func(stmt string) error
}

// SetQueryFault 安装语句级故障注入(仅测试用;传 nil 取消)。
// 生产路径不应调用。
func (d *DB) SetQueryFault(fn func(stmt string) error) { d.queryFault = fn }

// checkFault 在敏感语句执行前询问故障注入器。
func (d *DB) checkFault(stmt string) error {
	if d.queryFault == nil {
		return nil
	}
	return d.queryFault(stmt)
}

// Open 打开(或创建)数据库并执行迁移。
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("persistence: open %s: %w", path, err)
	}
	// modernc/sqlite 建议限制写连接,避免 database locked
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("persistence: ping: %w", err)
	}
	d := &DB{sql: db}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

// Close 关闭连接。
func (d *DB) Close() error { return d.sql.Close() }

// SQL 暴露底层连接(仅限包内服务使用)。
func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS workflows (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			variables_json TEXT DEFAULT '{}',
			nodes_json TEXT DEFAULT '[]',
			edges_json TEXT DEFAULT '[]',
			settings_json TEXT DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		// 版本快照。
		//
		// 列的取舍(只写不读的列已删除,理由见 docs/OPTIMIZATION.md):
		//   - seq:只写不读(自增主键,没有任何地方读它)→ 不再建;
		//     逻辑主键是 UNIQUE(workflow_id, version)。
		//   - dsl_text / created_at:有读点(见 repo.go 的
		//     GetWorkflowVersionDSL / ListWorkflowVersionMeta)→ 保留。
		//
		// 兼容:老库里多出来的 seq 保留(不做 ALTER TABLE DROP COLUMN),
		// 写入语句不按列名引用它 → 两种 schema 都能写。
		`CREATE TABLE IF NOT EXISTS workflow_versions (
			workflow_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			dsl_text TEXT NOT NULL,
			snapshot_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(workflow_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS executions (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			workflow_version INTEGER NOT NULL,
			workflow_name TEXT NOT NULL,
			state TEXT NOT NULL,
			task TEXT DEFAULT '',
			variables_json TEXT DEFAULT '{}',
			current_node_id TEXT DEFAULT '',
			iterations_json TEXT DEFAULT '{}',
			created_at TEXT NOT NULL,
			started_at TEXT DEFAULT '',
			finished_at TEXT DEFAULT '',
			error TEXT DEFAULT '',
			node_states_json TEXT DEFAULT '{}',
			snapshot_json TEXT DEFAULT '',
			state_json TEXT DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_executions_workflow ON executions(workflow_id)`,
		// 执行列表按 created_at DESC 排序(列表页与 Dashboard 都走它)
		`CREATE INDEX IF NOT EXISTS idx_executions_created ON executions(created_at)`,
		`CREATE TABLE IF NOT EXISTS execution_nodes (
			id TEXT PRIMARY KEY,
			execution_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			node_type TEXT NOT NULL,
			node_name TEXT NOT NULL,
			state TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 1,
			output TEXT DEFAULT '',
			result_json TEXT DEFAULT '',
			error TEXT DEFAULT '',
			started_at TEXT DEFAULT '',
			finished_at TEXT DEFAULT '',
			duration_ms INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_exec_nodes_exec ON execution_nodes(execution_id)`,
		`CREATE TABLE IF NOT EXISTS events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			execution_id TEXT NOT NULL,
			node_id TEXT DEFAULT '',
			type TEXT NOT NULL,
			data_json TEXT DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_exec ON events(execution_id)`,
		// 审计页按 type 前缀过滤(events 是唯一无上限增长的表,长期运行后
		// 无索引即全表扫描 + ORDER BY seq DESC)
		`CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			execution_id TEXT NOT NULL,
			node_id TEXT DEFAULT '',
			name TEXT NOT NULL,
			content_type TEXT DEFAULT 'text/plain',
			content TEXT DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		// Agent 配置:只存"读得到的东西"。
		//
		// name(恒等于 id)、enabled(恒为 1)、updated_at 都曾**只写不读**
		// —— 写进去却没有任何地方读,是纯负担(还让人误以为"启用/停用"
		// 是 Agent 的能力)。已从新库的 CREATE TABLE 里删除。
		//
		// 兼容:老库保留这三列,其中 name/updated_at 是 NOT NULL 且**无
		// 默认值** → 写入语句按"表实际拥有的列"拼(persistence.configUpsert),
		// 老库上照常补齐这些列,新库上不写。
		`CREATE TABLE IF NOT EXISTS agent_configs (
			id TEXT PRIMARY KEY,
			config_json TEXT DEFAULT '{}'
		)`,
		// Skill 配置:enabled 有读点(ListDisabledSkills)→ 保留;
		// updated_at 只写不读 → 删除(同 agent_configs 的兼容策略)。
		`CREATE TABLE IF NOT EXISTS skill_configs (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := d.sql.Exec(s); err != nil {
			return fmt.Errorf("persistence: migrate: %w", err)
		}
	}
	// 轻量列迁移:老库升级
	return d.migrateColumns()
}

// migrateColumns 做轻量列迁移(老库升级)。
//
// 只容忍"列已存在"(老库升级的正常路径,SQLite 报 duplicate column name);
// 其余错误必须上抛 —— 此前所有 ALTER 的错误都被 `_, _ =` 吞掉,升级失败的
// 应用带着半残 schema 启动,后续查询报出的是莫名其妙的"no such column",
// 真正的原因(迁移失败)被彻底掩盖。
func (d *DB) migrateColumns() error {
	alters := []string{
		`ALTER TABLE workflows ADD COLUMN permission_json TEXT DEFAULT '{}'`,
		`ALTER TABLE executions ADD COLUMN state_json TEXT DEFAULT '{}'`,
	}
	for _, a := range alters {
		if _, err := d.sql.Exec(a); err != nil {
			if isDuplicateColumn(err) {
				continue
			}
			return fmt.Errorf("persistence: migrate: %w", err)
		}
	}
	return nil
}

// isDuplicateColumn 判断错误是否为"列已存在"(SQLite: duplicate column name)。
func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}
