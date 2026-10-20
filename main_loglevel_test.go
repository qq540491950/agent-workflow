package main

import (
	"testing"

	"agentworkflow/logx"
)

// TestIsValidLogLevelMatchesLogx 命令行接受的日志级别必须与 logx 一致。
//
// main.go 此前自己维护一份白名单,logx 又一份。两份漂移就会出现
// "命令行接受、后端不认"(或反过来)的分裂 —— 参数校验与实际应用
// 对同一个值给出不同结论。
func TestIsValidLogLevelMatchesLogx(t *testing.T) {
	for _, l := range logx.Levels() {
		if !isValidLogLevel(l) {
			t.Errorf("logx 支持的级别 %q 不应被命令行拒绝", l)
		}
	}
	for _, l := range []string{"trace", "fatal", "off", "bogus"} {
		if isValidLogLevel(l) {
			t.Errorf("logx 不支持的级别 %q 不应被命令行接受", l)
		}
	}
}
