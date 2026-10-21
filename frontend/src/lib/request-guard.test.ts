import { describe, expect, it } from "vitest";
import { createRequestGuard } from "./request-guard";

// 请求序号守卫(第二轮 P2-3):旧请求的迟到响应**不得**覆盖新请求的结果。
describe("createRequestGuard", () => {
  it("序号单调递增", () => {
    const g = createRequestGuard();
    const a = g.next();
    const b = g.next();
    expect(b).toBeGreaterThan(a);
    expect(g.latest()).toBe(b);
  });

  it("只有最新一次请求的响应算数", () => {
    const g = createRequestGuard();
    const first = g.next();
    const second = g.next();
    expect(g.isCurrent(first)).toBe(false);
    expect(g.isCurrent(second)).toBe(true);
  });

  it("invalidate 作废所有在飞请求", () => {
    const g = createRequestGuard();
    const seq = g.next();
    expect(g.isCurrent(seq)).toBe(true);
    g.invalidate();
    expect(g.isCurrent(seq)).toBe(false);
  });

  it("invalidate 之后取到的新序号仍然算数", () => {
    const g = createRequestGuard();
    g.invalidate();
    const seq = g.next();
    expect(g.isCurrent(seq)).toBe(true);
  });
});
