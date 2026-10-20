package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentworkflow/permission"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

// 本文件锁住"工作流级 permissions 段"在**运行期**的消费点。
//
// 校验期(workflow/validator)已经用 permission.ValidateOverrides 拦下
// 非法结构;运行期是纵深防御:历史快照(规则之前保存的工作流、备份恢复
// 出来的执行)未必经过当次校验。
//
// 运行期此前用的是 Apply(= ApplyWorkflowOverrides):遇到非法结构把该
// Agent 的权限**全拒**并打一条 WARN,执行继续跑,最终失败原因变成
// "agent 无权执行 fs:write" —— 真正的原因("permissions 段写错了")被
// 掩盖,用户被带偏(约定 #14:静默降级同族问题)。
//
// 现在运行期用 ApplyChecked:非法结构 → 执行**明确失败**,失败原因里
// 直接写明"权限覆盖非法"。

// permWorkflowYAML 是"先在人工节点暂停"的工作流(用于拿到一个可被
// 注入坏快照的执行)。
const permWorkflowYAML = `
version: "1"
workflow:
  id: perm-guard
  name: Perm Guard
nodes:
  - id: gate
    type: human
    prompt: 确认继续
edges: []
`

// injectIllegalPermission 直接改库里的快照,塞进一份非法的 permissions。
//
// 这模拟的是"执行快照来自校验规则之前"的历史数据(备份恢复、手工改库),
// 走的是 Start 之外的路径 —— Start 已被校验期拦下。
func injectIllegalPermission(t *testing.T, eng *Engine, execID string) {
	t.Helper()
	cur, err := eng.Repo.GetExecution(execID)
	if err != nil {
		t.Fatalf("读取执行: %v", err)
	}
	if cur.Snapshot == nil {
		t.Fatal("执行缺少快照,无法注入")
	}
	cur.Snapshot.Permission = map[string]any{
		"pi-agent": map[string]any{
			"filesystem": map[string]any{"write": "no"}, // 值不是布尔 → 非法
		},
	}
	raw, err := json.Marshal(cur.Snapshot)
	if err != nil {
		t.Fatalf("序列化快照: %v", err)
	}
	if _, err := eng.Repo.SQL().Exec(
		`UPDATE executions SET snapshot_json=? WHERE id=?`, string(raw), execID); err != nil {
		t.Fatalf("注入坏快照: %v", err)
	}
}

// TestIllegalPermissionFailsExecutionWithReason 非法 permissions 必须让执行
// 明确失败,且失败原因直指"权限覆盖非法"(而不是"无权执行 fs:write")。
func TestIllegalPermissionFailsExecutionWithReason(t *testing.T) {
	eng := newMinimalEngine(t)
	doc, err := dsl.Parse([]byte(permWorkflowYAML))
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
	exec = waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)

	// 快照里塞入非法 permissions(模拟历史数据/备份恢复)
	injectIllegalPermission(t, eng, exec.ID)

	if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionFailed)
	if exec.Error == "" {
		t.Fatal("失败执行必须带失败原因")
	}
	if !strings.Contains(exec.Error, "权限") {
		t.Errorf("失败原因必须指向权限覆盖非法,实际: %q", exec.Error)
	}
}

// TestStartRejectsIllegalPermissionWithReason 非法 permissions 的工作流
// 不得被运行 —— 且错误消息必须**指明**是权限覆盖的问题。
//
// 校验期已拦住非法结构(workflow/validator:INVALID_PERMISSION_OVERRIDE);
// 运行期的 Start 是第二道门。两道门都要给出可读的原因:此前 Start 的
// 错误消息只有"工作流校验失败"六个字,用户既定位不到也改不了(死路)。
//
// 注意:permissions 是**文档根级**的段(与 workflow / nodes 同级),
// 写进 workflow: 段里会被解析器丢掉 —— 那一节不是"配置"而是 workflow 的
// 元数据(DSL 文档结构见 workflow/dsl/dsl.go)。
func TestStartRejectsIllegalPermissionWithReason(t *testing.T) {
	yml := `
version: "1"
permissions:
  pi-agent:
    filesystem:
      write: "no"
workflow:
  id: perm-bad
  name: Perm Bad
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`
	eng := newMinimalEngine(t)
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	// 前置:permissions 段必须真的被解析进来(否则下面的断言测的是别的东西)
	if len(wf.Permission) == 0 {
		t.Fatal("permissions 段未被解析(检查 YAML 层级:它是文档根级段)")
	}
	_, err = eng.Start(context.Background(), wf, "t", nil)
	if err == nil {
		t.Fatal("非法 permissions 的工作流不得被运行")
	}
	if !strings.Contains(err.Error(), "权限") {
		t.Errorf("错误消息必须指明是权限覆盖非法,实际: %q", err.Error())
	}
}

// TestLegalPermissionOverrideStillApplies 合法覆盖仍然生效
// (不能因为改成 fail closed 就把正常路径也堵死)。
//
// 覆盖把 mock-a 的 fs:read 关掉 → agent 节点因权限不足失败。
// 前置:mock-a 必须本来就有读权限(否则测到的是"Agent 没有策略"
// 而不是"覆盖生效了")。
func TestLegalPermissionOverrideStillApplies(t *testing.T) {
	yml := `
version: "1"
permissions:
  mock-a:
    filesystem:
      read: false
workflow:
  id: perm-ok
  name: Perm OK
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`
	eng := newMinimalEngine(t)
	// 前置:mock-a 默认可读
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true, FilesystemWrite: true})
	doc, err := dsl.Parse([]byte(yml))
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
	if !strings.Contains(exec.Error, "fs:read") {
		t.Errorf("失败原因应为权限不足,实际: %q", exec.Error)
	}
}
