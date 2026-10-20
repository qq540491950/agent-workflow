// api.ts 统一服务层测试:双模式探测、防御性转换、HTTP 错误传播、事件订阅。
// 桌面模式的 @wailsio/runtime 与 @bindings/* 全部 mock,纯逻辑验证。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import type { Workflow } from "./types";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const callByID = vi.fn();
vi.mock("@wailsio/runtime", () => ({
  Call: { ByID: (...a: unknown[]) => callByID(...a) },
  Events: { On: vi.fn() },
}));

const wfList = vi.fn();
const wfSavePayload = vi.fn();
const wfImportYAML = vi.fn();
const exAllEvents = vi.fn();
const exRun = vi.fn();
const stExportBackup = vi.fn();
const stRestoreBackup = vi.fn();
const stInfo = vi.fn();
const stUpdate = vi.fn();
// 桌面绑定用 bindingStub(见下):显式声明导出清单,访问后端并不存在的
// 导出会在读取属性时立刻抛错 —— 绑定漂移测得出来。
vi.mock("@bindings/agentworkflow/app/application/workflowservice.js", () =>
  bindingStub(
    "WorkflowService",
    [
      "Delete",
      "Duplicate",
      "ExportYAML",
      "Get",
      "ImportYAML",
      "List",
      "Save",
      "SavePayload",
      "SetEnabled",
      "Validate",
      "VersionDSL",
      "Versions",
    ],
    {
      List: (...a: unknown[]) => wfList(...a),
      SavePayload: (...a: unknown[]) => wfSavePayload(...a),
      ImportYAML: (...a: unknown[]) => wfImportYAML(...a),
    },
  ),
);
vi.mock("@bindings/agentworkflow/app/application/executionservice.js", () =>
  bindingStub(
    "ExecutionService",
    [
      "AllEvents",
      "Artifacts",
      "Cancel",
      "Delete",
      "Events",
      "Export",
      "Get",
      "List",
      "Nodes",
      "ProvideInput",
      "RetryNode",
      "Run",
      "Stats",
    ],
    {
      AllEvents: (...a: unknown[]) => exAllEvents(...a),
      Run: (...a: unknown[]) => exRun(...a),
    },
  ),
);
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
    {
      ExportBackup: (...a: unknown[]) => stExportBackup(...a),
      RestoreBackup: (...a: unknown[]) => stRestoreBackup(...a),
      Info: (...a: unknown[]) => stInfo(...a),
      Update: (...a: unknown[]) => stUpdate(...a),
    },
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
  wfSavePayload.mockReset();
  wfImportYAML.mockReset();
  exAllEvents.mockReset();
  exRun.mockReset();
  stExportBackup.mockReset();
  stRestoreBackup.mockReset();
  stInfo.mockReset();
  stUpdate.mockReset();
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

  // 此前 es.onerror 是空处理:断线时用户只看到"页面上的实时数据不动了",
  // 与"确实没有新数据"无法区分。现在必须报出来。
  it("SSE 断线会被报出来(不再静默吞掉)", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    let onerror: (() => void) | null = null;
    class FakeEventSource {
      url: string;
      onmessage: ((m: { data: string }) => void) | null = null;
      onerror: (() => void) | null = null;
      constructor(url: string) {
        this.url = url;
        onerror = () => this.onerror?.();
      }
    }
    vi.stubGlobal("window", {});
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.mocked(toast.error).mockClear();

    const { subscribeEvents } = await freshApi();
    subscribeEvents(() => {});
    await new Promise((r) => setTimeout(r, 0));
    (onerror as (() => void) | null)?.();
    expect(toast.error).toHaveBeenCalled();
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("实时事件流");
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

// ---- 迭代 11:后端错误体契约(错误体必带 code/kind + 新增 code + 向后兼容) ----
//
// 下面的响应体逐字取自真实 server 的返回(go run . --server 后 curl 实测):
//   400 {"code":"INVALID_JSON","error":"ValidationError(INVALID_JSON): 请求体不是合法 JSON: ...","kind":"ValidationError"}
//   413 {"code":"PAYLOAD_TOO_LARGE","error":"ValidationError(PAYLOAD_TOO_LARGE): 请求体超过 1 MB 上限","kind":"ValidationError"}
//   405 {"code":"METHOD_NOT_ALLOWED","error":"HTTP 方法不被允许","kind":"ValidationError"}
//   404 {"code":"NOT_FOUND","error":"接口不存在","kind":"ValidationError"}
describe("后端错误体契约", () => {
  function stubError(status: number, body: unknown) {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status,
        text: async () => JSON.stringify(body),
      }),
    );
  }

  it("结构化错误体:code/kind 落到 ApiError,Kind(CODE) 前缀被剥掉", async () => {
    stubError(400, {
      code: "INVALID_JSON",
      error: "ValidationError(INVALID_JSON): 请求体不是合法 JSON: invalid character 'b'",
      kind: "ValidationError",
    });
    const { api, ApiError } = await freshApi();
    const err = await api.getWorkflow("x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as InstanceType<typeof ApiError>;
    expect(e.code).toBe("INVALID_JSON");
    expect(e.kind).toBe("ValidationError");
    expect(e.status).toBe(400);
    // 前缀剥掉:用户看到人话,而不是 "ValidationError(INVALID_JSON): ..."
    expect(e.message).toBe("请求体不是合法 JSON: invalid character 'b'");
  });

  it("413 PAYLOAD_TOO_LARGE 给出可读提示(不是裸 HTTP 413)", async () => {
    stubError(413, {
      code: "PAYLOAD_TOO_LARGE",
      error: "ValidationError(PAYLOAD_TOO_LARGE): 请求体超过 1 MB 上限",
      kind: "ValidationError",
    });
    const { api } = await freshApi();
    const err = (await api.getWorkflow("x").catch((e: unknown) => e)) as {
      message: string;
      code?: string;
    };
    expect(err.code).toBe("PAYLOAD_TOO_LARGE");
    expect(err.message).toContain("超过 1 MB 上限");
    expect(err.message).not.toContain("ValidationError(");
  });

  it("错误体只有 code 没有 error 时按 code 给中文提示", async () => {
    stubError(413, { code: "PAYLOAD_TOO_LARGE", kind: "ValidationError" });
    const { api } = await freshApi();
    const err = (await api.getWorkflow("x").catch((e: unknown) => e)) as {
      message: string;
      code?: string;
    };
    expect(err.code).toBe("PAYLOAD_TOO_LARGE");
    expect(err.message).toContain("内容过大");
    expect(err.message).toContain("413");
  });

  it("未知 code 不落到 Object 原型链上(不会把函数当提示文案)", async () => {
    stubError(400, { code: "constructor", kind: "ValidationError" });
    const { api } = await freshApi();
    const err = (await api.getWorkflow("x").catch((e: unknown) => e)) as {
      message: string;
    };
    expect(err.message).toBe("HTTP 400: 请求参数不合法");
    expect(err.message).not.toContain("function");
  });

  it("405 METHOD_NOT_ALLOWED 给出可读提示", async () => {
    stubError(405, {
      code: "METHOD_NOT_ALLOWED",
      error: "HTTP 方法不被允许",
      kind: "ValidationError",
    });
    const { api } = await freshApi();
    const err = (await api.getWorkflow("x").catch((e: unknown) => e)) as {
      message: string;
      code?: string;
    };
    expect(err.code).toBe("METHOD_NOT_ALLOWED");
    expect(err.message).toContain("方法不被允许");
  });

  it("INVALID_LOG_LEVEL(后端新增码)给出可读提示", async () => {
    stubError(400, {
      code: "INVALID_LOG_LEVEL",
      error: 'ValidationError(INVALID_LOG_LEVEL): 日志级别非法: "trace"(可选 debug|info|warn|error)',
      kind: "ValidationError",
    });
    const { api } = await freshApi();
    const err = (await api.updateSettings({ log_level: "trace" }).catch((e: unknown) => e)) as {
      message: string;
      code?: string;
    };
    expect(err.code).toBe("INVALID_LOG_LEVEL");
    expect(err.message).toContain("日志级别非法");
    expect(err.message).not.toContain("ValidationError(");
  });

  it("404 未知路由的错误体照样解析出 code", async () => {
    stubError(404, {
      code: "NOT_FOUND",
      error: "接口不存在",
      kind: "ValidationError",
    });
    const { api } = await freshApi();
    const err = (await api.listWorkflows().catch((e: unknown) => e)) as {
      message: string;
      code?: string;
      status: number;
    };
    expect(err.code).toBe("NOT_FOUND");
    expect(err.status).toBe(404);
    expect(err.message).toBe("接口不存在");
  });

  // 第四/五轮新增的码必须能落到人话提示上:错误体只有 code 没有 error
  // 时(旧版后端/反向代理改写),用户不能只看到 "HTTP 400"。
  it.each([
    ["DSL_UNKNOWN_KEY", "未知顶层键"],
    ["DSL_MISPLACED_KEY", "层级"],
    ["UNKNOWN_FIELD", "未知顶层字段"],
    ["UNKNOWN_NODE_CONFIG", "未识别的配置键"],
    ["DEPRECATED_NODE_CONFIG", "已废弃"],
    ["INVALID_NODE_ENABLED", "enabled"],
  ])("%s 给出可读提示(不是裸 HTTP 码)", async (code, want) => {
    stubError(400, { code, kind: "ValidationError" });
    const { api } = await freshApi();
    const err = (await api.getWorkflow("x").catch((e: unknown) => e)) as {
      message: string;
      code?: string;
    };
    expect(err.code).toBe(code);
    expect(err.message).toContain(want);
    expect(err.message).toContain("400");
  });

  it("向后兼容:错误体只有 error 没有 code 时不抛未捕获异常", async () => {
    stubError(400, { error: "参数不合法" });
    const { api, ApiError } = await freshApi();
    const err = await api.getWorkflow("x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as InstanceType<typeof ApiError>;
    expect(e.code).toBeUndefined();
    expect(e.kind).toBeUndefined();
    expect(e.status).toBe(400);
    expect(e.message).toBe("参数不合法");
  });

  it("向后兼容:错误体是 HTML 兜底页时不崩(回落状态码消息)", async () => {
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

  it("文本端点(DSL 导出)同样剥前缀并保留 code/kind", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        text: async () =>
          JSON.stringify({
            code: "NOT_FOUND",
            error: "ValidationError(NOT_FOUND): 工作流版本 \"wf1/9\" 不存在",
            kind: "ValidationError",
          }),
      }),
    );
    const { api } = await freshApi();
    const err = (await api.versionDSL("wf1", 9).catch((e: unknown) => e)) as {
      message: string;
      code?: string;
      kind?: string;
    };
    expect(err.code).toBe("NOT_FOUND");
    expect(err.kind).toBe("ValidationError");
    expect(err.message).toBe('工作流版本 "wf1/9" 不存在');
  });
});

