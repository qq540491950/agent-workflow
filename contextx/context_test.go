package contextx

import (
	"strings"
	"testing"
)

func TestBuildMinimalContext(t *testing.T) {
	m := NewManager()
	ec := &ExecutionContext{
		WorkflowID:  "wf-1",
		ExecutionID: "exec-1",
		Variables:   map[string]any{"max_iter": 3},
		State: map[string]any{
			"task":      "任务",
			"plan":      "方案",
			"git_diff":  "diff --git a",
			"review":    "发现问题",
			"issues":    []string{"i1"},
			"iteration": 2,
		},
	}

	// review:保留 diff/review
	rv := m.Build("review", ec, "review")
	if !strings.Contains(rv["git_diff"].(string), "diff --git") {
		t.Error("review should include git_diff")
	}
	if rv["plan"] != "方案" || rv["task"] != "任务" {
		t.Errorf("review context incomplete: %+v", rv)
	}

	// plan:裁掉 diff/review
	pl := m.Build("plan", ec, "plan")
	if _, ok := pl["git_diff"]; ok {
		t.Error("plan should not include git_diff")
	}
	if _, ok := pl["review"]; ok {
		t.Error("plan should not include review")
	}

	// execute/fix:带上 issues 与 user_instruction
	fx := m.Build("fix", ec, "fix")
	if _, ok := fx["issues"]; !ok {
		t.Error("fix should include issues")
	}
}

func TestTruncate(t *testing.T) {
	m := NewManager()
	m.MaxDiffChars = 10
	got := m.truncate(strings.Repeat("x", 100))
	if len(got) <= 10 || !strings.Contains(got, "截断") {
		t.Errorf("truncate failed: %q", got)
	}
	if m.truncate("short") != "short" {
		t.Error("short text should not be truncated")
	}
}

func TestSaveNodeOutput(t *testing.T) {
	m := NewManager()
	state := map[string]any{}
	m.SaveNodeOutput("plan", "plan", map[string]any{"summary": "S", "output": "O"}, state)
	if state["plan"] != "O" {
		t.Errorf("plan output should win: %+v", state)
	}
	m.SaveNodeOutput("review", "review", map[string]any{"summary": "REJ", "issues": []string{"a"}}, state)
	if state["review"] != "REJ" {
		t.Errorf("review summary: %+v", state)
	}
	if _, ok := state["issues"]; !ok {
		t.Error("issues should be stored")
	}
}
