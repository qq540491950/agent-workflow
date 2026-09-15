// Package persistence 提供基于 SQLite 的持久化。
// 保存 Workflow、WorkflowVersion、Execution、ExecutionNode、Event、
// Artifact、Agent/Skill 配置与 Settings。执行过程中实时写库,
// 应用崩溃后重启可据此恢复。
package persistence

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB 封装 SQLite 连接与迁移。
type DB struct {
	sql *sql.DB
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
		`CREATE TABLE IF NOT EXISTS workflow_versions (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
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
		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			execution_id TEXT NOT NULL,
			node_id TEXT DEFAULT '',
			name TEXT NOT NULL,
			content_type TEXT DEFAULT 'text/plain',
			content TEXT DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_configs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			config_json TEXT DEFAULT '{}',
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS skill_configs (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL
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
	// 轻量列迁移:老库升级(列已存在时忽略错误)
	alters := []string{
		`ALTER TABLE workflows ADD COLUMN permission_json TEXT DEFAULT '{}'`,
		`ALTER TABLE executions ADD COLUMN state_json TEXT DEFAULT '{}'`,
	}
	for _, a := range alters {
		_, _ = d.sql.Exec(a)
	}
	return nil
}