// ---- 迭代 11:restoreBackup 返回形状(新增 agent_configs_unknown_ids) ----
describe("restoreBackup 返回形状", () => {
  const payload = {
    workflows_restored: 1,
    workflows_skipped: 0,
    workflows_invalid: 0,
    agent_configs_restored: 1,
    // 语义已扩大:结构非法 **或** 本机无此 Agent
    agent_configs_invalid: 2,
    agent_configs_unknown_ids: ["ghost-agent"],
  };

  it("浏览器分支透传新字段", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, text: async () => JSON.stringify(payload) }),
    );
    const { api } = await freshApi();
    const r = await api.restoreBackup({});
    expect(r.agent_configs_unknown_ids).toEqual(["ghost-agent"]);
    expect(r.agent_configs_invalid).toBe(2);
    expect(fetch).toHaveBeenCalledWith("/api/backup/restore", expect.anything());
  });

  it("桌面分支返回同样的形状(含新字段)", async () => {
    callByID.mockResolvedValue([]); // desktop
    stRestoreBackup.mockResolvedValue(payload);
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const { api } = await freshApi();
    const r = await api.restoreBackup({});
    expect(r.agent_configs_unknown_ids).toEqual(["ghost-agent"]);
    expect(r.agent_configs_invalid).toBe(2);
    expect(stRestoreBackup).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("后端未返回新字段时也不报错(可选字段)", async () => {
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        text: async () => JSON.stringify({ ...payload, agent_configs_unknown_ids: undefined }),
      }),
    );
    const { api } = await freshApi();
    const r = await api.restoreBackup({});
    expect(r.agent_configs_unknown_ids).toBeUndefined();
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
      "workflowservice": [
        "Delete",
        "Duplicate",
        "ExportYAML",
        "Get",
        "ImportYAML",
        "List",
        "Save",
        "SavePayload",
        "SetEnabled",
        "Validate",
        "VersionDSL",
        "Versions",
      ],
      "executionservice": [
        "AllEvents",
        "Artifacts",
        "Cancel",
        "Delete",
        "Events",
        "Export",
        "Get",
        "List",
        "Nodes",
        "ProvideInput",
        "RetryNode",
        "Run",
        "Stats",
      ],
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

// ---- 迭代 22:双模式一致性契约 ----
//
// 同一个语义在两种模式下必须给出**同一个**结果:桌面模式走 Wails 绑定
// (IPC),服务器模式走 REST + SSE。冒烟脚本只测服务器模式,抓不到这类
// 差异 —— 下面这些用例把两条分支钉在同一份契约上。
//
// 工作流的顶层键严格化此前只在服务器模式生效:桌面模式走模型级 Save,
// 未知顶层键在反序列化时被 json.Unmarshal 静默丢弃 → 同一个非法载荷,
// 桌面模式"保存成功",服务器模式 400。

// badWorkflowPayload 含未知顶层键 timeout_seconds。
const badWorkflowPayload = {
  id: "wf-bad",
  name: "非法",
  description: "",
  version: 1,
  enabled: true,
  nodes: [],
  edges: [],
  settings: {},
  // 未知顶层键(后端必须拒掉)
  timeout_seconds: 30,
} as unknown as Workflow;

// goodWorkflowPayload 是一份合法载荷。
const goodWorkflowPayload: Workflow = {
  id: "wf-good",
  name: "合法",
  description: "",
  version: 1,
  enabled: true,
  nodes: [],
  edges: [],
  settings: {},
};

describe("双分支契约:保存(顶层键严格化)", () => {
  it("桌面分支与浏览器分支对同一份非法 payload 都报错,且给出同一个 code", async () => {
    // ---- 桌面分支 ----
    callByID.mockResolvedValue([]); // desktop
    wfSavePayload.mockRejectedValue(
      new Error(
        "ValidationError(UNKNOWN_FIELD): 未知的顶层字段 timeout_seconds(允许: id, name, …)",
      ),
    );
    const { api, ApiError } = await freshApi();
    const deskErr = (await api
      .saveWorkflow(badWorkflowPayload)
      .catch((e: unknown) => e)) as InstanceType<typeof ApiError>;
    expect(deskErr, "桌面分支必须拒绝未知顶层键").toBeInstanceOf(ApiError);
    expect(deskErr.code).toBe("UNKNOWN_FIELD");
    expect(deskErr.kind).toBe("ValidationError");
    // 前缀必须被剥掉(与服务器模式给用户看的是同一句话)
    expect(deskErr.message).toContain("timeout_seconds");
    expect(deskErr.message).not.toContain("ValidationError(");

    // ---- 浏览器分支 ----
    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 400,
        text: async () =>
          JSON.stringify({
            error:
              "ValidationError(UNKNOWN_FIELD): 未知的顶层字段 timeout_seconds(允许: id, name, …)",
            code: "UNKNOWN_FIELD",
            kind: "ValidationError",
          }),
      }),
    );
    const { api: webApi } = await freshApi();
    const webErr = (await webApi
      .saveWorkflow(badWorkflowPayload)
      .catch((e: unknown) => e)) as InstanceType<typeof ApiError>;
    expect(webErr.code).toBe("UNKNOWN_FIELD");
    // 关键断言:两种模式给用户**同一句话**
    expect(webErr.message).toBe(deskErr.message);
  });

  it("桌面分支把原始载荷(JSON 文本)交给后端,而不是反序列化后的模型", async () => {
    callByID.mockResolvedValue([]);
    wfSavePayload.mockResolvedValue({ id: "wf-good", name: "合法" });
    const { api } = await freshApi();
    await api.saveWorkflow(goodWorkflowPayload);
    expect(wfSavePayload).toHaveBeenCalledTimes(1);
    const arg = wfSavePayload.mock.calls[0][0] as string;
    expect(typeof arg).toBe("string");
    // 载荷里必须保留原始键(后端据此判定未知顶层键)
    expect(JSON.parse(arg).id).toBe("wf-good");
  });

  it("合法载荷在两个分支都保存成功", async () => {
    callByID.mockResolvedValue([]);
    wfSavePayload.mockResolvedValue({ id: "wf-good", name: "合法" });
    const { api } = await freshApi();
    await expect(api.saveWorkflow(goodWorkflowPayload)).resolves.toMatchObject({
      id: "wf-good",
    });

    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        text: async () => JSON.stringify({ id: "wf-good", name: "合法" }),
      }),
    );
    const { api: webApi } = await freshApi();
    await expect(webApi.saveWorkflow(goodWorkflowPayload)).resolves.toMatchObject({
      id: "wf-good",
    });
  });
});

