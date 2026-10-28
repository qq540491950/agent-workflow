package runtime

// retry 退避参数(initial_backoff_seconds / max_backoff_seconds,迭代 36)。
//
// Temporal RetryPolicy 的 InitialInterval / MaximumInterval 对齐:
//   - initial:首次重试前的等待;缺省 2s(与历史行为一致 —— 历史 fixed 恒 2s、
//     exponential 首次重试 2s 起步);0 = 立即重试;
//   - max:退避增长的上限;缺省 exponential 30s(历史行为)、fixed 不封顶
//     (无增长,封顶会违背用户显式设置的 initial)。
//
// 本文件锁住:解析(fail closed)+ 延迟序列缺省不变 + 显式配置生效 +
// 端到端(initial=0 的重试不等 2s)。

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentworkflow/workflow/model"
)

func TestRetryBackoffDefaultsPreserved(t *testing.T) {
	cases := []struct {
		name        string
		cfg         map[string]any
		wantInitial float64
		wantMax     float64
	}{
		{"fixed 缺省", map[string]any{}, 2, 0},
		{"exponential 缺省", map[string]any{"backoff": "exponential"}, 2, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &model.Node{ID: "n", Config: map[string]any{"retry": tc.cfg}}
			p, err := retryPolicyOf(node)
			if err != nil {
				t.Fatalf("retryPolicyOf: %v", err)
			}
			if p.InitialSeconds != tc.wantInitial || p.MaxSeconds != tc.wantMax {
				t.Fatalf("initial/max = %v/%v, want %v/%v", p.InitialSeconds, p.MaxSeconds, tc.wantInitial, tc.wantMax)
			}
		})
	}
}

// TestRetryDelaySequenceUnchanged 历史延迟序列逐位不变(缺省配置)。
func TestRetryDelaySequenceUnchanged(t *testing.T) {
	fixed, _ := retryPolicyOf(&model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{}}})
	for _, attempt := range []int{2, 3, 5} {
		if got := fixed.delayFor(attempt); got != 2*time.Second {
			t.Fatalf("fixed attempt=%d: delay = %v, want 2s", attempt, got)
		}
	}
	exp, _ := retryPolicyOf(&model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{"backoff": "exponential"}}})
	want := map[int]time.Duration{2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second, 5: 16 * time.Second}
	for attempt, w := range want {
		if got := exp.delayFor(attempt); got != w {
			t.Fatalf("exponential attempt=%d: delay = %v, want %v", attempt, got, w)
		}
	}
	// 封顶 30s(attempt=7 → 64s → 30s)
	if got := exp.delayFor(7); got != 30*time.Second {
		t.Fatalf("exponential attempt=7 应封顶 30s,实际 %v", got)
	}
}

// TestRetryBackoffKnobsExplicit 显式配置生效。
func TestRetryBackoffKnobsExplicit(t *testing.T) {
	node := &model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{
		"backoff":                 "exponential",
		"initial_backoff_seconds": 1,
		"max_backoff_seconds":     3,
	}}}
	p, err := retryPolicyOf(node)
	if err != nil {
		t.Fatalf("retryPolicyOf: %v", err)
	}
	if p.InitialSeconds != 1 || p.MaxSeconds != 3 {
		t.Fatalf("initial/max = %v/%v, want 1/3", p.InitialSeconds, p.MaxSeconds)
	}
	if got := p.delayFor(2); got != time.Second {
		t.Fatalf("首次重试应 1s,实际 %v", got)
	}
	if got := p.delayFor(3); got != 2*time.Second {
		t.Fatalf("第二次重试应 2s,实际 %v", got)
	}
	if got := p.delayFor(5); got != 3*time.Second {
		t.Fatalf("超上限应封顶 3s,实际 %v", got)
	}
	// fixed 显式 initial(不封顶:fixed 无增长)
	fixedNode := &model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{
		"backoff":                 "fixed",
		"initial_backoff_seconds": 60,
	}}}
	fp, err := retryPolicyOf(fixedNode)
	if err != nil {
		t.Fatalf("retryPolicyOf fixed: %v", err)
	}
	if got := fp.delayFor(2); got != time.Minute {
		t.Fatalf("fixed initial=60 应等 60s,实际 %v", got)
	}
}

// TestRetryBackoffKnobsFailClosed 非法取值 fail closed(字符串/负数/NaN)。
func TestRetryBackoffKnobsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"字符串", "3"},
		{"布尔", true},
		{"负数", -1},
		{"负小数", -0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{
				"initial_backoff_seconds": tc.val,
			}}}
			if _, err := retryPolicyOf(node); err == nil {
				t.Fatalf("%s: 必须报错(fail closed)", tc.name)
			}
		})
	}
	// json.Number 与 float64(1e100)合法但被钳到上界
	node := &model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{
		"initial_backoff_seconds": json.Number("999999999"),
	}}}
	p, err := retryPolicyOf(node)
	if err != nil {
		t.Fatalf("json.Number 不该报错: %v", err)
	}
	if p.InitialSeconds != 86400 {
		t.Fatalf("超上界应钳到 86400,实际 %v", p.InitialSeconds)
	}
	// NaN 的 float64:JSON 里造不出 NaN,但快照路径可能带进来 → fail closed
	if _, err := retryPolicyOf(&model.Node{ID: "n", Config: map[string]any{"retry": map[string]any{
		"initial_backoff_seconds": math.NaN(),
	}}}); err == nil {
		t.Fatal("NaN 必须 fail closed")
	}
}

// TestScriptRetryZeroBackoffFast 端到端:initial=0 的重试不等待。
func TestScriptRetryZeroBackoffFast(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "fast-retry")
	yml := `
version: "1"
workflow:
  id: script-fast-retry
  name: Script Fast Retry
nodes:
  - id: sh
    type: script
    command: test -f "__M__" && echo ok || { touch "__M__"; exit 1; }
    retry:
      max_attempts: 2
      backoff: fixed
      initial_backoff_seconds: 0
edges: []
`
	yml = strings.ReplaceAll(yml, "__M__", marker)
	eng := startScriptEngine(t)
	start := time.Now()
	exec, history := runScriptWorkflow(t, eng, yml)
	elapsed := time.Since(start)
	if exec.State != model.ExecutionCompleted {
		t.Fatalf("执行应 COMPLETED,实际 %s(error=%s)", exec.State, exec.Error)
	}
	if n := retryEventCount(t, history, "sh"); n != 1 {
		t.Fatalf("应恰好 1 条 node.retry 事件,实际 %d", n)
	}
	// 修复前(fixed 缺省 2s)这个用例要睡 2s;0 退避下 0.5s 内必然完成
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("initial=0 的重试不应等待:耗时 %v", elapsed)
	}
}
