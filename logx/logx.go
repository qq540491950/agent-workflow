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

// Mask 脱敏文本中的敏感键值对(含 Bearer 前缀形式)。
func Mask(s string) string {
	return sensitivePattern.ReplaceAllString(s, `${1}***`)
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
