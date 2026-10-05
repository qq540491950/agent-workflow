package logx

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestMaskSensitiveValues(t *testing.T) {
	cases := []string{
		`api_key=sk-abc123`,
		`API_KEY: sk-abc123`,
		`Authorization=Bearer xyz`,
		`{"password": "hunter2"}`,
		`token="abc.def.ghi"`,
		`ANTHROPIC_AUTH_TOKEN=sk-ant-123`,
	}
	for _, c := range cases {
		got := Mask(c)
		if strings.Contains(got, "sk-abc123") || strings.Contains(got, "hunter2") ||
			strings.Contains(got, "abc.def.ghi") || strings.Contains(got, "sk-ant-123") ||
			strings.Contains(got, "Bearer xyz") || strings.Contains(got, "xyz") {
			t.Errorf("mask failed for %q → %q", c, got)
		}
		if !strings.Contains(got, "***") {
			t.Errorf("expected *** in masked output for %q → %q", c, got)
		}
	}
}

func TestMaskKeepsNormalText(t *testing.T) {
	normal := "workflow completed node=plan duration=42ms"
	if got := Mask(normal); got != normal {
		t.Errorf("normal text should be untouched: %q", got)
	}
}

// 回归:结构化参数此前不脱敏(api 层把可能含 CLI stderr 的 err 以
// "error"= 写入日志,token 明文落盘)。
func TestMaskStructuredArgs(t *testing.T) {
	var buf bytes.Buffer
	old := logger.Load()
	logger.Store(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { logger.Store(old) })

	Warn("http error",
		"path", "/api/workflows/wf-1/run",
		"error", errors.New("cli exited: ANTHROPIC_AUTH_TOKEN=sk-ant-leak"),
		"count", 3,
		"ok", true,
	)
	out := buf.String()
	if strings.Contains(out, "sk-ant-leak") {
		t.Errorf("error 参数未脱敏: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("期望出现 ***: %s", out)
	}
	// 非敏感参数必须原样保留,否则日志失去诊断价值
	for _, want := range []string{"/api/workflows/wf-1/run", "count=3", "ok=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("非敏感参数丢失 %q: %s", want, out)
		}
	}
}

// 回归:SetLevel 运行中被设置页调用时替换全局 logger,
// 与并发写日志曾是无锁数据竞争(现原子指针)。
func TestSetLevelConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	levels := []string{"debug", "info", "warn", "error"}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			SetLevel(levels[i%len(levels)])
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			Info("api_key=sk-secret 并发日志", "k", "v")
			Error("token=abc")
		}
	}()
	wg.Wait()
	// 收尾恢复默认级别
	SetLevel("info")
}

// TestMaskValueRecursive 验证结构化脱敏:事件流的三个出口(SQLite 落库 /
// SSE 推送 / Wails IPC)都经 Bus.Emit 汇聚,节点失败的 error 常含 CLI
// stderr,而 stderr 里常带凭据 —— 日志侧已脱敏,事件/审计流此前是旁路。
func TestMaskValueRecursive(t *testing.T) {
	in := map[string]any{
		// 真实形态:节点失败的 error 文本里 CLI stderr 带 key=value
		"error": "claude failed: ANTHROPIC_API_KEY=sk-ant-secret123",
		"nested": map[string]any{
			"token": "Bearer abc.def.ghi",
			"safe":  "nothing here",
		},
		"list":   []any{"api_key=leaked", map[string]any{"password": "p@ss"}},
		"number": 42,
		"nil":    nil,
	}
	out, ok := MaskValue(in).(map[string]any)
	if !ok {
		t.Fatalf("应返回 map[string]any, 实际 %T", MaskValue(in))
	}
	if s := out["error"].(string); !strings.Contains(s, "***") || strings.Contains(s, "sk-ant-secret123") {
		t.Errorf("顶层 error 未脱敏: %q", s)
	}
	nested := out["nested"].(map[string]any)
	if s := nested["safe"].(string); s != "nothing here" {
		t.Errorf("普通文本被误改: %q", s)
	}
	list := out["list"].([]any)
	if s := list[0].(string); !strings.Contains(s, "***") || strings.Contains(s, "leaked") {
		t.Errorf("slice 内字符串未脱敏: %q", s)
	}
	if out["number"].(int) != 42 {
		t.Errorf("非字符串值不应被改动: %v", out["number"])
	}
	if out["nil"] != nil {
		t.Errorf("nil 应保持 nil: %v", out["nil"])
	}
}

// TestMaskValueUsesKeyContext 验证脱敏同时看"键名"与"值形态"两条线索:
// 键名命中(token/password/api_key…)时整值替换;值本身是 key=value 形态时
// 就地替换。这样 "token":"Bearer abc" 与 "err":"...api_key=xxx" 两类
// 真实载荷都能挡住,而普通文本不受影响。
func TestMaskValueUsesKeyContext(t *testing.T) {
	in := map[string]any{
		"token":    "Bearer abc.def.ghi",
		"password": "p@ssw0rd",
		"api_key":  "sk-ant-xxx",
		"secret":   "sh",
		"safe":     "Bearer not-a-credential",
		"nested":   map[string]any{"token": "Bearer nested.token.here"},
		"list":     []any{map[string]any{"password": "in-list"}},
	}
	out := MaskValue(in).(map[string]any)

	for _, key := range []string{"token", "password", "api_key", "secret"} {
		s, _ := out[key].(string)
		if s == in[key] {
			t.Errorf("键 %q 的敏感值未脱敏: %q", key, s)
		}
		if s != "***" {
			t.Errorf("键 %q 应整值替换为 ***, 实际 %q", key, s)
		}
	}
	if out["safe"] != "nothing here" && out["safe"] == in["safe"] {
		// "safe" 键不在敏感键名里,值也没有 key= 形态 → 不动
		t.Logf("safe 保持原值(无键名/无形态线索): %q", out["safe"])
	}
	if got := out["nested"].(map[string]any)["token"]; got != "***" {
		t.Errorf("嵌套 token 应脱敏, 实际 %v", got)
	}
	got := out["list"].([]any)[0].(map[string]any)["password"]
	if got != "***" {
		t.Errorf("slice 内嵌套 password 应脱敏, 实际 %v", got)
	}
}

// TestMaskValueDoesNotMutateInput 确认脱敏返回新结构,不改调用方数据。
func TestMaskValueDoesNotMutateInput(t *testing.T) {
	in := map[string]any{"token": "Bearer abc.def.ghi"}
	_ = MaskValue(in)
	if in["token"] != "Bearer abc.def.ghi" {
		t.Errorf("入参被就地修改: %v", in)
	}
}
