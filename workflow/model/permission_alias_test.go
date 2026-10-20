package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorkflowUnmarshalAcceptsDSLPermissionsSection 工作流 JSON 里的
// "permissions"(DSL 根级段的正式写法)必须被识别。
//
// 此前 model.Workflow.Permission 的 json tag 是单数 "permission",而 DSL
// 文档(需求.md 第十八章)根级段名是复数 "permissions":json.Unmarshal 把
// "permissions" 当未知字段**静默丢弃** —— 用户在工作流里写了权限收紧,
// 导入后运行期拿到的仍是默认宽松策略,没有任何提示(约定 14:该失败却成功)。
func TestWorkflowUnmarshalAcceptsDSLPermissionsSection(t *testing.T) {
	const src = `{
      "id": "wf-1",
      "name": "X",
      "permissions": {"claude-code": {"filesystem": {"write": false}}}
    }`
	var wf Workflow
	if err := json.Unmarshal([]byte(src), &wf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(wf.Permission) != 1 {
		t.Fatalf("Permission = %v, want 1 个 agent(permissions 段被静默丢弃了)", wf.Permission)
	}
	agent, _ := wf.Permission["claude-code"].(map[string]any)
	fs, _ := agent["filesystem"].(map[string]any)
	if fs["write"] != false {
		t.Errorf("permissions 内容 = %v, want write=false", wf.Permission)
	}
}

// TestWorkflowUnmarshalLegacyPermissionSection 单数写法("permission",
// 模型自身的 json tag / 历史版本快照里的写法)必须继续可用 —— 不能为了
// 兼容 DSL 写法把已有数据读坏。
func TestWorkflowUnmarshalLegacyPermissionSection(t *testing.T) {
	const src = `{"id":"wf-1","permission":{"claude-code":{"git":{"commit":false}}}}`
	var wf Workflow
	if err := json.Unmarshal([]byte(src), &wf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(wf.Permission) != 1 {
		t.Fatalf("Permission = %v, want 1 个 agent", wf.Permission)
	}
}

// TestWorkflowUnmarshalRejectsBothPermissionSections 同时出现两个写法必须
// 报错,不能静默挑一个 —— 挑错的那个就是"用户以为生效了"的假象。
func TestWorkflowUnmarshalRejectsBothPermissionSections(t *testing.T) {
	const src = `{"id":"wf-1","permission":{"a":{}},"permissions":{"b":{}}}`
	var wf Workflow
	err := json.Unmarshal([]byte(src), &wf)
	if err == nil {
		t.Fatalf("同时出现 permission 与 permissions 时应报错,实际接受并得到 %v", wf.Permission)
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Errorf("错误消息 = %q, want 提到 permission", err.Error())
	}
}

// TestWorkflowMarshalKeepsLegacyKey 输出侧保持单数 "permission":
// 既有 API 响应与历史版本快照(snapshot_json)依赖这个键名,不能改。
func TestWorkflowMarshalKeepsLegacyKey(t *testing.T) {
	raw, err := json.Marshal(&Workflow{ID: "wf-1", Permission: map[string]any{"a": map[string]any{}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"permission"`) {
		t.Errorf("输出 = %s, want 含 \"permission\"", raw)
	}
}
