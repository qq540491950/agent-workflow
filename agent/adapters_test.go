package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/agent/claude"
	"agentworkflow/agent/pi"
)

// TestClaudeAdapterParsing 用假 CLI 脚本验证 Claude 适配器的解析与权限参数。
func TestClaudeAdapterParsing(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\necho '{\"result\":\"looks good. {\\\"decision\\\": \\\"APPROVED\\\", \\\"summary\\\": \\\"ok\\\"}\",\"is_error\":false}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := claude.New(claude.Config{Bin: bin, DefaultTimeoutSeconds: 10})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{
		Task: "review code", Mode: "review",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != agent.StatusSuccess {
		t.Fatalf("status = %s (%s)", resp.Status, resp.Error)
	}
	if resp.Decision != agent.DecisionApproved {
		t.Errorf("decision = %q, want APPROVED", resp.Decision)
	}
}

// TestClaudeAdapterForbiddenWrite 验证 Claude 适配器拒绝写操作:
// 即便策略配置错误,CLI 参数也强制 --disallowedTools 包含 Write/Edit。
func TestClaudeAdapterForbiddenWrite(t *testing.T) {
	// 通过参数捕获脚本验证 CLI 收到的参数(写入本次测试的临时目录,避免并发冲突)
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-claude-args")
	argsFile := filepath.Join(dir, "captured-args.txt")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done > \"" + argsFile + "\"\nprintf '{\"result\":\"{}\",\"is_error\":false}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := claude.New(claude.Config{Bin: bin})
	if _, err := a.Execute(context.Background(), agent.AgentRequest{Task: "t", Mode: "plan"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file unavailable: %v", err)
	}
	if !strings.Contains(string(raw), "Write,Edit") {
		t.Errorf("disallowedTools missing write tools: %s", raw)
	}
}

// TestPiAdapterStructuredOutput 验证 Pi 适配器的结构化输出解析。
func TestPiAdapterStructuredOutput(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-pi")
	script := "#!/bin/sh\necho 'fixed 2 issues'\necho '{\"status\":\"success\",\"decision\":\"DONE\",\"summary\":\"fixed\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := pi.New(pi.Config{Bin: bin})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Task: "fix", Mode: "fix"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Decision != agent.DecisionDone || resp.Summary != "fixed" {
		t.Errorf("unexpected response: %+v", resp)
	}
	raw, _ := json.Marshal(resp)
	if len(raw) == 0 {
		t.Error("marshal failed")
	}
}

// TestPiAdapterMissingBinary 验证二进制缺失时的结构化错误。
func TestPiAdapterMissingBinary(t *testing.T) {
	a := pi.New(pi.Config{Bin: "/nonexistent/pi-agent"})
	_, err := a.Execute(context.Background(), agent.AgentRequest{Task: "t", Mode: "execute"})
	if err == nil {
		t.Fatal("expected process error")
	}
	if !strings.Contains(err.Error(), "PI_PROCESS_ERROR") {
		t.Errorf("expected structured process error, got %v", err)
	}
}
