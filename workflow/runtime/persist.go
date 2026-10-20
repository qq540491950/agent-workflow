package runtime

// 执行记录的持久化被拆成两类写入,这是本轮修复的核心:
//
//  1. 状态列(state):**只**由 CAS 落定。
//     CAS 是"比较并交换"—— 只有当库里的状态仍是我基于的那个状态时,
//     迁移才生效。整行写一律不碰 state 列。
//     此前整行 upsert 会覆盖 state:一个拿着过期副本的写者能把刚由 CAS
//     落定的状态改回去("已完成"被改回"运行中"、"已取消"被改回"运行中"),
//     用户在 UI 上看到的状态与实际发生的执行不符(约定 #14 同族问题)。
//  2. 其余列(定位信息 current_node_id、节点状态、会话状态快照、错误文本):
//     由 saveMeta 整行更新,与状态迁移解耦 —— 这样"落库"这一步永远
//     不可能推翻"状态裁决"。
//
// 状态迁移与元信息落库合成**一条** SQL(casStateAndSave):状态与详情
// 必须同时可见,否则会出现"状态已完成、节点状态还是上一轮"的窗口。
//
// 下面的 SQL 列集合与 persistence.SaveExecution 的 ON CONFLICT 子句
// 一一对应(改动时两处必须同步)。之所以不复用 SaveExecution:它的 upsert
// 必然覆盖 state 列,而 state 列的裁决权必须留在 CAS 手里。

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentworkflow/logx"
	"agentworkflow/workflow/model"
)

// execMetaColumns 是"非状态列"的赋值列表。
//
// 刻意不包含 state:状态由 CAS 单独落定(见文件头注释)。
const execMetaColumns = `current_node_id=?, iterations_json=?, started_at=?, finished_at=?,
	error=?, node_states_json=?, snapshot_json=?, state_json=?`

// execMetaUpdate 只更新非状态列的整行更新语句。
const execMetaUpdate = `UPDATE executions SET ` + execMetaColumns + ` WHERE id=?`

// execCasUpdate 把"状态 CAS"与"元信息落库"合成**一条**语句。
//
// 为什么要合成一条:状态与元信息(node_states / state_data / error)必须
// 同时可见。分两次写就会出现"状态已是 COMPLETED、节点状态还是上一轮"
// 的窗口 —— 轮询执行详情的 UI 会在那个窗口里看到"成功但一个节点都没跑"。
// 一条语句 = 一次原子提交,读到的永远是自洽的快照。
//
// WHERE 里的 state IN (...) 就是 CAS:只有"库里的状态仍是我基于的那几个
// 状态之一"时迁移才生效(RowsAffected=0 即冲突)。
const execCasUpdate = `UPDATE executions SET state=?, ` + execMetaColumns + ` WHERE id=? AND state IN (%s)`

// saveMeta 只写执行记录的非状态列。
//
// 错误文本在这里统一脱敏(与 logx 一致):节点失败原因常带 CLI stderr,
// 而 stderr 里常有 "ANTHROPIC_API_KEY=sk-..." 这类凭据。落库前脱敏,
// 审计文件/备份 DB 里就不会留明文(约定 #8)。
//
// 注意:脱敏只作用于**落库的副本**,内存里的 exec.Error 保持原样 ——
// 它会被 API 直接返回,由读出口负责掩码。
func (e *Engine) saveMeta(exec *model.Execution) error {
	if exec == nil {
		return nil
	}
	args := append(execMetaArgs(exec), exec.ID)
	if _, err := e.Repo.SQL().Exec(execMetaUpdate, args...); err != nil {
		return fmt.Errorf("runtime: 保存执行 %s 的元信息失败: %w", exec.ID, err)
	}
	return nil
}