describe("双分支契约:校验警告(warnings)", () => {
  it("两个分支都把保存时的校验警告带回(此前只进后端日志)", async () => {
    const warnings = [
      { code: "DEPRECATED_NODE_CONFIG", message: "节点 c 的 expression 配置键已废弃" },
    ];

    callByID.mockResolvedValue([]);
    wfSavePayload.mockResolvedValue({ id: "wf-good", name: "合法", warnings });
    const { api } = await freshApi();
    const desk = await api.saveWorkflow(goodWorkflowPayload);
    expect(desk.warnings).toEqual(warnings);

    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        text: async () => JSON.stringify({ id: "wf-good", name: "合法", warnings }),
      }),
    );
    const { api: webApi } = await freshApi();
    const web = await webApi.saveWorkflow(goodWorkflowPayload);
    expect(web.warnings).toEqual(warnings);
  });

  it("两个分支都把运行时的校验警告带回", async () => {
    const warnings = [
      { code: "DEPRECATED_NODE_CONFIG", message: "节点 c 的 expression 配置键已废弃" },
    ];

    callByID.mockResolvedValue([]);
    exRun.mockResolvedValue({ id: "exec-1", workflow_id: "wf-good", state: "RUNNING", warnings });
    const { api } = await freshApi();
    const desk = await api.runWorkflow("wf-good", "t");
    expect(desk.warnings).toEqual(warnings);

    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        text: async () =>
          JSON.stringify({ id: "exec-1", workflow_id: "wf-good", state: "RUNNING", warnings }),
      }),
    );
    const { api: webApi } = await freshApi();
    const web = await webApi.runWorkflow("wf-good", "t");
    expect(web.warnings).toEqual(warnings);
  });
});

