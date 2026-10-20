package application

import (
	"errors"
	"strings"
	"testing"

	"agentworkflow/workflow/model"
)

// 回归(#234):随机 ID 的熵源失败被吞掉 → ID 变成**全零**。
//
// 此前 randHex 是 `_, _ = crypto_rand.Read(b)`:熵不足时 b 保持全零 →
// 工作流 ID 恒为 "wf-000000000000" → 与既有工作流**撞车** →
// SaveWorkflow 按 ID 覆盖 → 静默数据丢失(约定 14 同族:该失败却成功)。
//
// 修法:熵源失败必须让调用方知道 —— randHex 返回 error,保存路径直接失败。
//
// 测试接缝:randRead 是包级变量,测试临时替换它制造熵源失败。
// 本包没有 t.Parallel 的用例(全量 grep 确认),替换是安全的。

// errEntropy 模拟熵源失败。
var errEntropy = errors.New("模拟熵源失败")

func TestRandHexPropagatesEntropyFailure(t *testing.T) {
	orig := randRead
	t.Cleanup(func() { randRead = orig })
	randRead = func(b []byte) (int, error) { return 0, errEntropy }

	got, err := randHex(6)
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
func TestRandHexSucceedsNormally(t *testing.T) {
	got, err := randHex(6)
	if err != nil {
		t.Fatalf("正常路径不应失败: %v", err)
	}
	if len(got) != 12 {
		t.Errorf("ID 长度 = %d, 期望 12", len(got))
	}
	if strings.Trim(got, "0") == "" {
		t.Errorf("ID 不应是全零: %q", got)
	}
}

// 回归(#234):熵源失败时保存工作流必须**失败**,
// 而不是用全零 ID 覆盖既有工作流。
func TestSaveWorkflowFailsWhenEntropyUnavailable(t *testing.T) {
	a := newTestApp(t)

	// 先落一个工作流,占住 "wf-000000000000" 这个 ID。
	existing := &model.Workflow{
		ID:      "wf-000000000000",
		Name:    "既有工作流",
		Enabled: true,
		Nodes: []model.Node{{
			ID: "a", Name: "A", Type: "agent",
			Config: map[string]any{"agent": "mock-claude", "mode": "plan"},
		}},
	}
	if _, err := a.Workflows.Save(existing); err != nil {
		t.Fatalf("前置保存失败: %v", err)
	}

	orig := randRead
	t.Cleanup(func() { randRead = orig })
	randRead = func(b []byte) (int, error) { return 0, errEntropy }

	// 另存一个新工作流(不指定 ID → 走随机 ID 生成)。
	_, err := a.Workflows.Save(&model.Workflow{
		Name:    "新工作流",
		Enabled: true,
		Nodes: []model.Node{{
			ID: "a", Name: "A", Type: "agent",
			Config: map[string]any{"agent": "mock-claude", "mode": "plan"},
		}},
	})
	if err == nil {
		t.Fatal("熵源失败时保存必须失败(否则会用全零 ID 覆盖既有工作流)")
	}
	if !strings.Contains(err.Error(), "随机") {
		t.Errorf("错误信息应说明是随机 ID 生成失败,实际: %v", err)
	}

	// 既有工作流必须**原样还在**(没被全零 ID 覆盖)。
	got, err := a.Repo.GetWorkflow("wf-000000000000")
	if err != nil {
		t.Fatalf("既有工作流丢失: %v", err)
	}
	if got.Name != "既有工作流" {
		t.Errorf("既有工作流被覆盖: name = %q", got.Name)
	}
}
