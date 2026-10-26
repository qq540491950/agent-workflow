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

// ---- 迭代 35:结构化节点结果(result_json)必须可见 ----------------------
//
// 此前节点输出只展示 output/error 两个字段,而 agent 节点的价值几乎全在
// result_json 的结构化决策里(Summary / Decision):mock 跑完整条工作流,
// 用户在"节点输出"里看到的却全是"(无输出)",左侧列表对"代码评审 ×2"
// 也看不出第几次判了 APPROVED / REJECTED —— 要去 State 页签翻 JSON。
describe("ExecutionMonitor 结构化节点结果", () => {
  const nodeWithResult = {
    id: "n1",
    node_id: "review",
    node_type: "agent",
    node_name: "代码评审",
    state: "SUCCESS",
    attempt: 2,
    output: "",
    result_json: JSON.stringify({
      Data: {},
      Decision: "REJECTED",
      Error: "",
      Output: "",
      State: "SUCCESS",
      Summary: "Review 第 1 次:发现问题",
    }),
  };

  function switchToNodes() {
    // Radix Tabs 触发器在 mousedown 时激活(不是 click)
    const tab = screen.getByText("节点输出");
    fireEvent.mouseDown(tab);
    fireEvent.click(tab);
  }

  it("Summary 在展开区直接可见(不再是一律(无输出))", async () => {
    m.executionNodes.mockResolvedValue([nodeWithResult]);
    renderMonitor();
    await settle();
    switchToNodes();
    await settle();
    expect(screen.getByText("Review 第 1 次:发现问题")).toBeTruthy();
  });

  it("非 DONE 的 Decision 在折叠行上就有徽标(评审结果一眼可见)", async () => {
    m.executionNodes.mockResolvedValue([nodeWithResult]);
    renderMonitor();
    await settle();
    // 不展开,徽标就在 summary 行上
    expect(screen.getByText("REJECTED")).toBeTruthy();
  });

  it("DONE 不显示徽标(普通完成没有信息量)", async () => {
    m.executionNodes.mockResolvedValue([
      {
        ...nodeWithResult,
        node_name: "制定方案",
        result_json: JSON.stringify({
          Data: {},
          Decision: "DONE",
          Error: "",
          Output: "",
          State: "SUCCESS",
          Summary: "制定三步实施方案",
        }),
      },
    ]);
    renderMonitor();
    await settle();
    switchToNodes();
    await settle();
    expect(screen.queryByText("DONE")).toBeNull();
    expect(screen.getByText("制定三步实施方案")).toBeTruthy();
  });

  it("非对象 / 非法 result_json 不影响 output 展示(skill 等节点)", async () => {
    m.executionNodes.mockResolvedValue([
      {
        id: "n2",
        node_id: "submit",
        node_type: "skill",
        node_name: "提交",
        state: "SUCCESS",
        attempt: 1,
        output: "submitted: feat: x",
        result_json: "not-json{",
      },
    ]);
    renderMonitor();
    await settle();
    switchToNodes();
    await settle();
    expect(screen.getByText("submitted: feat: x")).toBeTruthy();
  });

  it("全空结果仍然显示(无输出)(不静默空白)", async () => {
    m.executionNodes.mockResolvedValue([
      {
        id: "n3",
        node_id: "plan",
        node_type: "agent",
        node_name: "计划",
        state: "SUCCESS",
        attempt: 1,
        output: "",
        result_json: JSON.stringify({ Data: {}, Decision: "DONE", Error: "", Output: "", State: "SUCCESS", Summary: "" }),
      },
    ]);
    renderMonitor();
    await settle();
    switchToNodes();
    await settle();
    expect(screen.getByText("(无输出)")).toBeTruthy();
  });
});

// ---- 迭代 35:审批面板不得挂在已完成的执行上 ------------------------------
//
// Resume 之后人工节点有两行(attempt 1 WAITING / attempt 2 SUCCESS):
// 旧行是历史。此前 waiting 判定是 nodes.some(state === "WAITING"),
// 被历史行骗中 —— 执行已 COMPLETED,"Workflow requires your input"
// 面板与 Approve/Reject 按钮仍然显示,再点只会吃一个错误 toast。
describe("ExecutionMonitor 审批面板与执行状态一致", () => {
  const twoRows = [
    { id: "r1", node_id: "approve", node_type: "human", node_name: "人工审批", state: "WAITING", attempt: 1 },
    { id: "r2", node_id: "approve", node_type: "human", node_name: "人工审批", state: "SUCCESS", attempt: 2 },
  ];

  it("执行 COMPLETED + 同节点有更高 attempt 的新行 → 不显示审批面板", async () => {
    m.getExecution.mockResolvedValue(exec({ state: "COMPLETED" }));
    m.executionNodes.mockResolvedValue(twoRows);
    renderMonitor();
    await settle();
    expect(screen.queryByText(/Workflow requires your input/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("执行 WAITING_USER 时面板照常显示(旧行为不回退)", async () => {
    m.getExecution.mockResolvedValue(exec({ state: "WAITING_USER" }));
    m.executionNodes.mockResolvedValue([twoRows[0]]);
    renderMonitor();
    await settle();
    expect(screen.getByText(/Workflow requires your input/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Approve" })).toBeTruthy();
  });

  it("仅节点行 WAITING(执行态刷新前的竞态窗口)→ 面板仍显示(兜底不丢)", async () => {
    m.getExecution.mockResolvedValue(exec({ state: "RUNNING" }));
    m.executionNodes.mockResolvedValue([twoRows[0]]);
    renderMonitor();
    await settle();
    expect(screen.getByText(/Workflow requires your input/)).toBeTruthy();
  });
});
