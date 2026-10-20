package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// 迭代 7 审计新增:JSON 解析路径不得有 panic 风险(约定 12)。
// 上层虽然有 recover 兜底,但新增路径不能依赖上层保护。
//
// 本文件此前有两处**恒真测试**(恒真测试比没有测试更危险:它给的是
// 虚假安全感)。本轮(迭代 14)把它们改成真实断言:
//
//  1. TestJSONExtractionOnDeepNesting 原断言是 `got != "" && len(got) == 0`
//     —— 逻辑上恒假(len==0 ⟺ ==""),整个用例零断言:把 agent/output.go 的
//     `return ""` 改成 `return "GARBAGE"`,测试照样 PASS。
//  2. TestJSONUnmarshalDeepNestingIsErrorNotPanic 原断言只有
//     `if err == nil { t.Skip(...) }` —— 两个分支都不失败,用例永远不会变红。
//
// 每条断言都在本轮做过"改坏实现 → 变红 → 还原"的实测(见交付报告)。

// mustNotPanic 调用 fn 并把 panic 转成测试失败。
//
// "不得 panic" 是一条真实不变量,但它**必须被显式断言**:只写
// "调用没崩" 而不检查返回值,等于把 panic 之后的零值当合法结果。
func mustNotPanic(t *testing.T, name string, fn func() string) string {
	t.Helper()
	var out string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s panic: %v", name, r)
			}
		}()
		out = fn()
	}()
	return out
}

// balancedBraces 独立于实现的括号配平检查。
//
// 刻意**不用**被测代码(BalancedJSON / scanJSONObjects)来验证被测代码 ——
// 那是拿实现证明实现。这里测试自己数一遍花括号(字符串字面量内的花括号
// 不参与配平)。
func balancedBraces(s string) bool {
	depth := 0
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && !inStr
}

// TestJSONExtractionOnDeepNesting 深层嵌套的 JSON 不能让提取器 panic,
// 且必须返回**正确**的结果。
//
// 真实不变量:
//  1. 不得 panic(显式断言,见 mustNotPanic);
//  2. 对配平的深层 JSON,提取结果必须逐字节等于输入(整个文本就是一个对象)
//     —— "返回空串"或"返回垃圾串"都会让这条变红;
//  3. 提取结果必须括号配平(提取器的契约:不能截出半截);
//  4. 在 Go 的 JSON 深度上限之内时,提取结果必须能被 json.Unmarshal 成功
//     —— 是真 JSON,不是看起来像 JSON 的文本。
func TestJSONExtractionOnDeepNesting(t *testing.T) {
	const depth = 50000
	payload := strings.Repeat("{", depth) + strings.Repeat("}", depth)

	got := map[string]string{
		"FirstJSONObject":    mustNotPanic(t, "FirstJSONObject", func() string { return FirstJSONObject(payload) }),
		"TrailingJSONObject": mustNotPanic(t, "TrailingJSONObject", func() string { return TrailingJSONObject(payload) }),
		"StructuredJSON":     mustNotPanic(t, "StructuredJSON", func() string { return StructuredJSON(payload) }),
	}
	for name, g := range got {
		if g != payload {
			t.Errorf("%s(深层配平 JSON) = 长度 %d,应等于整个输入(长度 %d)", name, len(g), len(payload))
		}
		if !balancedBraces(g) {
			t.Errorf("%s 返回的不是括号配平的片段", name)
		}
	}
}

// TestJSONExtractionWithinParseDepthLimit 在 Go 的 JSON 深度上限之内,
// 提取结果必须**能被解析** —— 提取器的产物要能直接喂给 json.Unmarshal。
//
// 这一条覆盖"半截 JSON":配平检查只能保证花括号数对,不能保证语义完整;
// 而上层正是靠 json.Unmarshal 解析出 decision 的。
func TestJSONExtractionWithinParseDepthLimit(t *testing.T) {
	const depth = 100
	payload := strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)

	for name, g := range map[string]string{
		"FirstJSONObject":    mustNotPanic(t, "FirstJSONObject", func() string { return FirstJSONObject(payload) }),
		"TrailingJSONObject": mustNotPanic(t, "TrailingJSONObject", func() string { return TrailingJSONObject(payload) }),
		"StructuredJSON":     mustNotPanic(t, "StructuredJSON", func() string { return StructuredJSON(payload) }),
	} {
		if g != payload {
			t.Errorf("%s = %q,应等于整个输入", name, g)
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(g), &v); err != nil {
			t.Errorf("%s 的产物不是合法 JSON: %v", name, err)
		}
	}
}

// TestJSONExtractionStopsAtDepthLimit 超过扫描深度上限(maxJSONDepth)时,
// 提取器必须**停止并返回空串**,而不是返回半截或超长的垃圾。
//
// 失控 CLI 的输出上限是 8MB,里面可能全是未配平的 '{' —— 提取器必须在这一
// 点封顶,否则会把半个输出当成 JSON 交上去(上层解析失败 → decision 丢失)。
func TestJSONExtractionStopsAtDepthLimit(t *testing.T) {
	payload := strings.Repeat("{", maxJSONDepth+1000)
	for name, g := range map[string]string{
		"FirstJSONObject":    mustNotPanic(t, "FirstJSONObject", func() string { return FirstJSONObject(payload) }),
		"TrailingJSONObject": mustNotPanic(t, "TrailingJSONObject", func() string { return TrailingJSONObject(payload) }),
		"StructuredJSON":     mustNotPanic(t, "StructuredJSON", func() string { return StructuredJSON(payload) }),
	} {
		if g != "" {
			t.Errorf("%s(超深未配平输入) = 长度 %d,应为空串", name, len(g))
		}
	}
}

// TestJSONUnmarshalDeepNestingIsErrorNotPanic encoding/json 对超深嵌套
// 返回错误而不是 panic —— 适配器据此走降级分支。
//
// 改前断言:只有 `if err == nil { t.Skip(...) }` —— err 非 nil 时不做任何
// 断言、err 为 nil 时跳过,**两个分支都不失败**:用例永远不会变红。
//
// 改后:深度取 1<<17(远超 Go 当前的 10000 层上限,任何 Go 版本都不该接受),
// 断言 (a) 返回错误、(b) 错误是 *json.SyntaxError(不是别的异常形态)、
// (c) 没有 panic、(d) 目标值未被写入。
// "err == nil" 分支**不 skip 而是失败** —— skip 会让用例在没有断言的情况下
// 通过,那正是本轮要清理的恒真模式。
func TestJSONUnmarshalDeepNestingIsErrorNotPanic(t *testing.T) {
	const depth = 1 << 17
	payload := strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)

	var v any
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("json.Unmarshal 超深嵌套 panic: %v", r)
			}
		}()
		err = json.Unmarshal([]byte(payload), &v)
	}()
	if err == nil {
		// 这里**不** skip:skip 会让用例在没有断言的情况下"通过"
		// (恒真测试的老毛病)。深度上限被提高、或有人把深度常量改小,
		// 都必须显式失败,由人来重新评估。
		t.Fatalf("json.Unmarshal 接受了 %d 层嵌套 —— Go 的 JSON 深度上限被提高(或本用例的深度常量被改小),请重新评估本用例", depth)
	}
	var se *json.SyntaxError
	if !errors.As(err, &se) {
		t.Errorf("超深嵌套应返回 *json.SyntaxError,实际 %T: %v", err, err)
	}
	if v != nil {
		t.Errorf("解析失败时不应写入目标值,实际 %v", v)
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
