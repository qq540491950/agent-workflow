package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/internal/stubcli"
)

// 本文件是迭代 31 的"同形替身"场景矩阵:用**一个**可复用的替身二进制
// 走一遍真实适配器路径(起子进程、拼参数、捕获 stdout/stderr、判退出码、
// 管进程组),覆盖真实 CLI 可能出现的各种恶劣形态。
//
// 为什么 mock agent 走不到这里:mock 直接返回结构体,不经过子进程 ——
// 输出解析、超时、退出码、管道与进程组的差异**全部**在 mock 之外。
//
// 断言不止"没崩":每个场景都断言终态(SUCCESS/FAILED)、决策值、错误
// 消息的可读性与凭据脱敏、以及进程残留。

// stubCase 是一个场景的期望。
type stubCase struct {
	name string
	mode string
	// wantStatus 期望的终态(Execute 不返回 error 时有意义)。
	wantStatus coreagent.AgentStatus
	// wantDecision 期望解析出的决策。
	wantDecision coreagent.AgentDecision
	// wantErrCode 期望的结构化错误码(Execute 返回 error 时有意义)。
	wantErrCode string
	// wantErrText 错误消息里必须出现的片段。
	wantErrText string
	// wantNoErrText 错误消息里绝不能出现的片段(凭据明文等)。
	wantNoErrText string
	// wantOutputText 响应 Output 里必须出现的片段。
	wantOutputText string
	// timeout 该场景的超时秒数(0 = 用默认)。
	timeout int
}

// stubAgent 用替身二进制构造一个 claude 适配器。
func stubAgent(t *testing.T, mode string, extra map[string]string, waitDelay int) *Agent {
	t.Helper()
	bin := stubcli.Build(t)
	env := stubcli.Env(mode, stubcli.ShapeClaude, extra)
	return New(Config{
		Bin:              bin,
		AgentEnv:         env,
		WaitDelaySeconds: waitDelay,
		// 超时场景单独设短超时;默认给足,避免把"超时"混进其它断言。
		DefaultTimeoutSeconds: 30,
	})
}

// TestStubScenarioMatrix 场景矩阵:14 个恶劣形态逐个走真实子进程路径。
func TestStubScenarioMatrix(t *testing.T) {
	const secret = "sk-live-9f3a-super-secret"
	cases := []stubCase{
		{
			name: "01 正常输出尾部 JSON", mode: stubcli.ModeOK,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
			wantOutputText: "分析完成",
		},
		{
			// 超大输出 → 截断 → 必须**显式失败**:SUCCESS + 空决策会让
			// 路由静默走默认分支(约定 14)。
			name: "02 超大输出", mode: stubcli.ModeHuge,
			wantStatus: coreagent.StatusFailed, wantErrText: "截断",
		},
		{
			// 退出码 0 但 stdout 一个字节都没有 → 必须失败。
			name: "03 空输出", mode: stubcli.ModeEmpty,
			wantStatus: coreagent.StatusFailed, wantErrText: "输出",
		},
		{
			name: "04 只有空白", mode: stubcli.ModeBlank,
			wantStatus: coreagent.StatusFailed, wantErrText: "输出",
		},
		{
			// --output-format json 的契约被破坏 → 明确失败。
			name: "05 非 JSON 输出", mode: stubcli.ModeNonJSON,
			wantStatus: coreagent.StatusFailed, wantErrText: "JSON",
		},
		{
			// 前置垃圾行 → 尾部信封仍要能被提取。
			name: "06 前置垃圾 + 尾部 JSON", mode: stubcli.ModeGarbagePrefix,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			// 多个 JSON → 取最后一个(尾部)。
			name: "07 多个 JSON 取尾部", mode: stubcli.ModeMultiJSON,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			name: "08 非零退出 + stderr", mode: stubcli.ModeExitNonZero,
			wantErrCode: "CLAUDE_PROCESS_ERROR", wantErrText: "--cwd",
		},
		{
			// 退出码 0 且完全静默 → 绝不能当成成功。
			name: "09 退出码 0 但无任何输出", mode: stubcli.ModeExitZeroSilent,
			wantStatus: coreagent.StatusFailed, wantErrText: "输出",
		},
		{
			name: "11 无限睡眠 → 超时", mode: stubcli.ModeSleep,
			wantErrCode: "CLAUDE_TIMEOUT", timeout: 1,
		},
		{
			// 进程被信号杀死 + 管道被孤儿持有:ErrWaitDelay **不得**
			// 把"进程已死"掩盖成成功。
			name: "15 信号杀死 + 孤儿持有管道", mode: stubcli.ModeForkAndSignal,
			wantErrCode: "CLAUDE_PROCESS_ERROR", timeout: 20,
		},
		{
			name: "12 非法字节落在 JSON 内", mode: stubcli.ModeBinary,
			wantStatus: coreagent.StatusFailed, wantErrText: "JSON",
		},
		{
			name: "12b 非法字节落在 JSON 外", mode: stubcli.ModeBinaryPrefix,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			name: "13 stderr 含凭据", mode: stubcli.ModeStderrCredential,
			wantErrCode:   "CLAUDE_PROCESS_ERROR",
			wantNoErrText: secret,
		},
		{
			name: "14 退出码 137", mode: stubcli.ModeExitCode,
			wantErrCode: "CLAUDE_PROCESS_ERROR", wantErrText: "137",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]string{}
			if tc.mode == stubcli.ModeStderrCredential {
				extra[stubcli.EnvSecret] = secret
			}
			if tc.mode == stubcli.ModeExitCode {
				extra[stubcli.EnvExitCode] = "137"
			}
			if tc.mode == stubcli.ModeForkAndSignal {
				// 必须真的 fork 出持有管道的子进程:否则"信号杀死 +
				// 管道被持有"这个组合根本没被复现。
				extra[stubcli.EnvChildPIDFile] = filepath.Join(t.TempDir(), "child.pid")
				extra[stubcli.EnvSleepSeconds] = "60"
			}
			a := stubAgent(t, tc.mode, extra, 2)
			if tc.timeout > 0 {
				a.cfg.DefaultTimeoutSeconds = tc.timeout
			}
			resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
				Task: "检查代码", Mode: "review",
			})
			if tc.wantErrCode != "" {
				if err == nil {
					t.Fatalf("期望结构化错误 %s,实际无错误(resp=%+v)", tc.wantErrCode, resp)
				}
				if !strings.Contains(err.Error(), tc.wantErrCode) {
					t.Errorf("错误码不对: %v", err)
				}
				if tc.wantErrText != "" && !strings.Contains(err.Error(), tc.wantErrText) {
					t.Errorf("错误信息缺少 %q: %v", tc.wantErrText, err)
				}
				if tc.wantNoErrText != "" && strings.Contains(err.Error(), tc.wantNoErrText) {
					t.Errorf("凭据明文泄漏进错误消息: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if resp.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (output=%q error=%q)",
					resp.Status, tc.wantStatus, resp.Output, resp.Error)
			}
			if tc.wantDecision != "" && resp.Decision != tc.wantDecision {
				t.Errorf("decision = %q, want %q (output=%q)", resp.Decision, tc.wantDecision, resp.Output)
			}
			if tc.wantErrText != "" && !strings.Contains(resp.Error, tc.wantErrText) {
				t.Errorf("错误信息缺少 %q: %q", tc.wantErrText, resp.Error)
			}
			if tc.wantNoErrText != "" && strings.Contains(resp.Error, tc.wantNoErrText) {
				t.Errorf("凭据明文泄漏进错误消息: %q", resp.Error)
			}
			if tc.wantOutputText != "" && !strings.Contains(resp.Output, tc.wantOutputText) {
				t.Errorf("Output 缺少 %q: %q", tc.wantOutputText, resp.Output)
			}
		})
	}
}

