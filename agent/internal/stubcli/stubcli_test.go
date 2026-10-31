package stubcli

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBuildStubEmitsExpectedShapes 替身本身的行为必须可信:它才是
// 全部适配器用例的"地面真值",替身坏了 → 全部结论都不可信。
func TestBuildStubEmitsExpectedShapes(t *testing.T) {
	bin := Build(t)
	if bin == "" {
		t.Fatal("替身路径为空")
	}
	cases := []struct {
		name   string
		mode   string
		shape  string
		want   []string
		absent []string
	}{
		{
			name: "ok/claude", mode: ModeOK, shape: ShapeClaude,
			want: []string{`"result"`, "REJECTED", `"is_error"`},
		},
		{
			name: "ok/pi", mode: ModeOK, shape: ShapePi,
			want: []string{"分析完成", "REJECTED"},
		},
		{
			name: "multi_json", mode: ModeMultiJSON, shape: ShapePi,
			want: []string{"APPROVED", "REJECTED"},
		},
		{
			name: "nonjson", mode: ModeNonJSON, shape: ShapePi,
			want:   []string{"普通文本"},
			absent: []string{"{"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin)
			cmd.Env = append(os.Environ(), EnvMode+"="+tc.mode, EnvShape+"="+tc.shape)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("替身执行失败: %v (out=%q)", err, out)
			}
			text := string(out)
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("输出缺少 %q:\n%s", w, text)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(text, a) {
					t.Errorf("输出不该含 %q:\n%s", a, text)
				}
			}
		})
	}
}

// TestBuildStubExitCodes 替身的退出码必须可控 —— 适配器对
// "非零退出"与"退出码 0 但无输出"的区分完全依赖它。
func TestBuildStubExitCodes(t *testing.T) {
	bin := Build(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), EnvMode+"="+ModeExitCode, EnvExitCode+"=137")
	err := cmd.Run()
	if err == nil {
		t.Fatal("exit_code 模式必须以非零码退出")
	}
	var exitErr *exec.ExitError
	if !asExitError(err, &exitErr) {
		t.Fatalf("err = %v, want *exec.ExitError", err)
	}
	if code := exitErr.ExitCode(); code != 137 {
		t.Errorf("退出码 = %d, want 137", code)
	}
}

// TestProcessAliveDetectsDeath 进程残留断言的地面真值:探测必须能
// 分辨"活着"与"已死"。
func TestProcessAliveDetectsDeath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ProcessAlive 在 Windows 上没有信号 0 探测(见 proc_windows.go)")
	}
	if ProcessAlive(0) {
		t.Fatal("PID 0 不该被判定为存活")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("本机无法启动 sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if !ProcessAlive(pid) {
		t.Fatalf("刚启动的进程 %d 应判定为存活", pid)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	// 回收可能需要一小会儿(僵尸 → 回收)。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("进程 %d 被杀掉后仍判定为存活", pid)
}

// asExitError 是 errors.As 的小包装(避免为一个断言引入 errors 包名冲突)。
func asExitError(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