describe("双分支契约:导入的 ignored_keys", () => {
  it("两个分支都带回被忽略的键(桌面模式此前完全没有)", async () => {
    const ignored = ["workflow.settings"];

    callByID.mockResolvedValue([]);
    wfImportYAML.mockResolvedValue({ id: "wf-imp", name: "导入", ignored_keys: ignored });
    const { api } = await freshApi();
    const desk = await api.importYAML("workflow:\n  id: wf-imp\n");
    expect(desk.ignored_keys).toEqual(ignored);

    callByID.mockRejectedValue(new Error("no ipc"));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        text: async () => JSON.stringify({ id: "wf-imp", name: "导入", ignored_keys: ignored }),
      }),
    );
    const { api: webApi } = await freshApi();
    const web = await webApi.importYAML("workflow:\n  id: wf-imp\n");
    expect(web.ignored_keys).toEqual(ignored);
  });

  it("后端没给 ignored_keys 时两个分支都兜底为空数组", async () => {
    callByID.mockResolvedValue([]);
    wfImportYAML.mockResolvedValue({ id: "wf-imp", name: "导入" });
    const { api } = await freshApi();
    await expect(api.importYAML("x")).resolves.toMatchObject({ ignored_keys: [] });
  });
});

describe("桌面模式的错误处理", () => {
  it("IPC reject 的原始文本被转成带 code/kind 的 ApiError", async () => {
    callByID.mockResolvedValue([]);
    wfSavePayload.mockRejectedValue(
      new Error("PermissionError(PERMISSION_DENIED): agent \"x\" 无权执行 git.push"),
    );
    const { api, ApiError } = await freshApi();
    const err = (await api
      .saveWorkflow(goodWorkflowPayload)
      .catch((e: unknown) => e)) as InstanceType<typeof ApiError>;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("PERMISSION_DENIED");
    expect(err.kind).toBe("PermissionError");
    expect(err.message).toBe('agent "x" 无权执行 git.push');
  });

  it("没有 Kind(CODE) 前缀的 IPC 错误也变成 ApiError(不抛裸字符串)", async () => {
    callByID.mockResolvedValue([]);
    wfSavePayload.mockRejectedValue("boom");
    const { api, ApiError } = await freshApi();
    const err = (await api
      .saveWorkflow(goodWorkflowPayload)
      .catch((e: unknown) => e)) as InstanceType<typeof ApiError>;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.message).toBe("boom");
    expect(err.code).toBeUndefined();
  });
});

