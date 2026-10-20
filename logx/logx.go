// Package logx 提供带敏感信息脱敏的结构化日志。
// API_KEY / TOKEN / PASSWORD / Authorization 等不会进入日志。
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

var logger atomic.Pointer[slog.Logger]

// levelValue 保存当前生效的级别(供 SetOutput 重建 logger 时保持一致)。
var levelValue atomic.Int64

// outWriter 保存当前日志输出目的地(默认 stderr;可由 SetOutput 切换)。
//
// 单独存着而不是只在 newLogger 时传进去:级别一变(SetLevelChecked)就要
// 重建 logger,若不带上当前目的地,日志会悄悄回到 stderr —— 测试里
// 捕获日志就失效了(约定 14:别让"看起来在工作"的机制静默失效)。
var outWriter atomic.Pointer[io.Writer]

func init() {
	w := io.Writer(os.Stderr)
	outWriter.Store(&w)
	levelValue.Store(int64(slog.LevelInfo))
	logger.Store(newLogger(slog.LevelInfo))
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(*outWriter.Load(), &slog.HandlerOptions{Level: level}))
}

// validLevels 是支持的日志级别。
//
// levelOrder 是同一份定义的**有序**形式:白名单需要可重复的展示顺序
// (命令行用法、接口回显),map 的遍历顺序是随机的。
var levelOrder = []string{"debug", "info", "warn", "error"}

var validLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// warnLogger 是独立于级别设置的告警出口:级别被设成 error 时,
// "配置写错了"这类问题仍然必须可见,否则就是静默失败(约定 14)。
var warnLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

// Levels 返回支持的日志级别(副本,顺序固定)。
//
// 唯一来源:调用方(命令行参数校验、设置页)不要再自己写一份白名单 ——
// 两份白名单必然漂移,漂移方向是"用户能选一个后端不认的值"。
func Levels() []string {
	return append([]string(nil), levelOrder...)
}

// levelOf 解析日志级别(大小写不敏感,首尾空白忽略)。
func levelOf(level string) (slog.Level, error) {
	l, ok := validLevels[strings.ToLower(strings.TrimSpace(level))]
	if !ok {
		return 0, fmt.Errorf("日志级别非法: %q(可选 %s)", level, strings.Join(levelOrder, "|"))
	}
	return l, nil
}

// ValidateLevel 只校验日志级别,**不改变**当前级别。
//
// 供"先校验、后落库"的调用方使用(设置页:级别要先落库再生效,
// 落库前必须确认这个级别真的能被应用)。
func ValidateLevel(level string) error {
	_, err := levelOf(level)
	return err
}

// SetLevelChecked 设置日志级别(debug/info/warn/error)。
// 非法值返回 error 且**不改变**当前级别 —— 调用方可以据此拒绝请求
// (设置页把非法级别存进库、界面回显"debug"实际跑 info,是静默失败)。
func SetLevelChecked(level string) error {
	l, err := levelOf(level)
	if err != nil {
		return err
	}
	levelValue.Store(int64(l))
	logger.Store(newLogger(l))
	return nil
}

// SetOutput 把日志输出切到 w(w == nil 时回到 stderr),返回恢复函数。
//
// 用途:桌面模式把日志落到文件;测试里捕获日志以断言"某条告警真的发出"
// (约定 14:降级路径必须有可验证的告警,不能只写在注释里)。
//
// 只换输出目的地,**不改变**生效级别(级别由 SetLevelChecked 管)。
func SetOutput(w io.Writer) func() {
	old := logger.Load()
	oldOut := outWriter.Load()
	if w == nil {
		w = os.Stderr
	}
	outWriter.Store(&w)
	logger.Store(newLogger(slog.Level(levelValue.Load())))
	return func() {
		outWriter.Store(oldOut)
		logger.Store(old)
	}
}

// SetLevel 控制日志级别(debug/info/warn/error)。
// 运行中可被设置页调用,用原子指针替换避免与并发写日志竞争。
//
// 兼容旧调用方:非法值不再静默回落 info —— 保持当前级别并记录 WARN
// (约定 14:"该失败却成功"等同 P0)。需要区分"非法"的调用方请改用
// SetLevelChecked(返回 error)。
func SetLevel(level string) {
	if err := SetLevelChecked(level); err != nil {
		warnLogger.Warn("忽略非法日志级别,保持当前级别", "level", level, "error", err)
	}
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
