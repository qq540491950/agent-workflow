// Designer 的纯逻辑单测:节点 ID 生成必须避开画布上已有的 ID。
// 回归背景:此前用组件内自增计数器(useRef(1)),且从不扫描已 hydrate 的
// 节点 —— 打开一个含 agent-1 的工作流再拖入 agent 节点,必然产生重复 ID,
// 保存时被后端 validator 以 DUPLICATE_NODE 拒绝,界面无法解释原因。
import { describe, expect, it } from "vitest";
import type { Node as RFNode } from "@xyflow/react";
import { nextNodeId, nodeEnabledInvalid } from "./Designer";

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
