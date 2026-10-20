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

// CALL_RE 匹配列表接口调用点。
//
// api 与方法名之间**允许空白/换行**:真实代码里存在
//
//   api
//     .listAgents()
//
// 这种换行写法(实测:pages/Designer.tsx、pages/Workflows.tsx、
// components/layout.tsx 各有一处)。原正则要求 "api." 紧挨着,
// 于是这三处调用点对守卫**完全不可见** —— 调用点级判定(直接 .map、
// 先赋值再 .map)对它们一律失效,只剩文件级判定兜着。
const CALL_RE = new RegExp(
  `\\bapi\\s*\\.\\s*(${LIST_METHODS.join("|")})\\s*\\(`,
  "g",
);

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
 * stripComments 把注释与字符串字面量的内容替换成**等长空格**。
 *
 * 为什么需要:守卫是**源码扫描**,注释里出现的代码形态会被当成真代码 ——
 * 实测 components/layout.tsx 的注释"这一异常返回,execs.filter 会抛
 * TypeError"让守卫把注释里的 execs.filter 当成了"未防御的使用"(误报)。
 *
 * 保持长度不变 → 所有偏移量仍然有效(调用点、绑定、使用的位置都对得上)。
 */
function stripComments(src: string): string {
  const out = src.split("");
  const n = src.length;
  const blank = (from: number, to: number) => {
    for (let k = from; k < to && k < n; k++) {
      if (out[k] !== "\n") out[k] = " ";
    }
  };
  let i = 0;
  while (i < n) {
    const c = src[i];
    const next = src[i + 1];
    if (c === "/" && next === "/") {
      let j = i;
      while (j < n && src[j] !== "\n") j++;
      blank(i, j);
      i = j;
      continue;
    }
    if (c === "/" && next === "*") {
      let j = i + 2;
      while (j < n && !(src[j] === "*" && src[j + 1] === "/")) j++;
      j = Math.min(j + 2, n);
      blank(i, j);
      i = j;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      const quote = c;
      let j = i + 1;
      while (j < n) {
        if (src[j] === "\\") {
          j += 2;
          continue;
        }
        if (src[j] === quote) {
          j++;
          break;
        }
        j++;
      }
      blank(i, j);
      i = j;
      continue;
    }
    i++;
  }
  return out.join("");
}

/**
 * enclosingArrayRange 返回包含 pos 的**数组字面量**的范围([ 的位置, ] 之后的位置)。
 * 不在数组字面量里时返回 null。
 *
 * 为什么需要:解构赋值 `const [a, b] = await Promise.all([api.f(), api.g()])`
 * 里,调用点的位置对应关系要靠**整个数组字面量**的元素顺序才能确定。
 * 此前只取"调用点结束之前"的文本 → 数组没截全 → 位置对不上 →
 * 退化成"全部变量名都当列表派生" → 非列表的返回值(比如 getExecution
 * 的单个对象)也被当成列表 → 误报。
 */
function enclosingArrayRange(src: string, pos: number): [number, number] | null {
  let depth = 0;
  for (let i = pos; i >= 0; i--) {
    const c = src[i];
    if (c === "]") depth++;
    else if (c === "[") {
      if (depth === 0) {
        const end = matchBracket(src, i, "[", "]");
        return end < 0 ? null : [i, end];
      }
      depth--;
    }
  }
  return null;
}

