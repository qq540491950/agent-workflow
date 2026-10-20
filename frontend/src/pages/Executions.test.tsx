// @vitest-environment jsdom
// Executions:实时刷新防抖(约定 7)+ 删除记录的二次确认。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import Executions from "./Executions";
import { advance, emit, makeEvent, settle } from "@/test/helpers";

const m = vi.hoisted(() => ({
  handlers: [] as Array<(ev: import("@/lib/types").UIEvent) => void>,
  listExecutions: vi.fn(),
  deleteExecution: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listExecutions: m.listExecutions,
      deleteExecution: m.deleteExecution,
    },
    subscribeEvents: vi.fn((h: (ev: import("@/lib/types").UIEvent) => void) => {
      m.handlers.push(h);
      return () => {
        m.handlers = m.handlers.filter((x) => x !== h);
      };
    }),
  };
});

const exec = {
  id: "ex-1",
  workflow_id: "wf-1",
  workflow_version: 1,
  workflow_name: "流程",
  state: "COMPLETED",
  task: "任务",
  created_at: "2026-01-01T00:00:00Z",
};

beforeEach(() => {
  vi.useFakeTimers();
  m.handlers = [];
  m.listExecutions.mockReset().mockResolvedValue([exec]);
  m.deleteExecution.mockReset().mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function renderExecutions() {
  return render(
    <MemoryRouter>
      <Executions />
    </MemoryRouter>,
  );
}

describe("Executions 实时刷新防抖", () => {
  it("workflow.* 事件在 200ms 窗口内合并为一次刷新", async () => {
    renderExecutions();
    await settle();
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 5; i++) emit(m.handlers, makeEvent("workflow.completed"));
    await advance(199);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(m.listExecutions).toHaveBeenCalledTimes(2);
  });

  it("与工作流无关的事件不触发刷新", async () => {
    renderExecutions();
    await settle();
    emit(m.handlers, makeEvent("agent.output"));
    await advance(500);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
  });
});

describe("Executions 删除二次确认", () => {
  it("用户取消确认时不删除", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    renderExecutions();
    await settle();
    fireEvent.click(screen.getByTitle("删除记录"));
    await settle();
    expect(confirmSpy).toHaveBeenCalled();
    expect(m.deleteExecution).not.toHaveBeenCalled();
  });

  it("确认后删除并刷新列表", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderExecutions();
    await settle();
    fireEvent.click(screen.getByTitle("删除记录"));
    await settle();
    expect(m.deleteExecution).toHaveBeenCalledWith("ex-1");
    expect(m.listExecutions).toHaveBeenCalledTimes(2);
  });
});

describe("Executions 列表防御", () => {
  it("列表接口返回非数组时渲染空列表", async () => {
    m.listExecutions.mockResolvedValue({ error: "<html></html>" });
    renderExecutions();
    await settle();
    expect(screen.getByText("暂无执行")).toBeTruthy();
  });
});
