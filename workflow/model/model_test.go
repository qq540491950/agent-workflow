package model

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// normalizeWorkflowMaps 把工作流里所有"开放 map"里的 JSON 数值归一为 int。
//
// 约定 2:任何经 SQLite state_json 持久化再读回的 int 都是 float64,
// 读取必须走解码边界(model.NormalizeStateJSON)或 compiler.IntOfOk。
// 结构体字段(Version / MaxIterations / Attempt …)是强类型,不受影响。
func normalizeWorkflowMaps(wf *Workflow) {
	NormalizeStateJSON(wf.Variables)
	NormalizeStateJSON(wf.Permission)
	for i := range wf.Nodes {
		NormalizeStateJSON(wf.Nodes[i].Config)
	}
}

func mustJSONRoundTrip[T any](t *testing.T, in *T) *T {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := new(T)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestWorkflowJSONRoundTrip 锁住工作流定义的 JSON 往返行为。
//
// 强类型字段(int/bool/float64)往返后类型与值不变;
// 开放 map(Variables / Permission / Node.Config)里的整数会变成 float64 ——
// 这正是约定 2 描述的陷阱,必须由解码边界归一,否则读取点会静默拿错类型。
func TestWorkflowJSONRoundTrip(t *testing.T) {
	wf := &Workflow{
		ID:          "wf-1",
		Name:        "示例工作流",
		Description: "往返测试",
		Version:     7,
		Enabled:     true,
		Variables:   map[string]any{"max_review": 5, "ratio": 1.5, "name": "x"},
		Permission:  map[string]any{"filesystem_write": true},
		Nodes: []Node{
			{
				ID:   "plan",
				Name: "制定方案",
				Type: NodeTypeAgent,
				Config: map[string]any{
					"agent":           "claude-code",
					"mode":            "plan",
					"timeout_seconds": 600,
				},
				Position: Position{X: 120.5, Y: -40},
			},
		},
		Edges: []Edge{
			{From: "plan", To: "review", Condition: `decision == "APPROVED"`},
			{From: "plan", To: "fix"},
		},
		Settings:  WorkflowSettings{MaxIterations: 10, OnLoopLimit: "wait_user", TimeoutSeconds: 300},
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-02T00:00:00Z",
	}

	got := mustJSONRoundTrip(t, wf)

	// 强类型字段:类型与值都保持不变
	if got.Version != 7 {
		t.Errorf("version = %d, want 7", got.Version)
	}
	if got.Settings.MaxIterations != 10 || got.Settings.TimeoutSeconds != 300 {
		t.Errorf("settings = %+v", got.Settings)
	}
	if got.Nodes[0].Position.X != 120.5 || got.Nodes[0].Position.Y != -40 {
		t.Errorf("position = %+v", got.Nodes[0].Position)
	}

	// 开放 map:整数变 float64(约定 2 的陷阱,显式钉住)
	if _, ok := got.Variables["max_review"].(float64); !ok {
		t.Errorf("variables.max_review 往返后应为 float64,实际 %T", got.Variables["max_review"])
	}
	if _, ok := got.Nodes[0].Config["timeout_seconds"].(float64); !ok {
		t.Errorf("config.timeout_seconds 往返后应为 float64,实际 %T", got.Nodes[0].Config["timeout_seconds"])
	}

	// 解码边界归一后,整体深度相等
	normalizeWorkflowMaps(got)
	if !reflect.DeepEqual(wf, got) {
		t.Errorf("往返后结构不一致:\nwant %+v\ngot  %+v", wf, got)
	}
}

// TestExecutionJSONRoundTrip 锁住执行实例的 JSON 往返行为。
//
// 关键区别:Iterations 是 map[string]int(强类型)→ 往返后仍是 int;
// StateData 是 map[string]any → 整数变 float64,必须归一。
func TestExecutionJSONRoundTrip(t *testing.T) {
	exec := &Execution{
		ID:              "exec-1",
		WorkflowID:      "wf-1",
		WorkflowVersion: 3,
		WorkflowName:    "示例工作流",
		State:           ExecutionRunning,
		Task:            "做点事",
		Variables:       map[string]any{"branch": "main"},
		CurrentNodeID:   "plan",
		Iterations:      map[string]int{"loop": 2},
		CreatedAt:       "2026-01-01T00:00:00Z",
		StartedAt:       "2026-01-01T00:00:01Z",
		Error:           "",
		NodeStates:      map[string]string{"plan": string(NodeSuccess)},
		StateData: map[string]any{
			"_loop_count": 2,
			"score":       1.5,
			"nested":      map[string]any{"attempt": 1},
			"history":     []any{1, "two"},
		},
	}

	got := mustJSONRoundTrip(t, exec)

	if got.WorkflowVersion != 3 {
		t.Errorf("workflow_version = %d, want 3", got.WorkflowVersion)
	}
	// 强类型 map:往返后仍是 int(不会被 JSON 变成 float64)
	if v, ok := got.Iterations["loop"]; !ok || v != 2 {
		t.Errorf("iterations[loop] = %v(%T), want int 2", v, v)
	}
	// 开放 map:整数变 float64
	if _, ok := got.StateData["_loop_count"].(float64); !ok {
		t.Errorf("state_data._loop_count 往返后应为 float64,实际 %T", got.StateData["_loop_count"])
	}
	if _, ok := got.StateData["nested"].(map[string]any); !ok {
		t.Errorf("state_data.nested 类型丢失: %T", got.StateData["nested"])
	}

	NormalizeStateJSON(got.StateData)
	if !reflect.DeepEqual(exec, got) {
		t.Errorf("归一后仍不一致:\nwant %+v\ngot  %+v", exec, got)
	}
}

// TestExecutionSnapshotNotSerialized 锁住 Snapshot 的 json:"-" 语义:
// 快照是执行期的内存态,不入 JSON(落库走单独的 snapshot_json 列)。
func TestExecutionSnapshotNotSerialized(t *testing.T) {
	exec := &Execution{
		ID:       "exec-1",
		Snapshot: &Workflow{ID: "wf-1", Name: "带快照"},
	}
	got := mustJSONRoundTrip(t, exec)
	if got.Snapshot != nil {
		t.Errorf("snapshot 不应被序列化进 execution JSON: %+v", got.Snapshot)
	}
}

// TestExecutionNodeJSONRoundTrip 锁住节点明细的往返(attempt / duration_ms 是强类型)。
func TestExecutionNodeJSONRoundTrip(t *testing.T) {
	node := &ExecutionNode{
		ID:          "en-1",
		ExecutionID: "exec-1",
		NodeID:      "plan",
		NodeType:    NodeTypeAgent,
		NodeName:    "制定方案",
		State:       NodeFailed,
		Attempt:     3,
		Output:      "部分输出",
		ResultJSON:  `{"decision":"REJECTED"}`,
		Error:       "boom",
		StartedAt:   "2026-01-01T00:00:00Z",
		FinishedAt:  "2026-01-01T00:00:02Z",
		DurationMS:  2000,
	}
	got := mustJSONRoundTrip(t, node)
	if !reflect.DeepEqual(node, got) {
		t.Errorf("节点明细往返不一致:\nwant %+v\ngot  %+v", node, got)
	}
}

// TestArtifactJSONRoundTrip 锁住制品的往返。
func TestArtifactJSONRoundTrip(t *testing.T) {
	art := &Artifact{
		ID:          "art-1",
		ExecutionID: "exec-1",
		NodeID:      "plan",
		Name:        "plan.md",
		ContentType: "text/markdown",
		Content:     "# 方案",
		CreatedAt:   "2026-01-01T00:00:00Z",
	}
	got := mustJSONRoundTrip(t, art)
	if !reflect.DeepEqual(art, got) {
		t.Errorf("制品往返不一致:\nwant %+v\ngot  %+v", art, got)
	}
}

// TestNormalizeStateJSON 锁住解码边界的归一行为。
func TestNormalizeStateJSON(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "nil 输入返回 nil",
			in:   nil,
			want: nil,
		},
		{
			name: "float64 整数转 int",
			in:   map[string]any{"n": float64(42)},
			want: map[string]any{"n": 42},
		},
		{
			name: "带小数保持 float64",
			in:   map[string]any{"n": 1.5},
			want: map[string]any{"n": 1.5},
		},
		{
			name: "负整数转 int",
			in:   map[string]any{"n": float64(-7)},
			want: map[string]any{"n": -7},
		},
		{
			name: "int 原样保留",
			in:   map[string]any{"n": 42},
			want: map[string]any{"n": 42},
		},
		{
			name: "int64 原样保留(JSON 不会产生 int64,内存态可能有)",
			in:   map[string]any{"n": int64(42)},
			want: map[string]any{"n": int64(42)},
		},
		{
			name: "uint64 转 int(YAML inline map 的整数)",
			in:   map[string]any{"n": uint64(42)},
			want: map[string]any{"n": 42},
		},
		{
			name: "超出 int64 范围的 uint64 保持不变",
			in:   map[string]any{"n": uint64(math.MaxUint64)},
			want: map[string]any{"n": uint64(math.MaxUint64)},
		},
		{
			name: "字符串 / 布尔 / nil 不动",
			in:   map[string]any{"s": "x", "b": true, "nil": nil},
			want: map[string]any{"s": "x", "b": true, "nil": nil},
		},
		{
			name: "嵌套 map 归一",
			in:   map[string]any{"m": map[string]any{"n": float64(3)}},
			want: map[string]any{"m": map[string]any{"n": 3}},
		},
		{
			name: "数组里的裸数字归一",
			in:   map[string]any{"l": []any{float64(1), "two"}},
			want: map[string]any{"l": []any{1, "two"}},
		},
		{
			name: "数组里的 map 归一(此前漏掉)",
			in:   map[string]any{"l": []any{map[string]any{"n": float64(1)}}},
			want: map[string]any{"l": []any{map[string]any{"n": 1}}},
		},
		{
			name: "嵌套数组归一",
			in:   map[string]any{"l": []any{[]any{float64(2)}}},
			want: map[string]any{"l": []any{[]any{2}}},
		},
		{
			name: "超出 int64 范围保持 float64",
			in:   map[string]any{"n": 1e20},
			want: map[string]any{"n": 1e20},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeStateJSON(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if !reflect.DeepEqual(tc.want, got) {
				t.Errorf("归一结果不符:\nwant %#v\ngot  %#v", tc.want, got)
			}
		})
	}

	// NaN / Inf 不能用 DeepEqual 比较(NaN != NaN),单独断言
	nanInf := NormalizeStateJSON(map[string]any{"nan": math.NaN(), "inf": math.Inf(1)})
	if v, ok := nanInf["nan"].(float64); !ok || !math.IsNaN(v) {
		t.Errorf("NaN 应保持 float64,实际 %v(%T)", nanInf["nan"], nanInf["nan"])
	}
	if v, ok := nanInf["inf"].(float64); !ok || !math.IsInf(v, 1) {
		t.Errorf("+Inf 应保持 float64,实际 %v(%T)", nanInf["inf"], nanInf["inf"])
	}
}

