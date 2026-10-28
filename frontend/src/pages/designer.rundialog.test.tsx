// @vitest-environment jsdom
// RunDialog(运行对话框)的单测:声明式入参的类型化字段(迭代 36)。
//
// 锁住四条行为:
//  1. inputs 声明渲染成类型化字段,且都有可访问名称(aria-label,
//     走查 #8 的教训:读屏不能只念"按钮");
//  2. default 预填(数字/字符串),布尔缺省 false;
//  3. 启动时字段值合并进 variables,且**覆盖** JSON 文本框里的同名键
//     (字段是声明入参的主控件);数字字段转不出数值时原样传字符串,
//     让后端报可读的 INPUT_TYPE_MISMATCH;
//  4. 变量文本框不是合法 JSON → 不启动。
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RunDialog } from "./Designer";
import type { Workflow } from "@/lib/types";

beforeAll(() => {
  const proto = Element.prototype as unknown as Record<string, unknown>;
  proto.scrollIntoView ??= () => {};
  if (!("ResizeObserver" in window)) {
    (window as unknown as Record<string, unknown>).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const wfWithInputs: Workflow = {
  id: "wf-run-dialog",
  name: "Run Dialog",
  description: "",
  version: 1,
  enabled: true,
  variables: { staticVar: "s" },
  inputs: [
    { name: "repo", type: "string", required: true, description: "目标仓库" },
    { name: "limit", type: "number", default: 3 },
    { name: "dry_run", type: "boolean", default: false },
  ],
  nodes: [],
  edges: [],
  settings: { max_iterations: 5, on_loop_limit: "wait_user" },
};

function renderDialog(overrides?: Partial<Workflow>) {
  const onLaunch = vi.fn().mockResolvedValue(undefined);
  const onOpenChange = vi.fn();
  render(
    <RunDialog
      open
      onOpenChange={onOpenChange}
      wf={{ ...wfWithInputs, ...overrides }}
      onLaunch={onLaunch}
    />,
  );
  return { onLaunch, onOpenChange };
}

describe("RunDialog 声明式入参", () => {
  it("声明渲染成类型化字段,且都有可访问名称", () => {
    renderDialog();
    expect(screen.getByLabelText("入参 repo")).toBeTruthy();
    expect(screen.getByLabelText("入参 limit")).toBeTruthy();
    expect(screen.getByLabelText("入参 dry_run")).toBeTruthy();
    // 布尔是开关(role=switch),不是文本框
    expect(screen.getByLabelText("入参 dry_run").getAttribute("role")).toBe("switch");
  });

  it("default 预填;布尔缺省 false;必填项留空", () => {
    renderDialog();
    expect((screen.getByLabelText("入参 limit") as HTMLInputElement).value).toBe("3");
    expect((screen.getByLabelText("入参 dry_run").getAttribute("data-state") ?? "")).toBe("unchecked");
    expect((screen.getByLabelText("入参 repo") as HTMLInputElement).value).toBe("");
  });

  it("启动时字段值合并进 variables,覆盖 JSON 里的同名键", async () => {
    const { onLaunch } = renderDialog();
    // 用户在 JSON 文本框里也写了同名键 → 字段优先
    fireEvent.change(screen.getByPlaceholderText(/未声明入参的变量/), {
      target: { value: JSON.stringify({ repo: "from-json", limit: 99, extra: 1 }) },
    });
    fireEvent.change(screen.getByLabelText("入参 repo"), { target: { value: "/tmp/repo" } });
    fireEvent.change(screen.getByLabelText("入参 limit"), { target: { value: "7" } });
    fireEvent.click(screen.getByLabelText("入参 dry_run"));

    fireEvent.click(screen.getByText("启动执行"));
    await waitFor(() => expect(onLaunch).toHaveBeenCalledTimes(1));
    const [task, variables] = onLaunch.mock.calls[0];
    expect(task).toBe("默认任务");
    expect(variables).toEqual({ repo: "/tmp/repo", limit: 7, dry_run: true, extra: 1 });
  });

  it("数字字段输入非数值 → 浏览器清洗成空串,不上送该键(交给默认值/必填校验)", async () => {
    // <input type="number"> 在真实浏览器与 jsdom 里都会把非法文本清洗成
    // ""(value sanitization)→ 字段视为未填,该入参不进 variables:
    // 引擎按 default 兜底,必填则报 INPUT_REQUIRED。
    const { onLaunch } = renderDialog();
    fireEvent.change(screen.getByLabelText("入参 limit"), { target: { value: "abc" } });
    fireEvent.click(screen.getByText("启动执行"));
    await waitFor(() => expect(onLaunch).toHaveBeenCalledTimes(1));
    expect(onLaunch.mock.calls[0][1]).toEqual({ staticVar: "s", dry_run: false });
  });

  it("onLaunch 失败 → 按钮恢复可点(错误上报在页面层,对话框不卡死)", async () => {
    const onLaunch = vi.fn().mockRejectedValue(new Error("INPUT_REQUIRED"));
    const onOpenChange = vi.fn();
    render(
      <RunDialog open onOpenChange={onOpenChange} wf={wfWithInputs} onLaunch={onLaunch} />,
    );
    fireEvent.change(screen.getByLabelText("入参 repo"), { target: { value: "/tmp/x" } });
    const launchBtn = () => screen.getByText("启动执行") as HTMLButtonElement;
    fireEvent.click(launchBtn());
    // 请求在途:按钮禁用(本仓库未装 jest-dom,用 disabled 属性断言)
    await waitFor(() => expect(launchBtn().disabled).toBe(true));
    // 失败后:按钮恢复(浏览器走查实测:此前 onLaunch 抛错无人接,
    // 既无提示也留白 —— 错误上报是页面层 onLaunch 的职责)
    await waitFor(() => expect(launchBtn().disabled).toBe(false));
    // 对话框保持打开(用户可修正入参重试)
    expect(screen.getByText("运行工作流")).toBeTruthy();
  });

  it("变量文本框不是合法 JSON → 不启动", () => {
    const { onLaunch } = renderDialog();
    fireEvent.change(screen.getByPlaceholderText(/未声明入参的变量/), { target: { value: "{bad" } });
    fireEvent.click(screen.getByText("启动执行"));
    expect(onLaunch).not.toHaveBeenCalled();
  });

  it("没有 inputs 声明时不渲染入参区(向后兼容)", () => {
    renderDialog({ inputs: undefined });
    expect(screen.queryByText("运行入参(工作流声明)")).toBeNull();
  });
});
