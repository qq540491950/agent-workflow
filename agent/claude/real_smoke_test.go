package claude

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/permission"
)

// 真机冒烟:验证真实 claude CLI 路径。
//
// 为什么需要:轮次 27/28 的 A7(尾部 JSON 提取)与 A8(输出上限)只由单元
// 测试覆盖,而单元测试用的是**假二进制**。假二进制无法暴露这类问题:
//   - 真实 CLI 是否接受我们拼的全部参数(--output-format / --disallowedTools …)
//   - 真实输出结构是否与解析假设一致(正文 + 尾部 JSON)
//   - 真实进程在超时/大输出/异常退出时的行为
// 实测发现:真实 CLI 返回 `OK\n\n{"decision": "DONE", "summary": "ok"}`,
// FirstJSONObject 正确取到尾部对象 —— 若沿用旧的"不跳过字符串字面量的
// 括号"实现,这类带空格的真实输出同样能过,但带嵌套对象就会失败。
//
// 无 claude CLI / 无凭据 / 无网络时全部 skip,不阻塞 CI。

func requireClaudeCLI(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("short 模式跳过真实 CLI 冒烟")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("本机未安装 claude CLI,跳过真实冒烟")
	}
	if out, err := exec.Command(bin, "--version").CombinedOutput(); err != nil {
		t.Skipf("claude CLI 不可用: %v (输出 %s)", err, out)
	}
}

// TestRealClaudeEndToEnd 真实调用一次,验证参数与输出解析。
func TestRealClaudeEndToEnd(t *testing.T) {
	requireClaudeCLI(t)
	a := New(Config{Perms: permission.NewManager()})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task:         "回复 OK",
		Mode:         "plan",
		Timeout:      90,
		Instructions: `只输出一个 JSON:{"decision":"DONE","summary":"ok"}`,
	})
	if err != nil {
		// 无凭据/无网络属于预期环境差异,跳过而不是失败 ——
		// 但错误必须是结构化的(不能是裸字符串或 panic)
		if !strings.Contains(err.Error(), "CLAUDE_") {
			t.Fatalf("错误形态不对,应带 CLAUDE_ 前缀码: %v", err)
		}
		t.Skipf("真实调用未成功(无凭据/网络),但错误形态正确: %v", err)
	}
	if resp.Status != coreagent.StatusSuccess {
		t.Errorf("status = %q, want SUCCESS (%s)", resp.Status, resp.Output)
	}
	// 关键断言:尾部 JSON 被正确提取(这是 A7 修复的核心)
	if resp.Decision != coreagent.DecisionDone {
		t.Errorf("decision = %q, want DONE —— 尾部 JSON 提取失败 (output=%q)",
			resp.Decision, resp.Output)
	}
	if resp.Summary == "" {
		t.Errorf("summary 为空 —— 结构化字段未解析 (output=%q)", resp.Output)
	}
	// Output 必须保留原始正文(没有被 JSON 覆盖掉)
	if !strings.Contains(resp.Output, "decision") {
		t.Errorf("Output 丢失了原始内容: %q", resp.Output)
	}
}

// TestRealClaudeTimeout 真实进程的超时路径:极短超时时必须返回
// CLAUDE_TIMEOUT,不能挂死(验证 ConfigureProcess 进程组击杀 + WaitDelay
// 在真实 CLI 上确实生效)。
func TestRealClaudeTimeout(t *testing.T) {
	requireClaudeCLI(t)
	a := New(Config{Perms: permission.NewManager()})
	start := time.Now()
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task:    "写一篇 5000 字的长文",
		Mode:    "plan",
		Timeout: 1, // 1 秒:真实 CLI 不可能完成
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Skip("本次恰好在 1 秒内完成,跳过超时断言")
	}
	if elapsed > 30*time.Second {
		t.Errorf("超时未生效,耗时 %v —— 进程组击杀可能失效", elapsed)
	}
	if !strings.Contains(err.Error(), "CLAUDE_TIMEOUT") {
		t.Errorf("应为 CLAUDE_TIMEOUT,实际: %v", err)
	}
}
