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
  toWorkflowModel,
} from "./Designer";
import type { Workflow } from "@/lib/types";

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

// 工作流级权限覆盖(后端 model.Workflow.Permission,JSON 键 "permission";
// DSL 里写作根级 permissions 段)。
//
// 设计器**没有**这个字段的编辑入口(UI 不呈现它),但服务端返回的工作流
// 里带着它 —— 而设计器的保存路径会**重建整个工作流对象**(只带固定字段
// 清单)。此前清单里没有 permission:用户在画布上改点别的再点保存,服务端
// 存的权限覆盖就被**静默抹掉**,而用户从头到尾没碰过它(约定 14:静默
// 丢弃 = 用户以为设置还在,实际已经没了)。
//
// 这里锁住:保存时原样带回 permission(不做任何改写)。
describe("工作流级权限覆盖不被保存丢掉", () => {
  const base: Workflow = {
    id: "wf-1",
    name: "W",
    description: "",
    version: 3,
    enabled: true,
    variables: {},
    nodes: [],
    edges: [],
    settings: { max_iterations: 5, on_loop_limit: "wait_user" },
  };

  it("保存时保留从服务端读到的 permission", () => {
    const permission = { "claude-code": { filesystem: { write: false } } };
    const model = toWorkflowModel({ ...base, permission }, [], []);
    expect(model.permission).toEqual(permission);
    // 真正发出去的 payload(序列化之后)必须带着这个字段
    const payload = JSON.parse(JSON.stringify(model)) as Workflow;
    expect(payload.permission).toEqual(permission);
  });

  it("没有权限覆盖时 payload 不带这个键", () => {
    const model = toWorkflowModel(base, [], []);
    expect(model.permission).toBeUndefined();
    expect(JSON.stringify(model)).not.toContain("permission");
  });
});

// 声明式运行入参(后端 model.Workflow.Inputs,JSON 键 "inputs";DSL 根级
// inputs 段)—— 与 permission 同口径:设计器不提供编辑入口,保存路径重建
// 整个工作流对象,漏掉这个字段等于把入参声明静默抹掉(迭代 36)。
describe("声明式入参不被保存丢掉", () => {
  const base: Workflow = {
    id: "wf-2",
    name: "W2",
    description: "",
    version: 1,
    enabled: true,
    variables: {},
    nodes: [],
    edges: [],
    settings: { max_iterations: 5, on_loop_limit: "wait_user" },
  };

  it("保存时保留从服务端读到的 inputs", () => {
    const inputs = [
      { name: "repo", type: "string", required: true, description: "目标仓库" },
      { name: "limit", type: "number", default: 3 },
    ];
    const model = toWorkflowModel({ ...base, inputs }, [], []);
    expect(model.inputs).toEqual(inputs);
    const payload = JSON.parse(JSON.stringify(model)) as Workflow;
    expect(payload.inputs).toEqual(inputs);
  });

  it("没有声明时 payload 不带这个键", () => {
    const model = toWorkflowModel(base, [], []);
    expect(model.inputs).toBeUndefined();
    expect(JSON.stringify(model)).not.toContain("inputs");
  });
});
