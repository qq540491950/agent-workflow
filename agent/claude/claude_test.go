package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/workflow/model"
)

// writeFakeCLI 在临时目录写一个假 claude CLI 脚本。
func writeFakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell 替身仅适用于类 Unix 环境(stubcli 覆盖真实子进程路径)")
	}
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestDefaults(t *testing.T) {
	a := New(Config{})
	if a.cfg.Bin != "claude" {
		t.Errorf("default Bin = %q", a.cfg.Bin)
	}
	if a.cfg.DefaultTimeoutSeconds != 600 {
		t.Errorf("default timeout = %d", a.cfg.DefaultTimeoutSeconds)
	}
	if a.ID() != "claude-code" || a.Name() != "Claude Code" {
		t.Errorf("ID/Name = %s/%s", a.ID(), a.Name())
	}
}

func TestResolveModel(t *testing.T) {
	a := New(Config{Model: "cfg-model"})
	if got := a.resolveModel("req-model"); got != "req-model" {
		t.Errorf("req-level model ignored: %s", got)
	}
	if got := a.resolveModel(""); got != "cfg-model" {
		t.Errorf("config-level model ignored: %s", got)
	}
}

func TestParseOutputStructured(t *testing.T) {
	a := New(Config{})
	resp, err := a.parseOutput([]byte(`{"result":"review done {\"decision\": \"approved\", \"summary\": \"2 issues\"}","is_error":false}`), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Status != coreagent.StatusSuccess {
		t.Errorf("status = %s", resp.Status)
	}
	if resp.Decision != coreagent.DecisionApproved {
		t.Errorf("decision = %q, want APPROVED(大小写归一)", resp.Decision)
	}
	if resp.Summary != "2 issues" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

// TestParseOutputNonJSON 非 JSON 输出**必须显式失败**。
//
// 旧行为(迭代 31 修掉):非 JSON 输出(含空输出)一律 StatusSuccess,
// 于是"CLI 退出码 0 但什么都没产出"被当成成功 —— 节点绿灯、决策为空、
// 路由静默走默认分支(约定 14 说的"该失败却成功")。
//
// claude 的契约是 --output-format json:stdout 必须是 JSON 信封。
// 拿不到信封 = 契约被破坏 = 失败,不是"原样返回"。
func TestParseOutputNonJSON(t *testing.T) {
	a := New(Config{})
	resp, err := a.parseOutput([]byte("plain text output"), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Status != coreagent.StatusFailed {
		t.Errorf("status = %q, want FAILED —— 非 JSON 输出不得当成成功", resp.Status)
	}
	if resp.Output != "plain text output" {
		t.Errorf("输出原文必须保留(便于定位): %q", resp.Output)
	}
	if resp.Error == "" {
		t.Error("失败必须给出可读原因")
	}
	if resp.Decision != "" {
		t.Errorf("非 JSON 输出不得猜测 decision, got %q", resp.Decision)
	}
}

// TestParseOutputEmptyFails 退出码 0 但 stdout 一个字节都没有 →
// 显式失败(约定 14)。这是 mock agent 永远走不到、而真实 CLI 完全
// 可能发生的形态(CLI 崩了但退出码是 0)。
func TestParseOutputEmptyFails(t *testing.T) {
	a := New(Config{})
	for _, in := range []string{"", "   ", "\n\t\n"} {
		resp, err := a.parseOutput([]byte(in), "")
		if err != nil {
			t.Fatalf("parse(%q): %v", in, err)
		}
		if resp.Status != coreagent.StatusFailed {
			t.Errorf("parse(%q): status = %q, want FAILED", in, resp.Status)
		}
		if resp.Error == "" {
			t.Errorf("parse(%q): 失败必须给出可读原因", in)
		}
	}
}

func TestParseOutputIsError(t *testing.T) {
	a := New(Config{})
	resp, err := a.parseOutput([]byte(`{"result":"quota exceeded","is_error":true}`), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Status != coreagent.StatusFailed || resp.Error != "quota exceeded" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`before {"a": {"b": 1}} after`, `{"a": {"b": 1}}`},
		{`no braces`, `no braces`},
		{`{"unbalanced": 1`, `{"unbalanced": 1`},
	}
	for _, c := range cases {
		if got := extractJSON(c.in); got != c.want {
			t.Errorf("extractJSON(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	p := buildPrompt(coreagent.AgentRequest{
		Task:         "review auth",
		Instructions: "Be strict.",
		Context:      map[string]any{"git_diff": "diff --git a", "empty": ""},
	})
	if !strings.Contains(p, "Be strict.") || !strings.Contains(p, "Task: review auth") {
		t.Errorf("prompt missing task/instructions:\n%s", p)
	}
	if !strings.Contains(p, "git_diff: diff --git a") {
		t.Errorf("prompt missing context:\n%s", p)
	}
	if strings.Contains(p, "empty:") {
		t.Errorf("空 context 值不应输出:\n%s", p)
	}
	if !strings.Contains(p, `"decision"`) {
		t.Errorf("prompt 缺少结构化输出指令:\n%s", p)
	}
}

func TestExecuteTimeout(t *testing.T) {
	bin := writeFakeCLI(t, "slow-claude", "sleep 5\necho '{}'")
	a := New(Config{Bin: bin, DefaultTimeoutSeconds: 1})
	start := time.Now()
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if !model.IsKind(err, model.KindTimeoutError) {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func TestExecuteCancel(t *testing.T) {
	bin := writeFakeCLI(t, "slow-claude", "sleep 5\necho '{}'")
	a := New(Config{Bin: bin, DefaultTimeoutSeconds: 30})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := a.Execute(ctx, coreagent.AgentRequest{Task: "t"})
	if !model.IsKind(err, model.KindCancelledError) {
		t.Fatalf("err = %v, want CancelledError", err)
	}
}

func TestExecuteProcessError(t *testing.T) {
	a := New(Config{Bin: "/nonexistent/claude-bin"})
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if !model.IsKind(err, model.KindProcessError) {
		t.Fatalf("err = %v, want ProcessError", err)
	}
	if !strings.Contains(err.Error(), "CLAUDE_PROCESS_ERROR") {
		t.Errorf("missing code: %v", err)
	}
}

// TestExecuteModelPassToCLI 验证 --model 参数传递与节点级优先。
func TestExecuteModelPassToCLI(t *testing.T) {
	bin := writeFakeCLI(t, "echo-claude", `printf '{"result":"ARGS:%s","is_error":false}' "$*"`)
	a := New(Config{Bin: bin, Model: "cfg-model"})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Model: "node-model"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Output, "--model node-model") {
		t.Errorf("node model not passed: %s", resp.Output)
	}
	resp, err = a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Output, "--model cfg-model") {
		t.Errorf("config model not used: %s", resp.Output)
	}
	if strings.Contains(resp.Output, "node-model") {
		t.Errorf("stale node model: %s", resp.Output)
	}
}

// TestExecuteEnvMerged 验证调用级环境变量仅作用于子进程。
func TestExecuteEnvMerged(t *testing.T) {
	bin := writeFakeCLI(t, "env-claude",
		`printf '{"result":"K=%s B=%s","is_error":false}' "$MY_TEST_KEY" "$ANTHROPIC_BASE_URL"`)
	a := New(Config{
		Bin:     bin,
		BaseURL: "http://proxy:8080",
		AgentEnv: map[string]string{
			"MY_TEST_KEY": "secret-value",
		},
	})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Output, "K=secret-value") {
		t.Errorf("AgentEnv not passed: %s", resp.Output)
	}
	if !strings.Contains(resp.Output, "B=http://proxy:8080") {
		t.Errorf("BaseURL not passed: %s", resp.Output)
	}
}

// TestExecuteWorkingDir 验证工作目录设置。
func TestExecuteWorkingDir(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeCLI(t, "pwd-claude", `printf '{"result":"PWD:%s","is_error":false}' "$(pwd)"`)
	a := New(Config{Bin: bin})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", WorkingDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(resp.Output, dir) {
		t.Errorf("working dir not applied: %s (want %s)", resp.Output, dir)
	}
}
