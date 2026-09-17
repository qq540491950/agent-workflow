package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

func writeFakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestDefaults(t *testing.T) {
	a := New(Config{})
	if a.cfg.Bin != "pi-agent" {
		t.Errorf("default Bin = %q", a.cfg.Bin)
	}
	if a.cfg.DefaultTimeoutSeconds != 900 {
		t.Errorf("default timeout = %d", a.cfg.DefaultTimeoutSeconds)
	}
	if a.ID() != "pi-agent" || a.Name() != "Pi Agent" {
		t.Errorf("ID/Name = %s/%s", a.ID(), a.Name())
	}
}

func TestParseOutputTrailingJSON(t *testing.T) {
	resp := parseOutput([]byte("working...\n{\"status\":\"success\",\"decision\":\"CONTINUE\",\"summary\":\"next round\"}"))
	if resp.Status != coreagent.StatusSuccess || resp.Decision != coreagent.DecisionContinue {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Summary != "next round" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestParseOutputFailedStatus(t *testing.T) {
	resp := parseOutput([]byte("error path\n{\"status\":\"failed\",\"error\":\"tests broke\"}"))
	if resp.Status != coreagent.StatusFailed || resp.Error != "tests broke" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestParseOutputPlainSummary(t *testing.T) {
	resp := parseOutput([]byte("line1\nline2\nline3\nline4\nline5\nline6"))
	if resp.Decision != "" {
		t.Errorf("plain output must not guess decision: %q", resp.Decision)
	}
	// 摘要取前 5 行
	if strings.Contains(resp.Summary, "line6") {
		t.Errorf("summary should be first 5 lines: %q", resp.Summary)
	}
}

// TestPermissionDeniedForWriteModes 验证执行/修复/提交模式需要写权限。
func TestPermissionDeniedForWriteModes(t *testing.T) {
	m := permission.NewManager()
	m.Set("pi-agent", permission.Policy{FilesystemRead: true}) // 无写权限
	a := New(Config{Perms: m})
	for _, mode := range []string{"execute", "fix", "submit"} {
		_, err := a.Execute(context.Background(), coreagent.AgentRequest{Mode: mode})
		if !model.IsKind(err, model.KindPermissionError) {
			t.Errorf("mode %s: err = %v, want PermissionError", mode, err)
		}
	}
	// review 等只读模式不需要写权限
	if _, err := a.Execute(context.Background(), coreagent.AgentRequest{Mode: "review"}); err != nil {
		// review 模式下无权限要求,应走到进程执行(用不存在的 bin 验证错误类型不是 PermissionError)
		if model.IsKind(err, model.KindPermissionError) {
			t.Errorf("review mode should not require write permission: %v", err)
		}
	}
}

func TestPermissionAllowed(t *testing.T) {
	m := permission.NewManager() // pi-agent 默认可写
	bin := writeFakeCLI(t, "fake-pi", `echo '{"status":"success","decision":"DONE","summary":"ok"}'`)
	a := New(Config{Bin: bin, Perms: m})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Mode: "execute"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Decision != coreagent.DecisionDone {
		t.Errorf("decision = %s", resp.Decision)
	}
}

func TestExecuteTimeout(t *testing.T) {
	bin := writeFakeCLI(t, "slow-pi", "sleep 5")
	a := New(Config{Bin: bin, DefaultTimeoutSeconds: 1})
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Mode: "execute"})
	if !model.IsKind(err, model.KindTimeoutError) {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
}

func TestExecuteCancel(t *testing.T) {
	bin := writeFakeCLI(t, "slow-pi", "sleep 5")
	a := New(Config{Bin: bin})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := a.Execute(ctx, coreagent.AgentRequest{Task: "t", Mode: "execute"})
	if !model.IsKind(err, model.KindCancelledError) {
		t.Fatalf("err = %v, want CancelledError", err)
	}
}

// TestExecuteArgs 验证参数组装:mode/task/model/instructions/cwd。
func TestExecuteArgs(t *testing.T) {
	bin := writeFakeCLI(t, "args-pi", `printf 'ARGS:%s\n' "$*"`)
	dir := t.TempDir()
	a := New(Config{Bin: bin, DefaultArgs: []string{"--verbose"}})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task:         "do it",
		Mode:         "fix",
		Model:        "m1",
		Instructions: "be careful",
		WorkingDir:   dir,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{
		"--verbose", "--mode fix", "--task do it",
		"--model m1", "--instructions be careful", "--cwd " + dir,
	} {
		if !strings.Contains(resp.Output, want) {
			t.Errorf("args missing %q in: %s", want, resp.Output)
		}
	}
}

// TestExecuteEnvMerged 验证 BaseURL → PI_BASE_URL 与调用级环境变量。
func TestExecuteEnvMerged(t *testing.T) {
	bin := writeFakeCLI(t, "env-pi", `printf 'K=%s B=%s\n' "$PI_KEY" "$PI_BASE_URL"`)
	a := New(Config{
		Bin:     bin,
		BaseURL: "http://pi-proxy:9000",
		AgentEnv: map[string]string{
			"PI_KEY": "pk-secret",
		},
	})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Mode: "execute"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Output, "K=pk-secret") || !strings.Contains(resp.Output, "B=http://pi-proxy:9000") {
		t.Errorf("env not merged: %s", resp.Output)
	}
}
