package application

// 本文件把"工作流 JSON 载荷的顶层键契约"下沉到应用服务层(两种模式共用)。
//
// 背景(迭代 22 · 双模式一致性):
// 顶层键严格校验此前只存在于 api 层(api/server.go 的 parseWorkflow)——
// 服务器模式走它,桌面模式走 Wails 绑定(app.Workflows.Save),而绑定把
// 载荷反序列化成 model.Workflow 时,未知顶层键被 json.Unmarshal **静默
// 丢弃**:同一份非法载荷在两种模式下行为不同(约定 10)。
//
// 桌面模式要看到未知键,必须拿到**原始载荷**(键在反序列化时就没了),
// 因此保存入口统一为载荷级(SavePayload),两个模式走同一段代码。

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"agentworkflow/workflow/model"
)

// workflowTopLevelKeys 是工作流 JSON 允许的顶层键集合。
//
// 唯一来源:model.Workflow 的 json tag(反射派生 —— 模型加字段这里自动
// 生效,不会与模型漂移),外加三个特例:
//
//   - "permissions":DSL 根级段的正式写法(需求.md 第十八章),模型读它时
//     映射到 Permission 字段(见 model.Workflow.UnmarshalJSON);
//   - "ignored_keys":导入结果的附加字段(见 WorkflowResult);
//   - "warnings":保存/运行结果的附加字段(见 WorkflowResult)。
//
// 后两个是"响应专用"字段:保存时忽略它们 —— 拒掉会让"导入 → 原样回存"
// 这种往返失败(与 ignored_keys 的既有处理同源)。
var workflowTopLevelKeys = buildWorkflowTopLevelKeys()

func buildWorkflowTopLevelKeys() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(model.Workflow{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	out["permissions"] = true
	out["ignored_keys"] = true
	out["warnings"] = true
	return out
}

// AllowedWorkflowTopLevelKeys 返回允许的顶层键(排序后),用于错误信息。
func AllowedWorkflowTopLevelKeys() []string {
	out := make([]string, 0, len(workflowTopLevelKeys))
	for k := range workflowTopLevelKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// UnknownWorkflowTopLevelKeys 返回载荷里不被识别的顶层键(排序)。
func UnknownWorkflowTopLevelKeys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		if !workflowTopLevelKeys[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ParseWorkflowPayload 解析工作流 JSON 载荷(两种模式的保存/校验入口共用)。
//
// 错误语义(与 api 层此前一致,只是搬到了共用层):
//   - 非法 JSON → INVALID_JSON
//   - 未知顶层键 → UNKNOWN_FIELD(点名字段与允许集合)
//   - 结构非法(如 permission/permissions 同时出现)→ BAD_REQUEST
//
// 未知顶层键必须报错:json.Unmarshal 默认宽松,多余的键被**静默丢弃** ——
// 用户提交了一份含多余键的 JSON,接口返回 200,用户以为那份设置生效了,
// 实际根本没进模型(约定 14:静默丢弃 = 用户以为设置还在)。
//
// 与 DSL 顶层键策略同源:workflow/dsl 对文档顶层的未知键报
// DSL_UNKNOWN_KEY(迭代 15),JSON 保存路径此前却没有对应行为 —— 同一份
// 定义换个格式导入行为就不同。
func ParseWorkflowPayload(raw []byte) (*model.Workflow, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, model.NewError(model.KindValidationError, "INVALID_JSON",
			"请求体不是合法 JSON: "+err.Error())
	}
	if unknown := UnknownWorkflowTopLevelKeys(probe); len(unknown) > 0 {
		return nil, model.NewError(model.KindValidationError, "UNKNOWN_FIELD",
			fmt.Sprintf("未知的顶层字段 %s(允许: %s)",
				strings.Join(unknown, ", "),
				strings.Join(AllowedWorkflowTopLevelKeys(), ", ")))
	}
	var wf model.Workflow
	if err := json.Unmarshal(raw, &wf); err != nil {
		return nil, model.NewError(model.KindValidationError, "BAD_REQUEST",
			"工作流结构非法: "+err.Error())
	}
	return &wf, nil
}
