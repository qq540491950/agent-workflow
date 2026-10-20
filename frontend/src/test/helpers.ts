// 前端组件测试的公共脚手架:假定时器推进 + 实时事件广播。
//
// 抽出来是为了让"防抖窗口"这类断言在多个页面里用同一套推进语义 ——
// 约定 7 要求实时事件刷新一律 200ms 防抖,若每个测试文件各写一份
// advance(),窗口期迟早悄悄不一致(此前 ExecutionMonitor 就是这么漂到 120ms 的)。
import { act } from "@testing-library/react";
import { vi } from "vitest";
import type { UIEvent } from "@/lib/types";

/** 推进假定时器 ms 毫秒,并在 act 内 flush 挂起的 promise 与状态更新 */
export async function advance(ms: number): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

/** 只 flush 挂起的 promise(不推进时间) */
export async function settle(): Promise<void> {
  await advance(0);
}

/** 构造一个最小可用的实时事件负载 */
export function makeEvent(type: string, execution_id = ""): UIEvent {
  return { type, execution_id, timestamp: "2026-01-01T00:00:00Z" };
}

/** 向所有已注册的实时事件订阅者广播一个事件 */
export function emit(
  handlers: ReadonlyArray<(ev: UIEvent) => void>,
  ev: UIEvent,
): void {
  act(() => {
    for (const h of [...handlers]) h(ev);
  });
}
