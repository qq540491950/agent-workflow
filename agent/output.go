package agent

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"unicode"
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
		s += "\n…[输出超出 " + strconv.FormatInt(int64(b.limit), 10) + " 字节上限,已丢弃 " +
			strconv.FormatInt(b.dropped, 10) + " 字节]"
	}
	return s
}

// Truncated 报告是否发生过截断。
func (b *BoundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped > 0
}

// maxJSONDepth 是配平扫描允许的最大嵌套深度。
// 超过它就停止扫描:更深的结构本来就解析不了(encoding/json 的深度
// 上限同量级),而栈必须封顶 —— 否则一个打印大量 '{' 的失控 CLI
// (输出上限 8MB)能把内存也吃光。
const maxJSONDepth = 100000

// scanJSONObjects 单趟扫描文本,返回两个对象区间 [start,end):
//
//	first —— 最早出现且能配平的那个对象(未找到时 start = -1)
//	tail  —— 紧贴文本末尾(忽略尾部空白)结束的那个对象(未找到时 start = -1)
//
// 为什么不用"从每个 '{' 起逐个配平"(旧实现):那是 O(n²) ——
// 文本里只要有大量未配平的 '{',节点就会被卡住几分钟到几小时
// (输出上限 8MB 的病态输出足以做到)。单趟 + 栈是线性的,
// 字符串字面量内的括号与转义引号同样被正确跳过。
func scanJSONObjects(text string) (first, tail [2]int) {
	first = [2]int{-1, -1}
	tail = [2]int{-1, -1}
	// 文本末尾(去空白)必须正好是对象的结尾,才算"尾部对象"。
	end := len(strings.TrimRightFunc(text, unicode.IsSpace))

	var stack []int
	inStr := false
	escaped := false
	for i := 0; i < len(text); i++ {
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
			if len(stack) >= maxJSONDepth {
				return first, tail // 病态输入:停止扫描
			}
			stack = append(stack, i)
		case c == '}':
			if len(stack) == 0 {
				continue // 落单的 '}' 不参与配平
			}
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if first[0] < 0 || start < first[0] {
				first = [2]int{start, i + 1}
			}
			if i+1 == end {
				tail = [2]int{start, i + 1}
			}
		}
	}
	return first, tail
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
	first, _ := scanJSONObjects(text)
	if first[0] < 0 {
		return ""
	}
	return text[first[0]:first[1]]
}

// TrailingJSONObject 返回文本末尾那个能配平到结尾的 JSON 对象。
// 找不到时返回空串。
//
// 不能用 strings.LastIndex(text, "{") 直接截断:输出里只要含嵌套对象
// ({"decision":"REJECTED","detail":{"file":"a.go"}})就会命中内层的 '{',
// 截出来的片段括号不配平,解析必然失败 —— decision 静默丢失,路由条件
// 恒不成立,直接走默认/循环分支。
func TrailingJSONObject(text string) string {
	_, tail := scanJSONObjects(text)
	if tail[0] < 0 {
		return ""
	}
	return text[tail[0]:tail[1]]
}

// StructuredJSON 返回文本中的结构化 JSON 对象:优先取**尾部**那个,
// 尾部没有才回退到第一个。
//
// 为什么优先尾部:提示词要求模型把 JSON 放在回复末尾,而模型正文里
// 常先出现一个 JSON 片段(最典型的是把提示词里的示例原样抄回来)。
// 取第一个会把那个片段当成决策,于是路由按示例走,而不是按模型的
// 实际结论走。
func StructuredJSON(text string) string {
	if js := TrailingJSONObject(text); js != "" {
		return js
	}
	return FirstJSONObject(text)
}

// StderrHint 把子进程 stderr 拼成一段可读的诊断尾巴(可为空)。
//
// 为什么需要:CLI **退出码 0 却什么都没产出**时,stderr 是**唯一**的
// 诊断线索("额度耗尽" / "鉴权失败" / "配置文件缺失"之类)—— 错误
// 消息里没有它,用户只能自己去命令行重跑一遍才能知道出了什么事。
//
// 凭据脱敏由调用方负责(先过 redactor.Text 再拼进来):这里只做拼接
// 与截断,不做任何内容判断。
func StderrHint(stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		return ""
	}
	const maxHint = 500
	if len(s) > maxHint {
		s = s[:maxHint] + "…(已截断)"
	}
	return "; stderr: " + s
}
