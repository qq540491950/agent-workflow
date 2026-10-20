package contextx

import (
	"strings"
	"sync"
	"testing"
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

// contextxRaceIterations 是并发探针的写侧迭代数(#208)。
//
// 下限保障:迭代数太少 = 恒真的假保护(去掉锁后 -race 仍全绿)。
// 实测(隔离副本,去掉 Manager 的写锁后跑 `go test -race`):
// 200 次 → 5/5 命中 DATA RACE。纯内存写,整个循环 <1ms。
const contextxRaceIterations = 200

// Build 的并发调用(约定 15:共享可变状态加锁)。
//
// **读写窗口必须确定**(#208):旧写法靠 time.Sleep(100ms) 建窗口 ——
// 写者(200 次纯内存写,亚毫秒级)几乎必然在读者就位前就跑完,
// 读写**零重叠**,-race 什么也报不出(恒真的假保护)。
// 现在用**屏障**:读者先就位并持续自旋,写者才开始写 —— 读写必然重叠。
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
	var ready sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done() // 就位信号:屏障 —— 写者开始写时读者已在自旋
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
	ready.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop) // 写者结束 → 读者退出
		for i := 0; i < contextxRaceIterations; i++ {
			m.SetMaxDiffChars(100 + i)
		}
	}()
	wg.Wait()
}
