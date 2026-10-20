package runtime

// 本文件锁住"裸值形态凭据"的落库脱敏(迭代 12)。
//
// 第十一轮终审实测到的泄漏:
//
//	cli stderr: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc
//
// 这段文本**不含任何键名**(没有 api_key / token / password),
// 于是两条旧规则都不触发:
//
//   - 键名规则(logx.Mask 的 sensitivePattern):没有键名 → 不触发;
//   - 值形态规则(当时的 agent.MaskCredentialValues):要求"键名 + 值"形态,
//     裸值没有键名 → 不触发。
//
// 结果:该 JWT 原样落进 execution_nodes.output / result_json 与
// executions.state_json,并经所有 API 出口(SSE / 执行详情 / 导出)流到前端。
//
// 修法:把"值的形态"规则收敛到 logx(唯一定义处),与键名规则并列 ——
// 于是本文件这类裸值形态在**所有**出口都被盖住。
//
// 本文件只锁**落库侧**(DB 直查):读出口由 app/application 的用例覆盖。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/workflow/model"
)

// bareJWT 是终审实测泄漏的那个 JWT(HS256,形态取自真实签发的 token)。
const bareJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ." +
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

// TestBareCredentialValueMaskedBeforePersist 裸值形态凭据在**落库前**必须脱敏。
//
// 覆盖四条落库通道:
//   - execution_nodes.output(节点明细)
//   - execution_nodes.result_json(节点 outcome 快照)
//   - executions.state_json(会话状态快照)
//   - events.data_json(持久化事件流 → SSE 回放)
func TestBareCredentialValueMaskedBeforePersist(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {OutputTemplate: "cli stderr: Bearer " + bareJWT},
		},
	})
	eng, repo, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: bare-cred
  name: Bare Credential
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)

	// 1. 节点明细
	nodes, err := repo.ListExecutionNodes(exec.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("没有节点明细落库")
	}
	for _, n := range nodes {
		if strings.Contains(n.Output, bareJWT) {
			t.Errorf("execution_nodes.output 落库仍是明文: %q", n.Output)
		}
		if strings.Contains(n.ResultJSON, bareJWT) {
			t.Errorf("execution_nodes.result_json 落库仍是明文: %q", n.ResultJSON)
		}
		if strings.Contains(n.Error, bareJWT) {
			t.Errorf("execution_nodes.error 落库仍是明文: %q", n.Error)
		}
	}
	// 脱敏不得把内容整体抹掉:方案词保留,值替换为 ***
	var out string
	for _, n := range nodes {
		if n.NodeID == "work" {
			out = n.Output
		}
	}
	if !strings.Contains(out, "Bearer") {
		t.Errorf("认证方案词应保留(可读性): %q", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("脱敏后的 output 应出现 ***: %q", out)
	}

	// 2. 会话状态快照
	raw := readStateJSON(t, repo, exec.ID)
	if strings.Contains(raw, bareJWT) {
		t.Errorf("executions.state_json 落库仍是明文: %s", raw)
	}
	snap := decodeStateJSON(t, raw)
	if got := fmt.Sprint(snap["output"]); strings.Contains(got, bareJWT) {
		t.Errorf("状态快照的 output 仍是明文: %q", got)
	}
	// 结构化键必须原样保留(脱敏不得改写执行语义)
	if got := fmt.Sprint(snap["node:work:status"]); got != string(model.NodeSuccess) {
		t.Errorf("结构化键 node:work:status 被改动: %q", got)
	}

	// 3. 持久化事件流(SSE 回放的数据源)
	events, err := repo.ListEvents(exec.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("没有事件落库")
	}
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("序列化事件: %v", err)
		}
		if strings.Contains(string(b), bareJWT) {
			t.Errorf("events 落库仍含凭据明文: %s", b)
		}
	}
}

// TestBareCredentialValueMaskedInFailedNode 失败路径同样必须脱敏。
//
// 节点失败的 error 常带 CLI stderr,而 stderr 里就是这种裸值形态。
func TestBareCredentialValueMaskedInFailedNode(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			"plan": {FailWith: "exit status 1: Bearer " + bareJWT},
		},
	})
	eng, repo, _ := newEngineWithAgent(t, agt)
	wf := parseYAML(t, eng, `
version: "1"
workflow:
  id: bare-cred-fail
  name: Bare Credential Fail
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionFailed)

	nodes, err := repo.ListExecutionNodes(exec.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	for _, n := range nodes {
		if strings.Contains(n.Error, bareJWT) {
			t.Errorf("execution_nodes.error 落库仍是明文: %q", n.Error)
		}
	}
	if raw := readStateJSON(t, repo, exec.ID); strings.Contains(raw, bareJWT) {
		t.Errorf("executions.state_json 落库仍是明文: %s", raw)
	}
}
