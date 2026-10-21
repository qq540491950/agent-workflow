package runtime

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// fakeState 是 compiler.StateAccess 的最小实现(测试用)。
type fakeState struct{ m map[string]any }

func (f *fakeState) Get(k string) (any, bool) {
	v, ok := f.m[k]
	return v, ok
}
func (f *fakeState) Set(k string, v any) error {
	f.m[k] = v
	return nil
}
func (f *fakeState) Delete(k string) error {
	delete(f.m, k)
	return nil
}

// TestCfgFloatAcceptsAllNumericShapes 锁住 P2-7:数值配置读取要认全所有形态。
//
// 数值类型随来源而变:YAML 整数是 int/uint64、JSON 整数是 float64、
// 经 SQLite state_json 往返后是 float64(约定 #2)。此前只认 float64 与
// int,YAML 来的 uint64 会被当成 0 —— 节点级 timeout_seconds /
// retry.max_attempts 静默失效(不报错,只是不生效)。
//
// 迭代 28(#317):本用例原先锁的是 `cfgInt`,而 `cfgInt` / `toInt` 在
// timeout_seconds 改走 `cfgFloat` + 钳制之后**已无生产调用方**,被删除。
// 断言强度**不变**(仍是"认全所有数值形态",字符串 "30" → 0),只是把
// 被测对象换成现在真正在用的那个助手。
func TestCfgFloatAcceptsAllNumericShapes(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want float64
	}{
		{"YAML 整数(uint64)", uint64(30), 30},
		{"int", 30, 30},
		{"int64", int64(30), 30},
		{"uint", uint(30), 30},
		{"JSON 整数(float64)", float64(30), 30},
		{"非法值按 0 处理", "30", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &model.Node{ID: "n1", Config: map[string]any{"timeout_seconds": tc.val}}
			if got := cfgFloat(node, "timeout_seconds"); got != tc.want {
				t.Errorf("cfgFloat = %v,want %v(值类型 %T)", got, tc.want, tc.val)
			}
		})
	}
}

// TestRetryPolicyAcceptsUint64 锁住 P2-7 的重试策略读取点。
func TestRetryPolicyAcceptsUint64(t *testing.T) {
	node := &model.Node{ID: "n1", Config: map[string]any{
		"retry": map[string]any{"max_attempts": uint64(3), "backoff": "exponential"},
	}}
	max, backoff, err := retryPolicyOf(node)
	if err != nil {
		t.Fatalf("retryPolicyOf: %v", err)
	}
	if max != 3 {
		t.Errorf("max_attempts = %d,want 3(uint64 未被识别)", max)
	}
	if backoff != "exponential" {
		t.Errorf("backoff = %q,want exponential", backoff)
	}
}

// TestNextAttemptAcceptsUint64 锁住 P2-7 的 attempt 计数读取点。
//
// attempt 认不出来 → 恒为 1 → 循环/重试的第二轮把第一轮的明细整行覆盖
// (明细主键 exec:node:attempt),历史完全不可见。
func TestNextAttemptAcceptsUint64(t *testing.T) {
	st := &fakeState{m: map[string]any{attemptKey("n1"): uint64(2)}}
	if got := nextAttempt(st, "n1"); got != 3 {
		t.Errorf("nextAttempt = %d,want 3(uint64 未被识别)", got)
	}
	st2 := &fakeState{m: map[string]any{attemptKey("n1"): float64(4)}}
	if got := nextAttempt(st2, "n1"); got != 5 {
		t.Errorf("nextAttempt(float64) = %d,want 5", got)
	}
}

// TestNodeEnabledFalseSkipsNode 锁住 P2-8:节点级开关 enabled=false →
// 节点被跳过(且不执行任何副作用)。
func TestNodeEnabledFalseSkipsNode(t *testing.T) {
	agt := mock.New(mock.Options{ID: "mock-a", Scripts: map[string]*mock.Script{
		"plan": {SummaryTemplate: "不该被执行"},
	}})
	eng := newMinimalEngine(t)
	// 授予权限:节点若被执行,应当成功 —— 这样"被跳过"才是可归因的
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})
	_ = agt

	node := &model.Node{ID: "n1", Type: model.NodeTypeAgent, Config: map[string]any{
		"enabled": false, "agent": "mock-a", "mode": "plan",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-enabled"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeSkipped {
		t.Fatalf("enabled=false 的节点状态 = %s,want SKIPPED", out.State)
	}
	if got := agt.CallsFor("exec-enabled", "plan"); got != 0 {
		t.Errorf("enabled=false 的节点不应调用 Agent,实际调用 %d 次", got)
	}
}

// TestNodeEnabledInvalidValueFailsNode 锁住 P2-8 的另一半:enabled 取值
// 非法(非布尔)时必须显式失败,不能静默按"启用"执行 ——
// 静默执行会让用户以为节点已关掉,实际却跑了副作用(写文件/跑测试)。
//
// 校验期已经会拦下这种写法(INVALID_NODE_ENABLED),这里是纵深防御:
// 恢复/重试路径用的是历史快照,未必经过当次校验。
func TestNodeEnabledInvalidValueFailsNode(t *testing.T) {
	agt := mock.New(mock.Options{ID: "mock-a", Scripts: map[string]*mock.Script{
		"plan": {SummaryTemplate: "不该被执行"},
	}})
	eng := newMinimalEngine(t)
	// 授予权限:排除"因为权限失败"这个混淆因素,失败原因必须来自 enabled
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})

	node := &model.Node{ID: "n1", Type: model.NodeTypeAgent, Config: map[string]any{
		"enabled": "yes", "agent": "mock-a", "mode": "plan",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-enabled"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("enabled 取值非法时节点状态 = %s,want FAILED", out.State)
	}
	if !strings.Contains(out.Error, "enabled") {
		t.Errorf("失败原因必须指向 enabled 开关,实际: %q", out.Error)
	}
	if got := agt.CallsFor("exec-enabled", "plan"); got != 0 {
		t.Errorf("enabled 取值非法时不应调用 Agent,实际调用 %d 次", got)
	}
}

// TestNodeEnabledSharesContractWithValidator 锁住约定 #13:运行期与校验期
// 用同一个 enabled 解析(compiler.NodeEnabledStateOf),两边不会各写一份。
func TestNodeEnabledSharesContractWithValidator(t *testing.T) {
	cases := []struct {
		cfg     map[string]any
		want    bool
		wantErr bool
	}{
		{map[string]any{}, true, false},
		{map[string]any{"enabled": true}, true, false},
		{map[string]any{"enabled": false}, false, false},
		{map[string]any{"enabled": "false"}, false, true},
		{map[string]any{"enabled": nil}, false, true},
	}
	for _, tc := range cases {
		node := &model.Node{ID: "n1", Config: tc.cfg}
		got, err := nodeEnabled(node)
		if tc.wantErr {
			if err == nil {
				t.Errorf("config %v: 期望报错,实际无错", tc.cfg)
			}
			continue
		}
		if err != nil {
			t.Errorf("config %v: 不期望报错,实际 %v", tc.cfg, err)
			continue
		}
		if got != tc.want {
			t.Errorf("config %v: enabled = %v,want %v", tc.cfg, got, tc.want)
		}
	}
}
