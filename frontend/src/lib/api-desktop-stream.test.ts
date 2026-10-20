// @vitest-environment jsdom
// 桌面端事件流:@wailsio/runtime 导入失败时必须被报出来。
//
// 此前 ensureEventStream 的桌面分支是 `import("@wailsio/runtime").then(...)`
// —— **没有 catch**:导入失败时桌面端彻底收不到实时事件,界面上只剩一条
// unhandled rejection,用户看到的只是"页面不动",完全不知道原因。
//
// 这个失败点难造:detectMode() 自己也要 import 同一个模块(失败会被它捕获
// 并回退成 web 模式),所以必须让"探测用的那次 import 成功、事件流用的
// 那次 import 失败"。这里的造法是确定性的:
//   1. 先用可用的 mock 跑一次 detectMode() → 模式缓存成 "desktop";
//   2. 用 vi.doMock 把 mock 换成会抛错的版本。**真正让工厂重新求值的是
//      vi.doMock**(它注册一条新的 mock 条目,旧的工厂结果随之作废),
//      不是 vi.resetModules() —— 后者显式跳过 /^mock:/ 路径,手动 mock 的
//      工厂结果是被记忆的(resetModules 只清普通模块)。这条语义由
//      src/test/async-determinism-guard.test.ts 守着,别把 doMock 换成
//      提升的 vi.mock —— 那样工厂不会重新求值,本文件会静默退化成恒真测试。
//   3. 此时 api 模块实例的模式缓存仍是 "desktop"(旧实例不受注册表重置
//      影响),事件流分支会去 import runtime → 失败 → 走 .catch 分支。
//
// **同步点**:ensureEventStream() 是 fire-and-forget 的异步链(subscribeEvents
// 不返回它的 promise),测试没有可 await 的句柄。此前用
// `await new Promise(r => setTimeout(r, 0))` × 2 去"猜"这条链要跑完几个
// 事件循环节拍 —— 而它**不是常数**(vite 的 resolveId 命中缓存时 1 拍,
// 需要 fs 往返时 2 拍):实测全量跑 20 次挂 1 次(AssertionError: expected
// "vi.fn()" to be called at least once),并行时更容易撞上。
// 现在改成等**真正发生的那件事**(toast.error 被调用),节拍数不再进入断言。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

// vi.hoisted:工厂函数本身要在 vi.mock 被提升到文件顶部**之前**就存在,
// 否则提升后的 vi.mock 会撞上 const 的 TDZ。
const { callByID, runtimeMock } = vi.hoisted(() => {
  const callByID = vi.fn();
  return {
    callByID,
    runtimeMock: () => ({
      Call: { ByID: (...a: unknown[]) => callByID(...a) },
      Events: { On: vi.fn() },
    }),
  };
});

vi.mock("@wailsio/runtime", runtimeMock);

beforeEach(() => {
  callByID.mockReset().mockResolvedValue([]);
  // mockReset 而不是 mockClear:mockClear 只清调用记录、**不清实现** ——
  // 用例里装的"被调用即 resolve"实现会漏到下一个用例。
  vi.mocked(toast.error).mockReset();
  vi.unstubAllGlobals();
  // 每个用例都重新装一遍 runtime 的 mock。
  //
  // 此前 afterEach 里的 vi.doUnmock 会把 mock 注册项**整个删掉**(不只是让
  // 缓存失效):于是从第二个用例起,import("@wailsio/runtime") 拿到的是真
  // 模块,detectMode() 直接退回 web 模式 —— 用例之间互相影响,顺序一变就红。
  // 这里改成"每个用例开始时装一个已知的 mock",顺序就与结果无关了。
  vi.doMock("@wailsio/runtime", runtimeMock);
});

afterEach(() => {
  vi.resetModules();
});

/**
 * 把 toast.error 换成"被调用即 resolve"的信号。
 *
 * 返回的 promise 在**错误真的被报出来**时才 resolve —— 这是本文件唯一的
 * 同步点:事件流初始化走几拍都不影响断言(此前固定等 2 拍,实测会不够)。
 * 若错误始终没被报出,promise 永不 resolve,用例会在 vitest 的超时处失败
 * (而不是误判成"没报错")。
 */
function catchErrorToast(): Promise<string> {
  let resolveReported!: (message: string) => void;
  const reported = new Promise<string>((resolve) => {
    resolveReported = resolve;
  });
  vi.mocked(toast.error).mockImplementation((message) => {
    resolveReported(String(message));
    return "";
  });
  return reported;
}

describe("桌面端事件流初始化", () => {
  it("runtime 导入失败时被报出来(不再只有 unhandled rejection)", async () => {
    vi.stubGlobal("window", {});
    const api = await import("./api");
    // 1. 先探测出桌面模式(此时 runtime 可用)
    await expect(api.detectMode()).resolves.toBe("desktop");

    // 2. 让 runtime 变得不可用:doMock 注册新条目 → 工厂缓存作废 →
    //    下一次 import 重新求值工厂 → 抛错。api 实例的模式缓存仍是 "desktop"。
    vi.doMock("@wailsio/runtime", () => {
      throw new Error("runtime 不可用");
    });
    vi.resetModules();

    // 3. 订阅事件 → ensureEventStream 走桌面分支 → import 失败 → .catch
    //    同步点是"错误真的被报出来",不是猜事件循环节拍数。
    const reported = catchErrorToast();
    api.subscribeEvents(() => {});
    const message = await reported;
    expect(message).toContain("实时事件流");
    expect(vi.mocked(toast.error)).toHaveBeenCalledTimes(1);
  });

  // 守卫:ensureEventStream 的 catch 分支把 started 复位(false),允许后续
  // 订阅重试(导入失败可能是瞬时的,永久卡死则事件流永远不可用)。
  // 此前这条路径**零覆盖** —— 把 started = false 删掉不会有任何用例变红。
  it("初始化失败不会把事件流卡死(后续订阅会重试)", async () => {
    vi.stubGlobal("window", {});
    const api = await import("./api");
    await expect(api.detectMode()).resolves.toBe("desktop");

    // 同步点分两处,各对准一件确定已经发生的事:
    //   - 第一次等 **toast**:toast 是在 catch 分支里报出来的,而 catch 分支
    //     先把 started 复位(false)再报错 —— 等到 toast 就等于等到"失败已被
    //     处理完",此时再订阅才是对 started 复位的真实检验。
    //   - 第二次等 **工厂被求值**:工厂每次被求值 = 一次初始化尝试。之所以
    //     不等第二次 toast:vitest 把工厂抛出的错误统一包成同一句固定文案
    //     ("[vitest] There was an error when mocking a module"),两次失败文案
    //     相同 → reportError 的 5 秒去重会把第二次提示静默掉。
    let attempts = 0;
    let notifyAttempt: () => void = () => {};
    const nextAttempt = () =>
      new Promise<void>((resolve) => {
        notifyAttempt = resolve;
      });
    vi.doMock("@wailsio/runtime", () => {
      attempts++;
      notifyAttempt();
      throw new Error("runtime 不可用");
    });
    vi.resetModules();

    const reported = catchErrorToast();
    api.subscribeEvents(() => {});
    await reported;
    expect(attempts).toBe(1);

    // started 已被 catch 分支复位 → 第二次订阅会重新走一遍初始化。
    const second = nextAttempt();
    api.subscribeEvents(() => {});
    await second;
    expect(attempts).toBe(2);
  });
});
