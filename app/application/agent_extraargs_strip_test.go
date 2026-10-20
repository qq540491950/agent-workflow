package application

import (
	"strings"
	"testing"

	"agentworkflow/agent"
)

// 本文件锁住迭代 13 的 F 项结论:**前端不得剔除 extra_args 里的凭据位**。
//
// 背景:迭代 10 提过一条"可选加固" —— 前端保存时剔除凭据位,或在
// extra_args 未被编辑时不回传。当时的判断是"后端已保证安全,无需改动"。
//
// 本用例把"为什么不能改"钉死:那不是加固,是**回归**。
//
// 后端按**选项名**合并(agent.MergeExtraArgs,约定 5 的同源实现):
// GET 返回掩码 "***",PUT 时 "***" = 沿用已存值。
// 前端若把凭据位剔除(或整段不回传),回传的列表里就没有那个选项 ——
// 后端无从恢复,库里已存的**真实凭据**被抹掉。
// 后果:Agent 每次调用都不带鉴权参数 → 鉴权失败,而用户只看到"已保存"。
//
// 凭据的权威处理点只有后端一处。前端不参与,也不该参与。

// TestUpdateConfigExtraArgsStrippingCredentialLosesIt 前端剔除凭据位
// (或省略 extra_args)会**丢掉**已存凭据 —— 这就是不做那条"加固"的原因。
func TestUpdateConfigExtraArgsStrippingCredentialLosesIt(t *testing.T) {
	a := newTestApp(t)
	svc := a.AgentSvc

	if err := svc.UpdateConfig("claude-code", agent.AgentConfig{
		ExtraArgs: []string{"--verbose", "--api-key", "sk-real-secret"},
	}); err != nil {
		t.Fatalf("初次配置: %v", err)
	}

	// 前端拿到掩码配置后,**剔除凭据位**再回传(拟议的"加固"做法)
	got, err := svc.GetConfig("claude-code")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stripped := stripCredentialSlots(got.ExtraArgs)
	if err := svc.UpdateConfig("claude-code", agent.AgentConfig{
		Model:     got.Model,
		ExtraArgs: stripped,
	}); err != nil {
		t.Fatalf("剔除后保存: %v", err)
	}

	stored := strings.Join(storedExtraArgs(t, a, "claude-code"), " ")
	if strings.Contains(stored, "sk-real-secret") {
		t.Fatalf("剔除凭据位后凭据竟还在(说明合并按名字回填了): %q", stored)
	}
	if strings.Contains(stored, agent.MaskedValue) {
		t.Errorf("库里不应出现字面量 %s: %q", agent.MaskedValue, stored)
	}
	if !strings.Contains(stored, "--verbose") {
		t.Errorf("普通参数必须保留: %q", stored)
	}
	// 这就是结论:剔除 → 凭据(连同选项名)一起没了,后端无从恢复。
	// 所以前端**不能**剔除,必须把 "***" 原样回传给后端合并。
	if strings.Contains(stored, "--api-key") {
		t.Errorf("剔除凭据位应连选项名一起消失(证明前端无法只剔除值): %q", stored)
	}
}

// stripCredentialSlots 模拟"前端剔除凭据位":把值是掩码哨兵的选项
// (名与值)整对剔除 —— 前端只看得见 "***",看不到真实值。
func stripCredentialSlots(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if i+1 < len(args) && args[i+1] == agent.MaskedValue &&
			strings.HasPrefix(args[i], "--") {
			i++ // 跳过整个选项(名 + 值)
			continue
		}
		out = append(out, args[i])
	}
	return out
}
