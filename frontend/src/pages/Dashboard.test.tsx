// @vitest-environment jsdom
// Dashboard:workflow.* 事件驱动的刷新必须 200ms 防抖(约定 7)。
//
// 此前这里对每条 workflow.* 事件都直接触发两次全量拉取
// (执行列表 + 统计),完全没有防抖:多个工作流同时结束时事件成串到达,
// 请求数与事件数一一对应。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import Dashboard from "./Dashboard";
import { advance, emit, makeEvent, settle } from "@/test/helpers";

const m = vi.hoisted(() => ({
  handlers: [] as Array<(ev: import("@/lib/types").UIEvent) => void>,
  listWorkflows: vi.fn(),
  listExecutions: vi.fn(),
  stats: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listWorkflows: m.listWorkflows,
      listExecutions: m.listExecutions,
      stats: m.stats,
    },
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
  m.handlers = [];
  m.listWorkflows.mockReset().mockResolvedValue([]);
  m.listExecutions.mockReset().mockResolvedValue([]);
  m.stats.mockReset().mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function renderDashboard() {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

describe("Dashboard 实时刷新防抖", () => {
  it("初次挂载各拉一次", async () => {
    renderDashboard();
    await settle();
    expect(m.listWorkflows).toHaveBeenCalledTimes(1);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    expect(m.stats).toHaveBeenCalledTimes(1);
  });

  it("workflow.* 事件在 200ms 窗口内合并为一次刷新", async () => {
    renderDashboard();
    await settle();
    for (let i = 0; i < 5; i++) emit(m.handlers, makeEvent("workflow.completed"));
    await advance(199);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    expect(m.stats).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(m.listExecutions).toHaveBeenCalledTimes(2);
    expect(m.stats).toHaveBeenCalledTimes(2);
  });

  it("与工作流无关的事件不触发刷新", async () => {
    renderDashboard();
    await settle();
    emit(m.handlers, makeEvent("agent.output"));
    await advance(500);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    expect(m.stats).toHaveBeenCalledTimes(1);
  });
});

describe("Dashboard 列表防御", () => {
  it("列表接口返回非数组时不崩溃", async () => {
    m.listWorkflows.mockResolvedValue({ error: "<html></html>" });
    m.listExecutions.mockResolvedValue({ error: "<html></html>" });
    m.stats.mockResolvedValue({ error: "<html></html>" });
    renderDashboard();
    await settle();
    expect(screen.getByText("Dashboard")).toBeTruthy();
    expect(screen.getByText("暂无工作流")).toBeTruthy();
  });
});
