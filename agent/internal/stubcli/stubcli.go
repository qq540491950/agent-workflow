// Package stubcli 提供"同形替身 CLI"的构建与驱动。
//
// 背景(迭代 31):前三十轮的验证几乎都用 mock agent,而 mock 走不到
// **真实适配器路径** —— claude / pi 适配器要起子进程、拼参数、解析
// stdout、判退出码、管进程组。真实 CLI 的输出形态千奇百怪(空输出、
// 非 JSON、超大输出、孤儿进程、信号退出),这些形态必须被验证。
//
// 做法:用一个**同形替身二进制**替代真实 CLI —— 它接收与真实 CLI
// 完全相同的参数,按环境变量切换行为模式,输出可预测的形态。
// 仓库里已有同源先例(agent/pi/stub_smoke_test.go 的 shell 替身、
// agent/claude/real_smoke_test.go 的真机冒烟);本包把那个先例升级成
// **可复用、跨包共用**的 Go 替身:一个二进制覆盖全部场景,不按场景
// 各写一个。
//
// 替身源码见 stubcli_main.go.txt(用 go:embed 内嵌),测试期编译成
// 独立可执行文件 —— 与真实 CLI 一样是一个真正的子进程,进程组、
// 管道、退出码、信号的行为都一致。
package stubcli

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

//go:embed stubcli_main.go.txt
var stubSource string

// 环境变量名(替身读这些变量决定行为)。
const (
	// EnvMode 选行为模式(见 Mode* 常量)。
	EnvMode = "STUBCLI_MODE"
	// EnvShape 选输出形态:ShapeClaude / ShapePi。
	EnvShape = "STUBCLI_SHAPE"
	// EnvSecret 凭据形态的测试值(替身把它打进 stderr)。
	EnvSecret = "STUBCLI_SECRET"
	// EnvExitCode 退出码(exit_code 模式用)。
	EnvExitCode = "STUBCLI_EXIT_CODE"
	// EnvPIDFile 替身把自身 PID 写到这里(验证进程残留)。
	EnvPIDFile = "STUBCLI_PIDFILE"
	// EnvChildPIDFile 替身把 fork 出的子进程 PID 写到这里。
	EnvChildPIDFile = "STUBCLI_CHILD_PIDFILE"
	// EnvSleepSeconds fork 出的子进程存活时长(秒)。
	EnvSleepSeconds = "STUBCLI_SLEEP_SECONDS"
)

// 行为模式(替身的行为开关)。
const (
	// ModeOK 正常输出:正文 + 尾部 JSON。
	ModeOK = "ok"
	// ModeHuge 输出远超上限(9MB)。
	ModeHuge = "huge"
	// ModeEmpty stdout 0 字节(stderr 有一行)。
	ModeEmpty = "empty"
	// ModeEmptyWithCredential stdout 为空、stderr 含凭据、退出码 0。
	ModeEmptyWithCredential = "empty_with_credential"
	// ModeBlank stdout 只有空白字符。
	ModeBlank = "blank"
	// ModeNonJSON stdout 是普通文本,没有 JSON。
	ModeNonJSON = "nonjson"
	// ModeGarbagePrefix stdout 前面有垃圾行,后面才是 JSON。
	ModeGarbagePrefix = "garbage_prefix"
	// ModeMultiJSON stdout 有多个 JSON,最后一个才是结论。
	ModeMultiJSON = "multi_json"
	// ModeExitNonZero 非零退出(3)+ stderr 有内容。
	ModeExitNonZero = "exit_nonzero"
	// ModeExitZeroSilent 退出码 0 且一个字节都不输出。
	ModeExitZeroSilent = "exit_zero_silent"
	// ModeForkHoldPipe fork 出持有 stdout 管道的子进程后立刻退出。
	ModeForkHoldPipe = "fork_hold_pipe"
	// ModeForkAndSignal fork 出持有 stdout 管道的子进程后,父进程被
	// 信号杀死(进程死了 → 必须判失败,不能被 ErrWaitDelay 掩盖)。
	ModeForkAndSignal = "fork_and_signal"
	// ModeSleep 无限睡眠(直到被超时杀掉)。
	ModeSleep = "sleep"
	// ModeBinary 非法字节(NUL / 非 UTF-8)落在 JSON 内部。
	ModeBinary = "binary"
	// ModeBinaryPrefix 非法字节落在 JSON 之外。
	ModeBinaryPrefix = "binary_prefix"
	// ModeStderrCredential stderr 里含凭据形态的值 + 非零退出。
	ModeStderrCredential = "stderr_credential"
	// ModeExitCode 按 EnvExitCode 退出(137 / 143 等)。
	ModeExitCode = "exit_code"
)

// 输出形态。
const (
	// ShapeClaude claude CLI 形态:{"result": "...", "is_error": false}。
	ShapeClaude = "claude"
	// ShapePi pi CLI 形态:正文 + 尾部 JSON。
	ShapePi = "pi"
)

var (
	buildOnce sync.Once
	buildPath string
	buildDir  string
	buildErr  error
)

// Build 编译替身二进制并返回其路径(每个测试进程只编译一次)。
//
// 编译产物放在进程级临时目录里,TestMain 里调 Cleanup 删除。
func Build(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() { buildErr = buildStub() })
	if buildErr != nil {
		t.Fatalf("构建同形替身失败: %v", buildErr)
	}
	return buildPath
}

func buildStub() error {
	dir, err := os.MkdirTemp("", "agentworkflow-stubcli-")
	if err != nil {
		return err
	}
	buildDir = dir
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(stubSource), 0o644); err != nil {
		return err
	}
	// 独立模块:替身只用标准库,不需要网络,也不该被本仓库的
	// 依赖图影响(go.mod 的 go 指令写低版本,避免触发工具链下载)。
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module stubcli\n\ngo 1.21\n"), 0o644); err != nil {
		return err
	}
	out := filepath.Join(dir, "stubcli")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v\n%s", err, b)
	}
	buildPath = out
	return nil
}

// Cleanup 删除替身二进制的临时目录(TestMain 里调用)。
func Cleanup() {
	if buildDir == "" {
		return
	}
	_ = os.RemoveAll(buildDir)
	buildDir = ""
	buildPath = ""
}

// Env 拼出替身需要的环境变量。
//
// mode 选行为模式,shape 选输出形态;extra 里可放 EnvSecret /
// EnvPIDFile 等其余变量。
func Env(mode, shape string, extra map[string]string) map[string]string {
	env := map[string]string{EnvMode: mode, EnvShape: shape}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// ReadPID 读替身写下的 PID 文件;文件不存在返回 0。
func ReadPID(t testing.TB, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}

// ProcessAlive 报告 PID 是否仍是一个活着的进程。
//
// 只在类 Unix 环境有意义(Windows 上恒返回 false)。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// 逐个信号 0 做存在性探测:ESRCH = 进程没了。
	_, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalZero(pid)
}
