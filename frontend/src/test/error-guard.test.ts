// 守卫:生产代码里不得有"静默吞掉请求失败"的 `.catch(() => …)`。
//
// 此前 13 处 `.catch(() => {})` 把接口失败吞成空值:后端不可用时 Dashboard
// 渲染「暂无工作流」,与"真的没有工作流"完全无法区分 —— 用户以为没数据,
// 实为加载失败。第八轮做好的 ApiError(错误码中文提示)因此成了死代码:
// 错误被吞掉,提示永远到不了 UI。
//
// 这条约定此前只靠人工维持,零测试覆盖 —— 下一轮审计很可能又抓回来。
//
// 唯一的例外是 lib/notify.ts 的 Notification.requestPermission():
// 那是**刻意的防御**(见该文件里的注释)—— 失败只意味着"没有桌面通知",
// 不影响工作流,报错反而让用户以为工作流出了问题。例外写进白名单,
// 连同理由一起:白名单是**显式登记**的,不是"扫描不到就算过"。
import { describe, expect, it } from "vitest";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

const SRC = path.join(import.meta.dirname, "..");

/**
 * 例外白名单:相对路径 → 允许出现的次数。
 *
 * 加新例外必须在这里写清理由(代码评审时能看到)。这里的每一项都必须
 * 在下面的"白名单本身有效"用例里被验证确实存在 —— 否则白名单写错了
 * 也不会有人知道。
 */
const ALLOWED: Record<string, number> = {
  // Notification.requestPermission() 失败只意味着"没有桌面通知",
  // 不影响工作流本身;不该为此打扰用户。
  "lib/notify.ts": 1,
};

/**
 * 此前静默吞错、现已改为可见提示的文件。
 *
 * 这条断言防的是"改了一半":只把 catch 换成 `.catch(() => {})` 之外的
 * 写法、却忘了接上可见提示(例如只加了日志)。判据放宽到
 * "有任意可见的错误处理" —— reportError / toast.error 都算。
 */
const MUST_REPORT: string[] = [
  "pages/Dashboard.tsx",
  "pages/Designer.tsx",
  "pages/Settings.tsx",
  "pages/WorkflowHistory.tsx",
  "pages/Workflows.tsx",
  "components/layout.tsx",
];

// `.catch(() =>` —— 空回调。注意带**任意**函数体:`.catch(() => null)` 与
// `.catch(() => {})` 同样是"把失败吞成默认值"。
const SILENT_CATCH = /\.catch\(\s*\(\s*\)\s*=>/g;

async function sourceFiles(dir: string): Promise<string[]> {
  const out: string[] = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // 脚手架目录不参与守卫
      if (entry.name === "test") continue;
      out.push(...(await sourceFiles(p)));
      continue;
    }
    if (!/\.tsx?$/.test(entry.name)) continue;
    if (/\.test\.tsx?$/.test(entry.name)) continue;
    out.push(p);
  }
  return out;
}

/** 相对 SRC 的 posix 风格路径(与白名单里的键一致) */
function rel(p: string): string {
  return path.relative(SRC, p).split(path.sep).join("/");
}

/**
 * 去掉注释再匹配。
 *
 * 必须去注释:本仓库的代码注释里**大量引用**旧的写法(例如
 * "// 此前是 `.catch(() => {})`"),不剥掉就会把注释当成违规代码 ——
 * 守卫会一直红,直到有人把注释也改掉(那才是真正的退化)。
 *
 * 剥注释是粗略的:行注释用 "//" 前一个字符不是 ":" 来避开 URL 里的
 * "://"(本仓库源码里只有一处 URL 字面量,已核对)。
 */
function stripComments(src: string): string {
  const noBlock = src.replace(/\/\*[\s\S]*?\*\//g, "");
  return noBlock.replace(/(^|[^:])\/\/.*$/gm, "$1");
}

/** 统计匹配次数(match 返回 null 时算 0) */
function countMatches(src: string): number {
  return src.match(SILENT_CATCH)?.length ?? 0;
}

describe("静默吞错守卫", () => {
  it("生产代码里没有静默吞掉失败的空 catch", async () => {
    const files = await sourceFiles(SRC);
    const violations: string[] = [];
    for (const file of files) {
      const src = stripComments(await readFile(file, "utf8"));
      const hits = countMatches(src);
      if (!hits) continue;
      const r = rel(file);
      const allowed = ALLOWED[r] ?? 0;
      if (hits > allowed) {
        violations.push(`${r}: ${hits} 处(允许 ${allowed})`);
      }
    }
    expect(violations).toEqual([]);
  });

  it("白名单里的例外确实存在(防空转:扫描器失效时白名单也一起失效)", async () => {
    const files = await sourceFiles(SRC);
    const counts = new Map<string, number>();
    for (const file of files) {
      const src = stripComments(await readFile(file, "utf8"));
      const hits = countMatches(src);
      if (hits) counts.set(rel(file), hits);
    }
    for (const [r, n] of Object.entries(ALLOWED)) {
      expect(counts.get(r) ?? 0, `${r} 的白名单例外应当存在`).toBe(n);
    }
  });

  it("扫描器本身看得见这些模式(防空转)", async () => {
    const files = await sourceFiles(SRC);
    expect(files.length).toBeGreaterThan(10);
    // 合成源码:扫描器必须能认出空 catch
    expect(countMatches("x.catch(() => {});")).toBe(1);
    expect(countMatches("x.catch(() => null);")).toBe(1);
    // 非空回调不算违规
    expect(countMatches("x.catch((e) => reportError(e));")).toBe(0);
    // 注释里的写法不算违规(否则本仓库自己的注释就会让守卫一直红)
    expect(countMatches(stripComments("// 此前是 `.catch(() => {})`"))).toBe(0);
  });

  it("此前静默吞错的文件都接上了可见的错误提示", async () => {
    const missing: string[] = [];
    for (const r of MUST_REPORT) {
      const src = stripComments(await readFile(path.join(SRC, r), "utf8"));
      if (!src.includes("reportError(") && !src.includes("toast.error(")) {
        missing.push(r);
      }
    }
    expect(missing).toEqual([]);
  });
});
