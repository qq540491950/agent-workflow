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
//   2. 用 vi.doMock 把 mock 换成会抛错的版本,并清掉模块注册表(于是下一次
//      import 会重新求值工厂 → 抛错);
//   3. 此时 api 模块实例的模式缓存仍是 "desktop"(旧实例不受注册表重置
//      影响),事件流分支会去 import runtime → 失败 → 走 .catch 分支。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const callByID = vi.fn();

vi.mock("@wailsio/runtime", () => ({
  Call: { ByID: (...a: unknown[]) => callByID(...a) },
  Events: { On: vi.fn() },
}));

beforeEach(() => {
  callByID.mockReset().mockResolvedValue([]);
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  vi.doUnmock("@wailsio/runtime");
  vi.resetModules();
});

describe("桌面端事件流初始化", () => {
  it("runtime 导入失败时被报出来(不再只有 unhandled rejection)", async () => {
    vi.stubGlobal("window", {});
    const api = await import("./api");
    // 1. 先探测出桌面模式(此时 runtime 可用)
    await expect(api.detectMode()).resolves.toBe("desktop");

    // 2. 让 runtime 变得不可用,并清掉模块缓存:下一次 import 会重新求值
    //    工厂 → 抛错。api 模块实例的模式缓存仍是 "desktop"。
    vi.doMock("@wailsio/runtime", () => {
      throw new Error("runtime 不可用");
    });
    vi.resetModules();

    // 3. 订阅事件 → ensureEventStream 走桌面分支 → import 失败 → .catch
    api.subscribeEvents(() => {});
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));
    expect(toast.error).toHaveBeenCalled();
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("实时事件流");
  });
});
