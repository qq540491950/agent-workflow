//go:build !unix && !windows

package stubcli

// signalZero 在既非 unix 也非 windows 的平台(js/wasm 等)上没有信号 0
// 探测 —— 进程残留断言在这些平台上恒为"不存活"。
//
// 单列一个文件而不是把条件塞进 proc_unix.go:unix 与 windows 两个文件
// 各覆盖一类,剩下的平台落到这里,保证本包在**任何** GOOS 下都能编译
// (门禁第 4 步的 Windows 交叉编译 + 孤儿包的跨平台 vet 都要求这一点)。
func signalZero(pid int) bool { return false }
