package application

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"agentworkflow/logx"
)

// 本文件锁住"日志级别的唯一来源"在设置层的收敛。
//
// 此前 app/application 自己维护一份白名单,logx 又一份,main.go 又一份。
// 三份必然漂移,漂移方向是"用户能选一个后端不认的值":设置页回显
// "debug"、库里存了 "debug"、实际跑的是 info(静默失败,约定 #14)。
//
// 现在唯一来源是 logx:校验用 logx.ValidateLevel,应用用
// logx.SetLevelChecked,错误真正回报给调用方(接口层 400)。

// TestLogLevelAcceptedSetMatchesLogx 设置层接受的级别集合必须与 logx 一致。
func TestLogLevelAcceptedSetMatchesLogx(t *testing.T) {
	a := newTestApp(t)
	// 收尾把全局级别调回 info(本用例会真的应用级别)
	t.Cleanup(func() { _ = logx.SetLevelChecked("info") })

	for _, l := range logx.Levels() {
		if _, err := a.Settings.Update("", l, nil); err != nil {
			t.Errorf("logx 支持的级别 %q 被设置层拒绝: %v", l, err)
		}
	}
	for _, l := range []string{"trace", "fatal", "off", "bogus"} {
		_, err := a.Settings.Update("", l, nil)
		if err == nil {
			t.Errorf("logx 不支持的级别 %q 不应被设置层接受", l)
			continue
		}
		// 错误必须可读:包含出错的级别本身
		if !strings.Contains(err.Error(), l) {
			t.Errorf("错误消息应指明出错的级别 %q,实际: %q", l, err.Error())
		}
	}
}

// TestInvalidLogLevelNotPersisted 非法级别不得落库。
//
// 库里躺着一个应用不了的级别 = 重启后依然无效,且界面回显与实际级别
// 不一致(静默失败)。校验必须发生在落库之前。
func TestInvalidLogLevelNotPersisted(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.Settings.Update("", "trace", nil); err == nil {
		t.Fatal("非法日志级别应被拒绝")
	}
	var stored string
	if err := a.Repo.GetSetting("log_level", &stored); err == nil && stored == "trace" {
		t.Errorf("非法级别 %q 不应落库", stored)
	}
	if a.LogLevelValue() == "trace" {
		t.Error("非法级别不应改变内存中的级别")
	}
}

// TestValidLogLevelAppliedAndPersisted 合法级别必须落库且**即时生效**。
//
// "即时生效"必须验的是 **logx 的实际生效级别**,不是 app 自己缓存的字符串。
// 此前本测试只断言 a.LogLevelValue()=="warn" 与库里的 log_level=="warn",
// 从未验证 logx 真的换掉了 logger —— 把 application.go 里的
// logx.SetLevelChecked(level) 改成 SetLevelChecked("info")
// (这正是 #116 的原 bug:"设置页回显 debug、实际跑 info")时,本测试仍全绿,
// app/application 的全部测试也全绿。
//
// 现在直接问 logx 的 logger:"这个级别放行吗?"—— 判据取自真正做过滤的
// 那个 Handler(logx.Logger() 返回的就是 logx.Info/Debug 用的那个 logger)。
func TestValidLogLevelAppliedAndPersisted(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(func() { _ = logx.SetLevelChecked("info") })
	if _, err := a.Settings.Update("", "warn", nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if a.LogLevelValue() != "warn" {
		t.Errorf("内存级别 = %q, want warn", a.LogLevelValue())
	}
	var stored string
	if err := a.Repo.GetSetting("log_level", &stored); err != nil {
		t.Fatalf("读取落库级别: %v", err)
	}
	if stored != "warn" {
		t.Errorf("落库级别 = %q, want warn", stored)
	}
	// 实际生效级别:logx 的 logger 必须按 warn 过滤。
	assertLogxEffectiveLevel(t, "warn")
}

// assertLogxEffectiveLevel 断言 logx 当前**实际生效**的日志级别。
//
// 判据是 logger 的 Enabled(即 logx 真正用来过滤日志的那道闸),而不是
// app 层缓存的字符串或库里存的值 —— 后两者与"实际生效"可以各说各话
// (设置页回显 debug、实际跑 info 就是这么来的)。
func assertLogxEffectiveLevel(t *testing.T, want string) {
	t.Helper()
	wantTable, ok := map[string]map[slog.Level]bool{
		"debug": {slog.LevelDebug: true, slog.LevelInfo: true, slog.LevelWarn: true, slog.LevelError: true},
		"info":  {slog.LevelDebug: false, slog.LevelInfo: true, slog.LevelWarn: true, slog.LevelError: true},
		"warn":  {slog.LevelDebug: false, slog.LevelInfo: false, slog.LevelWarn: true, slog.LevelError: true},
		"error": {slog.LevelDebug: false, slog.LevelInfo: false, slog.LevelWarn: false, slog.LevelError: true},
	}[want]
	if !ok {
		t.Fatalf("未知的期望级别 %q", want)
	}
	logger := logx.Logger()
	ctx := context.Background()
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		got := logger.Enabled(ctx, level)
		if got != wantTable[level] {
			t.Errorf("logx 实际生效级别不符: %s 放行=%v, 期望=%v(期望级别 %q)",
				level, got, wantTable[level], want)
		}
	}
}

// TestLogLevelEffectiveLevelFollowsEveryChoice 每一档可选级别都必须
// **真的**换掉 logx 的生效级别(不只是写进库)。
//
// 这条是上一条的加强:逐档切换,每档都问一次 logger。
// 若某个级别"存下了但没生效"(例如 SetLevelChecked 被写死成 info),
// 本测试立刻变红。
func TestLogLevelEffectiveLevelFollowsEveryChoice(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(func() { _ = logx.SetLevelChecked("info") })
	for _, l := range logx.Levels() {
		if _, err := a.Settings.Update("", l, nil); err != nil {
			t.Fatalf("设置级别 %q: %v", l, err)
		}
		assertLogxEffectiveLevel(t, l)
	}
}
