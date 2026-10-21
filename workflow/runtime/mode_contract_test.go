package runtime

import (
	"sort"
	"testing"

	"agentworkflow/contextx"
	"agentworkflow/workflow/model"
)

// 本文件锁住"mode 的语义表(workflow/model)与上下文层(contextx)同源"。
//
// mode 的取值域与每个 mode 的语义(是否需要写权限 / 产出进哪个上下文
// 槽位)现在只有一份:workflow/model.ModeSpec。此前是三份互不相同的
// 清单(agent 的写权限集合、contextx 的槽位映射、前端下拉),同一个
// mode 在不同层含义不同 —— `mode: submit` 在上下文层根本没有槽位,
// `mode: skill` 明明是**节点类型**却出现在上下文层的 mode 清单里。
//
// 本文件是 model ↔ contextx 的**漂移守卫**:任何一边单独改(比如
// contextx 新增一个 mode 分支)而另一边没跟上,这里立刻失败。

// slotRecorder 记录 contextx.SaveNodeOutput 写进了哪些键。
type slotRecorder struct{ keys map[string]any }

func (r *slotRecorder) Set(k string, v any) error {
	if r.keys == nil {
		r.keys = map[string]any{}
	}
	r.keys[k] = v
	return nil
}

func (r *slotRecorder) sortedKeys() []string {
	out := make([]string, 0, len(r.keys))
	for k := range r.keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestModeContextSlotMatchesContextx 每个 mode 的产出必须写进 model
// ModeSpec 声明的那个槽位;声明"不进上下文链"的 mode 必须什么都不写。
func TestModeContextSlotMatchesContextx(t *testing.T) {
	mgr := contextx.NewManager()
	for _, mode := range model.AllModes() {
		spec, ok := model.ModeSpecFor(mode)
		if !ok {
			t.Fatalf("mode %q 在 AllModes 里却没有 ModeSpec", mode)
		}
		rec := &slotRecorder{}
		result := map[string]any{"summary": "结论", "output": "产物"}
		if err := mgr.SaveNodeOutput(mode, "n1", result, rec); err != nil {
			t.Fatalf("SaveNodeOutput(%q): %v", mode, err)
		}
		if spec.ContextSlot == "" {
			if len(rec.keys) != 0 {
				t.Errorf("mode=%q 声明为不进上下文链,实际写入了: %v", mode, rec.sortedKeys())
			}
			continue
		}
		if _, ok := rec.keys[spec.ContextSlot]; !ok {
			t.Errorf("mode=%q 的产出应写进槽位 %q,实际写入: %v",
				mode, spec.ContextSlot, rec.sortedKeys())
		}
	}
}

// TestModelSlotKeysMatchContextx 槽位**键名**也必须两边一致:
// 键名是写进 state_json 的持久化契约,漂移会让存量会话状态读不出来。
func TestModelSlotKeysMatchContextx(t *testing.T) {
	cases := []struct {
		name     string
		modelKey string
		ctxKey   string
	}{
		{"task", model.ContextSlotTask, contextx.StateKeyTask},
		{"plan", model.ContextSlotPlan, contextx.StateKeyPlan},
		{"review", model.ContextSlotReview, contextx.StateKeyReview},
		{"issues", model.ContextSlotIssues, contextx.StateKeyIssues},
		{"test_result", model.ContextSlotTestResult, contextx.StateKeyTestResult},
		{"last_output", model.ContextSlotLastOutput, contextx.StateKeyLastOutput},
		{"git_diff", model.ContextSlotGitDiff, contextx.StateKeyGitDiff},
		{"iteration", model.ContextSlotIteration, contextx.StateKeyIteration},
		{"relevant_files", model.ContextSlotRelevantFiles, contextx.StateKeyRelevantFiles},
		{"user_instruction", model.ContextSlotUserInstruction, contextx.StateKeyUserInstruction},
	}
	for _, c := range cases {
		if c.modelKey != c.ctxKey {
			t.Errorf("槽位 %s:model=%q,contextx=%q(键名漂移会让存量 state_json 读不出来)",
				c.name, c.modelKey, c.ctxKey)
		}
	}
}

// TestSkillPseudoModeIsNotAnAgentMode "skill" 是**节点类型**(skill 节点),
// 不是 agent 节点的 mode:它不在取值域里,但上下文层仍按伪 mode 处理
// (skill 节点的产出要进上下文链)。这里把这条既有语义钉住,免得
// "收敛取值域"顺手把 skill 节点的产出踢出上下文链。
func TestSkillPseudoModeIsNotAnAgentMode(t *testing.T) {
	if model.IsValidMode("skill") {
		t.Error("\"skill\" 是节点类型,不应出现在 agent 节点的 mode 取值域里")
	}
	mgr := contextx.NewManager()
	rec := &slotRecorder{}
	if err := mgr.SaveNodeOutput("skill", "n1", map[string]any{"summary": "S", "output": "O"}, rec); err != nil {
		t.Fatalf("SaveNodeOutput(skill): %v", err)
	}
	if _, ok := rec.keys[model.ContextSlotLastOutput]; !ok {
		t.Errorf("skill 伪 mode 的产出应写进 %q,实际写入: %v",
			model.ContextSlotLastOutput, rec.sortedKeys())
	}
}
