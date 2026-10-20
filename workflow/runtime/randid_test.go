package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 回归(#234):随机 ID 的熵源失败被吞掉 → ID 变成**全零**。
//
// 此前 newID 是 `_, _ = rand.Read(b)`:熵不足时 b 保持全零 →
// 执行 ID 恒为 "exec-0000000000000000" → insertExecution 反复撞同一个
// ID(它会先查存在性再插入)→ 要么重试 5 次后报"反复冲突",
// 要么直接覆盖既有执行(约定 14 同族:该失败却成功)。
//
// 修法:熵源失败必须让调用方知道 —— newID 返回 error。
//
// 测试接缝:randRead 是包级变量,测试临时替换它制造熵源失败。
// 本包没有 t.Parallel 的用例(全量 grep 确认),替换是安全的。

// errEntropy 模拟熵源失败。
var errEntropy = errors.New("模拟熵源失败")

func TestNewIDPropagatesEntropyFailure(t *testing.T) {
	orig := randRead
	t.Cleanup(func() { randRead = orig })
	randRead = func(b []byte) (int, error) { return 0, errEntropy }

	got, err := newID("exec")
	if err == nil {
		t.Fatalf("熵源失败必须返回错误,实际得到 %q", got)
	}
	if !errors.Is(err, errEntropy) {
		t.Errorf("错误应保留原始原因,实际 %v", err)
	}
	if got != "" {
		t.Errorf("失败时不得返回半成品 ID,实际 %q", got)
	}
}

// 正常路径必须仍然产出非全零、长度正确的 ID(防止"永远失败"的假修法)。
func TestNewIDSucceedsNormally(t *testing.T) {
	got, err := newID("exec")
	if err != nil {
		t.Fatalf("正常路径不应失败: %v", err)
	}
	if !strings.HasPrefix(got, "exec-") {
		t.Errorf("ID 应带前缀,实际 %q", got)
	}
	if len(got) != len("exec-")+16 {
		t.Errorf("ID 长度 = %d, 期望 %d", len(got), len("exec-")+16)
	}
	if strings.Trim(got, "0-") == "" {
		t.Errorf("ID 不应是全零: %q", got)
	}
}

// 回归(#234):熵源失败时启动执行必须**失败**,而不是产出一个全零 ID。
func TestRunFailsWhenEntropyUnavailable(t *testing.T) {
	f := newFixture(t, nil)
	wf := loadWf(t, f.engine, `
version: "1"
workflow:
  id: entropy-wf
  name: Entropy
nodes:
  - id: plan
    type: agent
    agent: mock-claude
    mode: plan
edges: []
`)

	orig := randRead
	t.Cleanup(func() { randRead = orig })
	randRead = func(b []byte) (int, error) { return 0, errEntropy }

	exec, err := f.engine.Start(context.Background(), wf, "任务", nil)
	if err == nil {
		t.Fatalf("熵源失败时启动执行必须失败,实际拿到执行 %v", exec)
	}
	if !strings.Contains(err.Error(), "熵") {
		t.Errorf("错误信息应说明是熵源不可用,实际: %v", err)
	}
}
