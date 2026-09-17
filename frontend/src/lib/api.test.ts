// api.ts 统一服务层测试:双模式探测、防御性转换、HTTP 错误传播、事件订阅。
// 桌面模式的 @wailsio/runtime 与 @bindings/* 全部 mock,纯逻辑验证。
import { beforeEach, describe, expect, it, vi } from "vitest";

const callByID = vi.fn();
vi.mock("@wailsio/runtime", () => ({
  Call: { ByID: (...a: unknown[]) => callByID(...a) },
  Events: { On: vi.fn() },
}));

const wfList = vi.fn();
const exAllEvents = vi.fn();
vi.mock("@bindings/agentworkflow/app/application/workflowservice.js", () => ({
  List: (...a: unknown[]) => wfList(...a),
}));
vi.mock("@bindings/agentworkflow/app/application/executionservice.js", () => ({
  AllEvents: (...a: unknown[]) => exAllEvents(...a),
}));
vi.mock("@bindings/agentworkflow/app/application/agentservice.js", () => ({}));
vi.mock("@bindings/agentworkflow/app/application/skillservice.js", () => ({}));
vi.mock("@bindings/agentworkflow/app/application/gitservice.js", () => ({}));
vi.mock("@bindings/agentworkflow/app/application/settingsservice.js", () => ({}));

// 每个用例重新加载被测模块,清除 detectMode 的模式缓存。
async function freshApi() {
  vi.resetModules();
  return import("./api");
}

beforeEach(() => {
  callByID.mockReset();
  wfList.mockReset();
  exAllEvents.mockReset();
  vi.unstubAllGlobals();
});

describe("asArray", () => {
  it("数组原样返回", async () => {
    const { asArray } = await freshApi();
    const arr = [{ id: "1" }];
    expect(asArray(arr)).toBe(arr);
  });
  it("null/对象/undefined 归一为空数组", async () => {
    const { asArray } = await freshApi();
    expect(asArray(null)).toEqual([]);
    expect(asArray(undefined)).toEqual([]);
    expect(asArray({ error: "html fallback" })).toEqual([]);
  });
});

describe("detectMode", () => {
  it("IPC 探测成功 → desktop", async () => {
    callByID.mockResolvedValue([]);
    const { detectMode } = await freshApi();
    await expect(detectMode()).resolves.toBe("desktop");
  });
  it("IPC 探测失败 → web", async () => {
    callByID.mockRejectedValue(new Error("not in wails"));
    const { detectMode } = await freshApi();
    await expect(detectMode()).resolves.toBe("web");
  });
  it("结果被缓存,重复调用不重复探测", async () => {
    callByID.mockResolvedValue([]);
    const { detectMode } = await freshApi();
    await detectMode();
    await detectMode();
    expect(callByID).toHaveBeenCalledTimes(1);
  });
});

describe("web 模式 HTTP 调用", () => {
  it("listWorkflows 走 /api/workflows", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    const data = [{ id: "wf1" }];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        text: async () => JSON.stringify(data),
      }),
    );
    const { api } = await freshApi();
    await expect(api.listWorkflows()).resolves.toEqual(data);
    expect(fetch).toHaveBeenCalledWith("/api/workflows", expect.anything());
  });

  it("后端错误提取 error 字段", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        text: async () => JSON.stringify({ error: "NOT_FOUND" }),
      }),
    );
    const { api } = await freshApi();
    await expect(api.getWorkflow("x")).rejects.toThrow("NOT_FOUND");
  });

  it("非 JSON 响应(如 SPA fallback HTML)回退 HTTP 状态码消息", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        text: async () => "<html>fallback</html>",
      }),
    );
    const { api } = await freshApi();
    await expect(api.getWorkflow("x")).rejects.toThrow("HTTP 500");
  });

  it("listExecutions 组装查询参数", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, text: async () => "[]" }),
    );
    const { api } = await freshApi();
    await api.listExecutions("wf-1", 25);
    expect(fetch).toHaveBeenCalledWith(
      "/api/executions?workflow_id=wf-1&limit=25",
      expect.anything(),
    );
  });
});

describe("desktop 模式 IPC 调用", () => {
  it("listWorkflows 走 bindings.List", async () => {
    callByID.mockResolvedValue([]); // 探测成功
    wfList.mockResolvedValue([{ id: "wf1" }]);
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const { api } = await freshApi();
    await expect(api.listWorkflows()).resolves.toEqual([{ id: "wf1" }]);
    expect(wfList).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled(); // 不应发 HTTP
  });

  it("allEvents 解析 type/limit 参数", async () => {
    callByID.mockResolvedValue([]);
    exAllEvents.mockResolvedValue([]);
    const { api } = await freshApi();
    await api.allEvents("?type=node.started&limit=5");
    expect(exAllEvents).toHaveBeenCalledWith("node.started", 5);
    await api.allEvents("?type=workflow.completed");
    expect(exAllEvents).toHaveBeenCalledWith("workflow.completed", 200);
  });
});

describe("subscribeEvents(web 模式)", () => {
  it("SSE 消息分发到订阅者,退订后不再接收", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    type MessageHandler = (m: { data: string }) => void;
    let onmessage: MessageHandler | null = null;
    class FakeEventSource {
      url: string;
      onmessage: MessageHandler | null = null;
      constructor(url: string) {
        this.url = url;
        onmessage = (m) => this.onmessage?.(m);
      }
    }
    vi.stubGlobal("window", {});
    vi.stubGlobal("EventSource", FakeEventSource);

    const { subscribeEvents } = await freshApi();
    const got: string[] = [];
    const unsub = subscribeEvents((ev) => got.push(ev.type));
    // ensureEventStream 是异步链(detectMode → import runtime/建连),让微任务跑完
    await new Promise((r) => setTimeout(r, 0));
    const fire = onmessage as MessageHandler | null;
    fire?.({ data: JSON.stringify({ type: "node.started" }) });
    fire?.({ data: "not-json" }); // 非法消息被忽略
    unsub();
    fire?.({ data: JSON.stringify({ type: "node.completed" }) });
    expect(got).toEqual(["node.started"]);
  });
});
