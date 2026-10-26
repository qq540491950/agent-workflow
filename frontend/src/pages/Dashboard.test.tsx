// @vitest-environment jsdom
// Dashboard:workflow.* 事件驱动的刷新必须 200ms 防抖(约定 7)。
//
// 此前这里对每条 workflow.* 事件都直接触发两次全量拉取
// (执行列表 + 统计),完全没有防抖:多个工作流同时结束时事件成串到达,
// 请求数与事件数一一对应。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { toast } from "sonner";
import Dashboard from "./Dashboard";
import { advance, emit, makeEvent, settle } from "@/test/helpers";
import { resetErrorDedup } from "@/lib/errors";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

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
  resetErrorDedup();
  m.handlers = [];
  m.listWorkflows.mockReset().mockResolvedValue([]);
  m.listExecutions.mockReset().mockResolvedValue([]);
  m.stats.mockReset().mockResolvedValue([]);
  vi.mocked(toast.error).mockClear();
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

  // "接口返回了非数组"与"接口请求失败"是**两种**失败,处理方式不同:
  //   - 返回非数组:请求本身成功了,只是响应体不是预期的数组(asArray 的
  //     防御路径)。静默降级为空列表,不弹提示 —— 这是**刻意**的选择:
  //     asArray 是被二十来处共用的纯函数,在里面弹提示等于给每个调用点
  //     都加副作用。
  //   - 请求失败(reject):后端不可用/500/超时,用户必须知道 —— 此前是
  //     `.catch(() => {})`,Dashboard 渲染「暂无工作流」,与"真的没有工作流"
  //     完全无法区分。
  it("返回非数组(防御路径)静默降级,不弹错误提示", async () => {
    m.listWorkflows.mockResolvedValue({ error: "<html></html>" });
    renderDashboard();
    await settle();
    expect(screen.getByText("暂无工作流")).toBeTruthy();
    expect(toast.error).not.toHaveBeenCalled();
  });
});

describe("Dashboard 失败必须可见", () => {
  it("列表接口请求失败时弹出错误提示(而不是只显示空态)", async () => {
    m.listWorkflows.mockRejectedValue(new Error("后端不可用"));
    renderDashboard();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载工作流列表");
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("后端不可用");
  });

  it("后端整体不可用时:多个接口失败只提示一次(防提示风暴)", async () => {
    m.listWorkflows.mockRejectedValue(new Error("后端不可用"));
    m.listExecutions.mockRejectedValue(new Error("后端不可用"));
    m.stats.mockRejectedValue(new Error("后端不可用"));
    renderDashboard();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
  });

  it("实时事件触发的刷新失败同样要提示", async () => {
    renderDashboard(); // 挂载时一切正常
    await settle();
    vi.mocked(toast.error).mockClear();
    // 后端开始出问题:事件驱动的刷新链此前也是 `.catch(() => {})`
    m.listExecutions.mockRejectedValue(new Error("后端不可用"));
    m.stats.mockRejectedValue(new Error("后端不可用"));
    emit(m.handlers, makeEvent("workflow.completed"));
    await advance(200);
    expect(toast.error).toHaveBeenCalled();
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载执行记录");
  });

  it("事件反复触发的刷新失败不会变成提示风暴", async () => {
    renderDashboard(); // 挂载时一切正常
    await settle();
    vi.mocked(toast.error).mockClear();
    // 后端开始出问题,而事件仍在不断到达
    m.listExecutions.mockRejectedValue(new Error("后端不可用"));
    m.stats.mockRejectedValue(new Error("后端不可用"));
    for (let i = 0; i < 5; i++) {
      emit(m.handlers, makeEvent("workflow.completed"));
      await advance(200);
    }
    // 5 次事件驱动的刷新 → 同一个错误只提示一次(不刷屏)
    expect(toast.error).toHaveBeenCalledTimes(1);
  });
});

// 被取消(failed 之外)必须单列,并且**不进**成功率的分母。
//
// 此前后端把 CANCELLED 算进 failed 列,前端成功率 = completed/total ——
// 用户点一次取消,成功率就掉一截;"这次是失败还是被取消"在统计页上也
// 答不出来。现在后端分列(cancelled),前端分列展示,成功率的分母排除
// 被取消的执行(被用户叫停 ≠ 没做成)。
describe("Dashboard 取消与失败分列", () => {
  const stat = {
    workflow_id: "wf-1",
    name: "工作流一",
    total: 10,
    completed: 6,
    failed: 2,
    cancelled: 2,
    waiting: 0,
    running: 0,
  };

  it("Cancelled 单独成卡成列", async () => {
    m.stats.mockResolvedValue([stat]);
    renderDashboard();
    await settle();
    // 卡片标签与表头各一处
    expect(screen.getAllByText("Cancelled").length).toBeGreaterThan(0);
    // 卡片值(2)与表格列值(2)都在
    expect(screen.getAllByText("2").length).toBeGreaterThan(0);
  });

  it("成功率的分母排除被取消的执行(6/8 = 75%,而不是 6/10 = 60%)", async () => {
    m.stats.mockResolvedValue([stat]);
    renderDashboard();
    await settle();
    expect(screen.getByText("75%")).toBeTruthy();
    expect(screen.queryByText("60%")).toBeNull();
  });

  it("旧后端(没有 cancelled 字段)不会算出 NaN", async () => {
    const { cancelled: _dropped, ...legacy } = stat;
    m.stats.mockResolvedValue([legacy]);
    renderDashboard();
    await settle();
    expect(screen.queryByText("NaN%")).toBeNull();
    expect(screen.getByText("60%")).toBeTruthy();
  });
});

// 零执行时成功率不得显示 0%:0% 读作"全失败",而事实是"还没有数据"。
describe("Dashboard 成功率无数据显示", () => {
  it("零执行的工作流成功率显示 — 而不是 0%", async () => {
    m.stats.mockResolvedValue([
      { workflow_id: "wf-a", name: "A", total: 0, completed: 0, failed: 0, cancelled: 0, waiting: 0 },
      { workflow_id: "wf-b", name: "B", total: 4, completed: 2, failed: 1, cancelled: 1, waiting: 0 },
    ]);
    renderDashboard();
    await settle();
    const rowA = screen.getByText("A").closest("tr");
    expect(rowA?.textContent).toContain("—");
    expect(rowA?.textContent).not.toContain("0%");
    // 有落定执行(cancelled 不进分母:4-1=3,2/3≈67%)的行仍是百分比
    const rowB = screen.getByText("B").closest("tr");
    expect(rowB?.textContent).toContain("67%");
  });

  it("只有被取消的执行时同样显示 —(取消不算失败)", async () => {
    m.stats.mockResolvedValue([
      { workflow_id: "wf-c", name: "C", total: 2, completed: 0, failed: 0, cancelled: 2, waiting: 0 },
    ]);
    renderDashboard();
    await settle();
    const row = screen.getByText("C").closest("tr");
    expect(row?.textContent).toContain("—");
  });
});
