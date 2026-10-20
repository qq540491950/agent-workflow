// 约定 7 守卫:所有列表接口的结果必须先过 asArray() 再 .map。
//
// 这条约定此前只靠人工维持,零测试覆盖。它防的是同一类故障:
// 桌面 IPC 与 SPA fallback 异常路径下,列表接口可能返回 null 或被解析成
// HTML 兜底页的对象(而非数组),此时直接 .map 会抛 TypeError。
//
// 真实缺陷(本守卫首次运行时抓到):pages/Designer.tsx 直接对
// api.listAgents() 的结果 .map,异常返回时抛错又被 .catch(() => {}) 吞掉,
// 界面表现是"Agent 下拉框空的",没有任何提示。
//
// 第三条用例(assign-then-use)补的是"先赋值再 .map"的盲区:
//
//   const items = await api.listAgents();   // 先赋值
//   items.map(...)                           // 再 .map —— 原守卫看不见
//
// 原守卫只看"列表接口调用点之后的那条链",一旦结果被交给变量,链上就没有
// .map 了,于是绕过。约定 7 的要求是"结果在使用前必须过 asArray",不是
// "必须写在同一个表达式里",所以这条也算违规。
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

// LIST_MEMBERS 是"把值当列表用"的成员。
//
// 取的是**语义**(遍历/按索引取/取长度),不是具体方法名 —— 换一种遍历写法
// 不应该让守卫失效。concat/join 之类返回新数组、不读原数组的成员不在此列。
const LIST_MEMBERS = [
  "map",
  "forEach",
  "filter",
  "reduce",
  "reduceRight",
  "find",
  "findIndex",
  "some",
  "every",
  "flatMap",
  "sort",
  "reverse",
  "splice",
  "push",
  "pop",
  "shift",
  "unshift",
  "length",
  "entries",
  "keys",
  "values",
  "at",
];

/** 取调用结束处到语句结束之间的链式代码 */
function chainAfter(src: string, openParen: number): string {
  const end = matchParen(src, openParen);
  if (end < 0) return "";
  let j = end;
  while (j < src.length && src[j] !== ";" && src[j] !== "\n") j++;
  return src.slice(end, j);
}

/** 返回与 openParen 匹配的右括号之后的下标(找不到返回 -1) */
function matchParen(src: string, openParen: number): number {
  let depth = 0;
  for (let i = openParen; i < src.length; i++) {
    if (src[i] === "(") depth++;
    else if (src[i] === ")") {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  return -1;
}

/** 正则元字符转义 */
function esc(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * 在 src[0, upto) 里找最靠近 upto 的赋值号位置。
 *
 * 跳过 ==、=>、>=、<=、!=、+= 等;遇到 ; { } 视为语句边界(返回 -1)——
 * 这样"上一条语句的 =" 不会被误当成本次赋值的 =。
 */
function findAssignBefore(src: string, upto: number): number {
  for (let i = upto - 1; i >= 0; i--) {
    const c = src[i];
    if (c === ";" || c === "{" || c === "}") return -1;
    if (c !== "=") continue;
    const prev = src[i - 1] ?? "";
    const next = src[i + 1] ?? "";
    if ("=!<>+-*/%&|^".includes(prev)) continue;
    if (next === "=" || next === ">") continue;
    return i;
  }
  return -1;
}

/**
 * 从赋值号左侧的文本里取出声明目标。
 *
 * 只认 const/let/var 声明(赋值表达式的左侧不是新变量名时不算)。
 */
function declTarget(before: string): { names: string[]; isPattern: boolean } | null {
  const m = /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*|\[[^\]]*\])\s*$/.exec(before);
  if (!m) return null;
  const raw = m[1];
  if (raw.startsWith("[")) {
    const names = raw
      .slice(1, -1)
      .split(",")
      .map((s) => s.split("=")[0].trim().replace(/^\.\.\./, ""))
      .filter((s) => /^[A-Za-z_$][\w$]*$/.test(s));
    return { names, isPattern: true };
  }
  return { names: [raw], isPattern: false };
}

/** 取数组字面量(open 指向 "[")的顶层元素 */
function arrayElements(src: string, open: number): string[] {
  const out: string[] = [];
  let depth = 0;
  let start = open + 1;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    if (c === "[" || c === "(" || c === "{") depth++;
    else if (c === "]" || c === ")" || c === "}") {
      depth--;
      if (depth === 0) {
        out.push(src.slice(start, i).trim());
        return out.filter((s) => s.length > 0);
      }
    } else if (c === "," && depth === 1) {
      out.push(src.slice(start, i).trim());
      start = i + 1;
    }
  }
  return out;
}

