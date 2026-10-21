// 统一服务层:桌面模式走 Wails bindings(IPC),浏览器模式走 HTTP+SSE。
// 页面只依赖本模块,不感知底层传输方式。
import { reportError } from "@/lib/errors";
import type {
  AgentConfig,
  AgentInfo,
  Artifact,
  Execution,
  ExecutionNode,
  PermissionPolicy,
  SkillDTO,
  UIEvent,
  ValidationError,
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

// MAX_QUERY_LIMIT 是显式 limit 参数的上限 —— 与服务端
// api.maxQueryLimit 同值(唯一来源:服务端常量)。
//
// 桌面分支此前不设上限:同一个请求在服务器模式被收敛到 1000 条,在桌面
// 模式却可能把整表拉出来(同一个语义,两种模式行为不同 —— 约定 10)。
const MAX_QUERY_LIMIT = 1000;

/**
 * 把 limit 收敛到服务端允许的范围(两种模式同一口径)。
 *
 * 口径与服务端 api.queryInt 一致:非正数 = 不限制(0),超上限收敛到
 * MAX_QUERY_LIMIT(不取模、不溢出)。
 */
function clampLimit(limit: number): number {
  if (!Number.isFinite(limit) || limit <= 0) return 0;
  return Math.min(Math.trunc(limit), MAX_QUERY_LIMIT);
}

// Wails bindings 返回生成器类型(带 null),统一断言为本项目的领域类型。
function nn<T>(v: unknown): T {
  return (v ?? null) as T;
}

// 后端列表接口在 IPC/SPA fallback 异常路径下可能返回 null 或对象(被解析为 HTML 兜底页)而不是数组,
// 统一在这里转成空数组,避免上游 .map 崩溃。
//
// **为什么"返回非数组"是静默降级而不是弹提示**(有意取舍,登记在
// docs/OPTIMIZATION.md 第十三节第 14 条):
//
//   1. **合法形态**:后端确实可能返回 null/对象 —— 例如"没有工作流"
//      时返回 null、IPC 绑定返回生成器类型。弹"错误"会把正常情况
//      报成故障(提示风暴),比空列表更糟。
//   2. **纯函数**:asArray 是**纯函数**,在它里面弹提示等于给 20+ 个
//      调用点各加一次副作用 —— 而调用点里有的在渲染路径上、有的在
//      事件回调里,提示时机无法统一。
//   3. **失败已经可见**:请求失败(网络/后端报错)走的是另一条路 ——
//      http() 抛 ApiError,调用点 catch 里有 reportError 提示。
//      asArray 只负责"拿到的东西不是数组"这一种情况。
//
// **残留边界(已知,不修)**:后端**契约漂移**(声明返回数组却返回了
// 对象)与"真的没有数据"在界面上无法区分 —— 用户看到的都是"暂无数据"。
// 彻底解决需要接口自描述(后端在响应里带一个"这是数组"的标记),
// 属于协议变更,不在本轮范围内。
//
// 守卫(约定 7):所有列表调用点必须先过 asArray 再使用 ——
// 见 src/test/list-guard.test.ts(逐调用点判定)。
export function asArray<T>(v: unknown): T[] {
  return Array.isArray(v) ? (v as T[]) : [];
}

/**
 * 附加 CLI 参数的**分词器**(shell 式)。
 *
 * 背景(迭代 20 / P2-1):Agents 页此前用 `value.split(" ")` 切分 ——
 * 用户填 `--header "Authorization: Bearer ghp_x"` 会被切成 3 段
 * (`"Authorization:` / `Bearer` / `ghp_x"`),CLI 收到的参数与用户写的
 * **不是一回事**(引号被切碎了,而引号里的空格本该属于同一个参数)。
 *
 * 参数不经 shell(exec 直接传 argv),所以引号只是"分组"的书写方式:
 * 引号内的空格属于同一个参数,引号本身不进 argv。
 *
 * 与后端的契约一致:agent/config.go 明确"已存值按原文保存(含引号),
 * 往返必须逐字节还原" —— 前端必须能**原样往返**用户写的参数:
 * `parseExtraArgs(formatExtraArgs(args))` 必须等于 `args`。
 */
export function parseExtraArgs(input: string): string[] {
  const out: string[] = [];
  let cur = "";
  // started 区分"当前 token 为空"与"还没有 token":
  // `--opt ""` 是一个参数(值为空串),而不是两个。
  let started = false;
  let quote: '"' | "'" | null = null;

  for (let i = 0; i < input.length; i++) {
    const ch = input[i];
    if (quote) {
      // 双引号内支持反斜杠转义(\" 与 \\);单引号内一切都是字面量。
      if (ch === "\\" && quote === '"' && i + 1 < input.length) {
        const next = input[i + 1];
        if (next === '"' || next === "\\") {
          cur += next;
          i++;
          continue;
        }
      }
      if (ch === quote) {
        quote = null;
        continue;
      }
      cur += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      started = true;
      continue;
    }
    if (ch === " " || ch === "\t" || ch === "\n" || ch === "\r") {
      if (started) {
        out.push(cur);
        cur = "";
        started = false;
      }
      continue;
    }
    cur += ch;
    started = true;
  }
  if (started) out.push(cur);
  // 空参数没有意义(会被原样传给 CLI),丢弃。
  return out.filter((a) => a !== "");
}

/** 把一个参数渲染成输入框里的写法(必要时加引号)。 */
function quoteExtraArg(arg: string): string {
  if (arg === "") return '""';
  if (!/[\s"'\\]/.test(arg)) return arg;
  return `"${arg.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

/**
 * 把参数数组还原成输入框里的一行文本(与 parseExtraArgs 互逆)。
 *
 * 不能用简单的 `join(" ")`:带空格的参数会被切碎,用户改一个字符
 * 再保存,参数就变了(往返不保真)。
 */
export function formatExtraArgs(args: string[]): string {
  return args.map(quoteExtraArg).join(" ");
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
//
// **全集**:后端可能返回的每一个 code 都必须在这里有一句人话。
// api.test.ts 的"错误码全集守卫"会扫描后端 Go 源码(workflow/**、api/**、
// app/**、permission/** 等)与本表求差集:后端新增 code 而这里没补 →
// 测试红(防漂移的关键)。
const CODE_HINTS: Record<string, string> = {
  // ---- HTTP / 载荷层 ----
  PAYLOAD_TOO_LARGE: "内容过大(请求体超过后端上限)",
  METHOD_NOT_ALLOWED: "请求方法不被允许",
  INVALID_JSON: "请求内容不是合法 JSON",
  BAD_REQUEST: "请求参数不合法",
  NOT_FOUND: "资源不存在",
  UNKNOWN_FIELD: "请求含未知顶层字段(该字段不会生效)",
  INTERNAL: "后端内部错误",
  // ---- 工作流 / 执行 ----
  WORKFLOW_INVALID: "工作流校验未通过(见错误详情)",
  WORKFLOW_DISABLED: "工作流已禁用,先启用再运行",
  STATE_CONFLICT: "状态冲突(可能已被其他操作改变),请刷新后重试",
  INVALID_RESUME: "当前执行状态不允许恢复(可能已在运行或已结束)",
  INVALID_RETRY: "当前执行状态不允许重试",
  INVALID_TRANSITION: "执行状态迁移非法",
  NO_FAILED_NODE: "没有失败的节点可重试",
  SUBWORKFLOW_CANCELLED: "父执行取消,子工作流已中止",
  // ---- Agent / 子进程 ----
  AGENT_CANCELLED: "Agent 执行被取消",
  AGENT_CONFIG_CORRUPT: "Agent 配置已损坏,无法解析",
  CLAUDE_CANCELLED: "Claude 进程被取消",
  CLAUDE_PROCESS_ERROR: "Claude 进程异常退出",
  CLAUDE_TIMEOUT: "Claude 执行超时",
  PI_CANCELLED: "Pi 进程被取消",
  PI_PROCESS_ERROR: "Pi 进程异常退出",
  PI_TIMEOUT: "Pi 执行超时",
  UNSUPPORTED_AGENT_SETTING: "该 Agent 设置不生效(请改用节点级 working_dir,或清空该字段)",
  INVALID_GIT_WORKING_DIR: "Git 工作目录不存在或不是目录(本次保存未生效,请改成有效目录)",
  // ---- 权限 / 持久化 ----
  PERMISSION_DENIED: "权限不足",
  PERMISSION_OVERRIDE_LOOSENS: "权限段试图放宽全局禁用(已按拒绝处理)",
  INVALID_LOG_LEVEL: "日志级别非法(可选 debug|info|warn|error)",
  DB_ERROR: "数据库操作失败",
  // ---- DSL ----
  DSL_UNKNOWN_KEY: "DSL 含未知顶层键(检查拼写与缩进)",
  DSL_MISPLACED_KEY: "DSL 段放错了层级(该设置不会生效)",
  DSL_PARSE_ERROR: "DSL 解析失败(检查 YAML 语法)",
  DSL_ENCODE_ERROR: "DSL 导出失败",
  // ---- Git ----
  GIT_COMMAND_FAILED: "Git 命令执行失败",
  GIT_INVALID_REF: "Git 引用非法",
  GIT_MISSING_ARG: "Git 缺少必要参数",
  GIT_UNSUPPORTED_OP: "Git 操作不被支持",
  // ---- 校验期(validator;出现在校验结果的 errors/warnings 里) ----
  AGENT_NOT_FOUND: "引用的 Agent 不存在",
  CYCLE_WITHOUT_CONDITION: "存在无条件的环路(会死循环)",
  DEPRECATED_NODE_CONFIG: "节点配置键已废弃(不再按该键执行)",
  DUPLICATE_NODE: "节点 ID 重复",
  EMPTY_WORKFLOW: "工作流为空(至少要有一个节点)",
  INVALID_EDGE: "连线非法(端点不存在或成环)",
  INVALID_EXPRESSION: "条件表达式非法",
  INVALID_GIT_OPERATION: "Git 节点的操作非法",
  INVALID_LOOP_LIMIT: "settings.on_loop_limit 非法(允许: fail|wait_user)",
  INVALID_NODE: "节点定义非法",
  INVALID_NODE_ENABLED: "节点的 enabled 必须是布尔值(true=启用,false=运行期跳过)",
  INVALID_PERMISSION_OVERRIDE: "权限覆盖段结构非法(已按拒绝处理)",
  ISOLATED_NODE: "存在孤立节点(没有任何连线)",
  LOOP_WITHOUT_MAX_ITERATIONS: "存在环但未设置 max_iterations(会死循环)",
  MISSING_AGENT: "agent 节点缺少 agent 配置",
  MISSING_COMMAND: "script 节点缺少 command 配置",
  MISSING_CONFIG: "节点缺少必需配置",
  MISSING_ID: "缺少 id",
  MISSING_MODE: "agent 节点缺少 mode 配置",
  MISSING_NAME: "缺少 name",
  MISSING_SKILL: "skill 节点缺少 skill 配置",
  MISSING_WORKFLOW_ID: "subworkflow 节点缺少 workflow_id 配置",
  MULTIPLE_START: "存在多个开始节点",
  NO_EXIT_PATH: "存在没有出口的节点(执行会卡住)",
  NO_START: "缺少开始节点",
  SELF_LOOP: "节点自环(非法)",
  SKILL_NOT_FOUND: "引用的 Skill 不存在",
  SUBWORKFLOW_NOT_FOUND: "引用的子工作流不存在",
  UNKNOWN_NODE_CONFIG: "节点含未识别的配置键(该设置不会生效)",
  UNKNOWN_NODE_TYPE: "未知的节点类型",
  UNREACHABLE_NODE: "存在不可达节点",
};

// 状态码兜底提示:错误体完全没有 code 时(向后兼容路径)仍给一句人话。
//
// 5xx 是迭代 30 补的:后端此前把**内部错误**(数据库锁死/Agent 崩溃/
// 超时)也报 400,现在改为 5xx(见 docs/api.md 的状态码映射表)——
// 5xx 的意思就是"不是你的问题,是服务出问题了",这句提示必须说清楚,
// 否则用户会以为自己填错了什么。
const STATUS_HINTS: Record<number, string> = {
  400: "请求参数不合法",
  403: "权限不足",
  404: "资源不存在",
  405: "请求方法不被允许",
  413: "内容过大(请求体超过后端上限)",
  500: "服务内部错误(不是你的操作有问题,请稍后重试或查看日志)",
  502: "后端调用外部服务失败(Agent/子进程异常)",
  503: "服务暂时不可用,请稍后重试",
  504: "后端调用外部服务超时",
};

// 后端 model.Error.Error() 的文本格式是 "Kind(CODE): message"
// (带节点时 "Kind(CODE)[node=n1]: message")。直接把 Kind(CODE) 前缀
// 弹进 toast 没有信息量,这里剥掉只留人话;code 仍保留在 ApiError.code。
const KIND_PREFIX_RE = /^([A-Za-z]+)\(([A-Z][A-Z0-9_]*)\)(?:\[[^\]]*\])?:\s*/;

// PANIC_TEXT_RE 识别 Wails 传回的 panic 文本。
//
// 形态:"<Go 包路径>.<方法>: panic: <panic 原文>"(Wails 的 CallError),
// 也兼容裸的 "panic: …"。命中即归一为 INTERNAL —— **不外泄** Go 包路径
// 与 panic 原文(泄漏的路径对用户没有意义,对开发者也定位不到真正的问题;
// 真正的原文在桌面端日志里)。
//
// 用词边界匹配,避免把 "Panic mode" 之类的普通词误判成 panic。
const PANIC_TEXT_RE = /\bpanic:\s*/i;

/** 剥掉后端错误文本的 "Kind(CODE): " 前缀;非该形态原样返回。 */
export function stripErrorPrefix(text: string): string {
  const m = text.match(KIND_PREFIX_RE);
  return m ? text.slice(m[0].length) : text;
}

/**
 * 解析后端错误文本的 "Kind(CODE): " 前缀。
 *
 * 桌面模式(Wails IPC)没有 HTTP 错误体 —— 后端的 model.Error 以纯文本
 * 形式 reject,只能从这段文本里还原 kind/code;服务器模式则由错误体直接
 * 给出。少了这层还原,同一个错误在两种模式下前端拿到的 code/kind 全丢
 * (约定 10:双分支必须给出一致的结构化信息)。
 */
export function parseErrorPrefix(text: string): {
  kind?: string;
  code?: string;
  message: string;
} {
  const m = text.match(KIND_PREFIX_RE);
  if (!m) return { message: text };
  return { kind: m[1], code: m[2], message: text.slice(m[0].length) };
}

/** 取某个错误码的人话提示(带原型链守卫:code 来自服务端)。 */
export function hintForCode(code?: string): string {
  if (!code) return "";
  if (!Object.prototype.hasOwnProperty.call(CODE_HINTS, code)) return "";
  return CODE_HINTS[code];
}

/**
 * 把桌面模式 IPC 的错误统一成 ApiError。
 *
 * 桌面分支此前**完全没有错误处理**:绑定 reject 的原始字符串直接冒泡,
 * 用户看到的是 "ValidationError(UNKNOWN_FIELD): 未知的顶层字段 …" 这种
 * 带内部前缀的裸文本,而服务器模式看到的是剥掉前缀的 ApiError。
 *
 * 迭代 30 补的两件事:
 *   1. **panic 归一化**:Wails 把后端 panic 原文交给前端,形如
 *      "agentworkflow/app/application.WorkflowService.Get: panic: …"
 *      —— 泄漏 Go 包路径,用户看不懂。归一成 code=INTERNAL 的通用文案。
 *   2. **code/kind 兜底**:剥不出 "Kind(CODE): " 前缀时(旧版后端、
 *      代理改写、第三方绑定)补 code=BAD_REQUEST / kind=ValidationError,
 *      与服务器模式的默认形状**同构**(约定 10)—— 消费方按 code 分支
 *      时不必到处判 undefined。
 */
function toApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e;
  const text = e instanceof Error ? e.message : typeof e === "string" ? e : "";
  // panic:不外泄 Go 包路径与 panic 原文
  if (PANIC_TEXT_RE.test(text)) {
    return new ApiError("服务内部错误(后端处理时出错)", 500, "INTERNAL", "WorkflowError");
  }
  const parsed = parseErrorPrefix(text);
  const message = parsed.message || hintForCode(parsed.code);
  // 剥不出前缀 → 兜底 code/kind(与服务器模式同构)
  const code = parsed.code ?? "BAD_REQUEST";
  const kind = parsed.kind ?? "ValidationError";
  return new ApiError(message, 0, code, kind);
}

/**
 * 给 Wails 绑定服务的每个方法套一层错误转换(集中一处,不必逐个分支改)。
 *
 * 服务器模式的错误是结构化错误体 {error, code, kind};桌面模式是 IPC
 * reject 的纯文本 —— 不统一的话,同一个错误在两种模式下前端拿到的
 * code/kind 完全不同(约定 10)。
 */
function wrapService<T extends object>(mod: T): T {
  return new Proxy(mod, {
    get(target, prop, receiver) {
      const v = Reflect.get(target, prop, receiver);
      if (typeof v !== "function") return v;
      return (...args: unknown[]) => {
        let out: unknown;
        try {
          out = (v as (...a: unknown[]) => unknown)(...args);
        } catch (e) {
          throw toApiError(e);
        }
        if (out && typeof (out as { catch?: unknown }).catch === "function") {
          return (out as Promise<unknown>).catch((e: unknown) => {
            throw toApiError(e);
          });
        }
        return out;
      };
    },
  });
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
    const hint = hintForCode(code) || STATUS_HINTS[status];
    message = hint ? `HTTP ${status}: ${hint}` : `HTTP ${status}`;
  }
  return new ApiError(message, status, code, kind);
}

// text 端点(DSL 导出/下载)用:后端在错误时写的是 JSON 错误体,
// 直接 res.text() 会把 {"error":...,"code":"NOT_FOUND"} 当成 DSL 内容
// 弹进对话框或写进 .yaml 文件(fix: 绕过 http() 且不检查 res.ok)。
//
// **非 JSON 错误体**(HTML 兜底页、反向代理的整页报错)此前**把全文当
// 错误消息**:一是几百行 HTML 塞进 toast,二是里面可能带着内部路径。
// 现在截断到 200 字再交给 buildApiError 统一处理(剥前缀 + 码表提示)。
const MAX_ERROR_TEXT = 200;

/** 截断过长文本(保留前 N 字,末尾标注截断)。 */
function truncate(text: string, max = MAX_ERROR_TEXT): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max)}…(已截断,共 ${text.length} 字)`;
}

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
    // 非 JSON 错误体(HTML 兜底页等):截断后当纯文本错误体处理 ——
    // 走同一条 buildApiError 路径,提示语与状态码兜底口径一致。
    if (body === undefined) {
      const trimmed = truncate(text.trim());
      throw buildApiError(res.status, trimmed ? { error: trimmed } : undefined);
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
// 每个服务都套一层 wrapService:桌面模式的错误统一成 ApiError(与服务器
// 模式同构),否则同一个错误在两种模式下的 code/kind 全丢(约定 10)。
async function wailsBindings() {
  const [wf, ex, ag, sk, gt, st] = await Promise.all([
    import("@bindings/agentworkflow/app/application/workflowservice.js"),
    import("@bindings/agentworkflow/app/application/executionservice.js"),
    import("@bindings/agentworkflow/app/application/agentservice.js"),
    import("@bindings/agentworkflow/app/application/skillservice.js"),
    import("@bindings/agentworkflow/app/application/gitservice.js"),
    import("@bindings/agentworkflow/app/application/settingsservice.js"),
  ]);
  return {
    wf: wrapService(wf),
    ex: wrapService(ex),
    ag: wrapService(ag),
    sk: wrapService(sk),
    gt: wrapService(gt),
    st: wrapService(st),
  };
}

/**
 * 归一化工作流结果:两种模式都补齐附加字段的缺省值。
 *
 * 后端在"没有警告 / 没有被忽略的键"时**省略**这些字段(omitempty),
 * 两个分支都统一成空数组 —— 调用方不必区分"没有"与"空"。
 */
function normalizeWorkflow(wf: Workflow | null): Workflow {
  if (!wf) return wf as unknown as Workflow;
  return { ...wf, warnings: wf.warnings ?? [], ignored_keys: wf.ignored_keys ?? [] };
}

/** 归一化执行结果(同上:warnings 缺省为空数组)。 */
function normalizeExecution(ex: Execution | null): Execution {
  if (!ex) return ex as unknown as Execution;
  return { ...ex, warnings: ex.warnings ?? [] };
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
    if ((await detectMode()) === "desktop") {
      // 桌面模式必须走**载荷级**入口(SavePayload):未知顶层键在模型级
      // Save 里查不出来(反序列化时已被 json.Unmarshal 静默丢弃)——
      // 服务器模式会报 UNKNOWN_FIELD,桌面模式此前静默接受(约定 10)。
      //
      // 返回形状两种模式一致:工作流字段在顶层 + warnings(校验警告)。
      return normalizeWorkflow(
        nn<Workflow>(
          await (await wailsBindings()).wf.SavePayload(JSON.stringify(wf)),
        ),
      );
    }
    return normalizeWorkflow(
      await http<Workflow>("/api/workflows", { method: "POST", body: JSON.stringify(wf) }),
    );
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
      // 桌面模式此前拿不到"被忽略的键"(服务器模式的 import 端点自己又
      // 解析了一遍 DSL 才补上这个字段)—— 现在由应用层统一产出,两种
      // 模式同源(约定 10)。
      return normalizeWorkflow(
        nn<Workflow>(await (await wailsBindings()).wf.ImportYAML(content)),
      );
    }
    return normalizeWorkflow(
      await http<Workflow>("/api/workflows/import", {
        method: "POST",
        body: content,
        headers: { "Content-Type": "application/yaml" },
      }),
    );
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
      return normalizeExecution(
        nn<Execution>(
          await (await wailsBindings()).ex.Run(id, task, variables ?? {}),
        ),
      );
    return normalizeExecution(
      await http<Execution>(`/api/workflows/${id}/run`, {
        method: "POST",
        body: JSON.stringify({ task, variables }),
      }),
    );
  },
  async listExecutions(workflowID = "", limit = 50): Promise<Execution[]> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ex.List(workflowID, clampLimit(limit)));
    }
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
      await (await wailsBindings()).ag.UpdateConfig(id, cfg as unknown as never);
      // 服务器模式在更新后回读**掩码后**的配置(约定 8:API 返回掩码);
      // 桌面模式此前直接返回绑定的 void 结果 → 调用方拿到 null,
      // 掩码展示失效(两种模式行为不同)。
      return nn<AgentConfig>(await (await wailsBindings()).ag.GetConfig(id));
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
      await (await wailsBindings()).ag.ResetConfig(id);
      // 同 updateAgentConfig:回读掩码后的配置,两种模式形状一致。
      return nn<AgentConfig>(await (await wailsBindings()).ag.GetConfig(id));
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
    {
      workflow_id: string;
      name: string;
      total: number;
      completed: number;
      failed: number;
      /** 被用户取消的执行数(与 failed 分列,第二十轮拆分) */
      cancelled: number;
      waiting: number;
      running: number;
    }[]
  > {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ex.Stats());
    }
    return http("/api/stats");
  },
  /**
   * agent 节点的合法 mode 列表(设计器的 mode 下拉用)。
   *
   * 唯一来源是后端(model.AllModes):前端此前自己硬编码了一份清单 ——
   * 后端加了 mode 而前端没跟上,用户在下拉里**选不到**新 mode;前端留着
   * 后端已删的 mode,用户选了必然被校验期判非法(约定 13)。
   *
   * 两种模式同源(约定 10):桌面走 Wails 绑定,浏览器走 HTTP。
   */
  async modes(): Promise<string[]> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).wf.Modes());
    }
    return http("/api/modes");
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
          clampLimit(Number(params.get("limit")) || 200),
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
      // 省略 git_working_dir = **不修改**(后端契约:body 里是 *string,
      // nil 表示不改;显式 "" 才表示清除)。
      //
      // 桌面绑定的签名是 (gitWorkingDir: string, ...),没有 nil 的表达力
      // → 省略时必须回填当前值。直接下发 "" 会被后端当成"清除",
      // 把用户的 git 工作目录清空(迭代 13)。
      const dir =
        patch.git_working_dir ?? (await api.getSettings()).git_working_dir;
      return nn(
        await (await wailsBindings()).st.Update(
          dir,
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
    // 已落库但**运行时应用失败**的 Agent ID:配置进了库,但适配器实例
    // 没换成功 —— 这一路的调用仍走旧实例(旧凭据/旧端点/旧参数)。
    // 不计入"已恢复",必须逐个上抛(否则用户以为凭据已恢复)。
    agent_configs_apply_failed_ids?: string[];
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
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).gt.Log(clampLimit(limit)));
    }
    return http(`/api/git/log?limit=${limit}`);
  },
};

