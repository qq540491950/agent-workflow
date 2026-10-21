package claude

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

// 本文件锁住 P1-3:CLI 参数(argv)是**本机可见**的凭据出口。
//
// 事实:子进程的 argv 对本机所有进程可见(ps aux、/proc/<pid>/cmdline、
// 各类进程监控与审计工具)。Agent 配置里的 extra_args 会原样进 argv ——
// 于是 "--api-key sk-xxx" 这类凭据等于半公开。
//
// 修复口径(有意为之,不是偷懒):
//   - 启动 CLI **前**检查 extra_args 里有无**按值形态**判定为凭据的项
//     (唯一来源:logx/credential.go —— 值的形态规则全仓只有一份),
//     命中就**告警**;
//   - **绝不**把 argv 里的值脱敏后再下发:掩码哨兵 "***" 不是真凭据,
//     下发过去 CLI 鉴权必失败(约定 5 的哨兵语义:它只表示"这里被掩过")。
//     本文件同时断言"下发到 CLI 的仍是原值"—— 防止将来有人"顺手"加一层
//     脱敏而把鉴权改坏。

// fakeCLIRecordsArgs 返回一个把 argv 逐行写进文件的假 CLI 路径。
func fakeCLIRecordsArgs(t *testing.T, out string) string {
	t.Helper()
	body := fmt.Sprintf("printf '%%s\\n' \"$@\" > %q\necho '{\"result\":\"ok\"}'\n", out)
	return writeFakeCLI(t, "claude", body)
}

// TestExtraArgsCredentialWarnsAndKeepsOriginalValue 凭据形态的 extra_args
// 必须触发告警,且**原值**必须原样下发。
func TestExtraArgsCredentialWarnsAndKeepsOriginalValue(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	bin := fakeCLIRecordsArgs(t, out)

	var logs strings.Builder
	restore := logx.SetOutput(&logs)
	t.Cleanup(restore)

	// ghp_ 前缀 + 足够长的尾巴 = logx 认定的"凭据形态"(严格前缀规则)。
	const secret = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
	a := New(Config{Bin: bin, ExtraArgs: []string{"--api-key", secret}})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != coreagent.StatusSuccess {
		t.Fatalf("status = %s, want SUCCESS(err=%q)", resp.Status, resp.Error)
	}

	// 1) 告警必须发出来(约定 14:降级/风险路径必须有可验证的告警)。
	got := logs.String()
	if !strings.Contains(got, "命令行参数") {
		t.Errorf("extra_args 含凭据形态的值时必须告警(本机可见),实际日志: %q", got)
	}
	if !strings.Contains(got, "环境变量") {
		t.Errorf("告警必须指出正确做法(凭据放环境变量),实际日志: %q", got)
	}
	// 2) 告警本身不得泄漏凭据明文。
	if strings.Contains(got, secret) {
		t.Errorf("告警日志里出现了凭据明文: %q", got)
	}

	// 3) 绝不能改写 argv:下发到 CLI 的必须是原值(脱敏会让鉴权失败)。
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取假 CLI 记录的 argv: %v", err)
	}
	if !strings.Contains(string(raw), secret) {
		t.Errorf("argv 里的凭据被改写了(下发过去的必须是原值),实际 argv: %q", string(raw))
	}
}

// TestExtraArgsNoCredentialNoWarning 普通参数不得误报。
func TestExtraArgsNoCredentialNoWarning(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	bin := fakeCLIRecordsArgs(t, out)

	var logs strings.Builder
	restore := logx.SetOutput(&logs)
	t.Cleanup(restore)

	a := New(Config{Bin: bin, ExtraArgs: []string{"--verbose", "--model", "claude-sonnet-4-5"}})
	if _, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(logs.String(), "命令行参数") {
		t.Errorf("普通参数不应触发 argv 凭据告警,实际日志: %q", logs.String())
	}
}