// TestStubScenarioMatrixExit143 退出码 143(另一个信号杀形态)同样必须
// 明确失败且原因可读。
func TestStubScenarioMatrixExit143(t *testing.T) {
	a := stubAgent(t, stubcli.ModeExitCode, map[string]string{stubcli.EnvExitCode: "143"}, 2)
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err == nil {
		t.Fatal("退出码 143 必须返回错误")
	}
	if !strings.Contains(err.Error(), "CLAUDE_PROCESS_ERROR") {
		t.Errorf("错误码不对: %v", err)
	}
	if !strings.Contains(err.Error(), "143") {
		t.Errorf("错误信息应说明退出码: %v", err)
	}
}

// TestStubForkChildIsReaped 替身 fork 出持有 stdout 管道的子进程后
// 立刻退出:适配器必须**收掉这个孤儿**(进程组击杀),而不是让它继续
// 在后台跑(管道与进程泄漏)。
func TestStubForkChildIsReaped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fork 场景依赖 /bin/sh")
	}
	dir := t.TempDir()
	childPID := filepath.Join(dir, "child.pid")
	a := stubAgent(t, stubcli.ModeForkHoldPipe, map[string]string{
		stubcli.EnvChildPIDFile: childPID,
		stubcli.EnvSleepSeconds: "60",
	}, 1)

	start := time.Now()
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// WaitDelay 生效:不能无限等下去。
	if elapsed > 20*time.Second {
		t.Errorf("等待超时: %v —— WaitDelay 未生效", elapsed)
	}
	// 进程已成功退出、输出完整 → 判成功(约定 3)。
	if resp.Status != coreagent.StatusSuccess {
		t.Errorf("status = %q, want SUCCESS (output=%q error=%q)", resp.Status, resp.Output, resp.Error)
	}
	if resp.Decision != coreagent.DecisionRejected {
		t.Errorf("decision = %q, want REJECTED (output=%q)", resp.Decision, resp.Output)
	}
	pid := stubcli.ReadPID(t, childPID)
	if pid == 0 {
		t.Skip("替身没能写下子进程 PID(跳过残留断言)")
	}
	// 孤儿必须在适配器返回后被清理掉。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !stubcli.ProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = killPID(pid)
	t.Errorf("孤儿进程 %d 仍然存活 —— 进程组未被清理", pid)
}

