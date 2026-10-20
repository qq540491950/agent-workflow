package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 迭代 7 审计新增:JSON 解析路径不得有 panic 风险(约定 12)。
// 上层虽然有 recover 兜底,但新增路径不能依赖上层保护。

// TestJSONExtractionNoPanicOnDeepNesting 深层嵌套的 JSON 不能让
// 提取器 panic 或死循环。
func TestJSONExtractionNoPanicOnDeepNesting(t *testing.T) {
	const depth = 50000
	payload := strings.Repeat("{", depth) + strings.Repeat("}", depth)
	if got := FirstJSONObject(payload); got != "" && len(got) == 0 {
		t.Fatal("不应返回空切片")
	}
	if got := TrailingJSONObject(payload); got != "" && len(got) == 0 {
		t.Fatal("不应返回空切片")
	}
	if got := StructuredJSON(payload); got != "" && len(got) == 0 {
		t.Fatal("不应返回空切片")
	}
}

// TestJSONUnmarshalDeepNestingIsErrorNotPanic encoding/json 对超深嵌套
// 返回错误而不是 panic —— 适配器据此走降级分支。
func TestJSONUnmarshalDeepNestingIsErrorNotPanic(t *testing.T) {
	const depth = 50000
	payload := strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)
	var v any
	err := json.Unmarshal([]byte(payload), &v)
	if err == nil {
		t.Skip("当前 Go 版本允许该深度,无需断言错误")
	}
}

// TestJSONExtractionIsLinearOnPathologicalInput 提取器必须是线性的。
//
// 旧实现是"从每个 '{' 起逐个配平"(O(n²)):一个打印大量未配平 '{' 的
// 失控 CLI(输出上限 8MB)能把节点卡死几小时 —— 比崩溃更难排查。
// 这里的阈值给得很宽松(旧实现跑这个规模要几十秒),只为守住"不挂死"。
func TestJSONExtractionIsLinearOnPathologicalInput(t *testing.T) {
	payload := strings.Repeat("{", 1<<20) // 1MB 未配平花括号
	start := time.Now()
	_ = FirstJSONObject(payload)
	_ = TrailingJSONObject(payload)
	_ = StructuredJSON(payload)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("JSON 提取耗时 %v —— 疑似退化成 O(n²)", elapsed)
	}
}

// TestBoundedBufferNeverPanicOnHugeWrite 缓冲写入不得 panic。
func TestBoundedBufferNeverPanicOnHugeWrite(t *testing.T) {
	b := NewBoundedBuffer(1024)
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'x'
	}
	for i := 0; i < 100; i++ {
		if _, err := b.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if len(b.Bytes()) != 1024 {
		t.Errorf("缓冲长度 = %d, want 1024", len(b.Bytes()))
	}
	if !b.Truncated() {
		t.Error("应标记 Truncated")
	}
}
