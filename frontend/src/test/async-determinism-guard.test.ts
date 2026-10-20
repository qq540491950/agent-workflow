// 守卫:前端测试的**确定性**。
//
// 这条守卫防的是同一类故障的两半:
//
// (一) 手动 mock 的缓存语义被误解。
//     vitest 5 里 `vi.resetModules()` **不会**让手动 mock 的工厂重新求值:
//     resetModules 显式跳过 /^mock:/ 路径,而 ManualMockedModule 自己记着
//     工厂的结果(Memoize)。真正会让工厂重新求值的是 `vi.doMock()`
//     —— 它注册一条**新的** mock 条目,旧条目的缓存随之作废。
//     lib/api-desktop-stream.test.ts 正是靠这条语义造出"探测用的那次 import
//     成功、事件流用的那次 import 失败"的;一旦有人把 vi.doMock 换成提升的
//     vi.mock,工厂不会重新求值,那个用例会**静默退化成恒真测试**(断言照样
//     过,但它已经不再验证"导入失败会被报出来")。这里把语义钉死。
//
// (二) 用**真实计时器**当同步点。
//     `await new Promise(r => setTimeout(r, 0))` 是"等异步链跑完"的猜测:
//     这条链要跨几个事件循环节拍**不是常数**(vite 的 resolveId 命中缓存时
//     1 拍,需要 fs 往返时 2 拍),猜错就是断言跑在效果之前 → 偶发红。
//     真实事故:lib/api-desktop-stream.test.ts 全量跑 20 次挂 1 次
//     (AssertionError: expected "vi.fn()" to be called at least once)。
//     正确写法是等**确定发生的那件事**(一个 promise,由效果本身 resolve)。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

const SRC = path.join(import.meta.dirname, "..");

// vi.hoisted:工厂要在 vi.mock(被提升到文件顶部)之前就存在。
const { calls, factoryA } = vi.hoisted(() => {
  const calls = { n: 0 };
  return {
    calls,
    factoryA: () => {
      calls.n++;
      return { cn: () => "A" };
    },
  };
});

vi.mock("@/lib/utils", factoryA);

beforeEach(() => {
  calls.n = 0;
  // 每个用例都装一份已知的 mock:vi.doMock 会注册新条目(工厂缓存作废),
  // 因此用例之间互不影响,顺序颠倒也不会串味。
  vi.doMock("@/lib/utils", factoryA);
});

describe("手动 mock 的缓存语义", () => {
  it("vi.resetModules() 不会让手动 mock 的工厂重新求值", async () => {
    const first = await import("@/lib/utils");
    expect(first.cn()).toBe("A");
    expect(calls.n).toBe(1);

    vi.resetModules();
    const second = await import("@/lib/utils");

    // 工厂结果被记忆(resetModules 跳过 /^mock:/ 路径)→ 工厂没有重新求值。
    expect(second.cn()).toBe("A");
    expect(calls.n).toBe(1);
  });

  it("vi.doMock() 会让工厂重新求值", async () => {
    await import("@/lib/utils");
    expect(calls.n).toBe(1);

    vi.doMock("@/lib/utils", () => {
      calls.n++;
      return { cn: () => "B" };
    });
    const replaced = await import("@/lib/utils");

    expect(replaced.cn()).toBe("B");
    expect(calls.n).toBe(2);
  });
});

// ---- (二)禁止用真实计时器当同步点 ----

/**
 * 例外白名单:相对路径 → 理由。
 *
 * 白名单是**显式登记**的:每一项都必须写清为什么这里必须用真实计时器,
 * 并且在下面的用例里被验证确实存在(防空转)。默认应为空。
 */
const ALLOWED: Record<string, string> = {};

/**
 * 从每个 `new Promise` 出发,按圆括号配对截出它的完整表达式,
 * 看里面有没有 setTimeout。
 *
 * 用括号配对而不是正则通配:表达式里可能有嵌套括号、多行 body,
 * 靠"到下一个分号为止"这种切法会漏掉多行写法。
 */
function timerWaitHits(src: string): string[] {
  const hits: string[] = [];
  const re = /new\s+Promise/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src)) !== null) {
    const start = m.index;
    let i = src.indexOf("(", start);
    if (i < 0) break;
    let depth = 0;
    let end = -1;
    for (; i < src.length; i++) {
      const ch = src[i];
      if (ch === "(") depth++;
      else if (ch === ")") {
        depth--;
        if (depth === 0) {
          end = i;
          break;
        }
      }
    }
    if (end < 0) break;
    const body = src.slice(start, end + 1);
    if (/setTimeout\s*\(/.test(body)) {
      const line = src.slice(0, start).split("\n").length;
      hits.push(`${line}: ${body.split("\n")[0]!.trim()}`);
    }
    re.lastIndex = end + 1;
  }
  return hits;
}

/** 去掉注释再匹配:本仓库的注释里大量引用旧的写法,会把注释当成违规代码 */
function stripComments(src: string): string {
  const noBlock = src.replace(/\/\*[\s\S]*?\*\//g, "");
  return noBlock.replace(/(^|[^:])\/\/.*$/gm, "$1");
}

async function testFiles(dir: string): Promise<string[]> {
  const out: string[] = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...(await testFiles(p)));
      continue;
    }
    if (!/\.test\.tsx?$/.test(entry.name)) continue;
    out.push(p);
  }
  return out;
}

function rel(p: string): string {
  return path.relative(SRC, p).split(path.sep).join("/");
}

describe("真实计时器不得当同步点", () => {
  it("测试里没有用 new Promise + setTimeout 等异步链跑完", async () => {
    const self = path.join(import.meta.dirname, "async-determinism-guard.test.ts");
    const violations: string[] = [];
    for (const file of await testFiles(SRC)) {
      // 守卫文件自身含合成样本,跳过(否则自己举报自己)
      if (path.resolve(file) === path.resolve(self)) continue;
      const src = stripComments(await readFile(file, "utf8"));
      const hits = timerWaitHits(src);
      if (!hits.length) continue;
      const r = rel(file);
      if (r in ALLOWED) continue;
      violations.push(`${r}: ${hits.join(" / ")}`);
    }
    expect(violations).toEqual([]);
  });

  it("白名单里的例外确实存在(防空转:扫描器失效时白名单也一起失效)", async () => {
    for (const [r, why] of Object.entries(ALLOWED)) {
      expect(why.trim().length, `例外 ${r} 必须写清理由`).toBeGreaterThan(0);
      const hits = timerWaitHits(stripComments(await readFile(path.join(SRC, r), "utf8")));
      expect(hits.length, `例外 ${r} 应当真的含有该写法`).toBeGreaterThan(0);
    }
  });

  it("扫描器本身看得见这种写法(防空转)", () => {
    // 真实事故里的写法
    expect(timerWaitHits("await new Promise((r) => setTimeout(r, 0));")).toHaveLength(1);
    expect(timerWaitHits("await new Promise<void>((resolve) => setTimeout(resolve, 10));")).toHaveLength(
      1,
    );
    // 多行 body 也算
    expect(
      timerWaitHits("const p = new Promise((resolve) => {\n  setTimeout(resolve, 5);\n});"),
    ).toHaveLength(1);
    // 正常写法:等效果本身,不猜节拍
    expect(timerWaitHits("const p = new Promise((resolve) => { onDone(resolve); });")).toHaveLength(0);
    // 假定时器推进不是这个模式
    expect(timerWaitHits("await vi.advanceTimersByTimeAsync(200);")).toHaveLength(0);
  });
});
