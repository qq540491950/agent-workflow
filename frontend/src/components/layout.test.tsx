// @vitest-environment jsdom
// Layout:侧栏"运行中执行数"的实时刷新必须 200ms 防抖(约定 7)。
//
// 该约定此前只靠人工维持:每个组件各自内联一份 setTimeout,窗口期并不一致,
// 也没有任何测试锁住。node.* 是高频事件,一旦防抖失效,每条事件都会
// 触发一次全量执行列表拉取。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { toast } from "sonner";
import Layout from "./layout";
import { advance, emit, makeEvent, settle } from "@/test/helpers";
import { resetErrorDedup } from "@/lib/errors";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  handlers: [] as Array<(ev: import("@/lib/types").UIEvent) => void>,
  listExecutions: vi.fn(),
  detectMode: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: { ...actual.api, listExecutions: m.listExecutions },
    detectMode: m.detectMode,
    subscribeEvents: vi.fn((h: (ev: import("@/lib/types").UIEvent) => void) => {
      m.handlers.push(h);
      return () => {
        m.handlers = m.handlers.filter((x) => x !== h);
      };
    }),
  };
});

beforeEach(() => {
  vi.useFakeTimers();
  resetErrorDedup();
  m.handlers = [];
  m.listExecutions.mockReset();
  m.listExecutions.mockResolvedValue([]);
  m.detectMode.mockResolvedValue("web");
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function renderLayout() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="/" element={<Layout />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Layout 实时刷新防抖", () => {
  it("初次挂载只拉一次执行列表", async () => {
    renderLayout();
    await settle();
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    expect(m.listExecutions).toHaveBeenCalledWith("", 50);
  });

  it("高频事件在 200ms 窗口内合并为一次刷新", async () => {
    renderLayout();
    await settle();
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 5; i++) emit(m.handlers, makeEvent("node.started"));
    await advance(199);
    // 窗口未到:一条都不该触发全量拉取
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    await advance(1);
    // 5 条事件合并成一次
    expect(m.listExecutions).toHaveBeenCalledTimes(2);
  });

  it("间隔超过 200ms 的事件各自触发一次刷新", async () => {
    renderLayout();
    await settle();
    emit(m.handlers, makeEvent("node.completed"));
    await advance(200);
    expect(m.listExecutions).toHaveBeenCalledTimes(2);
    emit(m.handlers, makeEvent("node.completed"));
    await advance(200);
    expect(m.listExecutions).toHaveBeenCalledTimes(3);
  });

  it("与执行状态无关的事件不触发刷新", async () => {
    renderLayout();
    await settle();
    emit(m.handlers, makeEvent("skill.executed"));
    await advance(500);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
  });
});

describe("Layout 列表防御", () => {
  it("运行中计数只统计 RUNNING 与 WAITING_USER", async () => {
    m.listExecutions.mockResolvedValue([
      { id: "e1", state: "RUNNING" },
      { id: "e2", state: "WAITING_USER" },
      { id: "e3", state: "COMPLETED" },
    ]);
    renderLayout();
    await settle();
    expect(screen.getByText("2")).toBeTruthy();
  });

  it("列表接口返回非数组(HTML 兜底页)时不崩溃且不显示计数", async () => {
    m.listExecutions.mockResolvedValue({ error: "<html></html>" });
    renderLayout();
    await settle();
    expect(screen.getByText("Dashboard")).toBeTruthy();
    expect(screen.queryByText("2")).toBeNull();
  });
});

describe("Layout 失败必须可见", () => {
  it("执行列表请求失败时弹出错误提示(而不是静默停在 0)", async () => {
    m.listExecutions.mockRejectedValue(new Error("后端不可用"));
    renderLayout();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载运行中执行数");
  });

  it("事件反复触发刷新失败不会变成提示风暴", async () => {
    renderLayout(); // 挂载时一切正常
    await settle();
    vi.mocked(toast.error).mockClear();
    // 后端开始出问题,而事件仍在不断到达(Layout 挂在所有页面上)
    m.listExecutions.mockRejectedValue(new Error("后端不可用"));
    for (let i = 0; i < 5; i++) {
      emit(m.handlers, makeEvent("node.started"));
      await advance(200);
    }
    expect(toast.error).toHaveBeenCalledTimes(1);
  });
});
