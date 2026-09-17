// Package logx 提供带敏感信息脱敏的结构化日志。
// API_KEY / TOKEN / PASSWORD / Authorization 等不会进入日志。
package logx

import (
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

func Debug(msg string, args ...any) { get().Debug(Mask(msg), args...) }
func Info(msg string, args ...any)  { get().Info(Mask(msg), args...) }
func Warn(msg string, args ...any)  { get().Warn(Mask(msg), args...) }
func Error(msg string, args ...any) { get().Error(Mask(msg), args...) }

// Logger 返回当前 slog.Logger(供 Wails Options.Logger 集成,
// 使框架内部日志同样经过脱敏与级别控制)。
func Logger() *slog.Logger { return get() }
