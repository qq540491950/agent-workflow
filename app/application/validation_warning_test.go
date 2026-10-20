package application

// 本文件锁住"校验警告不得在保存/运行路径上被丢掉"(P3-1)。
//
// 此前 WorkflowService.Save 与 ExecutionService.Run 只回传 res.Errors,
// res.Warnings 整个被丢掉:DEPRECATED_NODE_CONFIG 这类警告只有显式
// validate 端点可见,直连 API 的调用方看不到 —— 静默丢失(约定 #14 同族:
// "该报告却没报告")。前端靠先调 validate 兜住了,但那只是前端的做法,
// 不是后端的保证。

import (
	"context"
	"testing"

	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
	"agentworkflow/workflow/validator"
)

// deprecatedConfigWorkflowYAML 是一个"能通过校验、但带警告"的工作流:
// condition 节点的 expression 配置键已废弃(编译器不再读取它)。
const deprecatedConfigWorkflowYAML = `
version: "1"
workflow:
  id: wf-deprecated
  name: Deprecated
nodes:
  - id: c
    type: condition
    expression: "1 == 1"
edges: []
`

// TestSavePathDoesNotDropValidationWarnings 保存路径不得丢掉校验警告。
func TestSavePathDoesNotDropValidationWarnings(t *testing.T) {
	a := newTestApp(t)

	var got []validator.ValidationError
	restore := setValidationWarningSink(func(scope, id string, w validator.ValidationError) {
		got = append(got, w)
	})
	defer restore()

	wf := parseWorkflowYAML(t, deprecatedConfigWorkflowYAML)
	if _, err := a.Workflows.Save(wf); err != nil {
		t.Fatalf("保存带警告的工作流应当成功: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("保存路径把校验警告整个丢掉了(DEPRECATED_NODE_CONFIG 等警告必须被带出来)")
	}
	found := false
	for _, w := range got {
		if w.Code == "DEPRECATED_NODE_CONFIG" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("未看到 DEPRECATED_NODE_CONFIG 警告,实际收到: %+v", got)
	}
}

// TestRunPathDoesNotDropValidationWarnings 运行路径不得丢掉校验警告。
func TestRunPathDoesNotDropValidationWarnings(t *testing.T) {
	a := newTestApp(t)

	var got []validator.ValidationError
	restore := setValidationWarningSink(func(scope, id string, w validator.ValidationError) {
		got = append(got, w)
	})
	defer restore()

	wf, err := a.Workflows.ImportYAML(deprecatedConfigWorkflowYAML)
	if err != nil {
		t.Fatalf("导入工作流: %v", err)
	}
	// 运行会真的启动执行;这里只关心"警告有没有被带出来"。
	_, _ = a.Executions.Run(context.Background(), wf.ID, "t", nil)
	if len(got) == 0 {
		t.Fatal("运行路径把校验警告整个丢掉了")
	}
}

// parseWorkflowYAML 解析 DSL 文本为工作流模型(测试用)。
func parseWorkflowYAML(t *testing.T, content string) *model.Workflow {
	t.Helper()
	doc, err := dsl.Parse([]byte(content))
	if err != nil {
		t.Fatalf("解析工作流: %v", err)
	}
	return doc.ToModel()
}
