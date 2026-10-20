package pi

import (
	"context"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
)

// 本文件是迭代 7 审计新增用例(先红后绿)。
// 关注:决策取值校验、输出截断的显式失败、凭据不进错误消息、
// 进程已退出但管道未关闭时必须判成功(约定 3)。

// TestParseOutputRejectsInvalidDecision 非法决策取值不得进入路由,
// 但必须留下可判定的痕迹(否则用户只看到"没有决策")。
func TestParseOutputRejectsInvalidDecision(t *testing.T) {
	resp := parseOutput([]byte(`{"decision":"APPROVED|REJECTED|CONTINUE|DONE|WAIT_USER","summary":"模板"}`))
	if resp.Decision != "" {
		t.Errorf("非法决策取值不得进入路由, got %q", resp.Decision)
	}
	if resp.Data["decision_invalid"] != true {
		t.Errorf("非法决策必须留下可判定的痕迹, data=%v", resp.Data)
	}
	if resp.Data["decision_raw"] == "" {
		t.Errorf("应保留原始取值便于定位, data=%v", resp.Data)
	}
}

// TestParseOutputAcceptsCustomDecision 自定义决策(如 REFACTOR)必须照常透传:
// 决策取值不枚举白名单,只校验形态。
func TestParseOutputAcceptsCustomDecision(t *testing.T) {
	resp := parseOutput([]byte(`{"decision":"refactor","summary":"需重构"}`))
	if resp.Decision != coreagent.AgentDecision("REFACTOR") {
		t.Errorf("decision = %q, want REFACTOR", resp.Decision)
	}
}

// TestExecuteTruncatedOutputFails 显式失败优于静默成功(约定 14):
// 输出被截断时尾部 JSON 必然不完整,结构化结果不可信。
func TestExecuteTruncatedOutputFails(t *testing.T) {
	bin := writeFakeCLI(t, "big-pi", `i=0
while [ $i -lt 9000 ]; do
  printf '%01024d\n' 0
  i=$((i+1))
done
`)
	a := New(Config{Bin: bin, DefaultTimeoutSeconds: 120})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "大输出", Mode: "plan"})
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

// TestExecuteProcessErrorRedactsSecrets 凭据不得进错误消息(约定 8)。
func TestExecuteProcessErrorRedactsSecrets(t *testing.T) {
	const secret = "supersecretvalue"
	bin := writeFakeCLI(t, "leaky-pi",
		`echo "PI_API_KEY=`+secret+`" >&2; exit 1`)
	a := New(Config{
		Bin:      bin,
		AgentEnv: map[string]string{"PI_API_KEY": secret},
	})
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Mode: "plan"})
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

// TestExecuteWaitDelayTreatedAsSuccess 进程已成功退出、只是孤儿子进程
// 延迟关闭管道时,Wait 返回 exec.ErrWaitDelay —— 输出完整,必须判成功。
func TestExecuteWaitDelayTreatedAsSuccess(t *testing.T) {
	bin := writeFakeCLI(t, "orphan-pi",
		`printf '{"decision":"DONE","summary":"ok"}'; sleep 2 & exit 0`)
	a := New(Config{Bin: bin, WaitDelaySeconds: 1, DefaultTimeoutSeconds: 30})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Mode: "plan"})
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
