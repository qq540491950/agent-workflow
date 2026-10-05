package agent

import (
	"bytes"
	"strings"
	"sync"
)

// 本文件是 Agent 适配器(claude/pi)共用的子进程输出与 JSON 提取工具。
// 两者此前各写一份且都有缺陷,收敛到这里便于统一修一次。

// MaxOutputBytes 是单个 Agent 子进程 stdout 的内存上限。
// cmd.Output() 会把子进程的全部 stdout 读进内存:一个失控的 CLI
// (打印整棵文件树、开调试日志)会让单次执行驻留数百 MB,而截断只发生在
// ExecuteNode 落库那一层 —— 内存峰值早于此处就发生了。
const MaxOutputBytes = 8 << 20 // 8MB

// BoundedBuffer 是带上限的输出缓冲区:超出部分被丢弃并记数,
// 绝不无界增长。用于 Agent 子进程 stdout。
type BoundedBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	limit   int
	written int64
	dropped int64
}

// NewBoundedBuffer 创建上限为 limit 字节的缓冲区(limit<=0 时用 MaxOutputBytes)。
func NewBoundedBuffer(limit int) *BoundedBuffer {
	if limit <= 0 {
		limit = MaxOutputBytes
	}
	return &BoundedBuffer{limit: limit}
}

// Write 实现 io.Writer,超出上限后只计数不缓冲。
func (b *BoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.written += int64(len(p))
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:room])
			b.dropped += int64(len(p) - room)
		}
		return len(p), nil
	}
	b.dropped += int64(len(p))
	return len(p), nil
}

// Bytes 返回已捕获的内容(超限时已按上限截断)。
func (b *BoundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

// String 返回内容,超限时追加显式截断标记 —— 静默截断会让用户以为
// Agent 真的只输出了这么多,标记让"被截断"这件事可见。
func (b *BoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if b.dropped > 0 {
		s += "\n…[输出超出 " + itoa(int64(b.limit)) + " 字节上限,已丢弃 " +
			itoa(b.dropped) + " 字节]"
	}
	return s
}

// Truncated 报告是否发生过截断。
func (b *BoundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped > 0
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// BalancedJSON 从 start 处的 '{' 开始做括号配平,能配平时返回该对象
// 文本。字符串字面量内的括号与转义引号会被正确跳过 —— 不跳过时
// {"summary":"格式 {x}"} 这类内容会算错深度。
// 配平成功返回 (text, true);否则 (text[start:]截断, false) 交由调用方决定。
func BalancedJSON(text string, start int) (string, bool) {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return "", false
	}
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case inStr && c == '\\':
			escaped = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// 字符串字面量内的括号不参与配平
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return text[start:], false
}

// FirstJSONObject 返回文本中第一个能配平的 JSON 对象。
// 模型常在正文之后附一段 JSON,取第一个完整对象即为目标。
func FirstJSONObject(text string) string {
	for start := 0; ; {
		i := strings.Index(text[start:], "{")
		if i < 0 {
			return ""
		}
		start += i
		if js, ok := BalancedJSON(text, start); ok {
			return js
		}
		start++ // 该 '{' 配不平,往后找下一个
	}
}

// TrailingJSONObject 返回文本末尾那个能配平到结尾的 JSON 对象。
// 找不到时返回空串。
//
// 不能用 strings.LastIndex(text, "{") 直接截断:输出里只要含嵌套对象
// ({"decision":"REJECTED","detail":{"file":"a.go"}})就会命中内层的 '{',
// 截出来的片段括号不配平,解析必然失败 —— decision 静默丢失,路由条件
// 恒不成立,直接走默认/循环分支。
func TrailingJSONObject(text string) string {
	for start := strings.LastIndex(text, "{"); start >= 0; {
		if js, ok := BalancedJSON(text, start); ok && strings.TrimSpace(text[start+len(js):]) == "" {
			return js
		}
		prev := strings.LastIndex(text[:start], "{")
		if prev < 0 {
			return ""
		}
		start = prev
	}
	return ""
}
