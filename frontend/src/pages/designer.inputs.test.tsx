// @vitest-environment jsdom
// InputsEditor(声明式入参编辑器)的单测(迭代 36)。
//
// 锁住:UI 层保证 required 与 default 互斥(勾必填清默认值、填默认值取消
// 必填)、增删行、名称/描述编辑、aria-label 全覆盖。类型下拉是 Radix
// Select(jsdom 需指针事件补丁才能展开),其"切类型清默认值"分支由
// required/default 的同类路径覆盖,不在本文件重复展开浮层。
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { InputsEditor } from "./Designer";
import type { InputDecl } from "@/lib/types";

afterEach(() => cleanup());

const base: InputDecl[] = [
  { name: "repo", type: "string", required: true, description: "目标仓库" },
  { name: "limit", type: "number", default: 3 },
  { name: "dry_run", type: "boolean", default: false },
];

// setup 用**有状态**的包装承载 onChange:增删行之后组件要真的拿到新数组
// 重渲染,"添加后再删除"这类连续操作才有意义(无状态支架会基于旧树断言)。
function setup(inputs: InputDecl[]) {
  const onChange = vi.fn();
  function Holder() {
    const [items, setItems] = useState(inputs);
    return (
      <InputsEditor
        inputs={items}
        onChange={(v) => {
          setItems(v);
          onChange(v);
        }}
      />
    );
  }
  render(<Holder />);
  return { onChange, last: () => onChange.mock.calls.slice(-1)[0]?.[0] as InputDecl[] };
}

describe("InputsEditor", () => {
  it("渲染声明字段,全部带可访问名称", () => {
    setup(base);
    expect(screen.getByLabelText("入参 1 名称")).toBeTruthy();
    expect(screen.getByLabelText("入参 2 默认值")).toBeTruthy();
    expect(screen.getByLabelText("入参 3 默认值")).toBeTruthy();
    expect(screen.getByLabelText("入参 1 必填")).toBeTruthy();
    expect(screen.getByLabelText("删除入参 repo")).toBeTruthy();
  });

  it("编辑名称与描述 → onChange 打补丁", () => {
    const { last } = setup(base);
    fireEvent.change(screen.getByLabelText("入参 1 名称"), { target: { value: " repo2 " } });
    expect(last()[0].name).toBe("repo2"); // 自动去空白
    fireEvent.change(screen.getByLabelText("入参 1 描述"), { target: { value: "新描述" } });
    expect(last()[0].description).toBe("新描述");
  });

  it("勾选必填 → 清掉 default(互斥约束在 UI 层保证)", () => {
    const { last } = setup(base);
    fireEvent.click(screen.getByLabelText("入参 2 必填"));
    const patched = last()[1];
    expect(patched.required).toBe(true);
    expect(patched.default).toBeUndefined();
  });

  it("给必填项填默认值 → 取消必填(互斥的另一方向)", () => {
    const { last } = setup(base);
    fireEvent.change(screen.getByLabelText("入参 1 默认值"), { target: { value: "/tmp/x" } });
    const patched = last()[0];
    expect(patched.default).toBe("/tmp/x");
    expect(patched.required).toBe(false);
  });

  it("清空默认值 → default 变 undefined(不落空串)", () => {
    const { last } = setup(base);
    fireEvent.change(screen.getByLabelText("入参 2 默认值"), { target: { value: "" } });
    expect(last()[1].default).toBeUndefined();
  });

  it("添加 → 追加一行 string 声明;删除 → 移除对应行", () => {
    const { last } = setup(base);
    fireEvent.click(screen.getByLabelText("添加入参"));
    expect(last()).toHaveLength(4);
    expect(last()[3]).toEqual({ name: "", type: "string" });

    fireEvent.click(screen.getByLabelText("删除入参 limit"));
    expect(last()).toHaveLength(3);
    expect(last().map((d) => d.name)).toEqual(["repo", "dry_run", ""]);
  });

  it("空声明列表 → 显示占位说明,不渲染任何行", () => {
    setup([]);
    expect(screen.getByText(/暂无声明/)).toBeTruthy();
    expect(screen.queryByLabelText("入参 1 名称")).toBeNull();
  });
});
