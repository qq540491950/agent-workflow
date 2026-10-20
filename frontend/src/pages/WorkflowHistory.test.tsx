// @vitest-environment jsdom
// WorkflowHistory:workflow.* 事件驱动的刷新必须 200ms 防抖(约定 7)。
//
// 未防抖时每条 workflow.* 事件都会触发三次全量拉取
// (工作流详情 + 执行列表 + 版本列表)。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { toast } from "sonner";
import WorkflowHistory from "./WorkflowHistory";
import { advance, emit, makeEvent, settle } from "@/test/helpers";
import { resetErrorDedup } from "@/lib/errors";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  handlers: [] as Array<(ev: import("@/lib/types").UIEvent) => void>,
  getWorkflow: vi.fn(),
  listExecutions: vi.fn(),
  workflowVersions: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getWorkflow: m.getWorkflow,
      listExecutions: m.listExecutions,
      workflowVersions: m.workflowVersions,
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
  resetErrorDedup();
  m.handlers = [];
  m.getWorkflow.mockReset().mockResolvedValue({
    id: "wf-1",
    name: "流程",
    version: 2,
    enabled: true,
    nodes: [],
    edges: [],
    settings: {},
  });
  m.listExecutions.mockReset().mockResolvedValue([]);
  m.workflowVersions.mockReset().mockResolvedValue([1, 2]);
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function renderHistory() {
  return render(
    <MemoryRouter initialEntries={["/workflows/wf-1/history"]}>
      <Routes>
        <Route path="/workflows/:id/history" element={<WorkflowHistory />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("WorkflowHistory 实时刷新防抖", () => {
  it("workflow.* 事件在 200ms 窗口内合并为一次刷新", async () => {
    renderHistory();
    await settle();
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 5; i++) emit(m.handlers, makeEvent("workflow.completed"));
    await advance(199);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
    expect(m.workflowVersions).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(m.listExecutions).toHaveBeenCalledTimes(2);
    expect(m.workflowVersions).toHaveBeenCalledTimes(2);
  });

  it("与工作流无关的事件不触发刷新", async () => {
    renderHistory();
    await settle();
    emit(m.handlers, makeEvent("agent.output"));
    await advance(500);
    expect(m.listExecutions).toHaveBeenCalledTimes(1);
  });
});

describe("WorkflowHistory 列表防御", () => {
  it("执行列表返回非数组时渲染空列表", async () => {
    m.listExecutions.mockResolvedValue({ error: "<html></html>" });
    m.workflowVersions.mockResolvedValue({ error: "<html></html>" });
    renderHistory();
    await settle();
    expect(screen.getByText("该工作流还没有执行记录")).toBeTruthy();
  });
});

describe("WorkflowHistory 失败必须可见", () => {
  // 同文件里此前口径不一致:listExecutions 那条链用了 toast.error,
  // 而 getWorkflow / workflowVersions 是 `.catch(() => {})` —— 加载失败
  // 时静默渲染空态,用户以为"这个工作流没有版本、没有执行记录"。
  it("工作流详情加载失败时弹出错误提示", async () => {
    m.getWorkflow.mockRejectedValue(new Error("后端不可用"));
    renderHistory();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载工作流详情");
  });

  it("版本列表加载失败时弹出错误提示", async () => {
    m.workflowVersions.mockRejectedValue(new Error("后端不可用"));
    renderHistory();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载版本列表");
  });

  it("执行列表加载失败时弹出错误提示(已有口径,加断言锁住)", async () => {
    m.listExecutions.mockRejectedValue(new Error("后端不可用"));
    renderHistory();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载执行记录");
  });
});