/**
 * 把校验警告拼成一句人话(UI 展示用)。
 *
 * 警告此前只进后端日志,用户永远看不到 —— 用户以为"已废弃的配置键"仍然
 * 生效。两种模式现在都带回 warnings,这里统一成一句可读的提示。
 *
 * 带 code 是为了可追溯(用户报问题时能直接给出码),message 本身已是中文。
 */
export function describeWarnings(warnings?: ValidationError[]): string {
  if (!warnings || warnings.length === 0) return "";
  return warnings
    .map((w) => {
      const where = w.node ? `节点 ${w.node}` : "工作流";
      return `${where}: ${w.message}(${w.code})`;
    })
    .join(";");
}

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
    // 桌面分支此前**没有 catch**:@wailsio/runtime 导入失败时,桌面端
    // 彻底收不到实时事件,界面上只剩一条 unhandled rejection —— 用户看到的
    // 是"页面不动",完全不知道原因。
    import("@wailsio/runtime")
      .then((runtime) => {
        runtime.Events.On("ui:event", (data: unknown) => {
          dispatch(data as UIEvent);
        });
      })
      .catch((e: unknown) => {
        // 允许后续订阅重试:模块导入失败可能是瞬时的(资源未就绪),
        // 永久卡死则事件流永远不可用。reportError 内部按 5 秒窗口去重,
        // 重试不会变成提示风暴。
        started = false;
        reportError(e, "实时事件流(桌面)");
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
      // EventSource 会自动重连,但此前这里是空处理 —— 断线时用户看到的
      // 只是"页面上的实时数据不动了",与"确实没有新数据"无法区分。
      // 这里报出来(reportError 5 秒内去重,频繁重连不会刷屏)。
      reportError(new Error("事件流连接中断,正在自动重连"), "实时事件流");
    };
  }
}
