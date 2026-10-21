// 守卫:生产代码里的错误提示**必须**走 lib/errors.ts 的 reportError。
//
// 背景(迭代 30,P1-3):同一个后端错误,前端此前有**两种文案** ——
//
//	toast.error(e.message)   → "工作流 \"ghost\" 不存在"
//	reportError(e, "加载工作流") → "加载工作流:工作流 \"ghost\" 不存在(NOT_FOUND)"
//
// 直连 toast.error 的那 45 处:
//   - **丢掉 scope**:用户不知道是哪一块出的问题;
//   - **丢掉错误码**:用户报障时给不出码,开发者也定位不到;
//   - **绕过 NETWORK_HINTS 的中文翻译**:网络失败时弹的是浏览器的
//     原始英文 "Failed to fetch"(reportError 会翻成"无法连接后端服务")。
//
// 这条约定此前只靠人工维持,零测试覆盖 —— 下一轮审计很可能又抓回来。
// 现在有守卫:生产代码里的 toast.error 只许出现在 lib/errors.ts
// (那是唯一的出口实现本身)。
import { describe, expect, it } from "vitest";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

const SRC = path.join(import.meta.dirname, "..");

/**
 * 例外白名单:相对路径 → 允许出现的次数。
 *
 * 空表 —— reportError 是**唯一**出口,lib/errors.ts 之外一律不许
 * 直接调 toast.error。
 *
 * 将来若真需要例外(例如某个不经过错误对象的提示),必须在这里
 * 写清理由,并在下面的"白名单本身有效"用例里验证它确实存在 ——
 * 白名单是**显式登记**的,不是"扫描不到就算过"。
 */
const ALLOWED: Record<string, number> = {};

/** 唯一允许出现 toast.error 的文件(reportError 的实现本身)。 */
const ERROR_EXIT = "lib/errors.ts";

/** 匹配 toast.error(排除 toast.errorXxx 之类)。 */
const TOAST_ERROR = /toast\.error\s*\(/g;

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
 * "// 此前是 toast.error"),不剥掉就会把注释当成违规代码 ——
 * 守卫会一直红,直到有人把注释也改掉(那才是真正的退化)。
 */
function stripComments(src: string): string {
  const noBlock = src.replace(/\/\*[\s\S]*?\*\//g, "");
  return noBlock.replace(/(^|[^:])\/\/.*$/gm, "$1");
}

/** 统计匹配次数(match 返回 null 时算 0) */
function countMatches(src: string): number {
  return src.match(TOAST_ERROR)?.length ?? 0;
}

describe("toast.error 出口守卫", () => {
  it("生产代码里的 toast.error 只出现在 lib/errors.ts", async () => {
    const files = await sourceFiles(SRC);
    const violations: string[] = [];
    for (const file of files) {
      const r = rel(file);
      if (r === ERROR_EXIT) continue;
      const src = stripComments(await readFile(file, "utf8"));
      const hits = countMatches(src);
      if (!hits) continue;
      const allowed = ALLOWED[r] ?? 0;
      if (hits > allowed) {
        violations.push(`${r}: ${hits} 处(允许 ${allowed})`);
      }
    }
    expect(violations).toEqual([]);
  });

  it("lib/errors.ts 里的 toast.error 确实存在(防空转:实现被删了守卫还在)", async () => {
    const src = stripComments(await readFile(path.join(SRC, ERROR_EXIT), "utf8"));
    expect(countMatches(src)).toBeGreaterThan(0);
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
    // 合成源码:扫描器必须能认出 toast.error
    expect(countMatches('toast.error("x")')).toBe(1);
    expect(countMatches("toast.error (x)")).toBe(1);
    // reportError 不是 toast.error
    expect(countMatches('reportError(e, "x")')).toBe(0);
    // 注释里的写法不算违规(否则本仓库自己的注释就会让守卫一直红)
    expect(countMatches(stripComments('// 此前是 toast.error(e.message)'))).toBe(0);
  });
});
