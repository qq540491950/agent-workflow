package agent

import (
	"testing"

	"agentworkflow/workflow/model"
)

// TestModeRequiresWrite 锁定"哪些 mode 隐含写操作"的唯一清单。
//
// 这份清单被两处消费:运行期的节点权限检查(workflow/runtime/executor.go)
// 与 pi 适配器的写权限兜底(agent/pi/pi.go)。两边共用同一个函数 ——
// 新增 mode 时只改这一处,不会出现"一边要写权限、另一边不要"。
//
// **未知/空 mode 恒为 true(fail closed)**:mode 拼错("exectue")时,
// 若按"不在只读清单里"处理,写权限检查会被跳过 —— 一个可能要改文件的
// 节点在权限收紧的工作流里畅通无阻(约定 14:用户以为生效了实际没有)。
// 因此"不认识"一律按"最危险"处理。
//
// 这条语义**改变**了本文件此前的断言(此前 unknown/空 → false):
// 未知值不再被当作"只读",而是"按会写处理"。
func TestModeRequiresWrite(t *testing.T) {
	cases := map[string]bool{
		"execute": true,  // 执行任务:改文件
		"fix":     true,  // 修复:改文件
		"submit":  true,  // 提交代码:git commit(伴随写文件)
		"plan":    false, // 只出方案
		"review":  false, // 只做评审
		"test":    false, // 只跑测试
		"":        true,  // 未指定 mode:不认识 → 按会写处理(fail closed)
		"unknown": true,  // 不认识的 mode:按会写处理(fail closed)
		"exectue": true,  // execute 拼错:同样按会写处理
	}
	for mode, want := range cases {
		if got := ModeRequiresWrite(mode); got != want {
			t.Errorf("ModeRequiresWrite(%q) = %v, want %v", mode, got, want)
		}
	}
}

// TestModeRequiresWriteMatchesModel 锁住"唯一来源":agent 包的判定必须
// 与 model.ModeRequiresWrite **逐个 mode 一致**(取值域也在 model 里,
// 约定 13:校验期与运行期同源,不允许各写一份清单)。
func TestModeRequiresWriteMatchesModel(t *testing.T) {
	for _, mode := range model.AllModes() {
		if got, want := ModeRequiresWrite(mode), model.ModeRequiresWrite(mode); got != want {
			t.Errorf("mode %q: agent.ModeRequiresWrite=%v, model.ModeRequiresWrite=%v", mode, got, want)
		}
	}
	// 未知 mode 也要一致(fail closed 不能只在一边生效)。
	for _, mode := range []string{"", "unknown", "exectue"} {
		if got, want := ModeRequiresWrite(mode), model.ModeRequiresWrite(mode); got != want {
			t.Errorf("mode %q: agent.ModeRequiresWrite=%v, model.ModeRequiresWrite=%v", mode, got, want)
		}
	}
}
