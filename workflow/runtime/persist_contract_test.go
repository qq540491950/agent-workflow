package runtime

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"agentworkflow/persistence"
)

// 本文件是"运行期直写 executions 表"与"持久层 SaveExecution"之间的
// **列集合契约**守卫。
//
// 背景:运行期不复用 persistence.SaveExecution 写状态(它的 upsert 必然
// 覆盖 state 列,而 state 的裁决权必须留在 CAS 手里),于是运行期自己
// 拼 SQL 写 executions 表。两边的列集合必须一一对应:
//
//   - 持久层加了列而运行期没加 → 运行期写的那部分信息静默丢失
//     (例如新增一个定位字段,运行期永远写不进去);
//   - 运行期加了列而持久层没加 → 整条 UPDATE 报"no such column",
//     执行记录的元信息再也落不了库(执行详情页永远空白)。
//
// 两边各写各的 SQL 时,这种漂移只能靠人记得同步 —— 文件头的注释提醒
// 过一次,注释不是守卫。本测试把注释变成断言。

// parseColumnList 从 "col=?, col2=?" 形态的赋值列表里取出列名(排序)。
func parseColumnList(list string) []string {
	out := []string{}
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := part
		if i := strings.Index(part, "="); i >= 0 {
			name = strings.TrimSpace(part[:i])
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestExecMetaColumnsMatchPersistenceUpsert 运行期写的非状态列必须与
// 持久层 upsert 的非状态列完全一致(逐列比对,多一列少一列都失败)。
func TestExecMetaColumnsMatchPersistenceUpsert(t *testing.T) {
	got := parseColumnList(execMetaColumns)
	want := parseColumnList(persistence.ExecUpsertMetaColumns)
	if len(got) == 0 {
		t.Fatal("运行期的元信息列清单为空")
	}
	gotSet := map[string]bool{}
	for _, c := range got {
		gotSet[c] = true
	}
	wantSet := map[string]bool{}
	for _, c := range want {
		wantSet[c] = true
	}
	for _, c := range want {
		if !gotSet[c] {
			t.Errorf("持久层 upsert 会更新列 %q,但运行期没有写这一列(请同步 workflow/runtime/persist.go 的 execMetaColumns)", c)
		}
	}
	for _, c := range got {
		if !wantSet[c] {
			t.Errorf("运行期写了列 %q,但持久层 upsert 不更新这一列(请同步 persistence.ExecUpsertMetaColumns)", c)
		}
	}
}

// TestExecMetaColumnsExistInTable 运行期写的列必须真的存在于 executions 表。
//
// 契约测试只比对"两份清单",若两份一起写错(都加了一个不存在的列),
// 比对仍会通过 —— 这里用真实的表结构兜底。
func TestExecMetaColumnsExistInTable(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "contract.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer repo.Close()
	rows, err := repo.SQL().Query(`SELECT name FROM pragma_table_info('executions')`)
	if err != nil {
		t.Fatalf("读取表结构: %v", err)
	}
	defer rows.Close()
	table := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("扫描列名: %v", err)
		}
		table[name] = true
	}
	for _, c := range parseColumnList(execMetaColumns) {
		if !table[c] {
			t.Errorf("运行期写了列 %q,但 executions 表里没有这一列", c)
		}
	}
	for _, c := range parseColumnList(persistence.ExecInsertColumns) {
		if !table[c] {
			t.Errorf("持久层插入列 %q,但 executions 表里没有这一列", c)
		}
	}
}
