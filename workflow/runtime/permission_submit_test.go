package runtime

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/permission"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// 本文件锁住"`mode: submit` 的节点也必须受工作流级 permissions 段约束"。
//
// 缺口(迭代 12 交接):运行期的节点权限检查只对 execute|fix 查写权限
// (workflow/runtime/executor.go),而 pi 适配器自己查的是 execute|fix|submit。
// 两边集合不一致 → `mode: submit` 的节点**不受工作流级 permissions 段约束**
// (适配器只看得到全局策略,看不到工作流的收紧段)。
//
// 后果:DSL 里写
//
//	permissions:
//	  pi-agent:
//	    filesystem:
//	      write: false
//
// 收紧 pi-agent 的写权限,但 `mode: submit` 的节点照样跑 —— 用户以为
// 收紧生效了,实际没有(约定 14:用户以为生效了实际没有)。
//
// 现在 mode→是否需要写权限只有一份来源(agent.ModeRequiresWrite),
// 运行期与适配器共用。

// submitPermYAML 是"工作流级 permissions 段把 mock-a 的写权限关掉"的工作流,
// 节点用 mode: submit。
const submitPermYAML = `
version: "1"
permissions:
  mock-a:
    filesystem:
      write: false
workflow:
  id: perm-submit
  name: Perm Submit
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: submit
edges: []
`

// TestSubmitModeObeysWorkflowPermissionOverride 工作流级 permissions 段
// 对 mode: submit 的节点必须生效(写权限被收紧 → 节点失败)。
func TestSubmitModeObeysWorkflowPermissionOverride(t *testing.T) {
	eng := newMinimalEngine(t)
	// 前置:mock-a 全局**允许**写 —— 被拒绝的原因只能是工作流级收紧段,
	// 否则测到的是"Agent 没有策略"而不是"工作流级收紧生效了"。
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: true})

	doc, err := dsl.Parse([]byte(submitPermYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if len(wf.Permission) == 0 {
		t.Fatal("permissions 段未被解析(检查 YAML 层级:它是文档根级段)")
	}
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("合法 permissions 应通过校验: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionFailed)
	if !strings.Contains(exec.Error, "fs:write") {
		t.Errorf("mode: submit 的节点必须受工作流级 permissions 段约束,失败原因应为写权限不足,实际: %q", exec.Error)
	}
}

// TestSubmitModeAllowedWhenWritePermitted 对照组:写权限没被收紧时,
// mode: submit 的节点必须**正常跑完**(不能把 submit 模式整体堵死)。
func TestSubmitModeAllowedWhenWritePermitted(t *testing.T) {
	yml := `
version: "1"
workflow:
  id: perm-submit-ok
  name: Perm Submit OK
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: submit
edges: []
`
	eng := newMinimalEngine(t)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionCompleted)
	if exec.Error != "" {
		t.Errorf("写权限未被收紧时 submit 节点必须成功,实际失败: %q", exec.Error)
	}
}
