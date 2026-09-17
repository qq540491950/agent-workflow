//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// ConfigureProcess 将命令置于独立进程组,并在 Context 取消/超时时
// 击杀整个进程组。exec.CommandContext 默认只杀直接子进程,CLI Agent
// 派生的工作进程会变成孤儿并继续持有 stdout 管道,导致调用方阻塞
// 到孤儿自行退出(需求 §8:进程取消必须及时)。
func ConfigureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}