// ---- 迭代 22:错误码全集守卫(防漂移) ----
//
// 后端新增一个错误码而前端没补提示 → 用户只能看到裸的 "HTTP 400"。
// 本用例扫描后端 Go 源码里的**全部** code,与前端 CODE_HINTS 求差集:
//
//   - 后端有、前端没有 → 红(必须补一句人话提示)
//   - 前端有、后端没有 → 红(键写错或后端已删)
//     此前就有一个这样的键:"DENIED" —— 后端真实码是 PERMISSION_DENIED,
//     这条权限提示从来没生效过。
//
// 扫描规则(刻意保守,只认"确凿是错误码"的写法):
//   1. model.NewError(kind, "CODE", …)      —— 结构化错误码
//   2. Code: "CODE"                          —— model.Error 字面量
//   3. "code": "CODE" / body["code"] = "…"   —— API 层的错误体字段
//   4. const x = "CODE"                      —— 具名错误码常量
//   5. workflow/validator 包里的全大写字面量  —— 校验期错误码
async function collectGoFiles(dir: string, out: string[] = []): Promise<string[]> {
  const fs = await import("node:fs");
  const path = await import("node:path");
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (
      entry.name.startsWith(".") ||
      entry.name === "node_modules" ||
      entry.name === "build" ||
      entry.name === "bin" ||
      entry.name === "frontend" ||
      entry.name === "dist"
    ) {
      continue;
    }
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) await collectGoFiles(p, out);
    else if (entry.name.endsWith(".go") && !entry.name.endsWith("_test.go")) out.push(p);
  }
  return out;
}

