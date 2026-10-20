// Designer 的纯逻辑单测:节点 ID 生成必须避开画布上已有的 ID。
// 回归背景:此前用组件内自增计数器(useRef(1)),且从不扫描已 hydrate 的
// 节点 —— 打开一个含 agent-1 的工作流再拖入 agent 节点,必然产生重复 ID,
// 保存时被后端 validator 以 DUPLICATE_NODE 拒绝,界面无法解释原因。
import { describe, expect, it } from "vitest";
import type { Node as RFNode } from "@xyflow/react";
import {
  nextNodeId,
  nodeEnabledInvalid,
  defaultConfigForType,
  legacyConditionExpression,
} from "./Designer";

const nodes = (...ids: string[]): RFNode[] =>
  ids.map((id) => ({ id, type: "wf", position: { x: 0, y: 0 }, data: {} }));

describe("nextNodeId", () => {
  it("空画布从 1 开始", () => {
    expect(nextNodeId([], "agent")).toBe("agent-1");
  });

  it("跳过已占用的 ID(打开已有工作流后再拖节点)", () => {
    expect(nextNodeId(nodes("agent-1"), "agent")).toBe("agent-2");
    expect(nextNodeId(nodes("agent-1", "agent-2"), "agent")).toBe("agent-3");
    // 中间有空洞时取最小可用值
    expect(nextNodeId(nodes("agent-1", "agent-3"), "agent")).toBe("agent-2");
  });

  it("不同类型各自独立计数", () => {
    const ns = nodes("agent-1", "agent-2", "human-1");
    expect(nextNodeId(ns, "agent")).toBe("agent-3");
    expect(nextNodeId(ns, "human")).toBe("human-2");
    expect(nextNodeId(ns, "git")).toBe("git-1");
  });

  it("对非数字后缀的既有 ID 也能正确让位", () => {
    expect(nextNodeId(nodes("agent", "agent-1"), "agent")).toBe("agent-2");
  });
});

// 节点级开关 enabled:后端 validator 的 INVALID_NODE_ENABLED 只接受布尔。
// 前端属性面板的开关只会写出布尔,但导入/恢复的定义可能带非布尔值 ——
// 属性面板据此给出提示,免得用户被自己的校验器挡住。
describe("nodeEnabledInvalid", () => {
  it("缺省与布尔值都合法", () => {
    expect(nodeEnabledInvalid({})).toBe(false);
    expect(nodeEnabledInvalid({ enabled: true })).toBe(false);
    expect(nodeEnabledInvalid({ enabled: false })).toBe(false);
  });

  it("非布尔值(字符串/数字/null)判非法", () => {
    expect(nodeEnabledInvalid({ enabled: "false" })).toBe(true);
    expect(nodeEnabledInvalid({ enabled: 0 })).toBe(true);
    expect(nodeEnabledInvalid({ enabled: null })).toBe(true);
  });
});

// condition 节点的 config.expression 是**死字段**:路由条件来自边
// (edge.condition),运行期与编译器都不读节点上的表达式
// (workflow/runtime/executor.go 的 dispatchNode 把 condition 节点当作
// 无副作用的直通节点)。
//
// 旧版 UI 却预填了一个表达式、还渲染成可编辑输入框 —— 用户填了完全无效,
// 却以为路由由它驱动;更糟的是校验期还对它做语法检查,填错一个**没有作用**
// 的字段就保存不了工作流(约定 13 的反例)。
//
// 这里锁住:新建 condition 节点不再预填 expression;遗留工作流里的
// expression 只作为提示出现,不再有可编辑输入框。
describe("condition 节点的废弃 expression", () => {
  it("新建节点不预填 expression", () => {
    const cfg = defaultConfigForType("condition");
    expect(cfg).not.toHaveProperty("expression");
    expect(Object.keys(cfg)).toHaveLength(0);
  });

  it("其它节点类型的默认配置不受影响", () => {
    expect(defaultConfigForType("agent")).toEqual({ agent: "mock-claude", mode: "plan" });
    expect(defaultConfigForType("git")).toEqual({ operation: "diff" });
  });

  it("遗留的 expression 能被识别(用于提示),非字符串视为没有", () => {
    expect(legacyConditionExpression({ expression: 'decision == "APPROVED"' })).toBe(
      'decision == "APPROVED"',
    );
    expect(legacyConditionExpression({})).toBe("");
    expect(legacyConditionExpression({ expression: 42 })).toBe("");
    expect(legacyConditionExpression({ expression: "" })).toBe("");
  });
});
