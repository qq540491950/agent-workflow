// @vitest-environment jsdom
// ExecutionMonitor:实时刷新必须 200ms 防抖(约定 7)+ 状态徽章 + 列表防御。
//
// 防抖窗口此前是 120ms(与 Layout/EventsAudit 的 200ms 不一致),没有任何测试
// 锁住,于是"统一 200ms"这条约定只能靠人工维持。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import ExecutionMonitor from "./ExecutionMonitor";
import { advance, emit, makeEvent, settle } from "@/test/helpers";

const m = vi.hoisted(() => ({
  handlers: [] as Array<(ev: import("@/lib/types").UIEvent) => void>,
  getExecution: vi.fn(),
  executionNodes: vi.fn(),
  executionEvents: vi.fn(),
  executionArtifacts: vi.fn(),
  provideInput: vi.fn(),
  cancelExecution: vi.fn(),
  retryExecution: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getExecution: m.getExecution,
      executionNodes: m.executionNodes,
      executionEvents: m.executionEvents,
      executionArtifacts: m.executionArtifacts,
      provideInput: m.provideInput,
      cancelExecution: m.cancelExecution,
      retryExecution: m.retryExecution,
    },
    subscribeEvents: vi.fn((h: (ev: import("@/lib/types").UIEvent) => void) => {
      m.handlers.push(h);
      return () => {
        m.handlers = m.handlers.filter((x) => x !== h);
      };
    }),
  };
});

function exec(over: Record<string, unknown> = {}) {
  return {
    id: "ex-1",
    workflow_id: "wf-1",
    workflow_version: 1,
    workflow_name: "评审流程",
    state: "RUNNING",
    task: "评审一下",
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  m.handlers = [];
  m.getExecution.mockReset().mockResolvedValue(exec());
  m.executionNodes.mockReset().mockResolvedValue([]);
  m.executionEvents.mockReset().mockResolvedValue([]);
  m.executionArtifacts.mockReset().mockResolvedValue([]);
  m.provideInput.mockReset().mockResolvedValue(exec());
  m.cancelExecution.mockReset().mockResolvedValue(undefined);
  m.retryExecution.mockReset().mockResolvedValue(exec());
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function renderMonitor() {
  return render(
    <MemoryRouter initialEntries={["/executions/ex-1"]}>
      <Routes>
        <Route path="/executions/:id" element={<ExecutionMonitor />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ExecutionMonitor 实时刷新防抖", () => {
  it("SSE 触发的刷新按 200ms 防抖(120ms 窗口会提前触发)", async () => {
    renderMonitor();
    await settle();
    expect(m.getExecution).toHaveBeenCalledTimes(1);
    emit(m.handlers, makeEvent("node.completed", "ex-1"));
    await advance(199);
    // 窗口未到:不得刷新
    expect(m.getExecution).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(m.getExecution).toHaveBeenCalledTimes(2);
  });

  it("连续高频事件合并为一次刷新", async () => {
    renderMonitor();
    await settle();
    for (let i = 0; i < 8; i++) emit(m.handlers, makeEvent("node.started", "ex-1"));
    await advance(200);
    expect(m.getExecution).toHaveBeenCalledTimes(2);
  });

  it("其它执行的事件不触发刷新", async () => {
    renderMonitor();
    await settle();
    emit(m.handlers, makeEvent("node.completed", "ex-other"));
    await advance(500);
    expect(m.getExecution).toHaveBeenCalledTimes(1);
  });
});

describe("ExecutionMonitor 状态徽章", () => {
  it.each([
    ["RUNNING", "Running"],
    ["WAITING_USER", "Waiting User"],
    ["COMPLETED", "Completed"],
    ["FAILED", "Failed"],
    ["CANCELLED", "Cancelled"],
  ])("执行状态 %s 渲染为 %s", async (state, label) => {
    m.getExecution.mockResolvedValue(exec({ state }));
    renderMonitor();
    await settle();
    expect(screen.getByText(label)).toBeTruthy();
  });
});

describe("ExecutionMonitor 列表防御", () => {
  it("节点/事件/制品接口返回非数组时不白屏", async () => {
    m.executionNodes.mockResolvedValue({ error: "<html></html>" });
    m.executionEvents.mockResolvedValue({ error: "<html></html>" });
    m.executionArtifacts.mockResolvedValue({ error: "<html></html>" });
    renderMonitor();
    await settle();
    expect(screen.getByText("等待节点执行…")).toBeTruthy();
    expect(screen.getByText("暂无日志")).toBeTruthy();
  });

  it("事件负载字段缺失时仍能渲染(不抛 TypeError)", async () => {
    m.executionEvents.mockResolvedValue([{ type: "node.started" }]);
    renderMonitor();
    await settle();
    expect(screen.getByText("node.started")).toBeTruthy();
  });
});
