package logx

import (
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
