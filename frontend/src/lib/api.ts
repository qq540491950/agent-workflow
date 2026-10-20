// 统一服务层:桌面模式走 Wails bindings(IPC),浏览器模式走 HTTP+SSE。
// 页面只依赖本模块,不感知底层传输方式。
import type {
  AgentConfig,
  AgentInfo,
  Artifact,
  Execution,
  ExecutionNode,
  PermissionPolicy,
  SkillDTO,
  UIEvent,
  ValidationResult,
  Workflow,
} from "./types";

// WorkflowService.List 的绑定 ID(wails3 generate 生成,
// 见 frontend/bindings/agentworkflow/app/application/workflowservice.ts);
// 仅用于模式探测,与生成代码保持同步。
const PROBE_BINDING_ID = 1924388702;

// 传输模式探测:调用一次真实绑定,500ms 内成功 → 桌面模式(IPC),
// 否则回退 HTTP+SSE(Wails runtime 注入时机晚于模块加载,不能用存在性判断)。
let modePromise: Promise<"desktop" | "web"> | null = null;

export function detectMode(): Promise<"desktop" | "web"> {
  if (!modePromise) {
    modePromise = (async () => {
      try {
        const rt = await import("@wailsio/runtime");
        const probe = rt.Call.ByID(PROBE_BINDING_ID);
        const winner = await Promise.race([
          probe.then(
            () => "desktop" as const,
            () => "web" as const,
          ),
          new Promise<"web">((r) => setTimeout(() => r("web"), 500)),
        ]);
        return winner;
      } catch {
        return "web";
      }
    })();
  }
  return modePromise;
}

// Wails bindings 返回生成器类型(带 null),统一断言为本项目的领域类型。
function nn<T>(v: unknown): T {
  return (v ?? null) as T;
}

// 后端列表接口在 IPC/SPA fallback 异常路径下可能返回 null 或对象(被解析为 HTML 兜底页)而不是数组,
// 统一在这里转成空数组,避免上游 .map 崩溃。
export function asArray<T>(v: unknown): T[] {
  return Array.isArray(v) ? (v as T[]) : [];
}

// ApiError 携带后端的结构化错误信息(code/kind/status)。
// 此前 http() 只取 error 文本,把 code/kind 丢掉:403 权限不足与 400
// 参数错误在 UI 上无法区分,用户只能看到一句裸消息。
export class ApiError extends Error {
  readonly code?: string;
  readonly kind?: string;
  readonly status: number;

  constructor(message: string, status: number, code?: string, kind?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.kind = kind;
  }
}

// 后端错误码 → 用户可读提示。
// 后端契约:4xx 一律带 error + code + kind(api/server.go wrap);
// 但错误体也可能只有 code 没有 error(旧版后端/反向代理改写),
// 此时不能退化成裸的 "HTTP 413" —— 用户看不懂 413 是什么。
// 码表与后端 model.Error 的 code 取值保持同步(前端只是兜底文案,
// 后端 error 文本在场时优先用后端的)。
const CODE_HINTS: Record<string, string> = {
  PAYLOAD_TOO_LARGE: "内容过大(请求体超过后端上限)",
  METHOD_NOT_ALLOWED: "请求方法不被允许",
  INVALID_JSON: "请求内容不是合法 JSON",
  BAD_REQUEST: "请求参数不合法",
  NOT_FOUND: "资源不存在",
  INVALID_LOG_LEVEL: "日志级别非法(可选 debug|info|warn|error)",
  WORKFLOW_DISABLED: "工作流已禁用,先启用再运行",
  STATE_CONFLICT: "状态冲突(可能已被其他操作改变),请刷新后重试",
  DENIED: "权限不足",
  // 第四/五轮新增的码(DSL 严格化 + 未知键上报)。
  // DSL_UNKNOWN_KEY / DSL_MISPLACED_KEY 是导入路径的硬错误(400),
  // 未知顶层键走 UNKNOWN_FIELD(400)。
  DSL_UNKNOWN_KEY: "DSL 含未知顶层键(检查拼写与缩进)",
  DSL_MISPLACED_KEY: "DSL 段放错了层级(该设置不会生效)",
  UNKNOWN_FIELD: "请求含未知顶层字段(该字段不会生效)",
  // 以下三个是**校验期**的 code(出现在校验结果的 errors/warnings 里,
  // 未必走 HTTP 错误体);留在表里是为了将来后端把它们升级为错误码时
  // 前端不必再改。
  UNKNOWN_NODE_CONFIG: "节点含未识别的配置键(该设置不会生效)",
  DEPRECATED_NODE_CONFIG: "节点配置键已废弃(不再按该键执行)",
  INVALID_NODE_ENABLED: "节点的 enabled 必须是布尔值(true=启用,false=运行期跳过)",
};

