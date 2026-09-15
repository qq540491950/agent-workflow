// Package logx 提供带敏感信息脱敏的结构化日志。
// API_KEY / TOKEN / PASSWORD / Authorization 等不会进入日志。
package logx

import (
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// 敏感键名模式(忽略大小写):key=value / key: value / "key": "value"。
var sensitivePattern = regexp.MustCompile(
	`(?i)(api[_-]?key|token|password|secret|authorization|credential)s?([:=]\s*"?|"?:\s*")([^",\s}&]+)`)

// Mask 脱敏文本中的敏感键值对。
func Mask(s string) string {
	return sensitivePattern.ReplaceAllString(s, `${1}${2}***`)
}

var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// SetLevel 控制日志级别(debug/info/warn/error)。
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
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func Debug(msg string, args ...any) { logger.Debug(Mask(msg), args...) }
func Info(msg string, args ...any)  { logger.Info(Mask(msg), args...) }
func Warn(msg string, args ...any)  { logger.Warn(Mask(msg), args...) }
func Error(msg string, args ...any) { logger.Error(Mask(msg), args...) }