// TestCanTransitionExecution 锁住执行状态机的合法转换。
func TestCanTransitionExecution(t *testing.T) {
	allowed := [][2]ExecutionState{
		{ExecutionCreated, ExecutionRunning},
		{ExecutionCreated, ExecutionCancelled},
		{ExecutionCreated, ExecutionFailed},
		{ExecutionRunning, ExecutionPaused},
		{ExecutionRunning, ExecutionWaitingUser},
		{ExecutionRunning, ExecutionCompleted},
		{ExecutionRunning, ExecutionFailed},
		{ExecutionRunning, ExecutionCancelled},
		{ExecutionPaused, ExecutionRunning},
		{ExecutionPaused, ExecutionCancelled},
		{ExecutionPaused, ExecutionFailed},
		{ExecutionWaitingUser, ExecutionRunning},
		{ExecutionWaitingUser, ExecutionCancelled},
		{ExecutionWaitingUser, ExecutionFailed},
		{ExecutionFailed, ExecutionRunning}, // 用户显式重试
	}
	for _, tc := range allowed {
		if !CanTransitionExecution(tc[0], tc[1]) {
			t.Errorf("%s → %s 应允许", tc[0], tc[1])
		}
	}

	denied := [][2]ExecutionState{
		{ExecutionCompleted, ExecutionRunning}, // 终态不可复活
		{ExecutionCompleted, ExecutionFailed},
		{ExecutionCancelled, ExecutionRunning}, // 终态不可复活
		{ExecutionCancelled, ExecutionFailed},
		{ExecutionFailed, ExecutionCompleted}, // 重试只能回到 RUNNING
		{ExecutionFailed, ExecutionCancelled},
		{ExecutionRunning, ExecutionRunning}, // 不允许原地重复
		{ExecutionCreated, ExecutionCreated},
		{ExecutionCreated, ExecutionPaused},    // 未运行不能暂停
		{ExecutionCreated, ExecutionCompleted}, // 未运行不能直接完成
		{"", ExecutionRunning},                 // 未知状态
		{ExecutionRunning, ""},
	}
	for _, tc := range denied {
		if CanTransitionExecution(tc[0], tc[1]) {
			t.Errorf("%s → %s 不应允许", tc[0], tc[1])
		}
	}
}

