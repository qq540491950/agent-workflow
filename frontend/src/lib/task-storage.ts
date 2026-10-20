// "上次任务文本"的存储。
//
// 此前 Workflows 页直接 `localStorage.setItem('last-task:<id>', task)`:
// 明文、**永不清理**、每个工作流一条、没有任何上限。而任务文本里经常带着
// 凭据(部署令牌、接口 Key、数据库口令)—— 这等于把凭据摊在磁盘上。
//
// 现在改成 sessionStorage:
//   - 生命周期与标签页一致(关掉标签页就没了),不再长期落在磁盘上;
//   - 同一标签页内"记住上次任务"的体验不变(大多数重跑就在同一会话里);
//   - 仍然限长、限量,不给存储留无上限的增长口;
//   - 顺手清掉旧版本遗留在 localStorage 里的明文(升级后不再读取它们)。
//
// 注意:这里存的是**方便用户少打一遍字**的副本,不是执行输入 ——
// 真正提交给后端的任务文本不受这里的截断影响。
export const LAST_TASK_KEY_PREFIX = "last-task:";

/** 单条任务文本的存储上限(字符)。超出的部分不记,避免超长文本占存储。 */
export const LAST_TASK_MAX_LEN = 2000;

/** 最多保留多少条任务文本(每个工作流一条)。 */
export const LAST_TASK_MAX_ENTRIES = 20;

/** 存储信封:{t: 写入时刻, v: 任务文本}。带上时刻才能确定性地淘汰最旧的。 */
interface TaskEnvelope {
  t: number;
  v: string;
}

function sessionStore(): Storage | null {
  try {
    if (typeof window === "undefined") return null;
    return window.sessionStorage;
  } catch {
    // 隐私模式 / 禁用存储:访问 sessionStorage 可能直接抛错
    return null;
  }
}

/** 读取上次任务文本;没有、坏了或读不到,一律返回空串。 */
export function loadLastTask(workflowId: string): string {
  const s = sessionStore();
  if (!s) return "";
  try {
    const raw = s.getItem(LAST_TASK_KEY_PREFIX + workflowId);
    if (!raw) return "";
    const env = JSON.parse(raw) as TaskEnvelope;
    return typeof env.v === "string" ? env.v : "";
  } catch {
    return "";
  }
}

/** 记下本次任务文本(超长截断、条数超限淘汰最旧的)。 */
export function saveLastTask(workflowId: string, task: string): void {
  const s = sessionStore();
  if (!s) return;
  try {
    const env: TaskEnvelope = { t: Date.now(), v: task.slice(0, LAST_TASK_MAX_LEN) };
    s.setItem(LAST_TASK_KEY_PREFIX + workflowId, JSON.stringify(env));
    prune(s);
  } catch {
    // 配额满 / 隐私模式:存不进去也不能阻断"运行工作流"主流程
  }
}

/** 只保留最近 LAST_TASK_MAX_ENTRIES 条,其余删掉(不设上限就会一直涨)。 */
function prune(s: Storage): void {
  const entries: Array<{ key: string; t: number }> = [];
  for (let i = 0; i < s.length; i++) {
    const key = s.key(i);
    if (!key || !key.startsWith(LAST_TASK_KEY_PREFIX)) continue;
    let t: number;
    try {
      const raw = s.getItem(key);
      const env = raw ? (JSON.parse(raw) as TaskEnvelope) : null;
      t = typeof env?.t === "number" ? env.t : 0;
    } catch {
      // 解析坏了:当作最旧(优先淘汰)
      t = 0;
    }
    entries.push({ key, t });
  }
  if (entries.length <= LAST_TASK_MAX_ENTRIES) return;
  entries.sort((a, b) => a.t - b.t);
  for (const e of entries.slice(0, entries.length - LAST_TASK_MAX_ENTRIES)) {
    s.removeItem(e.key);
  }
}

/**
 * 清掉旧版本遗留在 localStorage 里的明文任务文本。
 *
 * 本版本已改用 sessionStorage,旧数据不再被读取 —— 留着就等于把凭据一直
 * 躺在磁盘上,升级后顺手删掉。
 */
export function purgeLegacyLastTasks(): void {
  try {
    if (typeof window === "undefined") return;
    const ls = window.localStorage;
    const keys: string[] = [];
    for (let i = 0; i < ls.length; i++) {
      const key = ls.key(i);
      if (key && key.startsWith(LAST_TASK_KEY_PREFIX)) keys.push(key);
    }
    for (const key of keys) ls.removeItem(key);
  } catch {
    // 读不到 localStorage 就算了,不影响主流程
  }
}
