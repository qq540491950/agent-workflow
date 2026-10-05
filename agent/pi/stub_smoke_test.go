package pi

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/permission"
)

// 替身二进制冒烟:本机没有装 pi-agent CLI,但 A7(尾部 JSON 提取)与
// A8(输出上限)此前**只在 parseOutput 的单元测试里**覆盖过 —— 那些用例
// 直接调函数,不经过子进程。没有真机验证就无法确认:
//
//   - 拼给 pi 的全部参数(--mode/--task/--model/--instructions/--cwd)
//     不会让真实 CLI 报错退出
//   - 真实 stdout(含正文 + 尾部 JSON、含嵌套对象)能被正确解析
//   - 超时真的生效(进程组击杀 + WaitDelay 在真实子进程上有效)
//
// 用一个"同形替身"补上进程这一层:它接收与真实 pi 相同的参数,按场景
// 打印对应形态的 stdout,行为上等价于真实 CLI 的输出面。

// writeStub 写一个可执行的 shell 替身(跳过 Windows)。
func writeStub(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("替身脚本仅适用于类 Unix 环境")
	}
	path := filepath.Join(t.TempDir(), "pi-stub.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// TestPiStubEndToEndNestedJSON 真实进程形态的端到端:替身打印
// 「正文 + 含嵌套对象的尾部 JSON」—— 这正是旧实现
// (strings.LastIndex("{") + 直接截断)会解析失败的形态。
func TestPiStubEndToEndNestedJSON(t *testing.T) {
	bin := writeStub(t, `echo "分析中..."
echo "结果如下:"
echo '{"status":"ok","decision":"REJECTED","summary":"需要重构","detail":{"file":"a.go","lines":[1,2]}}'
`)
	a := New(Config{Bin: bin, Perms: permission.NewManager(), DefaultTimeoutSeconds: 30})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task: "检查代码", Mode: "review", Instructions: "给出结论",
		Model: "some-model", WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Decision != coreagent.DecisionRejected {
		t.Errorf("decision = %q, want REJECTED —— 嵌套对象下的尾部 JSON 提取失败 (output=%q)",
			resp.Decision, resp.Output)
	}
	if resp.Summary != "需要重构" {
		t.Errorf("summary = %q, want 需要重构 (output=%q)", resp.Summary, resp.Output)
	}
	if resp.Status != coreagent.StatusSuccess {
		t.Errorf("status = %q, want SUCCESS", resp.Status)
	}
	// Output 必须保留原始正文
	if !strings.Contains(resp.Output, "分析中") {
		t.Errorf("Output 丢失了原始正文: %q", resp.Output)
	}
}

// TestPiStubReceivesAllArgs 验证拼给子进程的全部参数都被送达 ——
// 参数名拼错在替身上同样会暴露(真实 CLI 会因未知参数报错退出)。
func TestPiStubReceivesAllArgs(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args.txt")
	bin := writeStub(t, `printf '%s\n' "$@" > `+out+`
echo '{"decision":"DONE","summary":"ok"}'
`)
	a := New(Config{Bin: bin, Perms: permission.NewManager(), DefaultTimeoutSeconds: 30})
	wd := t.TempDir()
	if _, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task: "TASK-1", Mode: "review", Model: "MODEL-X",
		Instructions: "INSTR-Y", WorkingDir: wd,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(raw)
	for _, want := range []string{
		"--mode", "review",
		"--task", "TASK-1",
		"--model", "MODEL-X",
		"--instructions", "INSTR-Y",
		"--cwd", wd,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("参数 %q 未送达子进程,实际参数:\\n%s", want, args)
		}
	}
}

// TestPiStubTimeout 真实子进程的超时路径:替身 sleep 到天荒地老,
// 必须按 CLAUDE_TIMEOUT 之外的 pi 码(PiTimeout)返回且不挂死。
func TestPiStubTimeout(t *testing.T) {
	bin := writeStub(t, "sleep 60\n")
	a := New(Config{Bin: bin, Perms: permission.NewManager(), DefaultTimeoutSeconds: 1})
	start := time.Now()
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task: "长任务", Mode: "plan",
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("超时后应返回错误")
	}
	if elapsed > 20*time.Second {
		t.Errorf("超时未生效,耗时 %v —— 进程组击杀可能失效", elapsed)
	}
	if !strings.Contains(err.Error(), "PI_TIMEOUT") {
		t.Errorf("应为 PI_TIMEOUT,实际: %v", err)
	}
}

// TestPiStubLargeOutputIsBounded 验证 A8 的输出上限在真实子进程上生效:
// 替身打印远超上限的内容,内存里只应保留 BoundedBuffer 的额度,
// 且 Output 里带显式截断标记(静默截断会让用户以为 Agent 真只输出那么多)。
func TestPiStubLargeOutputIsBounded(t *testing.T) {
	// 每行 1024 字节,共 20000 行 ≈ 20MB,远超 8MB 上限
	bin := writeStub(t, `i=0
while [ $i -lt 20000 ]; do
  printf '%01024d\n' 0
  i=$((i+1))
done
echo '{"decision":"DONE","summary":"ok"}'
`)
	a := New(Config{Bin: bin, Perms: permission.NewManager(), DefaultTimeoutSeconds: 120})
	start := time.Now()
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task: "大输出", Mode: "plan",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = time.Since(start)
	// Output 必须被截断(不能是 20MB),且带显式标记
	if len(resp.Output) > coreagent.MaxOutputBytes+4096 {
		t.Errorf("Output 未被上限约束: %d 字节(上限 %d)", len(resp.Output), coreagent.MaxOutputBytes)
	}
	if !strings.Contains(resp.Output, "已丢弃") {
		t.Errorf("截断必须显式可见(带标记),实际长度 %d", len(resp.Output))
	}
}

// TestPiStubStderrIsNotLeakedIntoOutput stderr 不得混进 Output(否则
// CLI 的调试日志会污染 Agent 输出,并可能带出敏感值)。
func TestPiStubStderrIsNotLeakedIntoOutput(t *testing.T) {
	bin := writeStub(t, `echo "警告: 内部调试信息" >&2
echo '{"decision":"DONE","summary":"ok"}'
`)
	a := New(Config{Bin: bin, Perms: permission.NewManager(), DefaultTimeoutSeconds: 30})
	resp, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task: "t", Mode: "plan",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(resp.Output, "内部调试信息") {
		t.Errorf("stderr 混进了 Output: %q", resp.Output)
	}
}

// TestPiStubNonZeroExit 替身以非零码退出:必须返回结构化错误
// (PI_PROCESS_ERROR)并带上 stderr 线索,而不是静默成功。
func TestPiStubNonZeroExit(t *testing.T) {
	bin := writeStub(t, `echo "pi-agent: 参数 --cwd 不被支持" >&2
exit 3
`)
	a := New(Config{Bin: bin, Perms: permission.NewManager(), DefaultTimeoutSeconds: 30})
	_, err := a.Execute(context.Background(), coreagent.AgentRequest{
		Task: "t", Mode: "plan", WorkingDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("子进程非零退出必须返回错误")
	}
	if !strings.Contains(err.Error(), "PI_PROCESS_ERROR") {
		t.Errorf("应为 PI_PROCESS_ERROR,实际: %v", err)
	}
	// 错误里应能看到 stderr 的线索(用户据此定位参数问题)
	if !strings.Contains(err.Error(), "--cwd") {
		t.Errorf("错误应包含 stderr 线索,实际: %v", err)
	}
}
