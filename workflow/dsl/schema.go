package dsl

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"agentworkflow/logx"
	"agentworkflow/workflow/model"
)

// 本文件实现 DSL 的"键集合校验"(迭代 15)。
//
// 背景:解析器此前对**闭合结构**里的未知键是非严格的 —— 键被直接丢掉,
// 用户以为设置生效了,实际没有:
//
//	workflow:
//	  id: x
//	  permissions:      ← 权限收紧写错层级 → 整段被丢弃 → 权限没收紧
//
// 顶层键此前只有 YAML 路径做白名单检查,JSON 路径**完全没查** —— 同一份
// DSL 换种格式导入行为就不同(违反约定 13 的"同源"精神)。
//
// 策略(混合,见交付报告):
//
//   - 顶层未知键 → 报错 DSL_UNKNOWN_KEY(YAML/JSON 同源);
//   - permissions 写错层级 → 报错 DSL_MISPLACED_KEY(权限收紧无声失效
//     = 安全语义被悄悄放开,必须 fail closed);
//   - 其余闭合结构里的未知键 → 忽略但**可观测**:记入 Document.IgnoredKeys
//     + WARN 日志。不阻断导入 —— 存量 DSL 里确实存在把 settings 写在
//     workflow: 段下的写法(仓库自己的测试夹具就有),直接报错会让这些
//     工作流导不进来。
//
// 节点 config 与 variables/permissions 的**内容**不在这里校验:
// 节点配置契约的唯一来源是 workflow/compiler/nodeconfig.go(约定 13),
// 由校验期负责;variables/permissions 是开放的自由映射。

// misplacedSection 是"放错层级的顶层段"在解析期给出的专属错误码。
const misplacedSection = "DSL_MISPLACED_KEY"

// sectionPath 描述闭合结构里的一个键路径(用于 IgnoredKeys 与日志)。
func sectionPath(parts ...string) string { return strings.Join(parts, ".") }

// structKeys 返回结构体在给定标签下的键集合(唯一来源 = 结构体标签本身)。
//
// 用反射从结构体派生,而不是手写白名单:手写清单会与结构体漂移
// —— 加了字段忘了改白名单,新字段就会被当成未知键丢掉(约定 13 的同源要求)。
// 标签为 "-" 或 inline(无名)的字段不计入。
func structKeys(t reflect.Type, tagName string) map[string]bool {
	out := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // 非导出字段
			continue
		}
		tag := f.Tag.Get(tagName)
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			continue // inline(节点 config)—— 开放结构,不校验
		}
		out[name] = true
	}
	return out
}

// keySets 是一组闭合结构的合法键集合。
type keySets struct {
	top      map[string]bool
	workflow map[string]bool
	settings map[string]bool
	edge     map[string]bool
}

var (
	yamlKeys = keySets{
		top:      structKeys(reflect.TypeOf(Document{}), "yaml"),
		workflow: structKeys(reflect.TypeOf(WorkflowMeta{}), "yaml"),
		settings: structKeys(reflect.TypeOf(Settings{}), "yaml"),
		edge:     structKeys(reflect.TypeOf(Edge{}), "yaml"),
	}
	jsonKeys = keySets{
		top:      structKeys(reflect.TypeOf(Document{}), "json"),
		workflow: structKeys(reflect.TypeOf(WorkflowMeta{}), "json"),
		settings: structKeys(reflect.TypeOf(Settings{}), "json"),
		edge:     structKeys(reflect.TypeOf(Edge{}), "json"),
	}
)

// allowedTopKeys 返回顶层合法键(排序后),用于错误信息。
func (k keySets) allowedTopKeys() []string {
	out := make([]string, 0, len(k.top))
	for name := range k.top {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// checkDocumentKeys 校验文档的键结构。
//
// raw 是把同一份文本解成通用树(map[string]any)后的结果:顶层键、workflow
// 段、settings 段与每条边的键集合都是封闭的,里面的未知键会被解析器丢掉。
//
// 返回被忽略的键路径(带 WARN 日志);遇到"顶层未知键"或"permissions 放错
// 层级"时返回错误(导入直接失败,不留下"看起来成功其实没生效"的工作流)。
func checkDocumentKeys(doc *Document, raw map[string]any, keys keySets) error {
	if raw == nil {
		return nil
	}
	// 1) 顶层:未知键 → 报错(YAML/JSON 同源)。
	for name := range raw {
		if keys.top[name] {
			continue
		}
		return model.NewError(model.KindValidationError, "DSL_UNKNOWN_KEY",
			fmt.Sprintf("未知的顶层字段 %q (允许: %s)", name, strings.Join(keys.allowedTopKeys(), ", ")))
	}
	// 2) workflow 段 / settings 段:未知键 → 忽略但记录;permissions 放错
	//    层级 → 报错(权限收紧无声失效,必须让用户看见)。
	ignored, err := checkSectionKeys(raw, "workflow", keys.workflow, keys)
	if err != nil {
		return err
	}
	ignored2, err := checkSectionKeys(raw, "settings", keys.settings, keys)
	if err != nil {
		return err
	}
	ignored = append(ignored, ignored2...)
	// 3) 每条边:键集合封闭(from/to/condition),未知键同样丢弃。
	ignored3, err := checkEdgeKeys(raw, keys.edge)
	if err != nil {
		return err
	}
	ignored = append(ignored, ignored3...)

	sort.Strings(ignored)
	doc.IgnoredKeys = ignored
	for _, key := range ignored {
		logx.Warn("DSL 存在未知键,已忽略(该设置不会生效)", "key", key)
	}
	return nil
}

// checkSectionKeys 校验单个闭合段(workflow/settings)的键。
func checkSectionKeys(raw map[string]any, section string, allowed map[string]bool, keys keySets) ([]string, error) {
	sectionMap, ok := raw[section].(map[string]any)
	if !ok {
		return nil, nil
	}
	var ignored []string
	for name := range sectionMap {
		if allowed[name] {
			continue
		}
		// 顶层段名出现在段内 = 放错层级。
		if keys.top[name] {
			if name == "permissions" {
				return nil, model.NewError(model.KindValidationError, misplacedSection,
					fmt.Sprintf("%s 必须写在文档根级(与 workflow 同级),不能写在 %s 段下 —— 写错层级会让权限收紧被静默丢弃", name, section))
			}
			ignored = append(ignored, sectionPath(section, name))
			continue
		}
		ignored = append(ignored, sectionPath(section, name))
	}
	return ignored, nil
}

// checkEdgeKeys 校验每条边的键集合。
func checkEdgeKeys(raw map[string]any, allowed map[string]bool) ([]string, error) {
	edges, ok := raw["edges"].([]any)
	if !ok {
		return nil, nil
	}
	var ignored []string
	for i, item := range edges {
		edgeMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for name := range edgeMap {
			if allowed[name] {
				continue
			}
			ignored = append(ignored, sectionPath("edges", fmt.Sprintf("%d", i), name))
		}
	}
	return ignored, nil
}

// decodeRawYAML 把 YAML 文本解成通用树(用于键校验)。
func decodeRawYAML(data []byte) (map[string]any, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// decodeRawJSON 把 JSON 文本解成通用树(用于键校验)。
func decodeRawJSON(data []byte) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
