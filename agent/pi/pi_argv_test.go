package pi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/logx"
)

// 本文件锁住 P1-3:pi-agent 的 CLI 参数(argv)是**本机可见**的凭据出口。
//
// Agent 配置里的 extra_args 在应用层映射为 pi.Config.DefaultArgs
// (app/application/application.go 的 applyAgentConfig),于是凭据原样进
// argv —— ps aux、/proc/<pid>/cmdline 本机全可见。
//
// 口径与 claude 适配器一致:启动前**告警**,**绝不**改写 argv
// (掩码哨兵不是真凭据,下发过去 CLI 鉴权必失败)。

// fakeCLIRecordsArgs 返回一个把 argv 逐行写进文件的假 CLI 路径。
func fakeCLIRecordsArgs(t *testing.T, out string) string {
	t.Helper()
	body := fmt.Sprintf("printf '%%s\\n' \"$@\" > %q\necho '{\"status\":\"success\"}'\n", out)
	return writeFakeCLI(t, "pi-agent", body)
}

// TestDefaultArgsCredentialWarnsAndKeepsOriginalValue 凭据形态的
// DefaultArgs(= Agent 配置的 extra_args)必须触发告警,且原值原样下发。
func TestDefaultArgsCredentialWarnsAndKeepsOriginalValue(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	bin := fakeCLIRecordsArgs(t, out)

	var logs strings.Builder
	restore := logx.SetOutput(&logs)
	t.Cleanup(restore)

	const secret = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
	a := New(Config{Bin: bin, DefaultArgs: []string{"--api-key", secret}})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp == nil {
		t.Fatal("execute 返回 nil 响应")
	}
	if resp.Status != coreagent.StatusSuccess {
		t.Fatalf("status = %s, want SUCCESS(err=%q)", resp.Status, resp.Error)
	}

	got := logs.String()
	if !strings.Contains(got, "命令行参数") {
		t.Errorf("DefaultArgs 含凭据形态的值时必须告警(本机可见),实际日志: %q", got)
	}
	if !strings.Contains(got, "环境变量") {
		t.Errorf("告警必须指出正确做法(凭据放环境变量),实际日志: %q", got)
	}
	if strings.Contains(got, secret) {
		t.Errorf("告警日志里出现了凭据明文: %q", got)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取假 CLI 记录的 argv: %v", err)
	}
	if !strings.Contains(string(raw), secret) {
		t.Errorf("argv 里的凭据被改写了(下发过去的必须是原值),实际 argv: %q", string(raw))
	}
}

// TestDefaultArgsNoCredentialNoWarning 普通参数不得误报。
func TestDefaultArgsNoCredentialNoWarning(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	bin := fakeCLIRecordsArgs(t, out)

	var logs strings.Builder
	restore := logx.SetOutput(&logs)
	t.Cleanup(restore)

	a := New(Config{Bin: bin, DefaultArgs: []string{"--verbose"}})
	if _, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(logs.String(), "命令行参数") {
		t.Errorf("普通参数不应触发 argv 凭据告警,实际日志: %q", logs.String())
	}
}
