//go:build windows

package stubcli

// signalZero 在 Windows 上没有信号 0 探测 —— 进程残留断言只在
// 类 Unix 环境跑(替身的 fork 场景本来就依赖 /bin/sh)。
func signalZero(pid int) bool { return false }
