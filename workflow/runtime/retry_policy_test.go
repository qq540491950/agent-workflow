package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住 P2-6:retry.max_attempts 的取值必须**可判定**。
//
// 缺陷(运行期):retryPolicyOf 用 toInt 读 max_attempts,而 toInt 只认数值
// 形态 —— 字符串 "3" 认不出来 → 返回 0 → 被钳到 1 → **重试被静默关闭**:
// 用户在 DSL 里写了 max_attempts: "3" 期望重试 3 次,实际只跑 1 次,
// 节点照常 SUCCESS,没有任何提示(约定 14:该失败却成功 / 静默丢弃同样危险)。
//
// 修复口径(两层一致,约定 13):
//   - 运行期 fail closed:读不出合法整数 → **节点失败**并说明原因;
//   - 校验期:max_attempts 必须是非负整数(workflow/validator/validator.go)。
//
// 兼容:合法写法(int / int64 / uint64 / float64 整数 / json.Number)照旧;

// TestRetryPolicyRejectsUnparsableMaxAttempts 不可判定的取值必须报错。
func TestRetryPolicyRejectsUnparsableMaxAttempts(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"字符串(此前被静默当成 0 → 钳到 1 → 重试被关闭)", "3"},
		{"小数", 3.5},
		{"负数", -1},
		{"布尔", true},
		{"null", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &model.Node{ID: "n1", Config: map[string]any{
				"retry": map[string]any{"max_attempts": tc.val},
			}}
			_, err := retryPolicyOf(node)
			if err == nil {
				t.Fatalf("max_attempts = %#v(%T): 必须报错(不得静默当成 0/1)", tc.val, tc.val)
			}
			if !strings.Contains(err.Error(), "max_attempts") {
				t.Errorf("错误信息必须指向 max_attempts,实际: %v", err)
			}
		})
	}
}

// TestRetryPolicyRejectsNonObjectRetry retry 本身不是对象时也必须报错。
//
// 此前 `node.Config["retry"].(map[string]any)` 断言失败就静默走默认
// (不重试)—— 用户写了 retry 却什么都没发生。
func TestRetryPolicyRejectsNonObjectRetry(t *testing.T) {
	node := &model.Node{ID: "n1", Config: map[string]any{"retry": "3"}}
	if _, err := retryPolicyOf(node); err == nil {
		t.Fatal("retry 不是对象时必须报错(不得静默按默认不重试)")
	}
}

// TestRetryPolicyAcceptsIntegerForms 合法数值形态照旧生效。
func TestRetryPolicyAcceptsIntegerForms(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		{"int", 3, 3},
		{"int64", int64(4), 4},
		{"uint64", uint64(5), 5},
		{"float64(JSON 往返)", float64(6), 6},
		{"json.Number", json.Number("7"), 7},
		{"0(与缺省同义 = 不重试)", 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &model.Node{ID: "n1", Config: map[string]any{
				"retry": map[string]any{"max_attempts": tc.val},
			}}
			p, err := retryPolicyOf(node)
			if err != nil {
				t.Fatalf("max_attempts = %#v: 不该报错,实际 %v", tc.val, err)
			}
			if p.MaxAttempts != tc.want {
				t.Errorf("max_attempts = %d,want %d", p.MaxAttempts, tc.want)
			}
			if p.Backoff != "fixed" {
				t.Errorf("backoff = %q,want fixed", p.Backoff)
			}
		})
	}
}

// TestRetryPolicyDefaults 缺省/空对象 → 不重试。
func TestRetryPolicyDefaults(t *testing.T) {
	for _, cfg := range []map[string]any{
		{},
		{"retry": map[string]any{}},
	} {
		node := &model.Node{ID: "n1", Config: cfg}
		p, err := retryPolicyOf(node)
		if err != nil {
			t.Fatalf("config %v: 不该报错,实际 %v", cfg, err)
		}
		if p.MaxAttempts != 1 {
			t.Errorf("config %v: max_attempts = %d,want 1", cfg, p.MaxAttempts)
		}
		if p.Backoff != "fixed" {
			t.Errorf("config %v: backoff = %q,want fixed", cfg, p.Backoff)
		}
	}
}

// TestAgentNodeFailsOnInvalidRetryConfig 节点级回归:retry 配置非法时
// 节点必须**失败**并说明原因,且**不得调用 Agent**(fail closed)。
func TestAgentNodeFailsOnInvalidRetryConfig(t *testing.T) {
	agt := mock.New(mock.Options{ID: "mock-a", Scripts: map[string]*mock.Script{
		"plan": {SummaryTemplate: "不该被执行"},
	}})
	eng := newMinimalEngine(t)
	eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})

	node := &model.Node{ID: "n1", Type: model.NodeTypeAgent, Config: map[string]any{
		"agent": "mock-a", "mode": "plan",
		"retry": map[string]any{"max_attempts": "3"},
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-retry"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("retry 配置非法时节点状态 = %s,want FAILED", out.State)
	}
	if !strings.Contains(out.Error, "max_attempts") {
		t.Errorf("失败原因必须指向 max_attempts,实际: %q", out.Error)
	}
	if got := agt.CallsFor("exec-retry", "plan"); got != 0 {
		t.Errorf("retry 配置非法时不应调用 Agent,实际调用 %d 次", got)
	}
}
