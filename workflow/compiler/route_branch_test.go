package compiler

// 本文件锁住"出口分支下标写不进会话状态"时的行为(第 19 轮 P3-1)。
//
// 背景:route:<决策节点>:branch 是**恢复路径回放分支**的唯一依据
// (见 runtime/state_mask.go 的"output 键的双重身份"一节与本文件
// selectExitBranch 的注释)。此前 selectExitBranch 里的写入是
// `_ = st.Set(RouteBranchKey(decisionNodeID), idx)` —— **错误被吞掉**。
//
// 吞掉的后果不是"少记一个键":写失败时恢复会**重新求值**出口条件,
// 而求值输入是**脱敏副本**(output)且可能被后续节点覆盖 ——
// "人批准 A 分支,恢复后走 B 分支"(约定 #14:走错分支比崩溃更危险,
// 用户看不到任何异常,只看到流程跑完了)。
//
// 契约:写失败 → 记 WARN + **显式让节点失败** + 写可读原因。

import (
	"errors"
	"strings"
	"testing"

	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

// failRouteState 是"写 route:<id>:branch 会失败"的 StateAccess。
type failRouteState struct {
	fakeState
	failKey string
}

func (f *failRouteState) Set(key string, val any) error {
	if key == f.failKey {
		return errors.New("disk on fire")
	}
	return f.fakeState.Set(key, val)
}

// TestSelectExitBranchReportsWriteFailure 写不进状态必须返回错误。
//
// 修前:selectExitBranch 吞掉错误,返回 (idx, true, nil) —— 调用方以为
// 路由依据已记录,恢复时却只能重新求值(可能走另一条分支)。
func TestSelectExitBranchReportsWriteFailure(t *testing.T) {
	l := &lowerer{env: &RunEnv{Vars: map[string]any{}}}
	st := &failRouteState{
		fakeState: fakeState{data: map[string]any{}},
		failKey:   RouteBranchKey("dec"),
	}
	branches := []RouteBranch{{Condition: ""}} // 无条件兜底分支

	idx, ok, err := l.selectExitBranch(branches, st, "dec", false)
	if err == nil {
		t.Fatalf("写入失败必须返回错误,实际 (idx=%d, ok=%v, err=nil)", idx, ok)
	}
	if !strings.Contains(err.Error(), RouteBranchKey("dec")) {
		t.Errorf("错误信息应点名状态键 %q: %q", RouteBranchKey("dec"), err)
	}
	// 写失败时不得声称"已选定分支"(ok=true 会让调用方继续往下走,
	// 而路由依据其实没记上)。
	if ok {
		t.Errorf("写入失败时不得返回 ok=true(idx=%d)", idx)
	}
}

// TestSelectExitBranchSucceedsNormally 正常路径不受影响:写入成功时
// 返回选中的分支,并且状态里确实记下了下标。
func TestSelectExitBranchSucceedsNormally(t *testing.T) {
	l := &lowerer{env: &RunEnv{Vars: map[string]any{}}}
	st := &fakeState{data: map[string]any{}}
	branches := []RouteBranch{
		{Condition: `decision == "APPROVED"`},
		{Condition: ""}, // 兜底
	}
	idx, ok, err := l.selectExitBranch(branches, st, "dec", false)
	if err != nil {
		t.Fatalf("正常路径不应报错: %v", err)
	}
	if !ok || idx != 1 {
		t.Fatalf("应选中兜底分支 (idx=1),实际 (idx=%d, ok=%v)", idx, ok)
	}
	got, found := st.Get(RouteBranchKey("dec"))
	if !found {
		t.Fatalf("出口分支下标没有写进状态: %+v", st.data)
	}
	if got != 1 {
		t.Errorf("记录的下标 = %v,want 1", got)
	}
}

// TestAbortRouteRecordFailed 写失败时必须显式失败:置中断标记、写可读
// 原因、标记节点失败、发 NodeFailed 事件。
//
// 修前:什么都不做(错误被吞)→ 执行照常 COMPLETED,用户看不到任何异常。
func TestAbortRouteRecordFailed(t *testing.T) {
	bus := event.NewBus()
	bus.KeepHistory = true
	exec := &model.Execution{ID: "exec-route"}
	env := &RunEnv{Exec: exec, Bus: bus}
	st := &fakeState{data: map[string]any{}}
	l := &lowerer{env: env}

	l.abortRouteRecordFailed(nil, nil, st, env, "dec", errors.New("disk on fire"))

	if v, ok := st.Get(KeyAbort); !ok || v != "FAILED:dec" {
		t.Errorf("中断标记 = %v,want FAILED:dec", v)
	}
	msg, _ := st.Get("_error")
	text, _ := msg.(string)
	if !strings.Contains(text, RouteBranchKey("dec")) {
		t.Errorf("失败原因应点名状态键: %q", text)
	}
	if !strings.Contains(text, "恢复") {
		t.Errorf("失败原因应说明后果(恢复时无法回放分支): %q", text)
	}
	if v, ok := st.Get(StatusKey("dec")); !ok || v != string(model.NodeFailed) {
		t.Errorf("节点状态 = %v,want %s", v, model.NodeFailed)
	}
	if reason, ok := env.abortReason(); !ok || reason != "FAILED:dec" {
		t.Errorf("env 中断原因 = %q,want FAILED:dec", reason)
	}
	found := false
	for _, ev := range bus.History() {
		if ev.Type == event.NodeFailed {
			found = true
		}
	}
	if !found {
		t.Errorf("应发出 NodeFailed 事件,实际事件流: %+v", bus.History())
	}
}
