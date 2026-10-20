package application

import (
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

// TestValidLogLevelAppliedAndPersisted 合法级别必须落库且即时生效。
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
}
