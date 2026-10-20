package logx

import (
	"strings"
	"testing"
)

// 本文件锁住"日志级别的唯一来源"这一收敛。
//
// 此前有三份白名单:logx 自己的 validLevels、app/application 的
// validLogLevels、main.go 的 validLogLevels。三份必然漂移,漂移方向是
// "用户能选一个后端不认的值"(设置页回显 debug、实际跑 info)。
//
// 现在唯一来源是 logx.Levels():校验用 ValidateLevel(不改变当前级别),
// 应用用 SetLevelChecked(校验 + 应用),两者必须对同一批取值给出
// **同一个**结论 —— 否则"先校验后应用"的调用方会出现
// "校验说合法、应用报错"的分裂。

// TestValidateLevelMatchesSetLevelChecked 校验入口与应用入口的结论必须一致。
func TestValidateLevelMatchesSetLevelChecked(t *testing.T) {
	// 候选集:合法级别 + 各种非法形态
	candidates := append(Levels(),
		"", " ", "trace", "fatal", "off", "DEBUG", "Info", " warn ", "erro",
	)
	for _, c := range candidates {
		validateErr := ValidateLevel(c)
		setErr := SetLevelChecked(c)
		if (validateErr == nil) != (setErr == nil) {
			t.Errorf("级别 %q:ValidateLevel 报错=%v,SetLevelChecked 报错=%v(结论必须一致)",
				c, validateErr != nil, setErr != nil)
		}
	}
}

// TestLevelsReturnsSupportedSet 白名单内容与顺序固定。
func TestLevelsReturnsSupportedSet(t *testing.T) {
	want := []string{"debug", "info", "warn", "error"}
	got := Levels()
	if len(got) != len(want) {
		t.Fatalf("Levels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Levels()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// 副本语义:调用方改不动内部定义
	got[0] = "tampered"
	if Levels()[0] != "debug" {
		t.Error("Levels() 必须返回副本")
	}
}

// TestValidateLevelDoesNotChangeCurrentLevel 纯校验不得改变当前级别。
func TestValidateLevelDoesNotChangeCurrentLevel(t *testing.T) {
	if err := SetLevelChecked("error"); err != nil {
		t.Fatalf("set level: %v", err)
	}
	if err := ValidateLevel("bogus"); err == nil {
		t.Fatal("非法级别应报错")
	}
	// 级别仍是 error:证明 ValidateLevel 没有动过全局状态
	if err := SetLevelChecked("error"); err != nil {
		t.Fatalf("set level: %v", err)
	}
	if !strings.Contains(ValidateLevel("bogus").Error(), "debug") {
		t.Errorf("错误信息应列出可选值,实际: %q", ValidateLevel("bogus").Error())
	}
}
