// @vitest-environment jsdom
// Designer 的 mode 下拉必须**后端派生**(约定 13):mode 的取值域只有一份
// (workflow/model.AllModes),前端此前自己硬编码了一份清单 ——
//   - 后端加了 mode 而前端没跟上 → 用户在下拉里**选不到**新 mode;
//   - 前端留着后端已删的 mode → 用户选了必然被校验期判非法,
//     而界面给不出任何解释。
//
// 兜底清单(FALLBACK_MODES)只在"后端还没返回 / 返回异常"时用:控件不能
// 空着 —— 空下拉框会让用户以为"这个节点不能设 mode"。
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import type { Node as RFNode } from "@xyflow/react";
import Designer, { FALLBACK_MODES, NodePropertyPanel, resolveModeList } from "./Designer";

const m = vi.hoisted(() => ({
  modes: vi.fn(),
  listAgents: vi.fn(),
  getWorkflow: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      modes: m.modes,
      listAgents: m.listAgents,
      getWorkflow: m.getWorkflow,
    },
  };
});

/**
 * jsdom 缺两样东西,Radix Select 的浮层都要:
 *
 *  1. 指针捕获 API(hasPointerCapture / setPointerCapture /
 *     releasePointerCapture)—— 不补会抛 "target.hasPointerCapture is
 *     not a function";
 *  2. PointerEvent —— jsdom 没有它,testing-library 退化成普通 Event,
 *     于是 event.pointerType 是 undefined,而 Radix 的打开条件是
 *     `event.pointerType === "mouse"` → 下拉永远打不开(静默地什么
 *     都没发生,用例只能看到一个空的浮层)。
 */
