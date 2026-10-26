// 前端统一的错误提示出口。
//
// 此前多个页面用 `.catch(() => {})` 把请求失败**静默吞掉**:后端不可用时
// Dashboard 渲染「暂无工作流」,与"真的没有工作流"完全无法区分 —— 用户以为
// 没数据,实际是加载失败。第八轮做好的 ApiError(64 个错误码的中文提示)
// 因此成了死代码:错误被吞掉,提示永远到不了 UI。
//
// 这里给出唯一出口 reportError():
//   - 把任意错误对象转成一句可读的中文(ApiError 的 message 已含后端文案与
//     码表提示,这里补上错误码,口径与 describeWarnings 一致);
//   - 对**同一个错误**做时间窗去重 —— Layout 挂在所有页面上、实时事件会
//     反复触发刷新,不去重就是提示风暴。
//
// 本模块刻意**不 import @/lib/api**:api.ts 的事件流也要用它报错,反向依赖
// 会形成循环。ApiError 的 message 在 api.ts 里就已经用掉码表提示(CODE_HINTS)
// 生成好了,这里不必重复拿。
import { toast } from "sonner";

/** 同一个错误在这个时间窗内只提示一次(毫秒) */
export const ERROR_DEDUP_WINDOW_MS = 5000;

/** 已提示过的错误 → 上次提示的时间(毫秒)。key 只含错误本身,不含来源。 */
const shownAt = new Map<string, number>();

interface ErrorLike {
  message?: unknown;
  code?: unknown;
  status?: unknown;
}

// NETWORK_HINTS 把"网络层失败"翻成人话。
//
// fetch 在网络层失败时给的是浏览器的通用文案(各浏览器措辞不同:
// "Failed to fetch" / "Load failed" / "NetworkError when attempting to
// fetch resource")。后端不可用是最常见的失败原因,直接把这句英文弹给用户
// 看不懂 —— 这里翻成一句人话。
//
// **全匹配而不是子串匹配**:后端的具体错误里也可能带 timeout 之类的词
// (例如 "Claude 执行超时"),子串匹配会把那句有信息量的话替换掉 ——
// 泛化提示只该用于"根本连不上"这一种情况。
//
// 下面两条是迭代 30 补的(此前它们会原样弹给用户):
//   - AbortError:fetch 被 AbortController 取消(组件卸载/请求竞态)——
//     用户看到 "The operation was aborted" 完全不知道发生了什么;
//   - "timeout of 5000ms exceeded":axios/包装层的超时文案。
//
// 这两条用**宽松匹配**(子串),因为它们不像 "Failed to fetch" 那样
// 独占整条消息(前面常带上下文),但仍限定在"明确的超时/取消形态"上。
const NETWORK_HINTS: Array<[RegExp, string]> = [
  [/^(failed to fetch|load failed|network request failed|networkerror.*)$/i, "无法连接后端服务(后端未启动或地址不可达)"],
  [/^(timeout|timed out|request timeout)$/i, "请求超时"],
  [/abort/i, "请求被中断(可能已取消或页面切换)"],
  [/timeout.*exceeded/i, "请求超时"],
];


/** 单条校验错误的结构(后端 validator.ValidationError 的 JSON 形态)。 */
interface ValidationErrLike {
  code?: unknown;
  message?: unknown;
  node?: unknown;
}

/**
 * 把 WORKFLOW_INVALID 的 message(校验错误数组的 JSON 文本)拍平成一句人话。
 * 不是合法的"错误对象数组"形态时返回 null(调用方按原文显示)。
 */
export function flattenValidationErrors(raw: string): string | null {
  if (!raw.startsWith("[")) return null;
  let arr: unknown;
  try {
    arr = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!Array.isArray(arr) || arr.length === 0) return null;
  const parts: string[] = [];
  for (const item of arr) {
    const o = (item && typeof item === "object" ? item : {}) as ValidationErrLike;
    if (typeof o.message !== "string" || !o.message.trim()) return null;
    parts.push(typeof o.node === "string" && o.node ? `[${o.node}] ${o.message}` : o.message);
  }
  return parts.join("; ");
}

/**
 * 把任意错误对象转成一句可读的中文。
 *
 * - ApiError:message 由后端 error 文本(剥掉 Kind(CODE) 前缀)或码表提示
 *   生成,这里补上错误码 —— 与 describeWarnings 同口径,用户报问题时能
 *   直接给出码。
 * - 普通 Error / 字符串:用它的 message。
 * - 其它:兜底成一句人话 —— 提示路径本身绝不能抛 TypeError。
 */
export function describeError(e: unknown): string {
  if (typeof e === "string" && e.trim()) return e.trim();
  const obj = (e && typeof e === "object" ? e : null) as ErrorLike | null;
  const raw = obj && typeof obj.message === "string" ? obj.message.trim() : "";
  const code = obj && typeof obj.code === "string" && obj.code ? obj.code : "";
  const status = obj && typeof obj.status === "number" && obj.status ? obj.status : 0;

  // WORKFLOW_INVALID 的 message 是校验错误数组的 JSON 文本(应用层把
  // validator.Result.Errors 整个 marshal 进 message)。直接显示就是一坨
  // 机器可读的 JSON(走查实测:"导入工作流:[{\"code\":\"MISSING_NAME\"..." ),
  // 每条错误的 message 本身已是人话 —— 这里拍平成一句,失败节点带上
  // 节点定位;解析不动(形态不合预期)就按原文走通用路径,不硬造。
  if (code === "WORKFLOW_INVALID") {
    const flat = flattenValidationErrors(raw);
    if (flat) return `${flat}(WORKFLOW_INVALID)`;
  }

  let base = raw;
  if (!base) {
    base = status ? `HTTP ${status}` : "未知错误";
  } else {
    for (const [re, hint] of NETWORK_HINTS) {
      if (re.test(base)) {
        base = hint;
        break;
      }
    }
  }
  return code ? `${base}(${code})` : base;
}

/**
 * 统一的错误提示出口:把错误变成一条可见的 toast。
 *
 * @param e     任意错误对象(ApiError / Error / 字符串)
 * @param scope 出错的地方(中文名词短语),拼在消息前面 —— 让用户知道是
 *               "哪一块"加载失败了,而不只是"有个错误"。
 *
 * 去重按**错误本身**(不含 scope):Layout 挂在所有页面上、Dashboard 与
 * WorkflowHistory 又各自订阅实时事件,后端挂掉时同一个错误会在极短时间内
 * 被报很多次 —— 不去重就是提示风暴。同一个错误 5 秒内只提示一次。
 */
export function reportError(e: unknown, scope?: string): void {
  const message = describeError(e);
  const now = Date.now();
  const prev = shownAt.get(message);
  if (prev !== undefined && now - prev < ERROR_DEDUP_WINDOW_MS) return;
  shownAt.set(message, now);
  // 顺手清掉过期条目:否则长时间运行会把见过的每个错误都留在 Map 里。
  for (const [k, t] of shownAt) {
    if (now - t >= ERROR_DEDUP_WINDOW_MS) shownAt.delete(k);
  }
  toast.error(scope ? `${scope}:${message}` : message);
}

/** 清空去重记录(测试用:用例之间不该互相影响)。 */
export function resetErrorDedup(): void {
  shownAt.clear();
}
