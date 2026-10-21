package runtime

// 本文件锁住"`mode: submit` 的产物必须进上下文链"(迭代 28 的 P2-1)。
//
// 缺陷(P2-1,轮次 84 登记、迭代 27 复审再次确认):
//
//	submit 是**合法 mode**(前端下拉可选、校验期放行、运行期放行),
//	但 model.ModeSpec 给它的 ContextSlot 是**空串** →
//	contextx.SaveNodeOutput 的 "slot == \"\"" 分支直接 return nil,
//	**什么都不写** → 会话状态里的 last_output 留着**上一个节点的旧值**。
//
// 后果(约定 14 同族:"该失败却成功" / "用户以为生效了实际没有"):
//
//	下游节点引用 {{last_output}} 拿到的是**陈旧上下文**(上一个 execute /
//	fix / skill 节点的产物),却**没有任何告警** —— reportEmptySlots 只报
//	"键存在但值为空",不报"值是陈旧的"(非空 → 不触发)。
//	于是下游拿着旧上下文照常执行、照常报 SUCCESS。
//
// 本文件的用例一律走**生产调用路径**(引擎真实跑工作流 → Agent 真实收到
// 提示词),而不是直接调 contextx 的纯函数 —— 纯函数层的单测此前是绿的,
// 却完全没覆盖"submit 的产物到底写没写进会话状态"。

import (
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
)

// TestSubmitOutputReachesDownstreamLastOutput 端到端:submit 节点的产物
// 必须成为下游节点 {{last_output}} 拿到的值。
//
// 修前:下游拿到的是 execute 节点的**旧产物**(submit 什么都没写),
// 且**没有**任何告警 —— 本用例变红。
func TestSubmitOutputReachesDownstreamLastOutput(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "execute":
			return coreagent.AgentResponse{Summary: "执行完成-OLD", Output: "EXECUTE-FULL-TEXT"}
		case "submit":
			return coreagent.AgentResponse{Summary: "提交摘要-NEW", Output: "COMMIT-FULL-TEXT"}
		case "plan":
			return coreagent.AgentResponse{Summary: "ok"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-submit
  name: Context Submit
nodes:
  - id: work
    type: agent
    agent: chain
    mode: execute
    prompt: "任务:{{task}}"
  - id: commit
    type: agent
    agent: chain
    mode: submit
    prompt: "任务:{{task}}"
  - id: after
    type: agent
    agent: chain
    mode: plan
    prompt: "上次输出:{{last_output}}"
edges:
  - from: work
    to: commit
  - from: commit
    to: after
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("plan", 0)
	if got == "" {
		t.Fatal("下游节点没有收到任何提示词")
	}
	if !strings.Contains(got, "提交摘要-NEW") {
		t.Errorf("下游节点收到的提示词里没有 submit 节点的产物: %q", got)
	}
	if strings.Contains(got, "执行完成-OLD") {
		t.Errorf("下游节点拿到的是**陈旧**的 last_output(上一个 execute 节点的产物): %q", got)
	}
	if strings.Contains(got, "{{last_output}}") {
		t.Errorf("提示词里残留未渲染的占位符: %q", got)
	}
}

// TestSubmitIsTheOnlyProducerOfLastOutputBetweenTwoOutputs submit 之后
// 再来一个 execute:下游必须拿到**后一个** execute 的产物(而不是 submit
// 的,也不是更早的)。
//
// 这条锁住的是"last_output = 最近一次产出"的语义:submit 与 execute
// 抢同一个槽位时,**按执行顺序**决定谁赢 —— 槽位不是"submit 专属",
// 也不是"execute 专属"。
func TestSubmitIsTheOnlyProducerOfLastOutputBetweenTwoOutputs(t *testing.T) {
	agt := &chainAgent{id: "chain", respond: func(mode string, call int) coreagent.AgentResponse {
		switch mode {
		case "execute":
			if call == 0 {
				return coreagent.AgentResponse{Summary: "第一次执行"}
			}
			return coreagent.AgentResponse{Summary: "第二次执行-LASTEST"}
		case "submit":
			return coreagent.AgentResponse{Summary: "提交摘要"}
		case "plan":
			return coreagent.AgentResponse{Summary: "ok"}
		}
		return coreagent.AgentResponse{Summary: "ok"}
	}}
	eng, _ := newChainEngine(t, agt)

	yml := `
version: "1"
workflow:
  id: ctx-submit-order
  name: Context Submit Order
nodes:
  - id: work
    type: agent
    agent: chain
    mode: execute
    prompt: "任务:{{task}}"
  - id: commit
    type: agent
    agent: chain
    mode: submit
    prompt: "任务:{{task}}"
  - id: work2
    type: agent
    agent: chain
    mode: execute
    prompt: "任务:{{task}}"
  - id: after
    type: agent
    agent: chain
    mode: plan
    prompt: "上次输出:{{last_output}}"
edges:
  - from: work
    to: commit
  - from: commit
    to: work2
  - from: work2
    to: after
`
	runChainWorkflow(t, eng, yml)

	got := agt.promptOf("plan", 0)
	if !strings.Contains(got, "第二次执行-LASTEST") {
		t.Errorf("下游节点拿到的不是最近一次节点产物: %q", got)
	}
	if strings.Contains(got, "提交摘要") {
		t.Errorf("下游节点拿到了更早的 submit 产物(槽位应按执行顺序刷新): %q", got)
	}
}
