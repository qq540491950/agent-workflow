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

// KillProcessGroup 尽力杀掉命令所在的**整个进程组**。
//
// 为什么需要(迭代 31):ConfigureProcess 只在 **Context 取消/超时** 时
// 触发进程组击杀。CLI **正常退出**的路径没有这一步 —— CLI 派生的
// 子进程(孤儿)继续持有 stdout 管道,exec.Wait 只能等到 WaitDelay
// 上限就返回 exec.ErrWaitDelay。此时适配器返回成功,而那些孤儿进程
// 仍在后台跑:进程泄漏 + 管道泄漏(约定 3 的"进程组击杀"语义只兑现
// 了一半)。
//
// 必须在 Wait 返回**之后**调用:那时直接子进程已退出,组里剩下的
// 就是孤儿,杀掉它们正是目的。
//
// 残留风险:直接子进程退出后其 PID 可能被系统回收并分配给别的
// 进程 —— 那一瞬间 kill(-pgid) 理论上可能误伤无关进程组。窗口是
// 微秒级,且只在"确实有孤儿持有管道"时才会走到这条路。
func KillProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
