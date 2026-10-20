package agent

// ModeRequiresWrite 判断某个节点 mode 是否隐含**写操作**。
//
// 唯一来源(AGENTS.md 硬性约定 #13 的同源精神):运行期的节点权限检查
// (workflow/runtime/executor.go)与适配器自身的写权限兜底
// (agent/pi/pi.go)必须认同一份 mode 集合。此前两边各写一份:
// 运行期只查 execute|fix,适配器查 execute|fix|submit ——
// 于是 `mode: submit` 的节点在工作流级 permissions 段收紧写权限时
// **不受运行期约束**(适配器只看得到全局策略),工作流的收紧规则对它无效。
//
// 语义:submit 表示"提交代码"(git commit),必然伴随写文件 → 需要写权限。
func ModeRequiresWrite(mode string) bool {
	switch mode {
	case "execute", "fix", "submit":
		return true
	default:
		return false
	}
}
