package logx

import (
	"strings"
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
