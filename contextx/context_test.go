package contextx

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
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

// 回归:diff 截断按字节切,会把中文字切成两半,Prompt 里出现乱码。
func TestTruncateKeepsRuneBoundary(t *testing.T) {
	m := NewManager()
	m.SetMaxDiffChars(5) // 5 字节正好落在"中"字(3 字节)中间
	got := m.truncate(strings.Repeat("中", 10))
	if !utf8.ValidString(got) {
		t.Errorf("截断切碎了 UTF-8 字符: %q", got)
	}
	if !strings.Contains(got, "截断") {
		t.Errorf("应带截断说明: %q", got)
	}
}

// Build 返回的变量表必须与执行状态解耦(约定 15:共享可变状态)。
func TestBuildCopiesVariables(t *testing.T) {
	m := NewManager()
	ec := &ExecutionContext{
		Variables: map[string]any{"max_iter": 3},
		State:     map[string]any{},
	}
	data := m.Build("plan", ec, "plan")
	vars, ok := data["variables"].(map[string]any)
	if !ok {
		t.Fatalf("variables 应为 map[string]any, 实际 %T", data["variables"])
	}
	vars["max_iter"] = 999
	if ec.Variables["max_iter"] != 3 {
		t.Errorf("修改返回值污染了执行状态: %v", ec.Variables)
	}
}

// SetMaxDiffChars 生效。
func TestSetMaxDiffChars(t *testing.T) {
	m := NewManager()
	m.SetMaxDiffChars(10)
	got := m.truncate(strings.Repeat("x", 100))
	if !strings.Contains(got, "截断") {
		t.Errorf("SetMaxDiffChars 未生效: %q", got)
	}
}

// Build 的并发调用(约定 15:共享可变状态加锁)。
func TestManagerConcurrentBuild(t *testing.T) {
	m := NewManager()
	ec := &ExecutionContext{
		WorkflowID:  "wf-1",
		ExecutionID: "exec-1",
		Variables:   map[string]any{"max_iter": 3},
		State: map[string]any{
			"task":     "任务",
			"git_diff": strings.Repeat("x", 20000),
		},
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Build("review", ec, "review")
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.SetMaxDiffChars(100 + i)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
