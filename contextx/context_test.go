package contextx

import (
	"errors"
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

// fakeWriter 是 StateWriter 的内存实现(测试专用)。
type fakeWriter struct {
	m    map[string]any
	err  error
	sets []string
}

func (f *fakeWriter) Get(k string) (any, bool) {
	v, ok := f.m[k]
	return v, ok
}

func (f *fakeWriter) Set(k string, v any) error {
	if f.err != nil {
		return f.err
	}
	f.sets = append(f.sets, k)
	f.m[k] = v
	return nil
}

func TestSaveNodeOutput(t *testing.T) {
	m := NewManager()
	state := &fakeWriter{m: map[string]any{}}
	if err := m.SaveNodeOutput("plan", "plan", map[string]any{"summary": "S", "output": "O"}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if state.m[StateKeyPlan] != "O" {
		t.Errorf("plan output should win: %+v", state.m)
	}
	if err := m.SaveNodeOutput("review", "review", map[string]any{"summary": "REJ", "issues": []string{"a"}}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if state.m[StateKeyReview] != "REJ" {
		t.Errorf("review summary: %+v", state.m)
	}
	if _, ok := state.m[StateKeyIssues]; !ok {
		t.Error("issues should be stored")
	}
}

// TestSaveNodeOutputFallsBackToSummaryWhenOutputEmpty output 为空串时必须
// 退回 summary。
//
// 回归(第 19 轮):节点的 output 键**总是存在**(哪怕是空串),"键存在就用"
// 于是把空串写进 plan 槽位 —— 只产出 summary 的 Agent(绝大多数)写出的
// plan 恒为空串,下游照样拿不到方案。
func TestSaveNodeOutputFallsBackToSummaryWhenOutputEmpty(t *testing.T) {
	m := NewManager()
	state := &fakeWriter{m: map[string]any{}}
	if err := m.SaveNodeOutput("plan", "plan", map[string]any{"summary": "只有摘要", "output": ""}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if state.m[StateKeyPlan] != "只有摘要" {
		t.Errorf("output 为空时应退回 summary: %+v", state.m)
	}
}

// TestSaveNodeOutputWritesLiveState 写入的是**调用方给的状态**,不是副本。
//
// 回归(第 19 轮 P0):此前入参是 map[string]any,生产调用方传的是
// e.stateView(st) —— 一次性副本,写完即丢 → 整条跨节点上下文链是死的。
// 本用例锁住"写进去的就是会话状态本身"。
func TestSaveNodeOutputWritesLiveState(t *testing.T) {
	m := NewManager()
	state := &fakeWriter{m: map[string]any{}}
	if err := m.SaveNodeOutput("plan", "plan", map[string]any{"summary": "S", "output": "O"}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	// 写过的键必须出现在状态里(而不是某个副本里)。
	if got, ok := state.Get(StateKeyPlan); !ok || got != "O" {
		t.Errorf("plan 未写进会话状态: %+v", state.m)
	}
	if len(state.sets) == 0 {
		t.Error("没有发生任何写入")
	}
}

// TestSaveNodeOutputPropagatesWriteError 写不进会话状态必须返回错误。
//
// 调用方据此让节点失败:写不进 = 上下文链断了,下游会拿到空上下文照常
// 执行(约定 #14:该失败却成功等同 P0)。
func TestSaveNodeOutputPropagatesWriteError(t *testing.T) {
	m := NewManager()
	state := &fakeWriter{m: map[string]any{}, err: errors.New("disk on fire")}
	err := m.SaveNodeOutput("plan", "plan", map[string]any{"summary": "S", "output": "O"}, state)
	if err == nil {
		t.Fatal("写入失败必须返回错误(不得静默吞掉)")
	}
	if !strings.Contains(err.Error(), StateKeyPlan) {
		t.Errorf("错误信息应点名状态键: %q", err)
	}
}

// TestSaveNodeOutputSkillWritesLastOutput skill 节点的产物写进
// last_output 槽位(此前 skill 分支根本没有任何槽位,产物直接丢失)。
func TestSaveNodeOutputSkillWritesLastOutput(t *testing.T) {
	m := NewManager()
	state := &fakeWriter{m: map[string]any{}}
	if err := m.SaveNodeOutput("skill", "sk", map[string]any{"summary": "SKILL-OUT", "output": "SKILL-OUT"}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if state.m[StateKeyLastOutput] != "SKILL-OUT" {
		t.Errorf("skill 产物应写进 last_output: %+v", state.m)
	}
}

// TestBuildAndSaveShareKeyNames Build(读)与 SaveNodeOutput(写)必须用
// 同一份键名 —— 拼写漂移会让整条链静默失效(下游读到 nil → 渲染空串)。
func TestBuildAndSaveShareKeyNames(t *testing.T) {
	m := NewManager()
	state := &fakeWriter{m: map[string]any{}}
	if err := m.SaveNodeOutput("plan", "plan", map[string]any{"summary": "S", "output": "PLAN-TEXT"}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if err := m.SaveNodeOutput("review", "review", map[string]any{"summary": "REVIEW-TEXT", "issues": []string{"i"}}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if err := m.SaveNodeOutput("test", "test", map[string]any{"summary": "S", "output": "TEST-TEXT"}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}
	if err := m.SaveNodeOutput("execute", "ex", map[string]any{"summary": "EXEC-TEXT", "output": ""}, state); err != nil {
		t.Fatalf("SaveNodeOutput: %v", err)
	}

	ec := &ExecutionContext{State: state.m}
	rv := m.Build("review", ec, "review")
	fx := m.Build("fix", ec, "fix")

	if rv[StateKeyPlan] != "PLAN-TEXT" {
		t.Errorf("Build 未读到 SaveNodeOutput 写入的 plan: %+v", rv)
	}
	if rv[StateKeyReview] != "REVIEW-TEXT" {
		t.Errorf("Build 未读到 SaveNodeOutput 写入的 review: %+v", rv)
	}
	if rv[StateKeyTestResult] != "TEST-TEXT" {
		t.Errorf("Build 未读到 SaveNodeOutput 写入的 test_result: %+v", rv)
	}
	if fx[StateKeyIssues] == nil {
		t.Errorf("Build 未读到 SaveNodeOutput 写入的 issues: %+v", fx)
	}
	if rv[StateKeyLastOutput] != "EXEC-TEXT" {
		t.Errorf("Build 未读到 SaveNodeOutput 写入的 last_output: %+v", rv)
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
