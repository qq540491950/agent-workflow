package application

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// 版本页要展示"**当初那份** DSL",读的是版本快照里的原文
// (workflow_versions.dsl_text),不是拿模型快照重新编码一遍。
//
// 为什么重新编码不等价:快照存的是**模型**(JSON),模型里的数字在
// JSON 往返后变成 float64,再编码成 YAML 就成了 `3.0` —— 用户看到的
// 是"我的变量 max_iter 从 3 变成了 3.0",而工作流一个字节都没改。
// 这不是排版差异,是**内容差异**。
//
// 修前:VersionDSL 走 `dsl.FromModel(snapshot).EncodeYAML()`(重新编码)。
// 修后:走 `Repo.GetWorkflowVersionDSL`(读原文);老库(没写这一列)
// 回退到重新编码,保证老数据仍能读。

// openRawDB 打开应用库文件的**第二个**连接(仅测试用)。
//
// 为什么需要:要模拟"老库没写 dsl_text"这个历史状态,只能在应用层
// 之外改一行数据(应用层没有暴露改这一列的接口 —— 那正是本轮要修的
// 读点缺失)。
func openRawDB(t *testing.T, a *App) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(a.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("打开原始连接: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// importVersionedWorkflow 导入一个带数值变量的工作流(测试用)。
//
// 变量里的整数是关键:它在 JSON 快照往返后变成 float64,重新编码的
// YAML 与原文因此不同(`3` vs `3.0`)。
func importVersionedWorkflow(t *testing.T, a *App, id string) {
	t.Helper()
	if _, err := a.Workflows.ImportYAML(`
version: "1"
workflow:
  id: ` + id + `
  name: Versioned
variables:
  max_iter: 3
nodes:
  - id: a
    type: agent
    agent: mock-claude
    mode: plan
`); err != nil {
		t.Fatalf("导入工作流 %s: %v", id, err)
	}
}

// TestVersionDSLReturnsStoredOriginal 版本页必须返回**当初存下的原文**。
//
// 修前(红):返回重新编码的结果,整数变量 max_iter 变成 `3.0`。
func TestVersionDSLReturnsStoredOriginal(t *testing.T) {
	a := newTestApp(t)
	importVersionedWorkflow(t, a, "dsl-orig")

	got, err := a.Workflows.VersionDSL("dsl-orig", 1)
	if err != nil {
		t.Fatalf("VersionDSL: %v", err)
	}
	if !strings.Contains(got, "max_iter: 3") {
		t.Errorf("版本页必须显示当初的原文(max_iter: 3),实际:\n%s", got)
	}
	if strings.Contains(got, "3.0") {
		t.Errorf("版本页显示的是**重新编码**的结果(整数变 3.0),不是原文:\n%s", got)
	}
}

// TestVersionDSLFallsBackWhenStoredTextEmpty 老库兼容:dsl_text 为空
// (老版本没写这一列)时必须回退到从快照重编码,不能报错、不能返回空。
func TestVersionDSLFallsBackWhenStoredTextEmpty(t *testing.T) {
	a := newTestApp(t)
	importVersionedWorkflow(t, a, "dsl-empty")

	raw := openRawDB(t, a)
	if _, err := raw.Exec(
		`UPDATE workflow_versions SET dsl_text='' WHERE workflow_id=?`, "dsl-empty"); err != nil {
		t.Fatalf("构造老库状态(dsl_text 为空): %v", err)
	}

	got, err := a.Workflows.VersionDSL("dsl-empty", 1)
	if err != nil {
		t.Fatalf("老库(没有 dsl_text)必须仍能读出版本: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("回退结果不得为空")
	}
	if !strings.Contains(got, "id: dsl-empty") {
		t.Errorf("回退结果应是可用的 DSL 文本,实际:\n%s", got)
	}
}

// TestVersionDSLNotFound 版本不存在 → NOT_FOUND(两条读路径同口径)。
func TestVersionDSLNotFound(t *testing.T) {
	a := newTestApp(t)
	_, err := a.Workflows.VersionDSL("no-such-wf", 99)
	if err == nil {
		t.Fatal("版本不存在必须报错")
	}
	if !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Errorf("应为 NOT_FOUND 口径,实际: %v", err)
	}
}
