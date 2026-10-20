package compiler

import (
	"testing"

	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

// 回归(迭代 14B):循环守卫读回的历史记录**类型不对**时必须显式失败。
//
// 此前 readHistory 一律 `_ = json.Unmarshal(raw, &out)`,错误被吞 →
// out 变成空切片,于是:
//   - 停滞检测(len(history) >= 2)永远不成立 → 循环不标记 stuck;
//   - 守卫不升级为人工介入 → 等待用户时的 prompts/responses 选项消失;
//   - 循环一路跑到底,执行还可能落成 COMPLETED —— "该失败却成功"
//     (约定 14:静默成功比崩溃更危险)。
func TestReadHistoryTypeMismatchIsError(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"字符串", "not-an-array"},
		{"对象", map[string]any{"a": "b"}},
		{"含非字符串元素", []any{"ok", 42}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &fakeState{data: map[string]any{"revisit:rev:history": c.val}}
			hist, err := readHistory(st, "revisit:rev:history")
			if err == nil {
				t.Fatalf("readHistory(%#v) = %v, nil;期望显式报错(状态类型不对 = 状态坏了)", c.val, hist)
			}
			if hist != nil {
				t.Errorf("出错时不应返回半截历史: %v", hist)
			}
		})
	}
}

// readHistory 的正向用例:正常值必须照常读出(向后兼容的硬要求 ——
// 不能因为加了显式失败就把正常循环判成失败)。
func TestReadHistoryAcceptsValidValues(t *testing.T) {
	// 键不存在 = 没有历史(不是损坏)
	st := &fakeState{data: map[string]any{}}
	hist, err := readHistory(st, "revisit:rev:history")
	if err != nil {
		t.Fatalf("键不存在时不应报错: %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("键不存在时历史应为空,得到 %v", hist)
	}

	// 内存里的 []string
	st = &fakeState{data: map[string]any{"revisit:rev:history": []string{"a", "b"}}}
	hist, err = readHistory(st, "revisit:rev:history")
	if err != nil {
		t.Fatalf("[]string 应读回: %v", err)
	}
	if len(hist) != 2 || hist[0] != "a" || hist[1] != "b" {
		t.Errorf("[]string 读回 = %v,期望 [a b]", hist)
	}

	// 经 SQLite 的 state_json 往返后是 []any(约定 #2 的同源形态)—— 必须仍然可用
	st = &fakeState{data: map[string]any{"revisit:rev:history": []any{"a", "b"}}}
	hist, err = readHistory(st, "revisit:rev:history")
	if err != nil {
		t.Fatalf("[]any(JSON 往返后)应读回: %v", err)
	}
	if len(hist) != 2 || hist[0] != "a" || hist[1] != "b" {
		t.Errorf("[]any 读回 = %v,期望 [a b]", hist)
	}
}

// 回归(迭代 14B):守卫读到**类型不对**的历史 → 节点显式失败,
// 而不是"当成没有历史"继续跑(那会一路跑到 COMPLETED)。
func TestLoopGuardFailsOnCorruptedHistory(t *testing.T) {
	ctrl, cleanup := newFakeInvocation(t)
	defer cleanup()

	bus := event.NewBus()
	failed := 0
	bus.Subscribe(func(ev event.UIEvent) {
		if ev.Type == event.NodeFailed {
			failed++
		}
	})

	env := &RunEnv{
		Exec: &model.Execution{ID: "exec-loop-guard"},
		Bus:  bus,
		Vars: map[string]any{},
	}
	l := &lowerer{env: env}
	p := &Plan{
		Loop:     &LoopSpec{RevisitNodeID: "rev"},
		Branches: []RouteBranch{{Condition: `decision == "APPROVED"`}},
	}

	// 预置一个**类型不对**的历史(模拟状态被写坏 / 被外部改坏)。
	if err := ctrl.Session().State().Set("revisit:rev:history", "corrupted"); err != nil {
		t.Fatalf("预置状态失败: %v", err)
	}

	guard, err := l.loopGuard(p, "loop")
	if err != nil {
		t.Fatalf("loopGuard: %v", err)
	}
	drain(guard.Run(ctrl))

	abort, _ := ctrl.Session().State().Get(KeyAbort)
	if abort == nil || abort == "" {
		t.Fatalf("历史类型不对时守卫必须中断执行(_abort 为空): %v", abort)
	}
	if abort != "FAILED:rev" {
		t.Errorf("_abort = %v,期望 FAILED:rev", abort)
	}
	status, _ := ctrl.Session().State().Get(StatusKey("rev"))
	if status != string(model.NodeFailed) {
		t.Errorf("节点状态 = %v,期望 %v", status, model.NodeFailed)
	}
	if failed != 1 {
		t.Errorf("应发出 1 条 node.failed 事件,得到 %d 条", failed)
	}
}
