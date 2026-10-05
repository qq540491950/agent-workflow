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
