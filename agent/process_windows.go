//go:build windows

package agent

import "os/exec"

// ConfigureProcess 在 Windows 上使用 exec.CommandContext 的默认击杀
// (仅直接子进程);派生进程的管道遗留由 cmd.WaitDelay 兜底关闭,
// 保证取消/超时后调用方及时返回。
func ConfigureProcess(cmd *exec.Cmd) {}
