package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentworkflow/agent"
	"agentworkflow/agent/claude"
	"agentworkflow/agent/pi"
)

// fakeCLIScript 生成一个把收到的参数与环境变量采样落盘的假 CLI。
func fakeCLIScript(t *testing.T, dir, name, dumpFile string) string {
	t.Helper()
	bin := filepath.Join(dir, name)
	script := "#!/bin/sh\n" +
		"echo \"$@\" > " + dumpFile + "\n" +
		"env | grep -E 'ANTHROPIC|PI_' >> " + dumpFile + " 2>/dev/null || true\n" +
		"printf '{\"result\":\"ok {\\\\\"decision\\\\\": \\\\\"DONE\\\\\"}\",\"is_error\":false}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestClaudeAdapterModelAndEnv(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "args.txt")
	bin := fakeCLIScript(t, dir, "fake-claude-m", dump)

	a := claude.New(claude.Config{
		Bin:      bin,
		Model:    "cfg-model",
		BaseURL:  "https://relay.example.com",
		AgentEnv: map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-secret-123"},
	})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Task: "t", Mode: "review"})
	if err != nil || resp.Status != agent.StatusSuccess {
		t.Fatalf("execute: %v %+v", err, resp)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "--model cfg-model") {
		t.Errorf("default model not passed: %s", text)
	}
	if !strings.Contains(text, "ANTHROPIC_BASE_URL=https://relay.example.com") {
		t.Errorf("base url env missing: %s", text)
	}
	if !strings.Contains(text, "ANTHROPIC_AUTH_TOKEN=sk-secret-123") {
		t.Errorf("agent env missing: %s", text)
	}

	// 节点级 model 覆盖配置级
	if err := os.Remove(dump); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Execute(context.Background(), agent.AgentRequest{Task: "t", Mode: "review", Model: "node-model"})
	raw2, _ := os.ReadFile(dump)
	if !strings.Contains(string(raw2), "--model node-model") {
		t.Errorf("request model should override: %s", raw2)
	}
}

func TestClaudeAdapterMaskedConfig(t *testing.T) {
	cfg := agent.AgentConfig{
		Model: "m1",
		Env:   map[string]string{"TOKEN": "abc", "OTHER": "xyz"},
	}
	masked := cfg.Masked()
	if masked.Env["TOKEN"] != agent.MaskedValue || masked.Env["OTHER"] != agent.MaskedValue {
		t.Fatalf("mask failed: %+v", masked.Env)
	}
	if cfg.Env["TOKEN"] != "abc" {
		t.Fatal("original mutated")
	}
}

func TestPiAdapterModelFlag(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "pi-args.txt")
	bin := filepath.Join(dir, "fake-pi-m")
	script := "#!/bin/sh\necho \"$@\" > " + dump + "\nprintf 'done'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := pi.New(pi.Config{Bin: bin, Model: "pi-default"})
	resp, err := a.Execute(context.Background(), agent.AgentRequest{Task: "t", Mode: "fix", Model: "pi-node"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != agent.StatusSuccess {
		t.Fatalf("status=%s", resp.Status)
	}
	raw, _ := os.ReadFile(dump)
	if !strings.Contains(string(raw), "--model pi-node") {
		t.Errorf("node model flag missing: %s", raw)
	}
	if strings.Contains(string(raw), "pi-default") {
		t.Errorf("node model should win over config model: %s", raw)
	}
}

func TestAgentConfigResolveAndMerge(t *testing.T) {
	cfg := agent.AgentConfig{Model: "m", BaseURL: "https://x", Env: map[string]string{"A": "1"}}
	if cfg.ResolveModel("node-m") != "node-m" || cfg.ResolveModel("") != "m" {
		t.Fatal("ResolveModel priority wrong")
	}
	merged := cfg.MergeEnv(map[string]string{"A": "2", "B": "3"})
	joined := strings.Join(merged, ",")
	if !strings.Contains(joined, "A=2") || !strings.Contains(joined, "B=3") {
		t.Fatalf("request env should win: %v", merged)
	}
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL=https://x") {
		t.Fatalf("base url missing: %v", merged)
	}
}
