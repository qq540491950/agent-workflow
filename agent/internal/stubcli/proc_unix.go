//go:build unix

package stubcli

import "syscall"

// signalZero 用信号 0 探测进程是否存在。
//
// ESRCH = 进程已消失;EPERM = 进程存在但不属于当前用户(仍算活着)。
func signalZero(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}
