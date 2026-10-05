package agent

import (
	"strings"
	"sync"
	"testing"
)

// 批次 A7/A8:两个适配器共用的 JSON 提取与输出上限。

// TestTrailingJSONObjectNested 覆盖原缺陷:输出含嵌套对象时,
// strings.LastIndex(text, "{") 命中内层 '{',截出的片段括号不配平,
// Unmarshal 必然失败 → decision 静默丢失、路由恒走默认分支。
func TestTrailingJSONObjectNested(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "嵌套对象取最外层",
			in:   `分析完成 {"decision":"REJECTED","detail":{"file":"a.go","lines":[1,2]}}`,
			want: `{"decision":"REJECTED","detail":{"file":"a.go","lines":[1,2]}}`,
		},
		{
			name: "单层对象",
			in:   `{"decision":"APPROVED","summary":"ok"}`,
			want: `{"decision":"APPROVED","summary":"ok"}`,
		},
		{
			name: "字符串字面量内的花括号不参与配平",
			in:   `{"decision":"DONE","summary":"格式 {x} 已应用"}`,
			want: `{"decision":"DONE","summary":"格式 {x} 已应用"}`,
		},
		{
			name: "转义引号",
			in:   `{"decision":"DONE","summary":"他说\"{hi}\"结束"}`,
			want: `{"decision":"DONE","summary":"他说\"{hi}\"结束"}`,
		},
		{
			name: "对象后有正文则不算尾部",
			in:   `{"decision":"DONE"} 后续说明文字`,
			want: "",
		},
		{
			name: "无对象",
			in:   `纯文本输出`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TrailingJSONObject(c.in); got != c.want {
				t.Errorf("TrailingJSONObject(%q)\n got = %q\nwant = %q", c.in, got, c.want)
			}
		})
	}
}

// TestFirstJSONObject 覆盖 claude 侧:旧实现不跳过字符串字面量内的括号,
// 遇到 {"summary":"格式 {x}"} 会算错深度而截出半截。
func TestFirstJSONObject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"正文后跟一个对象", "我看过了。\n{\"decision\":\"APPROVED\",\"summary\":\"ok\"}", `{"decision":"APPROVED","summary":"ok"}`},
		{"字符串内含花括号", `{"decision":"DONE","summary":"用 {placeholder} 占位"}`, `{"decision":"DONE","summary":"用 {placeholder} 占位"}`},
		{"无对象", "没有任何 json", ""},
		{"未闭合", `{"decision":`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FirstJSONObject(c.in); got != c.want {
				t.Errorf("FirstJSONObject(%q)\n got = %q\nwant = %q", c.in, got, c.want)
			}
		})
	}
}

// TestBoundedBufferCapsOutput 验证 A8:输出缓冲有上限,不会无界增长,
// 且截断是显式可见的(追加标记),不是静默丢弃。
func TestBoundedBufferCapsOutput(t *testing.T) {
	b := NewBoundedBuffer(10)
	n, err := b.Write([]byte("0123456789"))
	if err != nil || n != 10 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if b.Truncated() {
		t.Error("恰好等于上限不应算截断")
	}
	// 超限部分被丢弃,但 Write 必须报告全部消费(否则调用方会得到
	// short write 错误而中断)
	n, err = b.Write([]byte("ABCDEFGH"))
	if err != nil || n != 8 {
		t.Fatalf("超限 Write = %d, %v(应返回 8,nil)", n, err)
	}
	if !b.Truncated() {
		t.Error("超限应标记 Truncated")
	}
	if got := string(b.Bytes()); got != "0123456789" {
		t.Errorf("缓冲内容 = %q,应只保留上限内的字节", got)
	}
	s := b.String()
	if !strings.Contains(s, "已丢弃") {
		t.Errorf("截断必须显式可见,实际 %q", s)
	}
}

// TestBoundedBufferDefaultLimit 默认上限为 MaxOutputBytes 且非零。
func TestBoundedBufferDefaultLimit(t *testing.T) {
	if MaxOutputBytes <= 0 {
		t.Fatal("MaxOutputBytes 必须为正")
	}
	b := NewBoundedBuffer(0)
	b.Write([]byte(strings.Repeat("x", 1000)))
	if b.Truncated() {
		t.Error("1000 字节不应触发 8MB 上限")
	}
	if len(b.Bytes()) != 1000 {
		t.Errorf("内容长度 = %d", len(b.Bytes()))
	}
}

// TestBoundedBufferConcurrentWrite 缓冲可能被多个写协程并发使用
// (stdout 与管道转发的组合),必须无竞态。
func TestBoundedBufferConcurrentWrite(t *testing.T) {
	b := NewBoundedBuffer(1024)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = b.Write([]byte("abcdefghij"))
				_ = b.String()
				_ = b.Truncated()
			}
		}()
	}
	wg.Wait()
	if len(b.Bytes()) > 1024 {
		t.Errorf("并发写入后长度 = %d,超过上限 1024", len(b.Bytes()))
	}
}
