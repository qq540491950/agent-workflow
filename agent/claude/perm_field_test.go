package claude

import (
	"context"
	"reflect"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
)

// 本文件锁住"claude 适配器不持有 Permission Manager"这一决策(迭代 13)。
//
// 背景:Config 上曾挂着 Perms 字段,但适配器**从不读它** —— 死字段。
// 调用方(claude.New(claude.Config{Perms: a.Perms}))以为"权限已交给适配器
// 兜底",实际那层兜底根本不存在:与 default_working_dir 同族的
// "用户以为生效了、实际没生效"(约定 14)。
//
// 删掉它而不是"让适配器也查",理由:
//   - 写操作:CLI 层用 --disallowedTools 硬禁 Write/Edit/MultiEdit/
//     NotebookEdit/Bash(git commit:*)/Bash(git push:*) —— 适配器物理上
//     写不了文件,"查写权限"在这里没有可观测语义;
//   - 读操作:运行期的节点检查对**所有** agent 节点都查 fs:read,
//     且查的是**有效策略**(全局 ∧ 工作流 permissions 段)—— 比适配器
//     能查到的全局策略更严,两边结论不会相反。
//
// 因此权限判定留在唯一的权威位置(运行期节点检查),适配器不再挂一个
// 看得见却不起作用的字段。

// TestConfigHasNoPermsField claude.Config 不得再有 Perms 字段。
//
// 用反射而不是编译期断言:字段一旦加回来,这里立刻失败并指出"要么删字段、
// 要么真去读它",不会静默复活成死字段。
func TestConfigHasNoPermsField(t *testing.T) {
	if _, ok := reflect.TypeOf(Config{}).FieldByName("Perms"); ok {
		t.Error("claude.Config 不应有 Perms 字段(适配器从不读它:死字段," +
			"与 default_working_dir 同族);要么删掉,要么让适配器真的查有效权限")
	}
}

// TestWriteToolsAreDisallowedAtCLILevel 锁住"适配器写不了文件"这个前提。
//
// 上面的决策依赖 CLI 层硬禁写工具 —— 这个前提消失时(比如有人放宽了
// --disallowedTools),"适配器不需要查写权限"的结论就不再成立,
// 必须重新评估是否要在适配器层补一道检查。
func TestWriteToolsAreDisallowedAtCLILevel(t *testing.T) {
	bin := writeFakeCLI(t, "args-claude", `printf '{"result":"ARGS:%s","is_error":false}' "$*"`)
	a := New(Config{Bin: bin})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{Task: "t", Mode: "plan"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	args := resp.Output
	if !strings.Contains(args, "--disallowedTools") {
		t.Fatalf("CLI 参数必须带 --disallowedTools,实际参数: %s", args)
	}
	for _, banned := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "git commit", "git push"} {
		if !strings.Contains(args, banned) {
			t.Errorf("CLI 参数必须硬禁写工具 %q,实际参数: %s", banned, args)
		}
	}
}
