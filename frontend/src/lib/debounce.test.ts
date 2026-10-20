// @vitest-environment jsdom
// debounce.ts:实时事件刷新防抖(约定 7:一律 200ms)。
//
// 此前这个模块**0 命中** —— 它是所有实时刷新路径(ExecutionMonitor /
// Layout / EventsAudit / Dashboard …)的共同入口,窗口期、尾沿语义、
// cancel 语义都没有任何用例钉着:有人把尾沿改成首沿、把 200ms 改成
// 50ms、或者忘了 cancel(),前端测试**全绿**,只有真机上表现为
// "刷新风暴"或"组件卸载后 setState"。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRefreshDebounce, REFRESH_DEBOUNCE_MS } from "./debounce";

describe("debounce", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("窗口期常量是 200ms(约定 7)", () => {
    expect(REFRESH_DEBOUNCE_MS).toBe(200);
  });

  it("尾沿语义:连续调用只在最后一次之后触发一次", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn);
    d();
    d();
    d();
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("窗口期边界:第 199ms 不触发,第 200ms 触发", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn);
    d();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS - 1);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("窗口期内再次调用会重置计时(不是固定间隔触发)", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn);
    d();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS - 1);
    d(); // 重置计时
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS - 1);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("cancel 取消尚未触发的刷新", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn);
    d();
    d.cancel();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS * 10);
    expect(fn).not.toHaveBeenCalled();
  });

  it("cancel 之后仍能再次触发(计时器状态被清干净)", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn);
    d();
    d.cancel();
    d();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("cancel 幂等:连续 cancel 两次不抛错", () => {
    const d = createRefreshDebounce(vi.fn());
    d();
    expect(() => {
      d.cancel();
      d.cancel();
    }).not.toThrow();
  });

  it("触发后可以再次触发(不是一次性)", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn);
    d();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS);
    d();
    vi.advanceTimersByTime(REFRESH_DEBOUNCE_MS);
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("自定义窗口期生效", () => {
    const fn = vi.fn();
    const d = createRefreshDebounce(fn, 50);
    d();
    vi.advanceTimersByTime(49);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
