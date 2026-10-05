package validator

import (
	"testing"

	"agentworkflow/workflow/dsl"
)

// 本文件锁定一条硬性约定(AGENTS.md #13):校验期与运行期的条件表达式
// 环境必须同源。校验器曾用 struct env,只暴露 decision/status/output/
// iteration 四个字段,于是 `variables.env == "prod"` 被判成
// "unknown name variables" 拒绝保存,而同样表达式在运行期完全合法 ——
// 用户被自己的校验器挡住,无法使用运行期已支持的能力。

// TestVariablesExpressionAccepted 验证 variables.* 表达式不再被误判。
func TestVariablesExpressionAccepted(t *testing.T) {
	cases := []string{
		`variables.env == "prod"`,
		`variables.count + 1 > 3`,
		`variables.owner == "alice" && decision == "APPROVED"`,
		`variables.enabled`,
	}
	for _, cond := range cases {
		yml := workflowWithEdgeCondition(cond)
		wf := mustWf(t, yml)
		res := Validate(wf, agentResolver(nil), skillResolver(nil))
		if has(res, "INVALID_EXPRESSION") {
			t.Errorf("表达式 %q 应被接受(运行期支持),实际被判非法: %+v", cond, res.Errors)
		}
	}
}

// TestStillRejectsBrokenExpression 回归:真正非法的语法仍必须被拒。
func TestStillRejectsBrokenExpression(t *testing.T) {
	for _, cond := range []string{
		`decision ==`,
		`(1 +`,
		`decision === "APPROVED"`,
	} {
		wf := mustWf(t, workflowWithEdgeCondition(cond))
		res := Validate(wf, agentResolver(nil), skillResolver(nil))
		if !has(res, "INVALID_EXPRESSION") {
			t.Errorf("表达式 %q 应被判非法,实际通过", cond)
		}
	}
}

// TestConditionNodeExpressionAlsoSeesVariables condition 节点的
// expression 走同一套 checkExpression,必须同样支持 variables。
func TestConditionNodeExpressionAlsoSeesVariables(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: cond-vars
  name: Cond Vars
nodes:
  - id: plan
    type: agent
    agent: claude-code
    mode: plan
  - id: check
    type: condition
    expression: variables.level == "high"
  - id: submit
    type: skill
    skill: submit
edges:
  - from: plan
    to: check
  - from: check
    to: submit
    condition: decision == "DONE"
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if has(res, "INVALID_EXPRESSION") {
		t.Errorf("condition 节点也应支持 variables: %+v", res.Errors)
	}
}

// workflowWithEdgeCondition 生成一条 plan → review(带条件边)→ submit 的
// 工作流,把 cond 注入到 review 的出口条件上。
func workflowWithEdgeCondition(cond string) string {
	return `
workflow:
  id: expr-env
  name: Expr Env
nodes:
  - id: plan
    type: agent
    agent: claude-code
    mode: plan
  - id: review
    type: agent
    agent: claude-code
    mode: review
  - id: submit
    type: skill
    skill: submit
edges:
  - from: plan
    to: review
  - from: review
    condition: ` + cond + `
    to: submit
`
}

// TestValidatorEnvMatchesRuntimeEnv 直接比对两侧的环境键集合:
// 校验器少任何一个键,用户就会被误判。
func TestValidatorEnvMatchesRuntimeEnv(t *testing.T) {
	want := map[string]bool{
		"decision": true, "status": true, "output": true,
		"iteration": true, "variables": true,
	}
	env := newExpressionEnv()
	for k := range want {
		if _, ok := env[k]; !ok {
			t.Errorf("校验环境缺少键 %q(运行期支持但校验期会误判)", k)
		}
	}
	for k := range env {
		if !want[k] {
			t.Errorf("校验环境出现运行期没有的键 %q", k)
		}
	}
	// variables 必须是任意键的 map,而不是固定结构
	vars, ok := env["variables"].(map[string]any)
	if !ok {
		t.Fatalf("variables 应为 map[string]any, 实际 %T", env["variables"])
	}
	if _, known := vars["anything"]; known {
		t.Error("variables 不应被固化为某组具体键")
	}
	_ = dsl.Parse // 保持与本包其它测试相同的依赖面
}
