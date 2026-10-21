package claude

import (
	"context"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
)

// 本文件是迭代 7 审计新增用例(先红后绿)。
// 关注:结构化决策的取值与提取位置、输出截断的显式失败、凭据不进错误消息、
// 进程已退出但管道未关闭时必须判成功(约定 3)。

// TestParseOutputUsesTrailingJSON 决策必须取**尾部**的 JSON 对象。
//
// 红:模型正文里常先出现一个 JSON 片段(甚至是提示词里的示例),
// 旧实现取"第一个能配平的对象",于是示例被当成了决策 —— 路由按错的
// 决策走,工作流走向与模型结论相反的分支。
func TestParseOutputUsesTrailingJSON(t *testing.T) {
	a := New(Config{})
	// 正文先提到一个 APPROVED 片段,真正的结论在尾部
	out := `{"result":"示例 {\"decision\": \"APPROVED\"} —— 实际结论 {\"decision\": \"REJECTED\", \"summary\": \"需重构\"}","is_error":false}`
	resp, err := a.parseOutput([]byte(out), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Decision != coreagent.DecisionRejected {
		t.Errorf("decision = %q, want REJECTED —— 应取尾部 JSON,实际取到了正文里的片段", resp.Decision)
	}
	if resp.Summary != "需重构" {
		t.Errorf("summary = %q, want 需重构", resp.Summary)
	}
}

// TestParseOutputRejectsInvalidDecision 模型把提示词里的模板原样吐回来时,
// 那个字符串不是决策:不得进入路由,但必须留下痕迹(否则用户只看到
// "没有决策",无从判断模型到底说了什么)。
func TestParseOutputRejectsInvalidDecision(t *testing.T) {
	a := New(Config{})
	out := `{"result":"我将按要求返回 {\"decision\": \"APPROVED|REJECTED|CONTINUE|DONE|WAIT_USER\", \"summary\": \"...\"}","is_error":false}`
	resp, err := a.parseOutput([]byte(out), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Decision != "" {
		t.Errorf("非法决策取值不得进入路由, got %q", resp.Decision)
	}
	if resp.Data["decision_invalid"] != true {
		t.Errorf("非法决策必须留下可判定的痕迹, data=%v", resp.Data)
	}
	if resp.Data["decision_raw"] == "" {
		t.Errorf("应保留原始取值便于定位, data=%v", resp.Data)
	}
	// 迭代 31:没有可用决策 = 路由无从驱动 → 显式失败(约定 1 + 14)。
	// 旧行为是 SUCCESS + 空决策 → 路由静默走默认分支。
	if resp.Status != coreagent.StatusFailed {
		t.Errorf("status = %q, want FAILED —— 非法决策不得当成成功", resp.Status)
	}
	if resp.Error == "" {
		t.Error("失败必须给出可读原因")
	}
}

// TestParseOutputIsErrorStringForm is_error 写成字符串("true")时,
// 错误信号不能因为类型不符被整段丢弃 —— 旧实现里整个信封解析失败,
// 于是"额度耗尽"被当成成功返回。
func TestParseOutputIsErrorStringForm(t *testing.T) {
	a := New(Config{})
	resp, err := a.parseOutput([]byte(`{"result":"quota exceeded","is_error":"true"}`), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Status != coreagent.StatusFailed {
		t.Fatalf("status = %q, want FAILED —— is_error 的字符串形态被丢弃了", resp.Status)
	}
	if resp.Error != "quota exceeded" {
		t.Errorf("error = %q, want quota exceeded", resp.Error)
	}
}

// TestExecuteTruncatedOutputFails 显式失败优于静默成功(约定 14):
// 输出超过上限被截断时,结构化结果不可信,必须显式失败并说明原因,
// 而不是返回 SUCCESS + 空决策让路由静默走默认分支。
func TestExecuteTruncatedOutputFails(t *testing.T) {
	bin := writeFakeCLI(t, "big-claude", `i=0
while [ $i -lt 9000 ]; do
  printf '%01024d\n' 0
  i=$((i+1))
done
`)
	a := New(Config{Bin: bin, DefaultTimeoutSeconds: 120})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "大输出"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != coreagent.StatusFailed {
		t.Errorf("status = %q, want FAILED —— 截断后必须显式失败", resp.Status)
	}
	if !strings.Contains(resp.Error, "截断") {
		t.Errorf("错误信息应说明被截断, got %q", resp.Error)
	}
	if !strings.Contains(resp.Output, "已丢弃") {
		t.Errorf("截断必须显式可见, got len=%d", len(resp.Output))
	}
}

// TestExecuteProcessErrorRedactsSecrets 凭据不得进错误消息(约定 8):
// CLI 常把环境变量原样打进 stderr,而错误消息会把 stderr 拼进去。
func TestExecuteProcessErrorRedactsSecrets(t *testing.T) {
	const secret = "supersecretvalue"
	bin := writeFakeCLI(t, "leaky-claude",
		`echo "ANTHROPIC_AUTH_TOKEN=`+secret+`" >&2; exit 1`)
	a := New(Config{
		Bin:      bin,
		AgentEnv: map[string]string{"ANTHROPIC_AUTH_TOKEN": secret},
	})
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err == nil {
		t.Fatal("子进程非零退出必须返回错误")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("凭据泄漏进错误消息: %v", err)
	}
	if !strings.Contains(err.Error(), coreagent.MaskedValue) {
		t.Errorf("脱敏后应保留 *** 标记: %v", err)
	}
}

// TestExecuteProcessErrorRedactsInheritedSecret 父进程环境里的凭据
// 同样不能随 stderr 进错误消息:子进程继承了它们,CLI 会原样回显。
func TestExecuteProcessErrorRedactsInheritedSecret(t *testing.T) {
	const secret = "inheritedsecretvalue"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	bin := writeFakeCLI(t, "leaky-inherited-claude",
		`echo "ANTHROPIC_API_KEY=`+secret+`" >&2; exit 1`)
	a := New(Config{Bin: bin})
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err == nil {
		t.Fatal("子进程非零退出必须返回错误")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("继承来的凭据泄漏进错误消息: %v", err)
	}
}

// TestExecuteWaitDelayTreatedAsSuccess 进程已成功退出、只是孤儿子进程
// 延迟关闭管道时,Wait 返回 exec.ErrWaitDelay —— 此时输出是完整的,
// 必须判为成功(约定 3),不能当成进程失败。
func TestExecuteWaitDelayTreatedAsSuccess(t *testing.T) {
	bin := writeFakeCLI(t, "orphan-claude",
		`echo '{"result":"{\"decision\": \"DONE\"}","is_error":false}'; sleep 2 & exit 0`)
	a := New(Config{Bin: bin, WaitDelaySeconds: 1, DefaultTimeoutSeconds: 30})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t"})
	if err != nil {
		t.Fatalf("ErrWaitDelay 应判为成功, got err=%v", err)
	}
	if resp.Status != coreagent.StatusSuccess {
		t.Errorf("status = %q, want SUCCESS", resp.Status)
	}
	if resp.Decision != coreagent.DecisionDone {
		t.Errorf("decision = %q, want DONE —— 输出应完整可用", resp.Decision)
	}
}
