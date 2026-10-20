package agent

import "testing"

// TestModeRequiresWrite 锁定"哪些 mode 隐含写操作"的唯一清单。
//
// 这份清单被两处消费:运行期的节点权限检查(workflow/runtime/executor.go)
// 与 pi 适配器的写权限兜底(agent/pi/pi.go)。两边共用同一个函数 —
// 新增 mode 时只改这一处,不会出现"一边要写权限、另一边不要"。
func TestModeRequiresWrite(t *testing.T) {
	cases := map[string]bool{
		"execute": true,  // 执行任务:改文件
		"fix":     true,  // 修复:改文件
		"submit":  true,  // 提交代码:git commit(伴随写文件)
		"plan":    false, // 只出方案
		"review":  false, // 只做评审
		"test":    false, // 只跑测试
		"":        false, // 未指定 mode
		"unknown": false,
	}
	for mode, want := range cases {
		if got := ModeRequiresWrite(mode); got != want {
			t.Errorf("ModeRequiresWrite(%q) = %v, want %v", mode, got, want)
		}
	}
}
