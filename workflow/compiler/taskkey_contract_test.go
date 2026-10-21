package compiler

import (
	"testing"

	"agentworkflow/contextx"
)

// 本文件锁住"任务文本的会话状态键只有一份来源"。
//
// 缺口:compiler.KeyTask 与 contextx.StateKeyTask 此前各写一份字面量
// ("task")。两边漂移("task" vs "tasks")的后果很隐蔽:引擎把任务种进
// 会话状态用的是 compiler.KeyTask,而上下文层(Build / BuildWithSlots)
// 读的是 contextx.StateKeyTask —— 键对不上,{{task}} 渲染成空串,
// Agent 收到**没有任务**的提示词照常执行,整个执行报告 SUCCESS
// (第 19 轮实测:整条跨节点上下文链是死的)。
//
// 现在 compiler 消费 contextx 的定义(contextx 是上下文构造层,
// 明确不反向依赖编排层),本文件守住这条唯一来源。
func TestKeyTaskMatchesContextxStateKey(t *testing.T) {
	if KeyTask != contextx.StateKeyTask {
		t.Errorf("compiler.KeyTask=%q 与 contextx.StateKeyTask=%q 不一致(唯一来源被破坏)",
			KeyTask, contextx.StateKeyTask)
	}
}

// TestKeyTaskValueIsStable 锁住键名本身:state_json 里存量数据用的就是
// "task" 这个键,改名会让历史会话状态里的任务读不出来。
func TestKeyTaskValueIsStable(t *testing.T) {
	if KeyTask != "task" {
		t.Errorf("会话状态键必须是 %q(state_json 存量数据用的是这个键),实际 %q", "task", KeyTask)
	}
}
