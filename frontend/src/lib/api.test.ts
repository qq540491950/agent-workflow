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
const stExportBackup = vi.fn();
vi.mock("@bindings/agentworkflow/app/application/workflowservice.js", () => ({
  List: (...a: unknown[]) => wfList(...a),
}));
vi.mock("@bindings/agentworkflow/app/application/executionservice.js", () => ({
  AllEvents: (...a: unknown[]) => exAllEvents(...a),
}));
// 桌面绑定用 Proxy 桩:被调用的导出自动返回 vi.fn()(默认 undefined),
// 但**显式断言模块形状**—— 访问一个后端并不存在的绑定(例如
// ResetConfig 被改名)会在读取属性时立刻抛出,而不是静默得到 undefined
// 然后让某个用例以"期望 undefined"的姿态通过。
// 此前把整个模块 mock 成 {}:任何依赖它的用例都会静默失败而非报错,
// 绑定漂移(后端改了导出名)完全测不出来。
//
// exports 列表取自 frontend/bindings/ 下 wails 生成的真实文件,改动后端
// 导出名时这两个列表会与真实绑定对不上,测试即刻失败。
function bindingStub(
  name: string,
  exports: string[],
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  const known = new Set(exports);
  // Promise.all 会把每个桩当 thenable 处理并读 .then,这类协议属性
  // 不能当成"未知导出"抛错
  const protocol = new Set(["then", "catch", "finally"]);
  return new Proxy<Record<string, unknown>>(
    {},
    {
      get(_t, prop) {
        if (typeof prop === "symbol") return undefined;
        if (protocol.has(prop)) return undefined;
        if (!known.has(prop)) {
          throw new Error(
            `绑定 ${name} 不存在导出 ${prop}(可用: ${[...known].join(", ")})`,
          );
        }
        // 需要断言调用参数的绑定走 overrides(仍是已知导出,不破坏形状守卫)
        const ov = overrides[prop as string];
        if (ov) return ov;
        return vi.fn();
      },
      has: () => true,
    },
  );
}

vi.mock("@bindings/agentworkflow/app/application/agentservice.js", () =>
  bindingStub("AgentService", [
    "List",
    "GetConfig",
    "UpdateConfig",
    "ResetConfig",
    "Test",
    "SetPermission",
  ]),
);
vi.mock("@bindings/agentworkflow/app/application/skillservice.js", () =>
  bindingStub("SkillService", ["List", "SetEnabled", "Test"]),
);
vi.mock("@bindings/agentworkflow/app/application/gitservice.js", () =>
  bindingStub("GitService", [
    "Status",
    "Diff",
    "Log",
    "Commit",
    "SetWorkingDir",
  ]),
);
vi.mock("@bindings/agentworkflow/app/application/settingsservice.js", () =>
  bindingStub(
    "SettingsService",
    [
      "Info",
      "Update",
      "Get",
      "Set",
      "ExportBackup",
      "RestoreBackup",
    ],
    { ExportBackup: (...a: unknown[]) => stExportBackup(...a) },
  ),
);

// 每个用例重新加载被测模块,清除 detectMode 的模式缓存。
async function freshApi() {
  vi.resetModules();
  return import("./api");
}

beforeEach(() => {
  callByID.mockReset();
  wfList.mockReset();
  exAllEvents.mockReset();
  stExportBackup.mockReset();
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

describe("version(双模式)", () => {
  it("桌面模式同样走 /api/version(/api 已挂载)", async () => {
    callByID.mockResolvedValue([]); // desktop
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => JSON.stringify({ app: "x", version: "1", adk: "adk v1" }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { api } = await freshApi();
    const v = await api.version();
    expect(v.adk).toContain("adk");
    expect(fetchMock).toHaveBeenCalledWith("/api/version", expect.anything());
  });

  it("端点不可用时回退空对象", async () => {
    callByID.mockRejectedValue(new Error("no ipc")); // web
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 500, text: async () => "" }),
    );
    const { api } = await freshApi();
    await expect(api.version()).resolves.toEqual({});
  });
});