// TestCanTransitionNode 锁住节点状态机的合法转换。
func TestCanTransitionNode(t *testing.T) {
	allowed := [][2]NodeState{
		{NodePending, NodeRunning},
		{NodePending, NodeSkipped},
		{NodeRunning, NodeSuccess},
		{NodeRunning, NodeFailed},
		{NodeRunning, NodeWaiting},
		{NodeWaiting, NodeRunning},
		{NodeWaiting, NodeSkipped},
		{NodeWaiting, NodeSuccess},
	}
	for _, tc := range allowed {
		if !CanTransitionNode(tc[0], tc[1]) {
			t.Errorf("%s → %s 应允许", tc[0], tc[1])
		}
	}
	denied := [][2]NodeState{
		{NodeSuccess, NodeRunning},
		{NodeSuccess, NodeFailed},
		{NodeFailed, NodeRunning},
		{NodeSkipped, NodeRunning},
		{NodePending, NodeSuccess},
		{NodeRunning, NodeRunning},
		{"", NodeRunning},
	}
	for _, tc := range denied {
		if CanTransitionNode(tc[0], tc[1]) {
			t.Errorf("%s → %s 不应允许", tc[0], tc[1])
		}
	}
}

// TestWorkflowGraphHelpers 锁住图查询辅助函数(含边界:空图、重复 id)。
func TestWorkflowGraphHelpers(t *testing.T) {
	wf := &Workflow{
		ID:    "wf-1",
		Nodes: []Node{{ID: "a"}, {ID: "b"}, {ID: "a"}},
		Edges: []Edge{{From: "a", To: "b"}, {From: "a", To: "c"}},
	}
	node, err := wf.FindNode("b")
	if err != nil {
		t.Fatalf("FindNode: %v", err)
	}
	if node.ID != "b" {
		t.Errorf("FindNode 返回 %q", node.ID)
	}
	// 重复 id:返回第一个
	first, err := wf.FindNode("a")
	if err != nil {
		t.Fatalf("FindNode(a): %v", err)
	}
	if first != &wf.Nodes[0] {
		t.Errorf("FindNode(a) 应返回首个匹配节点")
	}
	if _, err := wf.FindNode("zzz"); err == nil {
		t.Error("缺失节点应报错")
	}

	if got := len(wf.OutgoingEdges("a")); got != 2 {
		t.Errorf("outgoing = %d, want 2", got)
	}
	if got := len(wf.IncomingEdges("b")); got != 1 {
		t.Errorf("incoming = %d, want 1", got)
	}
	// 无出边节点返回 nil 切片(调用方按 len 判断)
	if got := wf.OutgoingEdges("b"); got != nil {
		t.Errorf("无出边时应返回 nil,实际 %+v", got)
	}
	if got := wf.IncomingEdges("a"); got != nil {
		t.Errorf("无入边时应返回 nil,实际 %+v", got)
	}

	// 空图不 panic
	empty := &Workflow{}
	if _, err := empty.FindNode("a"); err == nil {
		t.Error("空图查找应报错")
	}
	if got := empty.OutgoingEdges("a"); len(got) != 0 {
		t.Errorf("空图出边 = %+v", got)
	}
}