// casStateAndSave 原子地完成"状态 CAS + 元信息落库"。
//
// 成功时返回 true(调用方据此同步内存里的 exec.State);失败 = 库里的状态
// 已不在 from 集合里(被别的写者改走),此时**什么都没写** —— 状态与元
// 信息都不落,避免出现"状态是新的、元信息是旧的"的自相矛盾快照。
func (e *Engine) casStateAndSave(exec *model.Execution, from []model.ExecutionState, to model.ExecutionState) (bool, error) {
	if exec == nil {
		return false, fmt.Errorf("runtime: casStateAndSave 缺少执行记录")
	}
	if len(from) == 0 {
		return false, fmt.Errorf("runtime: casStateAndSave 需要至少一个来源状态")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	args := append([]any{string(to)}, execMetaArgs(exec)...)
	args = append(args, exec.ID)
	for _, s := range from {
		args = append(args, string(s))
	}
	res, err := e.Repo.SQL().Exec(fmt.Sprintf(execCasUpdate, placeholders), args...)
	if err != nil {
		return false, fmt.Errorf("runtime: 迁移执行 %s 状态失败: %w", exec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("runtime: 迁移执行 %s 状态失败: %w", exec.ID, err)
	}
	return n > 0, nil
}

// execMetaArgs 返回元信息列的值(顺序必须与 execMetaColumns 一致)。
//
// 错误文本与**会话状态快照**在落库前脱敏(与 logx 一致):节点失败原因与
// 节点输出常带 CLI stderr,而 stderr 里常有 "ANTHROPIC_API_KEY=sk-..."
// 这类凭据。落库前脱敏,审计文件/备份 DB 里就不会留明文(约定 #8)。
//
// state_json 的脱敏是**按键分类**的(见 state_mask.go):结构化键
// (decision / node:<id>:status / _abort …)被运行期读取,用于路由与决策,
// 必须原样保留;自由文本键(output / node:<id>:result / _error …)才脱敏。
//
// 注意:脱敏只作用于**落库的副本**,内存里的 exec.StateData / exec.Error
// 保持原样 —— 它们会被 API 直接返回(由读出口负责掩码),运行期还要读
// 它们做路由决策。
func execMetaArgs(exec *model.Execution) []any {
	return []any{
		exec.CurrentNodeID,
		marshalIntMap(exec.Iterations),
		exec.StartedAt,
		exec.FinishedAt,
		logx.Mask(exec.Error),
		marshalStrMap(exec.NodeStates),
		marshalSnapshot(exec.Snapshot),
		marshalAnyMap(MaskStateData(exec.StateData)),
	}
}

// insertExecution 插入执行记录(首次写入)。
//
// 状态列在这一步直接落 RUNNING:此时行还不存在,不存在"过期写覆盖裁决"
// 的问题。但 ID 冲突必须显式挡住 —— SaveExecution 是 upsert,直接写会把
// 已存在的行(可能是已完成/已取消)覆盖成运行中。ID 是 8 字节随机数,
// 冲突概率约 2^-64,但"约等于零"不等于"等于零":撞上就换一个 ID。
//
// 落库的是**脱敏副本**(见 maskedInsertExec):SaveExecution 是整行 upsert,
// state_json / error 都是**原样**写的,而更新路径(saveMeta / casStateAndSave)
// 走 execMetaArgs,那里的 error 与 state_json 都已脱敏 —— 两条路径口径
// 不一致就意味着"落库副本已脱敏"这条不变式只在一条路径上成立。
func (e *Engine) insertExecution(exec *model.Execution) error {
	for attempt := 0; attempt < 5; attempt++ {
		exists, err := e.executionExists(exec.ID)
		if err != nil {
			return err
		}
		if exists {
			exec.ID = newID("exec")
			continue
		}
		if err := e.Repo.SaveExecution(maskedInsertExec(exec)); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("runtime: 生成执行 ID 反复冲突(已重试 5 次)")
}

// maskedInsertExec 返回"落库用"的执行记录副本:凭据载体字段已脱敏。
//
// 脱敏只作用于**副本**:内存里的 exec 保持原样 —— 运行期还要读它做路由
// 与决策,API 读出口另有掩码(app/application 的 maskExecution)。
//
// 脱敏的字段:
//
//   - error:节点失败原因常带 CLI stderr,而 stderr 里常有
//     "ANTHROPIC_API_KEY=sk-..." 这类凭据(与 execMetaArgs 同源);
//   - state_json:会话状态快照,按键分类脱敏(见 state_mask.go)。
//     今天 insert 时 StateData 为 nil 所以不漏,但只要有人复用这条路径
//     (带状态的插入、备份恢复),明文就直接进库 —— 这正是本函数存在的理由。
//
// 刻意**不**脱敏的字段:task 与 variables。
//
// 它们是**恢复输入**:恢复路径(loadExec → engine.execute 的 resume 分支)
// 把 exec.Task / exec.Variables 重新灌进会话,task 进模型消息、variables
// 进条件表达式求值(env.Vars)。落库副本一旦被改写,恢复后的执行语义就变了
// (条件走错边 = 约定 #14 的"该失败却成功"同族)—— 比明文落库的伤害更直接,
// 且明文在**读出口**已经掩码(app/application 的 maskExecution 对 task 与
// variables 都做了掩码)。这条取舍与 output 键的口径差同源,见
// state_mask.go 的说明。
func maskedInsertExec(exec *model.Execution) *model.Execution {
	if exec == nil {
		return nil
	}
	out := *exec
	out.Error = logx.Mask(exec.Error)
	out.StateData = MaskStateData(exec.StateData)
	return &out
}

// executionExists 判断执行行是否已存在。
func (e *Engine) executionExists(id string) (bool, error) {
	var n int
	if err := e.Repo.SQL().QueryRow(`SELECT COUNT(1) FROM executions WHERE id=?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("runtime: 检查执行 %s 是否存在失败: %w", id, err)
	}
	return n > 0, nil
}

// currentState 读取库里的当前状态(诊断用)。
func (e *Engine) currentState(id string) model.ExecutionState {
	cur, err := e.Repo.GetExecution(id)
	if err != nil {
		return "?"
	}
	return cur.State
}

// ---- JSON 编码助手(与 persistence 的编码保持一致) ----

func marshalIntMap(m map[string]int) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func marshalStrMap(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func marshalAnyMap(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func marshalSnapshot(wf *model.Workflow) string {
	if wf == nil {
		return ""
	}
	b, _ := json.Marshal(wf)
	return string(b)
}