/** 返回与 open 位置的开括号匹配的闭括号之后的位置(找不到返回 -1) */
function matchBracket(src: string, open: number, o: string, c: string): number {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === o) depth++;
    else if (src[i] === c) {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  return -1;
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
  const re = new RegExp(
    `\\bapi\\s*\\.\\s*(${LIST_METHODS.join("|")})\\s*\\(`,
    "g",
  );
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
  /** 派生自哪个调用点(同一个调用点的多个派生共享它) */
  callStart: number;
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
  // 注释与字符串先抹平(内容换成等长空格,偏移量不变):
  // 注释里的代码形态不是代码,扫描它会误报。
  src = stripComments(src);
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
            callStart,
          });
        } else {
          // 解构:按 Promise.all([…]) 的元素位置对应变量名。
          //
          // 元素来自**包含该调用点的数组字面量**(而不是"调用点之前"的
          // 文本):后者会把数组截断 → 位置对不上 → 退化成"全部变量名
          // 都当列表派生" → 非列表返回值(如 getExecution 的单个对象)
          // 也被当成列表 → 误报。
          const range = enclosingArrayRange(src, callStart);
          const elements =
            range !== null ? arrayElements(src, range[0]) : [];
          if (elements.length === target.names.length) {
            elements.forEach((el, i) => {
              const method2 = listCallIn(el);
              if (method2) {
                add({
                  name: target.names[i],
                  method: method2,
                  offset: assign,
                  guarded: false,
                  callStart,
                });
              }
            });
          } else {
            // 位置对不上(默认值/展开/嵌套):保守地把全部名字当列表派生。
            target.names.forEach((n) =>
              add({ name: n, method, offset: assign, guarded: false, callStart }),
            );
          }
        }
      }
    }

    // 回调形参:api.listX().then((x) => …)
    //
    // 形参可能带类型标注(.then((execs: Execution[] | null) => …)):
    // 原正则要求形参后面紧跟 ")",带标注的写法匹配不上 → 该绑定
    // 不可见 → 守卫看不见这条调用点的防御情况(实测:components/layout.tsx
    // 的 listExecutions 调用点正是这种写法)。
    const tail = src.slice(callEnd);
    const cb =
      /^\s*\.\s*then\s*\(\s*(?:async\s*)?\(\s*([A-Za-z_$][\w$]*)\s*(?::[^)]*)?\)/.exec(
        tail,
      );
    if (cb) {
      add({ name: cb[1], method, offset: callEnd, guarded: false, callStart });
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
        callStart: cm.index!,
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
      const src2 = bindings.find((b) => b.name === m[2]);
      bindings.push({
        name: m[1],
        method: src2?.method ?? "list",
        offset: m.index,
        guarded: false,
        callStart: src2?.callStart ?? m.index,
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
 * 判定依据(按**使用**判定,不是按"出现"判定):
 *   - 变量被当列表用:
 *       ① 后接 .map/.forEach/… 之一(遍历/按索引取/取长度);
 *       ② 按数字下标取值(x[0]);
 *       ③ **作为实参传给函数**(setRows(x) / f(x))—— 这是 #210 的最后一块:
 *          此前只判 ①②,而 `api.listX().then((x) => setRows(x))` 里 x 后面
 *          跟的是 ")" → 放行 → 丢掉 asArray 不会被发现。
 *          为什么 ③ 也算"当列表用":setRows(x) 之后渲染处必然要遍历它,
 *          x 若是 null 或对象就抛 TypeError —— 与 ① 是同一类故障。
 *   - 变量没有防御:定义处不是 asArray(…)(…),且**第一次使用之前**没有任何
 *     asArray(变量) / asArray<T>(变量) 形式的防御。
 *
 * 顺序也重要:防御必须出现在使用**之前** —— 先 setRows(rows) 再
 * asArray(rows) 是无效防御(渲染早就拿到未防御的值了)。
 */
function findUnguardedListUses(src: string, bindings: ListBinding[]): string[] {
  const violations: string[] = [];
  src = stripComments(src); // 同上:注释不是代码
  const ranges = asArrayRanges(src);
  for (const b of bindings) {
    if (b.guarded) continue;
    const uses = findListUses(src, b.name, b.offset, ranges);
    if (uses.length === 0) continue;
    if (bindingIsGuarded(src, ranges, b, uses[0])) continue;
    violations.push(
      `${b.method} → 变量 ${b.name}(第 ${b.offset} 字符处定义,"${uses[0].kind}")未经 asArray 就被当作列表使用`,
    );
  }
  return violations;
}

/**
 * bindingIsGuarded:绑定的**第一次使用之前**是否有 asArray 防御。
 *
 * 顺序也重要:防御必须出现在使用**之前** —— 先 setRows(rows) 再
 * asArray(rows) 是无效防御(渲染早就拿到未防御的值了)。
 */
function bindingIsGuarded(
  src: string,
  ranges: Array<[number, number]>,
  b: ListBinding,
  firstUse: ListUse,
): boolean {
  const guardedRe = new RegExp(`asArray(?:<[^(]*>)?\\(\\s*${esc(b.name)}\\b`, "g");
  for (const g of src.matchAll(guardedRe)) {
    if (g.index! >= b.offset && g.index! < firstUse.index) return true;
  }
  return false;
}

/**
 * findUnguardedCallSites:逐**调用点**判定"这个列表接口的结果有没有过 asArray"。
 *
 * 为什么要有"逐调用点"这一层(#210):
 *   - 文件级判定("这个文件里出现过 asArray 就算过")太粗:同一个文件里
 *     两个调用点,一个防御了、另一个没防御,文件级判定照样放行;
 *   - 绑定级判定(findUnguardedListUses)已经能抓到"未防御的使用",
 *     但它按**变量**报告,看不出是哪个调用点带来的。
 *
 * 判据:调用点的结果要么
 *   ① 本身被 asArray 包裹(asArray(await api.listX())),要么
 *   ② 流出到一个**受保护**的绑定(定义处自带 asArray,或首次使用前有 asArray)。
 * 两条都不满足 → 该调用点判违规。
 */
function findUnguardedCallSites(src: string): string[] {
  const masked = stripComments(src);
  const ranges = asArrayRanges(masked);
  const bindings = collectListBindings(src);
  const violations: string[] = [];
  for (const m of masked.matchAll(CALL_RE)) {
    const method = m[1];
    const callStart = m.index!;
    // ① 调用点本身在 asArray 里 → 已防御
    if (ranges.some(([s, e]) => callStart > s && callStart < e)) continue;
    // ② 结果流出的绑定必须受保护
    const derived = bindings.filter((b) => b.callStart === callStart);
    for (const b of derived) {
      if (b.guarded) continue;
      const uses = findListUses(masked, b.name, b.offset, ranges);
      if (uses.length === 0) continue; // 派生出来但没被当列表用 → 无风险
      if (bindingIsGuarded(masked, ranges, b, uses[0])) continue;
      violations.push(
        `api.${method}() 的结果(变量 ${b.name})未经 asArray 就被当作列表使用`,
      );
    }
  }
  return violations;
}

/** 一次"当列表用"的形态 */
interface ListUse {
  index: number;
  kind: string;
}

/** 控制关键字:出现在 "(" 前时,后面跟的表达式不是"函数实参" */
const CONTROL_KEYWORDS = new Set([
  "if",
  "for",
  "while",
  "switch",
  "catch",
  "return",
  "await",
  "typeof",
]);

/**
 * asArrayRanges 返回源码里所有 asArray(...) 调用的区间。
 *
 * 用途:判断某处代码是不是**在 asArray 里** —— 在 asArray 里的出现是
 * "防御",不是"使用"(asArray(x) 里的 x 正是被防御的那个值)。
 * 支持泛型写法 asArray<T>(x)。
 */
function asArrayRanges(src: string): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  const re = /\basArray\b/g;
  for (const m of src.matchAll(re)) {
    let i = m.index! + m[0].length;
    while (i < src.length && /\s/.test(src[i])) i++;
    // 跳过泛型参数
    if (src[i] === "<") {
      let depth = 0;
      for (; i < src.length; i++) {
        if (src[i] === "<") depth++;
        else if (src[i] === ">") {
          depth--;
          if (depth === 0) {
            i++;
            break;
          }
        }
      }
      while (i < src.length && /\s/.test(src[i])) i++;
    }
    if (src[i] !== "(") continue;
    const end = matchParen(src, i);
    if (end < 0) continue;
    out.push([m.index!, end]);
  }
  return out;
}

/**
 * findListUses 找出变量 name 在 from 之后所有"当列表用"的位置。
 *
 * ranges = asArray 调用区间:落在 asArray 里的出现**不算**使用
 * (那正是防御本身)。
 */
function findListUses(
  src: string,
  name: string,
  from: number,
  ranges: Array<[number, number]>,
): ListUse[] {
  const uses: ListUse[] = [];
  const inAsArray = (idx: number) =>
    ranges.some(([s, e]) => idx > s && idx < e);
  const ident = new RegExp(`\\b${esc(name)}\\b`, "g");
  for (const m of src.matchAll(ident)) {
    const idx = m.index!;
    if (idx < from) continue;
    if (inAsArray(idx)) continue; // 在 asArray 里 = 防御,不是使用
    const after = src.slice(idx + name.length);
    const before = src.slice(0, idx);
    // ① 成员访问:x.map / x.length / x?.map
    const member = /^\s*(?:\?\.|\.)\s*([A-Za-z_$][\w$]*)/.exec(after);
    if (member && LIST_MEMBERS.includes(member[1])) {
      uses.push({ index: idx, kind: `x.${member[1]}` });
      continue;
    }
    // ② 数字下标:x[0]
    if (/^\s*\[\s*\d/.test(after)) {
      uses.push({ index: idx, kind: "x[数字]" });
      continue;
    }
    // ③ 作为实参传给函数:setRows(x) / f(x, …)
    //    判据:紧邻的前一个非空白字符是 "(" 或 ",",
    //    且 "(" 前面不是控制关键字(if/for/while/… —— 那是条件判断,不是传参)。
    const trimmed = before.replace(/\s+$/, "");
    const prev = trimmed.slice(-1);
    if (prev === "(" || prev === ",") {
      if (prev === "(") {
        const head = trimmed.slice(0, -1).replace(/\s+$/, "");
        const fn = /([A-Za-z_$][\w$]*)$/.exec(head);
        if (fn && CONTROL_KEYWORDS.has(fn[1])) continue; // if (x) 不是传参
        if (!fn) continue; // (x) 是分组括号,不是调用
      }
      uses.push({ index: idx, kind: "作为实参传给函数" });
      continue;
    }
  }
  return uses;
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

  // 补"回调形参 → 状态 setter"这条链的盲区(#210 的最后一块):
  //   api.listX().then((x) => setRows(x))
  //
  // 此前"当列表用"的判据只有 `x.<列表成员>` 或 `x[数字]`,而这里 x 后面
  // 跟的是 ")" → 判定放行 → 丢掉 asArray 不会被发现(实测:把 Dashboard
  // 两个调用点的 asArray 去掉,守卫 4 条用例全绿)。
  //
  // 为什么"直接把列表结果交给状态 setter"就是"当列表用":setRows(x) 之后
  // 渲染处必然要遍历它(rows.map / rows.length)—— x 若是 null 或对象,
  // 抛 TypeError,整页白屏。与 .map 链是同一类故障。
  it("列表结果直接交给状态 setter 时必须判违规(去掉 asArray 不得放行)", () => {
    const unguarded = [
      "useEffect(() => {",
      '  api.listExecutions("", 50).then((x) => setRows(x));',
      "}, []);",
    ].join("\n");
    const violations = findUnguardedListUses(unguarded, collectListBindings(unguarded));
    expect(violations.length).toBeGreaterThan(0);

    // 有防御的写法不得误报(防"永远判违规"的假修法)。
    const guarded = unguarded.replace("setRows(x)", "setRows(asArray(x))");
    expect(findUnguardedListUses(guarded, collectListBindings(guarded))).toEqual([]);
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

  // 逐**调用点**判定(#210):文件级判定太粗 —— 同一文件里两个调用点,
  // 一个防御了、另一个没防御,文件级判定照样放行。
  // 这一条把判定下沉到调用点:每个列表接口的调用点,它的结果要么
  // 本身被 asArray 包裹,要么流出一个受保护的绑定。
  it("每个列表调用点的结果都必须过 asArray(逐调用点判定)", async () => {
    const files = await sourceFiles(SRC);
    const violations: string[] = [];
    let callSites = 0;
    for (const file of files) {
      const src = await readFile(file, "utf8");
      callSites += [...src.matchAll(CALL_RE)].length;
      for (const v of findUnguardedCallSites(src)) {
        violations.push(`${path.relative(SRC, file)}: ${v}`);
      }
    }
    expect(violations).toEqual([]);
    // 防空转:调用点扫不到(接口改名/正则失效)时,上面的"没有违规"会恒真
    // 通过。实测当前仓库有 15 处列表调用点,取 10 作下限。
    expect(callSites).toBeGreaterThanOrEqual(10);
  });

  // 守卫自身的盲区回归(不是产品代码的缺陷,是**判定逻辑**的缺陷):
  //
  // 1. 原 CALL_RE 要求 "api." 紧挨着方法名,于是换行写法
  //      api
  //        .listAgents()
  //    对调用点级判定**完全不可见** —— 实测仓库里有三处这种写法
  //    (pages/Designer.tsx、pages/Workflows.tsx、components/layout.tsx),
  //    它们只被文件级判定兜着。
  // 2. 回调形参带类型标注时(.then((execs: Execution[] | null) => …)),
  //    原正则匹配不上 → 该绑定不可见。
  //
  // 下面用**合成源码**验证判定逻辑本身:换行调用点必须被看见,
  // 且去掉防御后必须被判违规(否则守卫对这三处是瞎的)。
  it("换行写法的列表调用点必须被识别,且失去防御时必须判违规", () => {
    const guarded = [
      "useEffect(() => {",
      "  api",
      "    .listAgents()",
      "    .then((as) => setAgents(asArray<AgentInfo>(as).map((a) => a.id)))",
      "    .catch(() => {});",
      "}, []);",
    ].join("\n");
    const unguarded = [
      "useEffect(() => {",
      "  api",
      "    .listAgents()",
      "    .then((as) => setAgents(as.map((a) => a.id)))",
      "    .catch(() => {});",
      "}, []);",
    ].join("\n");

    // 换行调用点必须被看见(防空转:看不见的话下面两条断言都恒真)。
    expect([...guarded.matchAll(CALL_RE)].length).toBe(1);
    expect([...unguarded.matchAll(CALL_RE)].length).toBe(1);

    // 带类型标注的回调形参必须被识别成绑定。
    const typed = [
      "api",
      "  .listExecutions(\"\", 50)",
      "  .then((execs: Execution[] | null) => {",
      "    const list = asArray<Execution>(execs);",
      "    setRunning(list.filter((e) => e.state === \"RUNNING\").length);",
      "  });",
    ].join("\n");
    const typedBindings = collectListBindings(typed);
    expect(typedBindings.map((b) => b.name)).toContain("execs");

    // 去掉防御后必须判违规(换行 + 带类型标注两种形态都要覆盖)。
    for (const src of [unguarded, typed.replace("asArray<Execution>(execs)", "execs")]) {
      const violations = findUnguardedListUses(src, collectListBindings(src));
      expect(violations.length).toBeGreaterThan(0);
    }

    // 文件级判定的盲区:同一个文件里**一个调用点有防御、另一个没有**。
    // 文件级判定("这个文件里出现过 asArray 就算过")会放行;逐调用点
    // 判定必须抓到没防御的那一个。
    const mixed = [
      "useEffect(() => {",
      "  api.listAgents().then((x) => setAgents(asArray(x)));",
      "  api.listSkills().then((y) => setSkills(y));",
      "}, []);",
    ].join("\n");
    expect(mixed).toContain("asArray"); // 前置:文件里确实有 asArray
    const mixedViolations = findUnguardedCallSites(mixed);
    expect(mixedViolations.length).toBeGreaterThan(0);
    expect(mixedViolations.join(" ")).toContain("listSkills");
  });
});
