package agent

import "agentworkflow/workflow/model"

// ModeRequiresWrite 判断某个节点 mode 是否隐含**写操作**。
//
// 唯一来源(AGENTS.md 硬性约定 #13 的同源精神):运行期的节点权限检查
// (workflow/runtime/executor.go)、适配器自身的写权限兜底
// (agent/pi/pi.go)与校验期的 mode 取值域必须认同一份 mode 清单。
// 此前两边各写一份:
// 运行期只查 execute|fix,适配器查 execute|fix|submit ——
// 于是 `mode: submit` 的节点在工作流级 permissions 段收紧写权限时
// **不受运行期约束**(适配器只看得到全局策略),工作流的收紧规则对它无效。
//
// 现在清单在 workflow/model(mode 取值域的唯一定义处):校验期、运行期、
// 适配器全部引用同一份,新增 mode 只改一处。
//
// 语义:submit 表示"提交代码"(git commit),必然伴随写文件 → 需要写权限。
//
// **未知/空 mode 恒为 true(fail closed)**:不认识的 mode 一律按"会写"
// 处理 —— 否则 mode 拼错("exectue")会让写权限检查被跳过,一个可能要
// 改文件的节点在权限收紧的工作流里畅通无阻(约定 14)。
func ModeRequiresWrite(mode string) bool {
	return model.ModeRequiresWrite(mode)
}