// 状态码兜底提示:错误体完全没有 code 时(向后兼容路径)仍给一句人话。
const STATUS_HINTS: Record<number, string> = {
  400: "请求参数不合法",
  403: "权限不足",
  404: "资源不存在",
  405: "请求方法不被允许",
  413: "内容过大(请求体超过后端上限)",
};

// 后端 model.Error.Error() 的文本格式是 "Kind(CODE): message"
// (带节点时 "Kind(CODE)[node=n1]: message")。直接把 Kind(CODE) 前缀
// 弹进 toast 没有信息量,这里剥掉只留人话;code 仍保留在 ApiError.code。
const KIND_PREFIX_RE = /^[A-Za-z]+\(([A-Z][A-Z0-9_]*)\)(?:\[[^\]]*\])?:\s*/;

/** 剥掉后端错误文本的 "Kind(CODE): " 前缀;非该形态原样返回。 */
export function stripErrorPrefix(text: string): string {
  const m = text.match(KIND_PREFIX_RE);
  return m ? text.slice(m[0].length) : text;
}

/**
 * 统一的错误体 → ApiError。
 *
 * 兼容三种形状(必须向后兼容,任何一种都不能让前端崩):
 *   1. 完整结构化:{error, code, kind} → 用 error 文本(剥前缀)
 *   2. 只有 error:{error} → 用 error 文本,code/kind 为 undefined
 *   3. 非 JSON / 无 error:HTML 兜底页、代理改写 → "HTTP {status}" + 提示
 */
function buildApiError(status: number, body: unknown): ApiError {
  const d = (body && typeof body === "object" ? body : {}) as {
    error?: unknown;
    code?: unknown;
    kind?: unknown;
  };
  const code = typeof d.code === "string" && d.code ? d.code : undefined;
  const kind = typeof d.kind === "string" && d.kind ? d.kind : undefined;
  const raw = typeof d.error === "string" && d.error ? d.error : "";
  let message = raw ? stripErrorPrefix(raw) : "";
  if (!message) {
    // hasOwnProperty 守卫:code 来自服务端,不能让它命中 Object 原型链
    // ("constructor"/"toString" 会取到函数而不是提示文案)。
    const hint =
      (code && Object.prototype.hasOwnProperty.call(CODE_HINTS, code)
        ? CODE_HINTS[code]
        : "") || STATUS_HINTS[status];
    message = hint ? `HTTP ${status}: ${hint}` : `HTTP ${status}`;
  }
  return new ApiError(message, status, code, kind);
}

// text 端点(DSL 导出/下载)用:后端在错误时写的是 JSON 错误体,
// 直接 res.text() 会把 {"error":...,"code":"NOT_FOUND"} 当成 DSL 内容
// 弹进对话框或写进 .yaml 文件(fix: 绕过 http() 且不检查 res.ok)。
async function httpText(path: string): Promise<string> {
  const res = await fetch(path);
  const text = await res.text();
  if (!res.ok) {
    // 错误体是 JSON(见 api/server.go wrap),解析出可读消息与 code;
    // 解析不出(纯文本/HTML)时回退原始 body 与状态码。
    let body: unknown;
    try {
      body = JSON.parse(text);
    } catch {
      body = undefined;
    }
    // 非 JSON 错误体(HTML 兜底页等)保留原始文本,便于定位;
    // JSON 错误体交给 buildApiError 统一处理(剥 Kind(CODE) 前缀 + code 提示)。
    if (body === undefined) {
      throw new ApiError(text || `HTTP ${res.status}`, res.status);
    }
    throw buildApiError(res.status, body);
  }
  return text;
}

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  const text = await res.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = text;
  }
  if (!res.ok) {
    throw buildApiError(res.status, data);
  }
  return data as T;
}

