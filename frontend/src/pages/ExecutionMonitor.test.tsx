// @vitest-environment jsdom
// ExecutionMonitor:实时刷新必须 200ms 防抖(约定 7)+ 状态徽章 + 列表防御。
//
// 防抖窗口此前是 120ms(与 Layout/EventsAudit 的 200ms 不一致),没有任何测试
// 锁住,于是"统一 200ms"这条约定只能靠人工维持。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
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

// 第二轮 P2-3:请求竞态。
//
// 切换 :id 时旧执行的迟到响应会覆盖新执行 → 页面显示**上一条执行**的
// 数据;而"取消执行"/"重试失败节点"按 exec.id 操作 → 可能作用在用户
// 以为不是的那条执行上。
describe("ExecutionMonitor 请求竞态", () => {
  // 用**同一个路由**跳转(不是换 key 重挂载):组件实例不变,只有 :id 变 ——
  // 这正是真实场景(用户点另一条执行),也正是竞态会发生的地方。
  function NavMonitor() {
    const navigate = useNavigate();
    return (
      <>
        <button onClick={() => navigate("/executions/ex-2")}>切到 ex-2</button>
        <Routes>
          <Route path="/executions/:id" element={<ExecutionMonitor />} />
        </Routes>
      </>
    );
  }

  it("切换执行后,旧执行的迟到响应不得覆盖新执行", async () => {
    // 第一个执行(ex-1)的请求挂起,第二个(ex-2)先回来。
    let resolveOld: (v: unknown) => void = () => {};
    const oldReq = new Promise((r) => {
      resolveOld = r;
    });
    m.getExecution.mockReturnValueOnce(oldReq as never);

    render(
      <MemoryRouter initialEntries={["/executions/ex-1"]}>
        <NavMonitor />
      </MemoryRouter>,
    );
    await settle();
    expect(m.getExecution).toHaveBeenCalledTimes(1);

    // 切到 ex-2:第二次请求必须发出去。
    m.getExecution.mockResolvedValue(exec({ id: "ex-2", workflow_name: "新执行" }));
    fireEvent.click(screen.getByText("切到 ex-2"));
    await settle();
    expect(m.getExecution).toHaveBeenCalledTimes(2);
    // 新执行已渲染。
    expect(screen.getByText("新执行")).toBeTruthy();

    // 旧执行(ex-1)的响应现在才回来 —— 必须被丢弃。
    resolveOld(exec({ id: "ex-1", workflow_name: "旧执行" }));
    await settle();
    expect(screen.queryByText("旧执行")).toBeNull();
    expect(screen.getByText("新执行")).toBeTruthy();
  });

  it("切换执行时先清空上一条执行的数据(按钮不得作用在上一条执行上)", async () => {
    m.getExecution.mockResolvedValue(exec({ id: "ex-1", workflow_name: "旧执行" }));
    render(
      <MemoryRouter initialEntries={["/executions/ex-1"]}>
        <NavMonitor />
      </MemoryRouter>,
    );
    await settle();
    expect(screen.getByText("旧执行")).toBeTruthy();

    // 切换到 ex-2,但它的响应**还没回来**。
    m.getExecution.mockReturnValue(new Promise(() => {}) as never);
    fireEvent.click(screen.getByText("切到 ex-2"));
    await settle();
    // 旧执行的数据必须已经清空(页面回到"加载中…"),按钮无从作用在它上面。
    expect(screen.queryByText("旧执行")).toBeNull();
    expect(screen.getByText("加载中…")).toBeTruthy();
  });
});

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

// 节点 output/error 落库前已脱敏(约定 8):*** 是"已脱敏"的标记,
// 不是"出错";界面要给出说明,否则用户以为节点只输出了三个星号。
describe("ExecutionMonitor 脱敏输出", () => {
  it("输出含 *** 时给出脱敏说明", async () => {
    m.executionNodes.mockResolvedValue([
      {
        id: "n1",
        node_id: "n1",
        node_type: "agent",
        node_name: "计划",
        state: "SUCCESS",
        attempt: 1,
        output: "TOKEN=***",
      },
    ]);
    renderMonitor();
    await settle();
    // Radix Tabs 触发器在 mousedown 时激活(不是 click)
    const tab = screen.getByText("节点输出");
    fireEvent.mouseDown(tab);
    fireEvent.click(tab);
    await settle();
    expect(screen.getByText(/TOKEN=\*\*\*/)).toBeTruthy();
    expect(screen.getByText(/含脱敏内容/)).toBeTruthy();
  });

  it("未脱敏的输出不显示脱敏说明", async () => {
    m.executionNodes.mockResolvedValue([
      {
        id: "n1",
        node_id: "n1",
        node_type: "agent",
        node_name: "计划",
        state: "SUCCESS",
        attempt: 1,
        output: "一切正常",
      },
    ]);
    renderMonitor();
    await settle();
    // Radix Tabs 触发器在 mousedown 时激活(不是 click)
    const tab = screen.getByText("节点输出");
    fireEvent.mouseDown(tab);
    fireEvent.click(tab);
    await settle();
    expect(screen.getByText("一切正常")).toBeTruthy();
    expect(screen.queryByText(/含脱敏内容/)).toBeNull();
  });
});
