package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseArgsDefaults 锁住启动参数的默认值(桌面模式 + 默认监听地址)。
func TestParseArgsDefaults(t *testing.T) {
	opts, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.Server {
		t.Error("默认应为桌面模式")
	}
	if opts.Addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q", opts.Addr)
	}
	if opts.DataDir != "" {
		t.Errorf("data = %q, want 空(走用户配置目录)", opts.DataDir)
	}
	if opts.GitDir != "" {
		t.Errorf("git-dir = %q, want 空", opts.GitDir)
	}
	if opts.LogLevel != "info" {
		t.Errorf("log = %q", opts.LogLevel)
	}
}

// TestParseArgsOverrides 锁住显式参数的解析(含单横线写法)。
func TestParseArgsOverrides(t *testing.T) {
	opts, err := parseArgs([]string{
		"-server",
		"-addr", "127.0.0.1:19090",
		"-data", "/tmp/aw-data",
		"-git-dir", "/tmp/aw-git",
		"-log", "debug",
	})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !opts.Server {
		t.Error("server 应为 true")
	}
	if opts.Addr != "127.0.0.1:19090" {
		t.Errorf("addr = %q", opts.Addr)
	}
	if opts.DataDir != "/tmp/aw-data" {
		t.Errorf("data = %q", opts.DataDir)
	}
	if opts.GitDir != "/tmp/aw-git" {
		t.Errorf("git-dir = %q", opts.GitDir)
	}
	if opts.LogLevel != "debug" {
		t.Errorf("log = %q", opts.LogLevel)
	}
}

// TestParseArgsRejectsUnknownFlag 未知参数必须报错(不能默默按默认值跑)。
func TestParseArgsRejectsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"-nope"}); err == nil {
		t.Fatal("未知参数应报错")
	}
}

// TestParseArgsRejectsInvalidLogLevel 非法日志级别必须当场报错。
// 此前 logx.SetLevel 对未知值静默回落 info —— 拼错 "--log debugg"
// 的用户会以为开了 debug 实际没有(约定 14:该失败却成功等同 P0)。
func TestParseArgsRejectsInvalidLogLevel(t *testing.T) {
	_, err := parseArgs([]string{"-log", "verbose"})
	if err == nil {
		t.Fatal("非法日志级别应报错")
	}
	if !strings.Contains(err.Error(), "debug") {
		t.Errorf("错误信息应列出合法取值: %v", err)
	}
}

// TestParseArgsRejectsInvalidAddr 服务器模式下监听地址必须合法。
func TestParseArgsRejectsInvalidAddr(t *testing.T) {
	if _, err := parseArgs([]string{"-server", "-addr", "not-a-host-port"}); err == nil {
		t.Fatal("非法监听地址应报错")
	}
	if _, err := parseArgs([]string{"-server", "-addr", ""}); err == nil {
		t.Fatal("空监听地址应报错")
	}
	// 桌面模式不用监听地址,不因此拒绝启动
	if _, err := parseArgs([]string{"-addr", "not-a-host-port"}); err != nil {
		t.Errorf("桌面模式不应校验监听地址: %v", err)
	}
}

// TestParseArgsHelpFlag 锁住 -h 的语义(帮助,不报错)。
func TestParseArgsHelpFlag(t *testing.T) {
	_, err := parseArgs([]string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
}

// TestDefaultDataDir 锁住默认数据目录的计算。
func TestDefaultDataDir(t *testing.T) {
	if got := defaultDataDir("/Users/x/Library/Application Support"); got != filepath.Join("/Users/x/Library/Application Support", "agent-workflow") {
		t.Errorf("defaultDataDir = %q", got)
	}
	// 取不到用户配置目录时退化为当前目录下的 agent-workflow
	if got := defaultDataDir(""); got != "agent-workflow" {
		t.Errorf("defaultDataDir(\"\") = %q, want agent-workflow", got)
	}
}

// TestIsValidLogLevel 锁住日志级别白名单(大小写不敏感)。
func TestIsValidLogLevel(t *testing.T) {
	for _, l := range []string{"debug", "info", "warn", "error", "DEBUG", "Info"} {
		if !isValidLogLevel(l) {
			t.Errorf("%q 应为合法日志级别", l)
		}
	}
	for _, l := range []string{"", "trace", "fatal", "off"} {
		if isValidLogLevel(l) {
			t.Errorf("%q 不应为合法日志级别", l)
		}
	}
}

// TestRunExitCodes 锁住入口的退出码:参数错误 → 2,-h → 0。
// (run 在参数校验失败时不会初始化应用,不会碰数据库。)
func TestRunExitCodes(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = append([]string{"agent-workflow"}, "-nope")
	var code int
	captureStderr(t, func() { code = run() })
	if code != 2 {
		t.Errorf("参数错误退出码 = %d, want 2", code)
	}

	os.Args = append([]string{"agent-workflow"}, "-h")
	captureStderr(t, func() { code = run() })
	if code != 0 {
		t.Errorf("帮助退出码 = %d, want 0", code)
	}
}

// captureStderr 捕获 fn 往 stderr 写的内容(避免测试输出被用法说明刷屏)。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	out := <-done
	_ = r.Close()
	return out
}