async function collectBackendCodes(): Promise<Set<string>> {
  const fs = await import("node:fs");
  const path = await import("node:path");
  const codes = new Set<string>();
  // frontend/src/lib → 仓库根
  const root = path.join(import.meta.dirname, "..", "..", "..");
  for (const file of await collectGoFiles(root)) {
    const src = fs.readFileSync(file, "utf8");
    const isValidator = file.includes(`${path.sep}workflow${path.sep}validator${path.sep}`);
    const add = (re: RegExp) => {
      for (const m of src.matchAll(re)) codes.add(m[1]);
    };
    add(/model\.NewError\(\s*[^,()]+,\s*"([A-Z][A-Z0-9_]*)"/g);
    add(/\bCode:\s*"([A-Z][A-Z0-9_]*)"/g);
    add(/"code":\s*"([A-Z][A-Z0-9_]*)"/g);
    add(/body\["code"\]\s*=\s*"([A-Z][A-Z0-9_]*)"/g);
    add(/const\s+\w+\s*=\s*"([A-Z][A-Z0-9_]{3,})"/g);
    if (isValidator) add(/"([A-Z][A-Z0-9_]{3,})"/g);
  }
  return codes;
}

async function collectCodeHints(): Promise<Set<string>> {
  const fs = await import("node:fs");
  const path = await import("node:path");
  const src = fs.readFileSync(path.join(import.meta.dirname, "api.ts"), "utf8");
  const block = src.match(/const CODE_HINTS: Record<string, string> = \{([\s\S]*?)\n\};/);
  if (!block) throw new Error("api.ts 里找不到 CODE_HINTS 表");
  return new Set(
    [...block[1].matchAll(/^\s*([A-Z][A-Z0-9_]*):/gm)].map((m) => m[1]),
  );
}

describe("错误码全集守卫(后端 code ↔ 前端 CODE_HINTS)", () => {
  it("后端的每个错误码都有中文提示", async () => {
    const backend = await collectBackendCodes();
    const hints = await collectCodeHints();
    const missing = [...backend].filter((c) => !hints.has(c)).sort();
    expect(
      missing,
      "后端存在但前端 CODE_HINTS 没有的错误码(用户只能看到裸 HTTP 码)",
    ).toEqual([]);
  });

  it("前端没有为不存在的错误码留提示(键写错/后端已删)", async () => {
    const backend = await collectBackendCodes();
    const hints = await collectCodeHints();
    const stale = [...hints].filter((c) => !backend.has(c)).sort();
    expect(stale, "CODE_HINTS 里存在后端没有的错误码").toEqual([]);
  });

  it("扫描确实扫到了后端的错误码(守卫本身不是恒真的)", async () => {
    const backend = await collectBackendCodes();
    // 抽样几个已知的码:来自 model.NewError / 校验器 / 权限包
    for (const code of [
      "UNKNOWN_FIELD",
      "WORKFLOW_INVALID",
      "INVALID_JSON",
      "PERMISSION_DENIED",
      "INTERNAL",
      "DSL_MISPLACED_KEY",
      "MISSING_ID",
      "UNKNOWN_NODE_TYPE",
    ]) {
      expect(backend.has(code), `后端应包含错误码 ${code}`).toBe(true);
    }
    // 规模下限:后端现有 60+ 个错误码
    expect(backend.size).toBeGreaterThan(50);
  });
});

// ---- 迭代 22:校验警告的展示文案 ----
describe("describeWarnings", () => {
  it("没有警告时返回空串(调用方据此决定要不要提示)", async () => {
    const { describeWarnings } = await freshApi();
    expect(describeWarnings(undefined)).toBe("");
    expect(describeWarnings([])).toBe("");
  });

  it("把警告拼成可读文案(带节点与错误码,便于追溯)", async () => {
    const { describeWarnings } = await freshApi();
    const text = describeWarnings([
      {
        code: "DEPRECATED_NODE_CONFIG",
        node: "c",
        message: "节点 c 的配置键 expression 已废弃",
      },
      { code: "UNKNOWN_NODE_CONFIG", message: "节点含未识别的配置键" },
    ]);
    expect(text).toContain("节点 c");
    expect(text).toContain("expression 已废弃");
    expect(text).toContain("DEPRECATED_NODE_CONFIG");
    expect(text).toContain("UNKNOWN_NODE_CONFIG");
  });
});

// ---- 迭代 22:显式 limit 参数的上限(两种模式同一口径) ----
//
// 服务端 api.queryInt 把 limit 收敛到 maxQueryLimit=1000(防整数溢出成
// 负数后被仓储层当成"不限制")。桌面分支此前不设上限:同一个请求,服务器
// 模式返回 1000 条,桌面模式可能把整表拉出来。
describe("limit 上限(双模式一致)", () => {
  it("桌面分支把超大的 limit 收敛到 1000", async () => {
    callByID.mockResolvedValue([]);
    exAllEvents.mockResolvedValue([]);
    const { api } = await freshApi();
    await api.allEvents("?type=node.started&limit=999999");
    expect(exAllEvents).toHaveBeenCalledWith("node.started", 1000);
  });

  it("limit 缺省与非正数沿用服务端口径(0 = 不限制)", async () => {
    callByID.mockResolvedValue([]);
    exAllEvents.mockResolvedValue([]);
    const { api } = await freshApi();
    await api.allEvents("?type=node.started");
    expect(exAllEvents).toHaveBeenCalledWith("node.started", 200);
  });
});

// ---- 迭代 13:E —— 设置接口"省略 = 不修改"的契约(双模式) ----
//
// 后端 POST /api/settings 的 git_working_dir 改成 *string:省略(nil)=
// 不修改,显式 ""= 清除。此前 body 是普通 string,省略的字段被解成零值
// "",而应用层把 "" 当作"清除" —— 一个只想改日志级别的请求会顺手把用户
// 的 git 工作目录清空。
//
// 桌面分支同理:绑定签名是 (gitWorkingDir: string, ...),没有 nil 的
// 表达力 → 省略时必须回填当前值,不能下发 ""。
describe("双分支契约:设置更新省略 git_working_dir", () => {
  const info = {
    data_dir: "/data",
    git_working_dir: "/repo",
    log_level: "info",
    allow_script_nodes: false,
  };

  it("桌面分支:省略 git_working_dir 时回填当前值(不得下发空串)", async () => {
    callByID.mockResolvedValue([]); // desktop
    stInfo.mockResolvedValue(info);
    stUpdate.mockResolvedValue(info);
    const { api } = await freshApi();
    await api.updateSettings({ log_level: "debug" });
    expect(stUpdate).toHaveBeenCalledWith("/repo", "debug", null);
  });

  it("桌面分支:显式给了 git_working_dir 就用它", async () => {
    callByID.mockResolvedValue([]); // desktop
    stInfo.mockResolvedValue(info);
    stUpdate.mockResolvedValue(info);
    const { api } = await freshApi();
    await api.updateSettings({ git_working_dir: "/other" });
    expect(stUpdate).toHaveBeenCalledWith("/other", "", null);
  });

  it("桌面分支:显式空串 = 清除(原样下发,不回填)", async () => {
    callByID.mockResolvedValue([]); // desktop
    stInfo.mockResolvedValue(info);
    stUpdate.mockResolvedValue(info);
    const { api } = await freshApi();
    await api.updateSettings({ git_working_dir: "" });
    expect(stUpdate).toHaveBeenCalledWith("", "", null);
  });

  it("web 分支:省略的字段不进入请求体(不得补成空串)", async () => {
    callByID.mockRejectedValue(new Error("not in wails")); // web
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(info),
    });
    vi.stubGlobal("fetch", fetchMock);
    const { api } = await freshApi();
    await api.updateSettings({ log_level: "debug" });
    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect(body).toEqual({ log_level: "debug" });
    expect("git_working_dir" in body).toBe(false);
  });
});
