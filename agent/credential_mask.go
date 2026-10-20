package agent

import "agentworkflow/logx"

// 本文件是"自由文本里的凭据形态"脱敏的**对外入口**(约定 8:敏感信息
// 不进日志、API 返回掩码)。
//
// ---- 规则只有一份来源 ----
//
// 迭代 12 之前这里自带一份"值形态"规则(已知前缀 / JWT / PEM / 高熵),
// 而 logx 另有一份"键名"规则,两套并行、覆盖范围不同 —— 同一个凭据在
// 不同出口结论不一致(终审实测:裸 `Bearer <JWT>` 落库并经所有 API 出口
// 泄漏)。
//
// 现在:**值的形态规则全部收敛到 logx**(logx/credential.go 是唯一定义
// 处),本文件只剩一个薄封装。agent 依赖 logx 单向,没有环。
//
// ---- 为什么这里要保留一个"只看值形态"的入口 ----
//
// logx.Mask 的规则是"键名敏感就整值替换",会把**代码里的标识符**一并
// 抹掉 —— 而 git diff 制品正是代码/配置片段:
//
//	token = getToken()        → logx.Mask: token = ***
//	const apiKey = cfg.key    → logx.Mask: const apiKey = ***
//
// 那样会把审计证据("改了什么")毁掉 —— 第八轮把"artifact 不脱敏"钉死
// 在测试里,理由正是"不要破坏 git diff 产物的可读性"。
//
// logx.MaskCredentialValues 只按**值形态**判定,键名指向凭据 **且** 值
// 本身呈凭据形态时才替换,代码标识符原样保留:
//
//	ANTHROPIC_API_KEY=sk-ant-xxx  → ANTHROPIC_API_KEY=***
//	authorization: Bearer eyJ...   → authorization: Bearer ***
//	token = getToken()             → 原样保留
//
// 键名判定复用 logx.keySensitivity(与结构化脱敏同一份来源),不另写一份
// 白名单(约定 13 的同源精神)。

// MaskCredentialValues 脱敏文本里"凭据形态"的值,返回新字符串。
//
// 只替换**值**,键名与上下文原样保留 —— 可读性(审计价值)不退化。
// 非凭据形态的值(代码标识符、普通配置值)一律原样保留。
//
// 实现:转发到 logx.MaskCredentialValues(唯一定义处)。
func MaskCredentialValues(text string) string {
	return logx.MaskCredentialValues(text)
}
