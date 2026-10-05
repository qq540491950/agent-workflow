// Package logx 提供带敏感信息脱敏的结构化日志。
// API_KEY / TOKEN / PASSWORD / Authorization 等不会进入日志。
package logx

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
)

// 敏感键名模式(忽略大小写):key=value / key: value / "key": "value",
// 值可带可选的 Bearer 前缀。
var sensitivePattern = regexp.MustCompile(
	`(?i)((?:api[_-]?key|token|password|secret|authorization|credential)s?"?\s*[:=]\s*"?(?:bearer\s+)?)([^",\s}&]+)`)

// sensitiveKeyPattern 匹配"作为键名"的敏感词(token / api_key / password …)。
// 值形态(key=value)由 sensitivePattern 负责;键名形态需要单独一条规则:
// 事件 payload 里最常见的正是 {"token":"Bearer abc"} 这种裸值。
var sensitiveKeyPattern = regexp.MustCompile(
	`(?i)^(api[_-]?key|token|password|secret|authorization|credential|access[_-]?key|private[_-]?key)s?$`)

// sensitiveHint 是敏感词的公共子串。正则很贵,而事件总线的热路径上
// 每个 payload 都要过一遍(Emit 是每节点每次执行都调用的),所以先用
// 廉价的 strings.Contains 预筛:绝大多数正常文本(node 名、summary、
// 状态名)根本不含这些子串,可直接跳过正则。
var sensitiveHint = []string{"key", "token", "password", "secret", "authorization", "credential"}

// maybeSensitive 是 mayContainSensitive 的别名(语义更直白)。
func mayContainSensitive(s string) bool {
	for _, h := range sensitiveHint {
		if strings.Contains(strings.ToLower(s), h) {
			return true
		}
	}
	return false
}

// Mask 脱敏文本中的敏感键值对(含 Bearer 前缀形式)。
func Mask(s string) string {
	if !mayContainSensitive(s) {
		return s
	}
	return sensitivePattern.ReplaceAllString(s, `${1}***`)
}

// MaskValue 递归脱敏任意结构(字符串/嵌套 map/slice)里的敏感值。
// 事件流的三个出口(SQLite 落库 / SSE 推送 / Wails IPC)都经这里:
// 节点失败的 error 文本常带 CLI stderr,而 stderr 里常有
// "ANTHROPIC_API_KEY=sk-..." 这类凭据 —— 日志侧此前已脱敏(见 maskArgs),
// 但事件/审计流是旁路,同一份敏感值照样落库并推给前端。
//
// 两条线索都认:键名是敏感词 → 整值替换为 ***;值本身含 key=value 形态
// → 就地替换(保持上下文可读)。非字符串值原样返回。
func MaskValue(v any) any { return maskRecursive("", v) }

// MaskMap 是 MaskValue 的 map 入口(事件 payload 用)。
func maskRecursive(key string, v any) any {
	// 键名即线索:整个值都是凭据,直接替换(键名短,正则代价可忽略)
	if v != nil && mayContainSensitive(key) && sensitiveKeyPattern.MatchString(key) {
		return "***"
	}
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return Mask(t)
	case error:
		if t == nil {
			return v
		}
		return Mask(t.Error())
	case fmt.Stringer:
		return Mask(t.String())
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = maskRecursive(k, val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = maskRecursive("", val)
		}
		return out
	default:
		return v
	}
}

var logger atomic.Pointer[slog.Logger]

func init() {
	logger.Store(newLogger(slog.LevelInfo))
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// SetLevel 控制日志级别(debug/info/warn/error)。
// 运行中可被设置页调用,用原子指针替换避免与并发写日志竞争。
func SetLevel(level string) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	logger.Store(newLogger(l))
}

func get() *slog.Logger { return logger.Load() }

// maskArgs 对结构化参数里的敏感值脱敏。
// 此前只对 msg 脱敏,args 原样进入日志:api 层把 err(可能含 CLI stderr,
// 而 stderr 常带 token)以 "error"= 形态写出,掩码形同虚设。
func maskArgs(args []any) []any {
	if len(args) < 2 {
		return args
	}
	out := make([]any, len(args))
	copy(out, args)
	// slog 的 args 是 key/value 交替,只处理 value 位
	for i := 1; i < len(out); i += 2 {
		out[i] = maskValue(out[i])
	}
	return out
}

// maskValue 对单个日志参数值脱敏:字符串与 error/Stringer 走 Mask,
// 其余类型(数字、布尔、结构体)原样保留。
func maskValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return Mask(x)
	case error:
		return Mask(x.Error())
	case fmt.Stringer:
		return Mask(x.String())
	default:
		return v
	}
}

func Debug(msg string, args ...any) { get().Debug(Mask(msg), maskArgs(args)...) }
func Info(msg string, args ...any)  { get().Info(Mask(msg), maskArgs(args)...) }
func Warn(msg string, args ...any)  { get().Warn(Mask(msg), maskArgs(args)...) }
func Error(msg string, args ...any) { get().Error(Mask(msg), maskArgs(args)...) }

// Logger 返回当前 slog.Logger(供 Wails Options.Logger 集成,
// 使框架内部日志同样经过脱敏与级别控制)。
func Logger() *slog.Logger { return get() }