// Wails bindings 按需加载(仅桌面模式)。
async function wailsBindings() {
  const [wf, ex, ag, sk, gt, st] = await Promise.all([
    import("@bindings/agentworkflow/app/application/workflowservice.js"),
    import("@bindings/agentworkflow/app/application/executionservice.js"),
    import("@bindings/agentworkflow/app/application/agentservice.js"),
    import("@bindings/agentworkflow/app/application/skillservice.js"),
    import("@bindings/agentworkflow/app/application/gitservice.js"),
    import("@bindings/agentworkflow/app/application/settingsservice.js"),
  ]);
  return { wf, ex, ag, sk, gt, st };
}

export const api = {
  
  // ---- Workflows ----
  async listWorkflows(): Promise<Workflow[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.List());
    return http("/api/workflows");
  },
  async getWorkflow(id: string): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.Get(id));
    return http(`/api/workflows/${id}`);
  },
  async saveWorkflow(wf: Workflow): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.Save(wf as unknown as never));
    return http("/api/workflows", { method: "POST", body: JSON.stringify(wf) });
  },
  async deleteWorkflow(id: string): Promise<void> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).wf.Delete(id);
    return http(`/api/workflows/${id}`, { method: "DELETE" });
  },
  async duplicateWorkflow(id: string): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.Duplicate(id));
    return http(`/api/workflows/${id}/duplicate`, { method: "POST" });
  },
  async setEnabled(id: string, enabled: boolean): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.SetEnabled(id, enabled));
    return http(`/api/workflows/${id}/enable?value=${enabled}`, { method: "POST" });
  },
  async workflowVersions(id: string): Promise<number[]> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).wf.Versions(id));
    }
    return http(`/api/workflows/${id}/versions`);
  },
  async versionDSL(id: string, version: number): Promise<string> {
    if ((await detectMode()) === "desktop") {
      return (await wailsBindings()).wf.VersionDSL(id, version);
    }
    return httpText(`/api/workflows/${id}/versions/${version}`);
  },
  async importYAML(content: string): Promise<Workflow> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).wf.ImportYAML(content));
    }
    return http("/api/workflows/import", {
      method: "POST",
      body: content,
      headers: { "Content-Type": "application/yaml" },
    });
  },
  async validateWorkflow(wf: Workflow): Promise<ValidationResult> {
    if ((await detectMode()) === "desktop") {
      return nn(
        await (await wailsBindings()).wf.Validate(wf as unknown as never),
      );
    }
    return http("/api/workflows/validate", {
      method: "POST",
      body: JSON.stringify(wf),
    });
  },
  async exportYAML(id: string): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).wf.ExportYAML(id);
    return httpText(`/api/workflows/${id}/export`);
  },

  // ---- Executions ----
  async runWorkflow(
    id: string,
    task: string,
    variables?: Record<string, unknown>,
  ): Promise<Execution> {
    if ((await detectMode()) === "desktop")
      return nn(
        await (await wailsBindings()).ex.Run(id, task, variables ?? {}),
      );
    return http(`/api/workflows/${id}/run`, {
      method: "POST",
      body: JSON.stringify({ task, variables }),
    });
  },
  async listExecutions(workflowID = "", limit = 50): Promise<Execution[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.List(workflowID, limit));
    const q = workflowID
      ? `?workflow_id=${encodeURIComponent(workflowID)}&limit=${limit}`
      : `?limit=${limit}`;
    return http(`/api/executions${q}`);
  },
  async getExecution(id: string): Promise<Execution> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Get(id));
    return http(`/api/executions/${id}`);
  },
  async executionNodes(id: string): Promise<ExecutionNode[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Nodes(id));
    return http(`/api/executions/${id}/nodes`);
  },
  async executionEvents(id: string): Promise<Record<string, unknown>[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Events(id));
    return http(`/api/executions/${id}/events`);
  },
  async executionArtifacts(id: string): Promise<Artifact[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Artifacts(id));
    return http(`/api/executions/${id}/artifacts`);
  },
  async provideInput(
    id: string,
    response: Record<string, unknown>,
  ): Promise<Execution> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.ProvideInput(id, response));
    return http(`/api/executions/${id}/input`, {
      method: "POST",
      body: JSON.stringify(response),
    });
  },
  async deleteExecution(id: string): Promise<void> {
    if ((await detectMode()) === "desktop") {
      return (await wailsBindings()).ex.Delete(id);
    }
    return http(`/api/executions/${id}`, { method: "DELETE" });
  },
  async retryExecution(id: string, skip: boolean): Promise<Execution> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ex.RetryNode(id, skip));
    }
    return http(`/api/executions/${id}/retry`, {
      method: "POST",
      body: JSON.stringify({ skip }),
    });
  },
  async exportExecutionURL(id: string): Promise<string> {
    // 浏览器与桌面均用下载链接(桌面由资产处理器旁路到同源)
    return `/api/executions/${id}/export`;
  },
  async cancelExecution(id: string): Promise<void> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).ex.Cancel(id);
    return http(`/api/executions/${id}/cancel`, { method: "POST" });
  },

  // ---- Agents / Skills ----
  async listAgents(): Promise<AgentInfo[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ag.List());
    return http("/api/agents");
  },
  async getAgentConfig(id: string): Promise<AgentConfig> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ag.GetConfig(id));
    }
    return http(`/api/agents/${id}/config`);
  },
  async updateAgentConfig(id: string, cfg: AgentConfig): Promise<AgentConfig> {
    if ((await detectMode()) === "desktop") {
      return nn(
        await (await wailsBindings()).ag.UpdateConfig(id, cfg as unknown as never),
      );
    }
    return http(`/api/agents/${id}/config`, {
      method: "PUT",
      body: JSON.stringify(cfg),
    });
  },
  async resetAgentConfig(id: string): Promise<AgentConfig> {
    // 后端 ResetConfig 与 POST /api/agents/{id}/reset 都已存在,但两个模式
    // 都没封装、UI 也没入口 —— 能力空洞(配错配置后无法恢复默认)。
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ag.ResetConfig(id));
    }
    return http(`/api/agents/${id}/reset`, { method: "POST" });
  },
  async testAgent(id: string): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).ag.Test(id);
    const r = await http<{ output: string }>(`/api/agents/${id}/test`, {
      method: "POST",
    });
    // 字段缺失(异常响应形状)时兜底为空串:调用方会直接 split()/slice(),
    // undefined 会变成 TypeError 把整页打挂。
    return r?.output ?? "";
  },
  async setAgentPermission(id: string, p: PermissionPolicy): Promise<void> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).ag.SetPermission(id, p);
    return http(`/api/agents/${id}/permission`, {
      method: "POST",
      body: JSON.stringify(p),
    });
  },
  async listSkills(): Promise<SkillDTO[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).sk.List());
    return http("/api/skills");
  },
  async setSkillEnabled(id: string, enabled: boolean): Promise<void> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).sk.SetEnabled(id, enabled));
    }
    return http(`/api/skills/${id}/enable`, {
      method: "POST",
      body: JSON.stringify({ enabled }),
    });
  },
  async testSkill(id: string): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).sk.Test(id);
    const r = await http<{ output: string }>(`/api/skills/${id}/test`, {
      method: "POST",
    });
    return r?.output ?? "";
  },

  async stats(): Promise<
    { workflow_id: string; name: string; total: number; completed: number; failed: number; waiting: number; running: number }[]
  > {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ex.Stats());
    }
    return http("/api/stats");
  },
  async copyYAML(id: string): Promise<string> {
    return this.exportYAML(id);
  },
  async allEvents(q: string): Promise<
    {
      seq: number;
      execution_id: string;
      node_id: string;
      type: string;
      data: Record<string, unknown>;
      created_at: string;
    }[]
  > {
    if ((await detectMode()) === "desktop") {
      const params = new URLSearchParams(q.startsWith("?") ? q : `?${q}`);
      return nn(
        await (await wailsBindings()).ex.AllEvents(
          params.get("type") ?? "",
          Number(params.get("limit")) || 200,
        ),
      );
    }
    return http(`/api/events/all${q}`);
  },

  // ---- Settings ----
  async isDesktopMode(): Promise<boolean> {
    return (await detectMode()) === "desktop";
  },
  async getSettings(): Promise<{
    data_dir: string;
    git_working_dir: string;
    log_level: string;
    allow_script_nodes: boolean;
  }> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).st.Info());
    }
    return http("/api/settings");
  },
  async updateSettings(patch: {
    git_working_dir?: string;
    log_level?: string;
    /** script 节点放行开关;缺省表示不修改(后端保持既有值) */
    allow_script_nodes?: boolean;
  }): Promise<{
    data_dir: string;
    git_working_dir: string;
    log_level: string;
    allow_script_nodes: boolean;
  }> {
    if ((await detectMode()) === "desktop") {
      return nn(
        await (await wailsBindings()).st.Update(
          patch.git_working_dir ?? "",
          patch.log_level ?? "",
          patch.allow_script_nodes ?? null,
        ),
      );
    }
    return http("/api/settings", { method: "POST", body: JSON.stringify(patch) });
  },

  backupURL(): string {
    return "/api/backup";
  },
  /**
   * 导出备份 JSON 文本。
   * 桌面模式:通过 IPC 取备份对象;浏览器(服务器)模式:走同源 /api/backup。
   * 此前只有桌面分支:浏览器模式下仍去调 Wails 绑定,绑定不可用 → 导出必然失败,
   * 而 Settings 页面的注释却写着"浏览器模式走同源下载链接"(约定 10 双分支一致)。
   */
  async exportBackupText(): Promise<string> {
    if ((await detectMode()) === "desktop") {
      const data = await (await wailsBindings()).st.ExportBackup();
      return JSON.stringify(data, null, 2);
    }
    return httpText("/api/backup");
  },
  async restoreBackup(backup: Record<string, unknown>): Promise<{
    workflows_restored: number;
    workflows_skipped: number;
    // 校验不通过而被拒绝的工作流(悬空边/空图等)与 ID 列表
    workflows_invalid: number;
    workflows_invalid_ids?: string[];
    agent_configs_restored: number;
    // 未落库的 Agent 配置:结构非法 **或** 本机不存在此 Agent
    // (后端 agent_configs_invalid 的语义已扩大,见 app/application)
    agent_configs_invalid: number;
    // 本机不存在的 Agent ID(配置无法生效,未落库)
    agent_configs_unknown_ids?: string[];
  }> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).st.RestoreBackup(backup as unknown as never));
    }
    return http("/api/backup/restore", {
      method: "POST",
      body: JSON.stringify(backup),
    });
  },

  async version(): Promise<Record<string, string>> {
    // 桌面模式下 /api 已通过 AssetHandler 挂载到 AssetServer,两种模式同源可用
    try {
      return await http("/api/version");
    } catch {
      return {};
    }
  },

  // ---- Git ----
  async gitStatus(): Promise<{
    branch: string;
    modified: string[];
    staged: string[];
    untracked: string[];
  }> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).gt.Status());
    return http("/api/git/status");
  },
  async gitDiff(staged = false): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).gt.Diff(staged);
    const r = await http<{ diff: string }>(`/api/git/diff?staged=${staged}`);
    return r?.diff ?? "";
  },
  async gitCommit(message: string): Promise<string> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).gt.Commit(message));
    }
    const r = await http<{ output: string }>(`/api/git/commit`, {
      method: "POST",
      body: JSON.stringify({ message }),
    });
    // 同 testAgent:output 缺失时兜底空串,调用方直接 split("\n")[0]。
    return r?.output ?? "";
  },
  async gitLog(limit = 10): Promise<
    { hash: string; author: string; date: string; subject: string }[]
  > {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).gt.Log(limit));
    return http(`/api/git/log?limit=${limit}`);
  },
};

// ---- 实时事件(统一订阅层) ----

type EventHandler = (ev: UIEvent) => void;
const handlers = new Set<EventHandler>();
let started = false;

export function subscribeEvents(h: EventHandler): () => void {
  handlers.add(h);
  ensureEventStream();
  return () => handlers.delete(h);
}

function dispatch(ev: UIEvent) {
  handlers.forEach((h) => h(ev));
}

async function ensureEventStream() {
  if (started || typeof window === "undefined") return;
  started = true;
  if ((await detectMode()) === "desktop") {
    import("@wailsio/runtime").then((runtime) => {
      runtime.Events.On("ui:event", (data: unknown) => {
        dispatch(data as UIEvent);
      });
    });
  } else {
    const es = new EventSource("/api/events");
    es.onmessage = (m) => {
      try {
        dispatch(JSON.parse(m.data) as UIEvent);
      } catch {
        // 忽略无法解析的事件
      }
    };
    es.onerror = () => {
      // 断线由 EventSource 自动重连
    };
  }
}
