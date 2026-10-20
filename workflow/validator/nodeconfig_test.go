package validator

import (
	"sort"
	"testing"

	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁定"校验期与运行期必须同源"(AGENTS.md 硬性约定 #13)在**节点配置**
// 一侧的三个具体缺陷:
//
//  1. 节点级开关 config.enabled=false:运行期直接跳过该节点(executor.go 的
//     dispatchNode 在类型分发之前就返回 SKIPPED),于是该节点的配置一个键都
//     不会被读取。校验器此前仍要求被禁用节点的必填配置 → 用户把节点关掉
//     (前端属性面板的"启用节点"开关)后反而存不了工作流。
//  2. condition 节点的 expression:运行期与编译器**都不读**它(路由只看边上的
//     condition),校验器此前却把它列为必填 → 用户被要求填一个没有作用的键,
//     而且会误以为这个表达式在驱动路由。
//  3. enabled 的取值类型:运行期只认布尔(false 才跳过)。写成字符串 "false"
//     时运行期会**忽略它并照常执行节点**,而用户以为节点已禁用。
//
// 下面每个用例都先写成"当前实现会失败"的断言(红),再由实现改绿。

// TestDisabledNodeSkipsRequiredConfig 禁用节点不再要求必填配置。
func TestDisabledNodeSkipsRequiredConfig(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: disabled
  name: Disabled
nodes:
  - id: a
    type: agent
    enabled: false
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if has(res, "MISSING_AGENT") {
		t.Errorf("被禁用的节点运行期不会执行,不应要求 agent 配置: %+v", res.Errors)
	}
	if has(res, "MISSING_MODE") {
		t.Errorf("被禁用的节点运行期不会执行,不应要求 mode 配置: %+v", res.Errors)
	}
	if !res.Valid {
		t.Errorf("仅配置 enabled=false 的禁用节点不应报校验错误: %+v", res.Errors)
	}
}

// TestDisabledNodeSkipsAgentExistence 被禁用节点引用的 Agent 不必存在。
func TestDisabledNodeSkipsAgentExistence(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: disabled-agent
  name: Disabled Agent
nodes:
  - id: a
    type: agent
    agent: ghost-agent
    mode: plan
    enabled: false
`)
	res := Validate(wf, agentResolver([]string{"claude-code"}), skillResolver(nil))
	if has(res, "AGENT_NOT_FOUND") {
		t.Errorf("被禁用的节点运行期不会调用 Agent,不应校验 Agent 是否存在: %+v", res.Errors)
	}
}

// TestNonBoolEnabledRejected enabled 不是布尔值时必须报错。
//
// 运行期只认布尔:`if b, ok := enabled.(bool); ok && !b` —— 字符串 "false"
// 会被忽略,节点照常执行,用户却以为它已禁用(约定:该失败却成功等同 P0)。
func TestNonBoolEnabledRejected(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: bad-enabled
  name: Bad Enabled
nodes:
  - id: a
    type: human
    enabled: "false"
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if !has(res, "INVALID_NODE_ENABLED") {
		t.Errorf("enabled 非布尔值应报 INVALID_NODE_ENABLED: %+v", res.Errors)
	}
}

// TestConditionNodeExpressionNotRequired condition 节点的 expression
// 运行期不读(路由只看边上的 condition),不应作为必填项。
func TestConditionNodeExpressionNotRequired(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: cond
  name: Cond
nodes:
  - id: a
    type: human
  - id: gate
    type: condition
edges:
  - from: a
    to: gate
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if has(res, "MISSING_EXPRESSION") {
		t.Errorf("condition 节点的 expression 运行期不读,不应作为必填项: %+v", res.Errors)
	}
}

// TestConditionNodeExpressionStillSyntaxChecked 回归:expression 仍然做语法
// 检查(拼错了要报错),只是不再是必填项。
func TestConditionNodeExpressionStillSyntaxChecked(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: cond-broken
  name: Cond Broken
nodes:
  - id: a
    type: human
  - id: gate
    type: condition
    expression: "decision ==== "
edges:
  - from: a
    to: gate
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if !has(res, "INVALID_EXPRESSION") {
		t.Errorf("expression 语法错误仍应报 INVALID_EXPRESSION: %+v", res.Errors)
	}
}

// ---- 必填配置契约的同源守卫 ----

// TestRequiredConfigKeysHaveErrorCodes 必填配置键(唯一来源:
// compiler.NodeConfigSpecFor)与错误码表必须一一对应。
//
// 必填清单在 compiler 里,错误码在 validator 里 —— 两处各写各的必然漂移
// (新增一个必填键却忘了配错误码 → 校验器报出一个前端不认识的码)。
func TestRequiredConfigKeysHaveErrorCodes(t *testing.T) {
	declared := map[string]bool{}
	for _, nt := range model.AllNodeTypes() {
		for _, key := range compiler.RequiredConfigKeys(nt) {
			declared[key] = true
		}
	}
	for key := range declared {
		if _, ok := missingConfigByKey[key]; !ok {
			t.Errorf("必填配置键 %q 没有对应的错误码(请补进 missingConfigByKey)", key)
		}
	}
	for key := range missingConfigByKey {
		if !declared[key] {
			t.Errorf("错误码表里的键 %q 不是任何节点类型的必填项(请从 missingConfigByKey 删除)", key)
		}
	}
}

// TestRequiredConfigCodesPerNodeType 逐节点类型锁定"空配置 → 报哪些错"。
//
// 这张表是"必填配置结构"的对外表现:任何一侧改动(新增必填键、取消必填)
// 都会在这里显形,不会悄悄改变校验器的行为。
func TestRequiredConfigCodesPerNodeType(t *testing.T) {
	want := map[model.NodeType][]string{
		model.NodeTypeAgent:       {"MISSING_AGENT", "MISSING_MODE"},
		model.NodeTypeSkill:       {"MISSING_SKILL"},
		model.NodeTypeCondition:   nil,
		model.NodeTypeParallel:    nil,
		model.NodeTypeMerge:       nil,
		model.NodeTypeHuman:       nil,
		model.NodeTypeScript:      {"MISSING_COMMAND"},
		model.NodeTypeGit:         {"INVALID_GIT_OPERATION"},
		model.NodeTypeSubWorkflow: {"MISSING_WORKFLOW_ID"},
	}
	// 只统计"必填配置"类错误码,忽略图结构类错误(NO_EXIT_PATH 等)。
	configCodes := map[string]bool{
		"MISSING_AGENT": true, "MISSING_MODE": true, "MISSING_SKILL": true,
		"MISSING_COMMAND": true, "MISSING_WORKFLOW_ID": true,
		"INVALID_GIT_OPERATION": true, "MISSING_EXPRESSION": true,
		"MISSING_CONFIG": true, "INVALID_NODE_ENABLED": true,
	}
	for _, nt := range model.AllNodeTypes() {
		wf := &model.Workflow{
			ID:   "wf",
			Name: "wf",
			Nodes: []model.Node{
				{ID: "n1", Type: nt, Config: map[string]any{}},
			},
		}
		res := Validate(wf, nil, nil)
		var got []string
		for _, e := range res.Errors {
			if configCodes[e.Code] {
				got = append(got, e.Code)
			}
		}
		sort.Strings(got)
		exp := append([]string{}, want[nt]...)
		sort.Strings(exp)
		if len(got) != len(exp) {
			t.Errorf("%s 节点空配置报出 %v, want %v", nt, got, exp)
			continue
		}
		for i := range exp {
			if got[i] != exp[i] {
				t.Errorf("%s 节点空配置报出 %v, want %v", nt, got, exp)
				break
			}
		}
	}
}

// TestValidatorEnvIsRuntimeEnv 校验期用的环境就是运行期那份(同源)。
//
// 此前两侧各写一份 env 字面量,靠人工保持一致。这里钉死:校验期的 env
// 必须与 compiler 的可用变量清单完全一致 —— 多一个少一个都失败。
func TestValidatorEnvIsRuntimeEnv(t *testing.T) {
	env := newExpressionEnv()
	want := compiler.ConditionEnvKeys()
	if len(env) != len(want) {
		t.Fatalf("校验环境键数 = %d, want %d(%v)", len(env), len(want), want)
	}
	for _, k := range want {
		if _, ok := env[k]; !ok {
			t.Errorf("校验环境缺少键 %q(运行期支持但校验期会误判)", k)
		}
	}
	for k := range env {
		found := false
		for _, w := range want {
			if k == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("校验环境出现运行期没有的键 %q", k)
		}
	}
	// 静态环境与运行期环境是同一份定义(不是两份拷贝)。
	runtimeEnv := compiler.NewConditionEnv(nil)
	for k := range runtimeEnv {
		if _, ok := env[k]; !ok {
			t.Errorf("运行期环境有键 %q 而校验期没有", k)
		}
	}
}

// TestDisabledNodeStillReportsUnknownType 禁用节点的类型仍必须是已注册类型。
//
// 节点类型是结构性事实(编译器要按类型降级它),与"这个节点跑不跑"无关。
func TestDisabledNodeStillReportsUnknownType(t *testing.T) {
	wf := mustWf(t, `
workflow:
  id: disabled-unknown
  name: Disabled Unknown
nodes:
  - id: a
    type: quantum
    enabled: false
`)
	res := Validate(wf, agentResolver(nil), skillResolver(nil))
	if !has(res, "UNKNOWN_NODE_TYPE") {
		t.Errorf("未注册的节点类型即使被禁用也应报 UNKNOWN_NODE_TYPE: %+v", res.Errors)
	}
}
