package pi

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
	"agentworkflow/permission"
)

// 本文件是迭代 31 的"同形替身"场景矩阵(pi 侧):一个可复用的替身
// 二进制走一遍真实适配器路径,覆盖真实 CLI 可能出现的恶劣形态。
//
// 与 claude 侧的关键差异:pi 的输出形态是**自由文本 + 尾部 JSON**
// (适配器不强制 JSON 输出格式),所以"纯文本输出"对 pi 不是契约破坏 ——
// 仓库自带的示例工作流(coding-task.yaml 的 plan / execute 节点)正是
// 靠 pi 的自由文本产出当上下文用。因此 pi 侧对"没有结构化决策"的
// 要求是**显式可判定**(留痕),而不是直接判失败。
//
// 断言不止"没崩":每个场景都断言终态、决策值、错误消息的可读性与
// 凭据脱敏、以及进程残留。

type stubCase struct {
	name          string
	mode          string
	wantStatus    coreagent.AgentStatus
	wantDecision  coreagent.AgentDecision
	wantErrCode   string
	wantErrText   string
	wantNoErrText string
	// wantDecisionMissing 期望响应里留下"没有结构化决策"的痕迹。
	wantDecisionMissing bool
	// timeout 该场景的超时秒数(0 = 用默认)。
	timeout int
}

// stubAgent 用替身二进制构造一个 pi 适配器。
func stubAgent(t *testing.T, mode string, extra map[string]string, waitDelay int) *Agent {
	t.Helper()
	bin := stubcli.Build(t)
	env := stubcli.Env(mode, stubcli.ShapePi, extra)
	return New(Config{
		Bin:                   bin,
		Perms:                 permission.NewManager(),
		AgentEnv:              env,
		WaitDelaySeconds:      waitDelay,
		DefaultTimeoutSeconds: 30,
	})
}

// TestStubScenarioMatrix 场景矩阵:恶劣形态逐个走真实子进程路径。
func TestStubScenarioMatrix(t *testing.T) {
	const secret = "pk-live-7c21-super-secret"
	cases := []stubCase{
		{
			name: "01 正常输出尾部 JSON", mode: stubcli.ModeOK,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			name: "02 超大输出", mode: stubcli.ModeHuge,
			wantStatus: coreagent.StatusFailed, wantErrText: "截断",
		},
		{
			name: "03 空输出", mode: stubcli.ModeEmpty,
			wantStatus: coreagent.StatusFailed, wantErrText: "输出",
		},
		{
			name: "04 只有空白", mode: stubcli.ModeBlank,
			wantStatus: coreagent.StatusFailed, wantErrText: "输出",
		},
		{
			// 纯文本输出对 pi 不是契约破坏,但**不得静默**:必须留下
			// "没有结构化决策"的痕迹(见下方 wantDecisionMissing)。
			name: "05 非 JSON 输出", mode: stubcli.ModeNonJSON,
			wantStatus: coreagent.StatusSuccess, wantDecisionMissing: true,
		},
		{
			name: "06 前置垃圾 + 尾部 JSON", mode: stubcli.ModeGarbagePrefix,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			name: "07 多个 JSON 取尾部", mode: stubcli.ModeMultiJSON,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			name: "08 非零退出 + stderr", mode: stubcli.ModeExitNonZero,
			wantErrCode: "PI_PROCESS_ERROR", wantErrText: "--cwd",
		},
		{
			name: "09 退出码 0 但无任何输出", mode: stubcli.ModeExitZeroSilent,
			wantStatus: coreagent.StatusFailed, wantErrText: "输出",
		},
		{
			name: "11 无限睡眠 → 超时", mode: stubcli.ModeSleep,
			wantErrCode: "PI_TIMEOUT", timeout: 1,
		},
		{
			// 进程被信号杀死 + 管道被孤儿持有:ErrWaitDelay **不得**
			// 把"进程已死"掩盖成成功。
			name: "15 信号杀死 + 孤儿持有管道", mode: stubcli.ModeForkAndSignal,
			wantErrCode: "PI_PROCESS_ERROR", timeout: 20,
		},
		{
			// 非法字节落在 JSON 内部 → JSON 解析失败,但**不得 panic**,
			// 且必须留痕(没有决策)。
			name: "12 非法字节落在 JSON 内", mode: stubcli.ModeBinary,
			wantStatus: coreagent.StatusSuccess, wantDecisionMissing: true,
		},
		{
			name: "12b 非法字节落在 JSON 外", mode: stubcli.ModeBinaryPrefix,
			wantStatus: coreagent.StatusSuccess, wantDecision: coreagent.DecisionRejected,
		},
		{
			name: "13 stderr 含凭据", mode: stubcli.ModeStderrCredential,
			wantErrCode:   "PI_PROCESS_ERROR",
			wantNoErrText: secret,
		},
		{
			name: "14 退出码 137", mode: stubcli.ModeExitCode,
			wantErrCode: "PI_PROCESS_ERROR", wantErrText: "137",
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
			if tc.wantDecisionMissing {
				if resp.Decision != "" {
					t.Errorf("不该有决策: %q", resp.Decision)
				}
				if resp.Data["decision_missing"] != true {
					t.Errorf("缺少决策必须留痕(decision_missing), data=%v", resp.Data)
				}
			}
			if tc.wantErrText != "" && !strings.Contains(resp.Error, tc.wantErrText) {
				t.Errorf("错误信息缺少 %q: %q", tc.wantErrText, resp.Error)
			}
			if tc.wantNoErrText != "" && strings.Contains(resp.Error, tc.wantNoErrText) {
				t.Errorf("凭据明文泄漏进错误消息: %q", resp.Error)
			}
		})
	}
}

// TestStubForkChildIsReaped 替身 fork 出持有 stdout 管道的子进程后
// 立刻退出:适配器必须**收掉这个孤儿**(进程组击杀)。
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
	if elapsed > 20*time.Second {
		t.Errorf("等待超时: %v —— WaitDelay 未生效", elapsed)
	}
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
	if !strings.Contains(err.Error(), "PI_PROCESS_ERROR") {
		t.Errorf("应为 PI_PROCESS_ERROR: %v", err)
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

// TestStubTimeoutLeavesNoResidue 超时杀掉替身后不得留下残留进程。
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
	if !strings.Contains(err.Error(), "PI_TIMEOUT") {
		t.Errorf("应为 PI_TIMEOUT: %v", err)
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
// 不得有共享可变状态(约定 15)。
func TestStubConcurrentCallsShareNothing(t *testing.T) {
	a := stubAgent(t, stubcli.ModeOK, nil, 2)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
				Task: "并发任务", Mode: "review", Model: "m",
			})
			if err != nil {
				errs <- err
				return
			}
			if resp.Status != coreagent.StatusSuccess || resp.Decision != coreagent.DecisionRejected {
				errs <- errUnexpected(resp)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("并发调用失败: %v", err)
	}
}

// killPID 尽力杀掉残留进程(测试失败时的兜底)。
func killPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
