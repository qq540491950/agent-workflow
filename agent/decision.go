package agent

import "strings"

// 决策取值的归一化与形态校验。
//
// 约定 1:路由只能由**结构化** decision 驱动,禁止字符串猜测。
// 约定 14:"该失败却成功"等同 P0 —— 一个非法决策值流进路由,
// 条件永远不成立,工作流要么走默认分支(做错事)要么在决策点失败
// (报错信息还看不出是模型说了怪话)。
//
// 这里**不枚举白名单**:工作流允许自定义决策(如 REFACTOR,见
// workflow/runtime/routing_test.go),白名单会把合法用法判死。
// 只校验形态 —— 决策必须是一个标识符样的词。

// maxDecisionLen 是决策取值的长度上限。
const maxDecisionLen = 64

// NormalizeDecision 归一化并校验模型给出的决策取值。
//
// 归一化:去首尾空白 + 转大写(与既有约定一致:决策值一律大写)。
// 校验:只允许 [A-Z][A-Z0-9_]* 且长度不超过 maxDecisionLen。
//
// 第二个返回值为 false 表示取值非法,调用方**不得**把它当作决策
// 送进路由 —— 典型非法形态是模型把提示词里的模板原样吐回来:
// {"decision": "APPROVED|REJECTED|CONTINUE|DONE|WAIT_USER"}
func NormalizeDecision(raw string) (AgentDecision, bool) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" || len(s) > maxDecisionLen {
		return "", false
	}
	if s[0] < 'A' || s[0] > 'Z' {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return "", false
		}
	}
	return AgentDecision(s), true
}

// ApplyDecision 把模型给出的原始决策写入响应。
//
// 合法取值 → resp.Decision;
// 非法取值 → **不写** Decision(绝不把怪值送进路由,约定 1),
// 改为在 Data 里留痕并在摘要里标注 —— 降级必须可判定,
// 否则用户只看到"没有决策",无从判断模型到底说了什么。
//
// 调用前应先填好 resp.Summary(模型的摘要),这里只做追加。
func ApplyDecision(resp *AgentResponse, raw string) {
	if resp == nil || raw == "" {
		return
	}
	dec, ok := NormalizeDecision(raw)
	if ok {
		resp.Decision = dec
		return
	}
	if resp.Data == nil {
		resp.Data = map[string]any{}
	}
	resp.Data["decision_invalid"] = true
	resp.Data["decision_raw"] = raw
	note := "决策取值非法: " + raw
	if resp.Summary == "" {
		resp.Summary = note
		return
	}
	resp.Summary += "(" + note + ")"
}
