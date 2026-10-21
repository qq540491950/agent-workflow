package model

// 本文件锁住"mode → 上下文槽位"的映射是**全覆盖**的(迭代 28 的 P2-1)。
//
// 背景:submit 此前是唯一 ContextSlot 为空串的合法 mode →
// contextx.SaveNodeOutput 的 "slot == \"\"" 分支什么都不写 →
// 会话状态里的 last_output 留着**上一个节点的旧值** → 下游
// {{last_output}} 拿到陈旧上下文照常执行,而"空槽位告警"只报"空"、
// 不报"陈旧" → 全程无告警(约定 14 同族)。
//
// 结构性修法:mode → 槽位的映射**全覆盖** —— 每个合法 mode 都必须刷新
// 一个槽位(专属槽位或 last_output)。这样"陈旧"在结构上就不可能发生:
// 每个节点要么刷新了自己的槽位,要么刷新了 last_output。
//
// 本文件是 model 包内的**纯单元**守卫(不依赖 contextx / runtime):
// 任何"给某个 mode 加一个空槽位"的改动都会在这里立刻失败。

import "testing"

// TestEveryValidModeHasContextSlot 每个合法 mode 都必须声明一个
// **非空**的上下文槽位。
//
// 断言强度:不是"submit 有槽位"这一条,而是**遍历全部合法 mode** ——
// 以后再新增一个不带槽位的 mode(哪怕是另一个名字),这里同样失败。
func TestEveryValidModeHasContextSlot(t *testing.T) {
	modes := AllModes()
	if len(modes) == 0 {
		t.Fatal("AllModes() 为空:mode 取值域不能为空")
	}
	for _, mode := range modes {
		spec, ok := ModeSpecFor(mode)
		if !ok {
			t.Errorf("mode %q 在 AllModes() 里却没有 ModeSpec", mode)
			continue
		}
		if spec.ContextSlot == "" {
			t.Errorf("mode %q 的 ContextSlot 为空:该 mode 的产物不进上下文链 → "+
				"槽位里会留着上一个节点的**陈旧值**,且没有任何告警(约定 14 同族)", mode)
		}
	}
}

// TestSubmitWritesLastOutput submit 的产物必须进 last_output 槽位。
//
// 为什么是 last_output 而不是"不进上下文链":submit(提交代码)也是
// "一次节点产出"(提交摘要 / commit message),下游引用 {{last_output}}
// 时应当拿到**它**。不写 → 槽位留旧值 → 下游拿着陈旧上下文照常执行。
func TestSubmitWritesLastOutput(t *testing.T) {
	spec, ok := ModeSpecFor(ModeSubmit)
	if !ok {
		t.Fatalf("mode %q 不在取值域内", ModeSubmit)
	}
	if spec.ContextSlot != ContextSlotLastOutput {
		t.Errorf("submit 的 ContextSlot = %q,want %q", spec.ContextSlot, ContextSlotLastOutput)
	}
	if !spec.RequiresWrite {
		t.Error("submit 隐含写操作(git commit),RequiresWrite 必须为 true")
	}
}

// TestOutputProducingModesShareLastOutput execute / fix / submit 三个
// mode 的产物都归 last_output —— 它们都是"最近一次节点输出"的候选。
//
// 断言的是**集合关系**而不是逐条 if:以后再加一个"执行类"mode,
// 这里会提示它也需要一个槽位(而不是让它悄悄留空)。
func TestOutputProducingModesShareLastOutput(t *testing.T) {
	for _, mode := range []string{ModeExecute, ModeFix, ModeSubmit} {
		spec, ok := ModeSpecFor(mode)
		if !ok {
			t.Errorf("mode %q 不在取值域内", mode)
			continue
		}
		if spec.ContextSlot != ContextSlotLastOutput {
			t.Errorf("mode %q 的 ContextSlot = %q,want %q", mode, spec.ContextSlot, ContextSlotLastOutput)
		}
	}
}