/** 文本里第一个列表接口调用(返回接口名,没有则 null) */
function listCallIn(text: string): string | null {
  const re = new RegExp(`\\bapi\\.(${LIST_METHODS.join("|")})\\s*\\(`, "g");
  const m = re.exec(text);
  return m ? m[1] : null;
}

/** 一个"由列表接口结果派生"的变量 */
interface ListBinding {
  name: string;
  method: string;
  /** 定义位置:asArray 的防御必须出现在它之后(定义处自带 asArray 的除外) */
  offset: number;
  /** 定义处是否已经过 asArray */
  guarded: boolean;
}

/**
 * 收集文件里"由列表接口结果派生"的变量。
 *
 * 覆盖四种来源:
 *  1. const X = await api.listX()          —— 直接赋值;
 *  2. const [a, b] = await Promise.all([api.listX(), …]) —— 解构(按位置对应);
 *  3. api.listX().then((x) => …)           —— 回调形参;
 *  4. const [rows, setRows] = useState(…); setRows(await api.listX()) —— 状态变量。
 *
 * 再做一遍别名传播(const rows = x):x 是列表派生时,rows 也是。
 */
function collectListBindings(src: string): ListBinding[] {
  const bindings: ListBinding[] = [];
  const seen = new Set<string>();
  const add = (b: ListBinding) => {
    const key = `${b.name}@${b.offset}`;
    if (seen.has(key)) return;
    seen.add(key);
    bindings.push(b);
  };

  for (const m of src.matchAll(CALL_RE)) {
    const method = m[1];
    const callStart = m.index!;
    const openParen = callStart + m[0].length - 1;
    const callEnd = matchParen(src, openParen);
    if (callEnd < 0) continue;

    const assign = findAssignBefore(src, callStart);
    if (assign >= 0) {
      const target = declTarget(src.slice(0, assign));
      if (target) {
        if (!target.isPattern) {
          // 直接赋值:定义处是否自带 asArray。
          const wrapper = src.slice(assign + 1, callStart);
          add({
            name: target.names[0],
            method,
            offset: assign,
            guarded: wrapper.includes("asArray"),
          });
        } else {
          // 解构:按 Promise.all([…]) 的元素位置对应变量名。
          const after = src.slice(assign + 1, callEnd);
          const arrOpen = after.indexOf("[");
          const elements = arrOpen >= 0 ? arrayElements(after, arrOpen) : [];
          if (elements.length === target.names.length) {
            elements.forEach((el, i) => {
              const method2 = listCallIn(el);
              if (method2) {
                add({
                  name: target.names[i],
                  method: method2,
                  offset: assign,
                  guarded: false,
                });
              }
            });
          } else {
            // 位置对不上(默认值/展开/嵌套):保守地把全部名字当列表派生。
            target.names.forEach((n) =>
              add({ name: n, method, offset: assign, guarded: false }),
            );
          }
        }
      }
    }

    // 回调形参:api.listX().then((x) => …)
    const tail = src.slice(callEnd);
    const cb = /^\s*\.\s*then\s*\(\s*(?:async\s*)?\(\s*([A-Za-z_$][\w$]*)\s*\)/.exec(tail);
    if (cb) {
      add({ name: cb[1], method, offset: callEnd, guarded: false });
    }
  }

  // 状态变量:const [rows, setRows] = useState(…); setRows(await api.listX())
  const setterToState = new Map<string, string>();
  const useStateRe =
    /\b(?:const|let|var)\s*\[\s*([A-Za-z_$][\w$]*)\s*,\s*([A-Za-z_$][\w$]*)\s*\]\s*=\s*useState\b/g;
  for (const m of src.matchAll(useStateRe)) {
    setterToState.set(m[2], m[1]);
  }
  for (const [setter, stateVar] of setterToState) {
    const callRe = new RegExp(`\\b${esc(setter)}\\s*\\(`, "g");
    for (const cm of src.matchAll(callRe)) {
      const openParen = cm.index! + cm[0].length - 1;
      const end = matchParen(src, openParen);
      if (end < 0) continue;
      const argText = src.slice(openParen + 1, end - 1);
      const method = listCallIn(argText);
      if (!method) continue;
      add({
        name: stateVar,
        method,
        offset: cm.index!,
        guarded: argText.includes("asArray"),
      });
    }
  }

  // 别名传播:const rows = x(x 是列表派生)→ rows 也是列表派生。
  for (let round = 0; round < 3; round++) {
    let changed = false;
    const names = new Set(bindings.map((b) => b.name));
    const declRe = /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*([A-Za-z_$][\w$]*)\s*;/g;
    for (const m of src.matchAll(declRe)) {
      if (!names.has(m[2])) continue;
      const key = `${m[1]}@${m.index}`;
      if (seen.has(key)) continue;
      seen.add(key);
      bindings.push({
        name: m[1],
        method: bindings.find((b) => b.name === m[2])?.method ?? "list",
        offset: m.index,
        guarded: false,
      });
      changed = true;
    }
    if (!changed) break;
  }

  return bindings;
}

/**
 * 找出"列表派生变量未经 asArray 就被当作列表用"的调用点。
 *
 * 判定依据:
 *   - 变量被当列表用:后接 .map/.forEach/… 之一,或按数字下标取值;
 *   - 变量没有防御:定义处不是 asArray(…)(…),且定义之后没有任何
 *     asArray(变量) / asArray<T>(变量) 形式的防御。
 */
function findUnguardedListUses(src: string, bindings: ListBinding[]): string[] {
  const violations: string[] = [];
  for (const b of bindings) {
    if (b.guarded) continue;
    const guardedRe = new RegExp(
      `asArray(?:<[^(]*>)?\\(\\s*${esc(b.name)}\\b`,
      "g",
    );
    let guarded = false;
    for (const g of src.matchAll(guardedRe)) {
      if (g.index! >= b.offset) {
        guarded = true;
        break;
      }
    }
    if (guarded) continue;

    const useRe = new RegExp(
      `\\b${esc(b.name)}\\s*(?:\\?\\.\\s*)?(?:\\.\\s*(?:${LIST_MEMBERS.join("|")})\\b|\\[\\s*\\d)`,
      "g",
    );
    for (const u of src.matchAll(useRe)) {
      if (u.index! < b.offset) continue;
      violations.push(
        `${b.method} → 变量 ${b.name}(第 ${u.index} 字符处)未经 asArray 就被当作列表使用`,
      );
      break;
    }
  }
  return violations;
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

  // 补"先赋值再 .map"的盲区:原守卫只看调用点之后的链,结果一旦交给变量
  // 链上就没有 .map 了,于是绕过。约定 7 要求的是"使用前必须过 asArray"。
  it("列表接口结果被赋值给变量后,使用前必须过 asArray", async () => {
    const files = await sourceFiles(SRC);
    const violations: string[] = [];
    let bindingCount = 0;
    for (const file of files) {
      const src = await readFile(file, "utf8");
      const bindings = collectListBindings(src);
      bindingCount += bindings.length;
      for (const v of findUnguardedListUses(src, bindings)) {
        violations.push(`${path.relative(SRC, file)}: ${v}`);
      }
    }
    expect(violations).toEqual([]);
    // 防空转:扫描器的识别逻辑退化时(接口改名/赋值形式变化),绑定会扫不到,
    // 上面的"没有违规"就会恒真通过。绑定数下限把它变成红灯。
    expect(bindingCount).toBeGreaterThan(3);
  });
});