// TestStubEmptyOutputCarriesStderrHint CLI 退出码 0 却什么都没产出时,
// stderr 是**唯一**的诊断线索 —— 错误消息必须带上它(否则用户只能
// 自己去命令行重跑才知道出了什么事),而且**不得**带出凭据明文。
func TestStubEmptyOutputCarriesStderrHint(t *testing.T) {
	const secret = "sk-empty-output-secret-value"
	a := stubAgent(t, stubcli.ModeEmpty, nil, 2)
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != coreagent.StatusFailed {
		t.Fatalf("status = %q, want FAILED", resp.Status)
	}
	if !strings.Contains(resp.Error, "stderr") {
		t.Errorf("错误消息必须带上 stderr 线索: %q", resp.Error)
	}
	if !strings.Contains(resp.Error, "未产生任何输出") {
		t.Errorf("错误消息必须说明「没产出」: %q", resp.Error)
	}

	// 同一形态,但 stderr 里带凭据 → 错误消息不得带出明文。
	b := stubAgent(t, stubcli.ModeEmptyWithCredential, map[string]string{
		stubcli.EnvSecret: secret,
	}, 2)
	resp2, err := b.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp2.Status != coreagent.StatusFailed {
		t.Fatalf("status = %q, want FAILED", resp2.Status)
	}
	if strings.Contains(resp2.Error, secret) {
		t.Errorf("凭据明文泄漏进错误消息: %q", resp2.Error)
	}
	if !strings.Contains(resp2.Error, coreagent.MaskedValue) {
		t.Errorf("脱敏后应保留 *** 标记: %q", resp2.Error)
	}
}

// TestStubSignalKillReapsOrphan 进程被信号杀死、管道又被孤儿持有时:
//  1. 绝不能被 exec.ErrWaitDelay 掩盖成"成功"(进程已经死了);
//  2. 孤儿必须被收掉(失败路径也要杀进程组)。
func TestStubSignalKillReapsOrphan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fork 场景依赖 /bin/sh")
	}
	dir := t.TempDir()
	childPID := filepath.Join(dir, "child.pid")
	a := stubAgent(t, stubcli.ModeForkAndSignal, map[string]string{
		stubcli.EnvChildPIDFile: childPID,
		stubcli.EnvSleepSeconds: "60",
	}, 2)

	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err == nil {
		t.Fatal("进程被信号杀死必须返回错误(不得被 ErrWaitDelay 掩盖成成功)")
	}
	if !strings.Contains(err.Error(), "CLAUDE_PROCESS_ERROR") {
		t.Errorf("应为 CLAUDE_PROCESS_ERROR: %v", err)
	}
	pid := stubcli.ReadPID(t, childPID)
	if pid == 0 {
		t.Skip("替身没能写下子进程 PID(跳过残留断言)")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !stubcli.ProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = killPID(pid)
	t.Errorf("失败后孤儿进程 %d 仍然存活 —— 进程组未被清理", pid)
}

// TestStubTimeoutLeavesNoResidue 超时杀掉替身后,不能留下任何残留
// 进程(进程组击杀必须覆盖子进程)。
func TestStubTimeoutLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "self.pid")
	a := stubAgent(t, stubcli.ModeSleep, map[string]string{
		stubcli.EnvPIDFile: pidFile,
	}, 2)
	a.cfg.DefaultTimeoutSeconds = 1

	start := time.Now()
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("无限睡眠必须以超时结束")
	}
	if !strings.Contains(err.Error(), "CLAUDE_TIMEOUT") {
		t.Errorf("应为 CLAUDE_TIMEOUT: %v", err)
	}
	if elapsed > 15*time.Second {
		t.Errorf("超时未及时生效: %v", elapsed)
	}
	pid := stubcli.ReadPID(t, pidFile)
	if pid == 0 {
		t.Skip("替身没能写下 PID(跳过残留断言)")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !stubcli.ProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = killPID(pid)
	t.Errorf("超时后进程 %d 仍然存活 —— 进程组击杀未生效", pid)
}

// TestStubConcurrentCallsShareNothing 同一个适配器实例被并发调用:
// 不得有共享可变状态(约定 15)。跑 -race 时若有竞争会直接报出来。
func TestStubConcurrentCallsShareNothing(t *testing.T) {
	a := stubAgent(t, stubcli.ModeOK, nil, 2)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
				Task:  "并发任务",
				Mode:  "review",
				Model: "m",
			})
			if err != nil {
				errs <- err
				return
			}
			if resp.Status != coreagent.StatusSuccess || resp.Decision != coreagent.DecisionRejected {
				errs <- errUnexpected(resp)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("并发调用失败: %v", err)
	}
}

// killPID 尽力杀掉残留进程(测试失败时的兜底,避免留下垃圾进程)。
func killPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