// ---- C4:备份导出的双分支契约 ----
// exportBackupText 此前只有桌面分支:浏览器(服务器)模式下仍去调 Wails 绑定,
// 绑定不可用 → 导出备份必然失败,而 Settings 页面的注释却写着
// "浏览器模式走同源下载链接"。约定 10 要求两个分支返回结构一致。
describe("exportBackupText(双模式)", () => {
  it("浏览器模式走同源 /api/backup", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    const body = JSON.stringify({ workflows: [], agent_configs: {} });
    const fetchMock = vi
      .fn()
      .mockResolvedValue({ ok: true, text: async () => body });
    vi.stubGlobal("fetch", fetchMock);
    const { api } = await freshApi();
    await expect(api.exportBackupText()).resolves.toBe(body);
    expect(fetchMock).toHaveBeenCalledWith("/api/backup");
  });

  it("浏览器模式后端报错时抛错(不得把错误体当备份导出)", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        text: async () => JSON.stringify({ error: "导出失败" }),
      }),
    );
    const { api } = await freshApi();
    await expect(api.exportBackupText()).rejects.toThrow("导出失败");
  });

  it("桌面模式走 bindings.ExportBackup 且不发 HTTP", async () => {
    callByID.mockResolvedValue([]); // desktop
    stExportBackup.mockResolvedValue({ workflows: [] });
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const { api } = await freshApi();
    const text = await api.exportBackupText();
    expect(JSON.parse(text)).toEqual({ workflows: [] });
    expect(stExportBackup).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

// 绑定 ID 漂移守卫:bindings 再生成可能改变 List 的数字 ID,
// 探测常量若不同步,detectMode 在桌面模式会静默回退 web。
describe("PROBE_BINDING_ID 与生成代码同步", () => {
  it("等于 workflowservice.ts 中 List 的 ByID", async () => {
    const { readFileSync } = await import("node:fs");
    const bindingSrc = readFileSync(
      new URL("../../bindings/agentworkflow/app/application/workflowservice.ts", import.meta.url),
      "utf8",
    );
    const generated = bindingSrc.match(
      /export function List\(\)[\s\S]*?\$Call\.ByID\((\d+)\)/,
    );
    expect(generated).toBeTruthy();
    const apiSrc = readFileSync(new URL("./api.ts", import.meta.url), "utf8");
    const probe = apiSrc.match(/PROBE_BINDING_ID = (\d+)/);
    expect(probe).toBeTruthy();
    expect(probe![1]).toBe(generated![1]);
  });
});

// ---- 轮次 27:文本端点错误处理与结构化错误 ----

describe("文本端点(版本 DSL / YAML 导出)", () => {
  it("成功时返回原始文本", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, text: async () => "workflow:\n  id: wf1\n" }),
    );
    const { api } = await freshApi();
    await expect(api.versionDSL("wf1", 2)).resolves.toContain("id: wf1");
    await expect(api.exportYAML("wf1")).resolves.toContain("id: wf1");
  });

  it("后端 404 的 JSON 错误体不得被当成 DSL 内容返回", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        text: async () =>
          JSON.stringify({
            error: '工作流版本 "wf1/9" 不存在',
            code: "NOT_FOUND",
            kind: "validation_error",
          }),
      }),
    );
    const { api } = await freshApi();
    // 关键:抛错而不是把 {"error":...} 当成 DSL 弹进对话框
    await expect(api.versionDSL("wf1", 9)).rejects.toThrow("不存在");
    // 导出同样不能把错误 JSON 写进 .yaml 文件
    await expect(api.exportYAML("wf1")).rejects.toThrow("不存在");
  });

  it("非 JSON 错误体回退为状态码消息", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 502, text: async () => "bad gateway" }),
    );
    const { api } = await freshApi();
    await expect(api.versionDSL("wf1", 1)).rejects.toThrow(/502|bad gateway/);
  });
});

describe("ApiError 保留后端 code/kind/status", () => {
  it("JSON 错误响应携带结构化字段", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 403,
        text: async () =>
          JSON.stringify({ error: "权限不足", code: "FORBIDDEN", kind: "permission_error" }),
      }),
    );
    const { api, ApiError } = await freshApi();
    const err = await api.listAgents().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as InstanceType<typeof ApiError>;
    expect(e.status).toBe(403);
    expect(e.code).toBe("FORBIDDEN");
    expect(e.kind).toBe("permission_error");
    expect(e.message).toBe("权限不足");
  });

  it("非 JSON 错误响应 status 仍可读", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 500, text: async () => "<html/>" }),
    );
    const { api, ApiError } = await freshApi();
    const err = await api.getWorkflow("x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as InstanceType<typeof ApiError>).status).toBe(500);
  });
});

// ---- C3:绑定形状守卫 ----

// TestBindingStubRejectsUnknownExport 证明桩确实能挡住绑定漂移。
// 若桩退化成普通空对象(mock 成 {} 的旧做法),访问不存在的导出会
// 静默得到 undefined,所有依赖它的用例继续"通过",漂移永远测不出来。
describe("绑定形状守卫", () => {
  it("访问未声明的导出立刻抛错", async () => {
    const stub = bindingStub("AgentService", ["List", "ResetConfig"]);
    expect(typeof stub.List).toBe("function");
    expect(() => stub.NotAThing).toThrow(/不存在导出 NotAThing/);
  });

  it("then/catch/finally 等协议属性不触发该守卫(Promise.all 需要)", async () => {
    const stub = bindingStub("AgentService", ["List"]);
    expect(stub.then).toBeUndefined();
    expect(stub.catch).toBeUndefined();
    expect(stub.finally).toBeUndefined();
    // Promise.all 能正常吞掉桩
    await expect(Promise.all([stub])).resolves.toBeDefined();
  });

  it("导出清单与真实 wails 绑定一致(防止手写清单漂移)", async () => {
    const fs = await import("node:fs/promises");
    const path = await import("node:path");
    // vitest 的 import.meta.dirname 指向 src/lib,绑定的别名指向
    // frontend/bindings(见 vite.config.ts),故上溯两级
    const dir = path.join(import.meta.dirname, "..", "..", "bindings",
      "agentworkflow", "app", "application");
    const expected: Record<string, string[]> = {
      "agentservice": ["GetConfig", "List", "ResetConfig", "SetPermission", "Test", "UpdateConfig"],
      "skillservice": ["List", "SetEnabled", "Test"],
      "gitservice": ["Commit", "Diff", "Log", "SetWorkingDir", "Status"],
      "settingsservice": ["ExportBackup", "Get", "Info", "RestoreBackup", "Set", "Update"],
    };
    for (const [file, names] of Object.entries(expected)) {
      const src = await fs.readFile(path.join(dir, `${file}.ts`), "utf8");
      const actual = [...src.matchAll(/^export function ([A-Za-z]+)/gm)].map((m) => m[1]).sort();
      expect(actual, `${file} 的绑定导出清单`).toEqual([...names].sort());
    }
  });
});
