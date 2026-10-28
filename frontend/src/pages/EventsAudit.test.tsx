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

// 第二轮 P2-3:请求竞态。
//
// 旧实现的 refresh 依赖 filter → 输入一变 effect 就重跑 → 每次按键发一次
// 请求,且旧前缀的迟到响应会覆盖新前缀的结果(输入框是 "node.",列表却是
// "n" 的结果)。
describe("EventsAudit 请求竞态", () => {
  function ev(seq: number, type: string) {
    return {
      seq,
      execution_id: "ex-1",
      node_id: "n1",
      type,
      data: {},
      created_at: "2026-01-01T00:00:00Z",
    };
  }

  it("旧请求的迟到响应不得覆盖新前缀的结果", async () => {
    // 第一个请求(初始加载)挂起,第二个(按分类过滤)先回来。
    let resolveFirst: (v: unknown) => void = () => {};
    const first = new Promise((r) => {
      resolveFirst = r;
    });
    m.allEvents
      .mockReturnValueOnce(first as never)
      .mockResolvedValueOnce([ev(2, "workflow.started")]);

    renderAudit();
    await settle();
    fireEvent.click(screen.getByText("Workflow"));
    await settle();
    // 新前缀的结果已经渲染。
    expect(screen.getByText("workflow.started")).toBeTruthy();

    // 旧请求现在才回来 —— 它必须被丢弃,不得覆盖。
    resolveFirst([ev(1, "node.started")]);
    await settle();
    expect(screen.queryByText("node.started")).toBeNull();
    expect(screen.getByText("workflow.started")).toBeTruthy();
  });

  it("输入框每次按键不得发请求(回车才提交)", async () => {
    renderAudit();
    await settle();
    expect(m.allEvents).toHaveBeenCalledTimes(1);
    const input = screen.getByPlaceholderText("自定义前缀(回车应用)…");
    fireEvent.change(input, { target: { value: "node." } });
    fireEvent.change(input, { target: { value: "node.x" } });
    await settle();
    // 两次按键都不得发请求。
    expect(m.allEvents).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(input, { key: "Enter" });
    await settle();
    expect(m.allEvents).toHaveBeenCalledTimes(2);
    expect(m.allEvents).toHaveBeenLastCalledWith("?type=node.x&limit=300");
  });
});

// 迭代 36:node.retry(节点级重试)在审计流里有专属着色("正在自愈"的信号,
// 与 failed 的红、workflow. 的紫区分开)。
describe("EventsAudit 重试事件着色", () => {
  function row(seq: number, type: string) {
    return {
      seq,
      execution_id: "ex-1",
      node_id: "n1",
      type,
      data: {},
      created_at: "2026-01-01T00:00:00Z",
    };
  }

  it("node.retry 与 agent.retry 都带 retry 着色类", async () => {
    m.allEvents.mockResolvedValue([row(1, "node.retry"), row(2, "agent.retry"), row(3, "node.failed")]);
    renderAudit();
    await settle();
    const spans = Array.from(document.querySelectorAll("span")).filter(
      (s) => s.textContent === "node.retry" || s.textContent === "agent.retry",
    );
    expect(spans.length).toBe(2);
    for (const sp of spans) {
      expect(sp.className).toContain("text-cyan-300");
    }
    const failed = Array.from(document.querySelectorAll("span")).find(
      (s) => s.textContent === "node.failed",
    );
    expect(failed?.className).not.toContain("text-cyan-300");
    expect(failed?.className).toContain("text-red-400");
  });
});
