// @vitest-environment jsdom
// EventsAudit:实时事件流按 200ms 防抖刷新(约定 7)+ 列表防御。
//
// agent.output 等事件在一次执行里可达数十条,不防抖就是每条一次
// 全量 /api/events/all 拉取(limit=300)。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import EventsAudit from "./EventsAudit";
import { advance, emit, makeEvent, settle } from "@/test/helpers";

const m = vi.hoisted(() => ({
  handlers: [] as Array<(ev: import("@/lib/types").UIEvent) => void>,
  allEvents: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: { ...actual.api, allEvents: m.allEvents },
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
  m.allEvents.mockReset().mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function renderAudit() {
  return render(
    <MemoryRouter>
      <EventsAudit />
    </MemoryRouter>,
  );
}

describe("EventsAudit 实时刷新防抖", () => {
  it("初次挂载拉一次事件流", async () => {
    renderAudit();
    await settle();
    expect(m.allEvents).toHaveBeenCalledTimes(1);
    expect(m.allEvents).toHaveBeenCalledWith("?limit=300");
  });

  it("高频事件在 200ms 窗口内合并为一次刷新", async () => {
    renderAudit();
    await settle();
    for (let i = 0; i < 10; i++) emit(m.handlers, makeEvent("agent.output"));
    await advance(199);
    expect(m.allEvents).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(m.allEvents).toHaveBeenCalledTimes(2);
  });

  it("间隔超过 200ms 的事件各自触发一次刷新", async () => {
    renderAudit();
    await settle();
    emit(m.handlers, makeEvent("agent.output"));
    await advance(200);
    expect(m.allEvents).toHaveBeenCalledTimes(2);
    emit(m.handlers, makeEvent("agent.output"));
    await advance(200);
    expect(m.allEvents).toHaveBeenCalledTimes(3);
  });
});

describe("EventsAudit 列表防御", () => {
  it("空列表渲染占位文案", async () => {
    m.allEvents.mockResolvedValue([]);
    renderAudit();
    await settle();
    expect(screen.getByText("暂无事件")).toBeTruthy();
  });

  it("接口返回非数组(HTML 兜底页)时渲染空列表而不是崩溃", async () => {
    m.allEvents.mockResolvedValue({ error: "<html></html>" });
    renderAudit();
    await settle();
    expect(screen.getByText("暂无事件")).toBeTruthy();
  });

  it("按分类过滤时带上 type 前缀", async () => {
    renderAudit();
    await settle();
    fireEvent.click(screen.getByText("Workflow"));
    await settle();
    expect(m.allEvents).toHaveBeenCalledWith("?type=workflow.&limit=300");
  });
});