beforeAll(() => {
  const proto = Element.prototype as unknown as Record<string, unknown>;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};

  if (!("PointerEvent" in window)) {
    class PointerEventPolyfill extends MouseEvent {
      pointerId: number;
      pointerType: string;
      constructor(type: string, params: PointerEventInit = {}) {
        super(type, params);
        this.pointerId = params.pointerId ?? 1;
        this.pointerType = params.pointerType ?? "mouse";
      }
    }
    (window as unknown as Record<string, unknown>).PointerEvent =
      PointerEventPolyfill;
  }

  // ReactFlow(画布)用 ResizeObserver 观察容器尺寸,jsdom 没有它。
  if (!("ResizeObserver" in window)) {
    (window as unknown as Record<string, unknown>).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

/** 打开 Radix Select(Radix 监听 pointerdown 打开浮层) */
async function openSelect(trigger: Element | null) {
  if (!trigger) throw new Error("未找到 Select 触发器");
  // pointerType 必须显式给 "mouse":Radix 的打开条件是
  // `event.pointerType === "mouse"`,jsdom 没有 PointerEvent,
  // testing-library 退化成普通 Event → pointerType 是 undefined →
  // 下拉静默地打不开(用例只看到一个空浮层,什么也不报错)。
  fireEvent.pointerDown(trigger, {
    button: 0,
    ctrlKey: false,
    pointerType: "mouse",
    pointerId: 1,
  });
  // 浮层是异步挂载的(portal + presence),等它真的出现。
  await waitFor(() => {
    if (document.querySelectorAll('[role="option"]').length === 0) {
      throw new Error("下拉浮层尚未出现");
    }
  });
}

/** 取下拉浮层里的选项文本 */
function optionTexts(): string[] {
  return Array.from(document.querySelectorAll('[role="option"]')).map(
    (o) => o.textContent ?? "",
  );
}

/** 取属性面板里的第 n 个 Select 触发器(0=Agent,1=Mode) */
function selectTrigger(index: number): Element | null {
  // Radix Select 的触发器是 role="combobox" 的 button(这一版 Radix
  // 不再输出 aria-haspopup,别按那个属性找)。
  const list = document.querySelectorAll('button[role="combobox"]');
  return list[index] ?? null;
}

const agentNode: RFNode = {
  id: "n1",
  type: "wf",
  position: { x: 0, y: 0 },
  data: {
    wtype: "agent",
    label: "节点",
    cfg: { agent: "mock-claude", mode: "plan" },
  },
} as unknown as RFNode;

function renderPanel(modes: string[]) {
  return render(
    <NodePropertyPanel
      node={agentNode}
      agents={["mock-claude"]}
      modes={modes}
      onChange={() => {}}
      onDelete={() => {}}
      onDuplicate={() => {}}
    />,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  m.modes.mockReset();
  m.listAgents.mockReset();
  m.getWorkflow.mockReset();
});

describe("resolveModeList", () => {
  it("后端返回的清单优先", () => {
    expect(resolveModeList(["plan", "custom"])).toEqual(["plan", "custom"]);
  });

  it("后端返回空/null 时用兜底清单(控件不留空)", () => {
    expect(resolveModeList([])).toEqual(FALLBACK_MODES);
    expect(resolveModeList(null)).toEqual(FALLBACK_MODES);
    expect(resolveModeList(undefined)).toEqual(FALLBACK_MODES);
    // 异常返回(HTML 兜底页被解析成对象)也不能让下拉空着
    expect(resolveModeList({ error: "html fallback" })).toEqual(FALLBACK_MODES);
  });

  it("兜底清单本身非空(防空转)", () => {
    expect(FALLBACK_MODES.length).toBeGreaterThan(0);
  });
});

describe("Designer 的 mode 下拉", () => {
  it("下拉的选项来自传入的清单(后端派生)", async () => {
    renderPanel(["plan", "execute", "brand-new-mode"]);
    // 属性面板里第一个 Select 是 Agent,第二个才是 Mode
    await openSelect(selectTrigger(1));
    const options = optionTexts();
    expect(options).toContain("brand-new-mode");
    expect(options).toContain("plan");
    // 不再出现前端硬编码清单里的、但后端没给的 mode
    expect(options).not.toContain("submit");
  });

  it("清单为空时用兜底(下拉不留空)", async () => {
    renderPanel(FALLBACK_MODES);
    await openSelect(selectTrigger(1));
    const options = optionTexts();
    expect(options.length).toBe(FALLBACK_MODES.length);
    for (const m of FALLBACK_MODES) {
      expect(options).toContain(m);
    }
  });

  it("当前 mode 在下拉里可见(选中值渲染在触发器上)", () => {
    renderPanel(["plan", "execute"]);
    const trigger = selectTrigger(1);
    expect(trigger?.textContent ?? "").toContain("plan");
    // 兜底:触发器存在即可(不空)
    expect(trigger).not.toBeNull();
    expect(screen.getByText("Mode")).toBeTruthy();
  });
});

// 页面级:mode 清单必须**真的向后端要**(约定 13 的另一半 —— 下拉的
// 数据来源是后端,不是前端那份硬编码清单)。
describe("Designer 页面:mode 清单来自后端", () => {
  const wf = {
    id: "wf-1",
    name: "W",
    description: "",
    version: 1,
    enabled: true,
    nodes: [
      {
        id: "n1",
        name: "N1",
        type: "agent",
        position: { x: 0, y: 0 },
        config: { agent: "mock-claude", mode: "plan" },
      },
    ],
    edges: [],
    settings: {},
  };

  function renderDesigner() {
    // 生产 App 已迁移到 data router(设计器的未保存守卫依赖 useBlocker,
    // 它只在 data router 下可用)—— 夹具用同一形态族的 createMemoryRouter,
    // 否则 useBlocker 直接抛 invariant。
    const router = createMemoryRouter(
      [
        {
          path: "/workflows/:id",
          element: <Designer />,
        },
      ],
      { initialEntries: ["/workflows/wf-1"] },
    );
    return render(<RouterProvider router={router} />);
  }

  it("挂载时向后端拉取 mode 清单", async () => {
    m.modes.mockResolvedValue(["plan", "execute", "review", "fix", "test", "submit"]);
    m.listAgents.mockResolvedValue([]);
    m.getWorkflow.mockResolvedValue(wf);
    renderDesigner();
    await waitFor(() => expect(m.modes).toHaveBeenCalledTimes(1));
  });

  it("下拉里的选项是后端给的那份(不是前端硬编码清单)", async () => {
    m.modes.mockResolvedValue(["plan", "brand-new-mode"]);
    m.listAgents.mockResolvedValue([]);
    m.getWorkflow.mockResolvedValue(wf);
    renderDesigner();
    await waitFor(() => expect(m.modes).toHaveBeenCalledTimes(1));
    // 选中节点 → 属性面板出现 → 打开 mode 下拉
    const node = await waitFor(() => {
      const el = document.querySelector('[data-id="n1"]');
      if (!el) throw new Error("画布节点尚未出现");
      return el;
    });
    fireEvent.click(node);
    await waitFor(() => expect(screen.getByText("Mode")).toBeTruthy());
    await openSelect(selectTrigger(1));
    const options = optionTexts();
    expect(options).toContain("brand-new-mode");
    // 前端兜底清单里的 "submit" 后端没给 → 不该出现
    expect(options).not.toContain("submit");
  });
});
