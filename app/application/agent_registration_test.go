package application

import (
	"fmt"
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 本文件锁住"内置 Agent 注册失败"不得被完全吞掉(迭代 19 / P3-3)。
//
// 背景:`reloadAgents` 里 4 处 `_ = a.Agents.Register(...)` —— 内置 Agent
// (claude-code / pi-agent / mock-claude / mock-pi)注册失败被完全吞掉:
// 该 Agent 不在注册表里(Agents 页不列出它,引用它的节点在运行期报
// "Agent 不存在"),而**一行日志都没有** —— 内置 Agent 缺失这件事完全不可见。
//
// 可达性判断(见报告):注册表在 NewApp 里是**新建的空表**,且 4 个内置
// Agent 的 ID 互不重复(claude-code / pi-agent / mock-claude / mock-pi),
// 因此**当前代码路径下注册失败不可达**。正因为不可达,把它做成"启动即失败"
// 不会误伤;而"不可达"不等于"永远不可达" —— 将来有人把 reloadAgents
// 改成可重复调用(重建适配器),失败就会重新变成静默故障。
//
// 因此:注册失败必须**上抛**(NewApp 失败 → 启动即失败)并**记 ERROR 日志**。

// failOnRegister 让 Register 在指定 ID 上失败(故障注入用)。
type failOnRegister struct {
	agent.Registry
	failID string
}

func (r *failOnRegister) Register(a agent.Agent) error {
	if a.ID() == r.failID {
		return fmt.Errorf("注入的注册失败")
	}
	return r.Registry.Register(a)
}

// TestBuiltinAgentRegisterFailureIsLogged 内置 Agent 注册失败必须留下 ERROR 日志。
//
// 此前 `_ = a.Agents.Register(...)` 把错误吞得干干净净:内置 Agent 缺失
// 这件事在日志里查无实据。
func TestBuiltinAgentRegisterFailureIsLogged(t *testing.T) {
	a := newTestApp(t)
	// 用一张**空表**当注册表:避开 NewApp 里已经注册过的同名 Agent
	// (重复 ID 也会让 Register 失败,那不是本用例要注入的故障)。
	a.Agents = &failOnRegister{Registry: agent.NewRegistry(), failID: "pi-agent"}

	buf, restore := captureLogx(t)
	defer restore()

	err := a.reloadAgents()
	if err == nil {
		t.Fatal("内置 Agent 注册失败必须上抛(启动即失败),不能被吞掉")
	}
	if !strings.Contains(err.Error(), "pi-agent") {
		t.Errorf("错误必须指明失败的是哪个 Agent,实际: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "内置 Agent 注册失败") {
		t.Errorf("内置 Agent 注册失败必须留下日志,实际日志: %q", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("内置 Agent 注册失败必须记 ERROR,实际日志: %q", out)
	}
	if !strings.Contains(out, "pi-agent") {
		t.Errorf("日志必须指明失败的是哪个 Agent,实际日志: %q", out)
	}
}

// TestBuiltinAgentsRegisterSuccessfully 正常路径下 4 个内置 Agent 都要在注册表里。
//
// 这条锁住"上抛"没有把正常注册也弄坏:注册表里必须真的有这 4 个内置
// Agent(Agent 页靠它们列清单,工作流节点靠它们解析引用)。
func TestBuiltinAgentsRegisterSuccessfully(t *testing.T) {
	a := newTestApp(t)

	for _, id := range []string{"claude-code", "pi-agent", "mock-claude", "mock-pi"} {
		if _, err := a.Agents.Get(id); err != nil {
			t.Errorf("内置 Agent %q 必须已注册: %v", id, err)
		}
	}
}
