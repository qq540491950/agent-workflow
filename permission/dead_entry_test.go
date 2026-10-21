package permission

// 本文件锁住"权限管理器只有一个应用覆盖的入口"(迭代 28 的 P3-4)。
//
// 背景:`Manager.Apply` 曾是 `ApplyWorkflowOverrides` 的**无错包装**
// (静默版:非法 permissions 结构只打一条 WARN 就把该 Agent 的权限
// **全部拒绝**),而生产**零调用点**。留着一条"静默"入口,后人就可能
// 绕过 `ApplyChecked` 直接调它 → 回到"非法 permissions 结构被静默全拒
// 但只打 WARN"的老形态(约定 14:该失败却成功 / 用户以为生效了实际没有)。
//
// 因此 `Manager.Apply` 已被删除。本文件用**反射**锁住这个事实:
//  - 方法集里**不得**再有 `Apply`(任何签名都不行);
//  - 应当存在的两个入口必须都在:`ApplyChecked`(带错)与
//    `ApplyWorkflowOverrides`(静默,但**只**用于"拒绝并告警"这条既定语义)。
//
// 反射断言不是恒真:把 `Apply` 加回去(哪怕换个名字以外的任何形态)都会
// 让本用例失败。

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestManagerHasNoSilentApplyEntry Manager 上不得再有"静默应用覆盖"的入口。
func TestManagerHasNoSilentApplyEntry(t *testing.T) {
	typ := reflect.TypeOf(&Manager{})
	if typ == nil {
		t.Fatal("reflect.TypeOf(&Manager{}) 为 nil")
	}
	var methods []string
	for i := 0; i < typ.NumMethod(); i++ {
		methods = append(methods, typ.Method(i).Name)
	}
	sort.Strings(methods)

	for _, name := range methods {
		if name == "Apply" {
			t.Errorf("Manager 不得再有静默入口 Apply(方法集: %s)", strings.Join(methods, ", "))
		}
	}
	// 期望存在的两个入口:缺一个就是"应用覆盖"的路径被改没了。
	for _, want := range []string{"ApplyChecked", "ApplyWorkflowOverrides"} {
		found := false
		for _, name := range methods {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Manager 缺少入口 %q(方法集: %s)", want, strings.Join(methods, ", "))
		}
	}
}
