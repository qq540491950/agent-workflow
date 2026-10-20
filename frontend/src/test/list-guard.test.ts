// 约定 7 守卫:所有列表接口的结果必须先过 asArray() 再 .map。
//
// 这条约定此前只靠人工维持,零测试覆盖。它防的是同一类故障:
// 桌面 IPC 与 SPA fallback 异常路径下,列表接口可能返回 null 或被解析成
// HTML 兜底页的对象(而非数组),此时直接 .map 会抛 TypeError。
//
// 真实缺陷(本守卫首次运行时抓到):pages/Designer.tsx 直接对
// api.listAgents() 的结果 .map,异常返回时抛错又被 .catch(() => {}) 吞掉,
// 界面表现是"Agent 下拉框空的",没有任何提示。
import { describe, expect, it } from "vitest";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

const SRC = path.join(import.meta.dirname, "..");

// 会返回列表的接口(其余接口返回单个对象/字符串,不需要 asArray)
const LIST_METHODS = [
  "listWorkflows",
  "listExecutions",
  "listAgents",
  "listSkills",
  "stats",
  "allEvents",
  "executionNodes",
  "executionEvents",
  "executionArtifacts",
  "workflowVersions",
  "gitLog",
];

const CALL_RE = new RegExp(`\\bapi\\.(${LIST_METHODS.join("|")})\\s*\\(`, "g");

/** 取调用结束处到语句结束之间的链式代码 */
function chainAfter(src: string, openParen: number): string {
  let depth = 0;
  let i = openParen;
  for (; i < src.length; i++) {
    if (src[i] === "(") depth++;
    else if (src[i] === ")") {
      depth--;
      if (depth === 0) {
        i++;
        break;
      }
    }
  }
  let j = i;
  while (j < src.length && src[j] !== ";" && src[j] !== "\n") j++;
  return src.slice(i, j);
}

async function sourceFiles(dir: string): Promise<string[]> {
  const out: string[] = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // 测试脚手架目录不参与守卫
      if (entry.name === "test") continue;
      out.push(...(await sourceFiles(p)));
      continue;
    }
    if (!/\.tsx?$/.test(entry.name)) continue;
    if (/\.test\.tsx?$/.test(entry.name)) continue;
    // api.ts 是传输层本身(asArray 的定义处),不参与守卫
    if (entry.name === "api.ts" && path.basename(dir) === "lib") continue;
    out.push(p);
  }
  return out;
}

describe("约定 7:列表接口先过 asArray 再 .map", () => {
  it("没有对列表接口结果直接 .map 的调用点", async () => {
    const files = await sourceFiles(SRC);
    expect(files.length).toBeGreaterThan(5);
    const violations: string[] = [];
    for (const file of files) {
      const src = await readFile(file, "utf8");
      for (const m of src.matchAll(CALL_RE)) {
        const openParen = m.index! + m[0].length - 1;
        const chain = chainAfter(src, openParen);
        if (chain.includes(".map(") && !chain.includes("asArray")) {
          violations.push(
            `${path.relative(SRC, file)}: api.${m[1]}() 的结果被直接 .map,未过 asArray`,
          );
        }
      }
    }
    expect(violations).toEqual([]);
  });

  it("调用了列表接口的文件都使用了 asArray 防御", async () => {
    const files = await sourceFiles(SRC);
    const missing: string[] = [];
    for (const file of files) {
      const src = await readFile(file, "utf8");
      if (!CALL_RE.test(src)) continue;
      CALL_RE.lastIndex = 0;
      if (!src.includes("asArray")) {
        missing.push(path.relative(SRC, file));
      }
    }
    expect(missing).toEqual([]);
  });
});
