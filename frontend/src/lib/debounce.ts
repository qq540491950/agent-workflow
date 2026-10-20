// 实时事件刷新防抖(约定 7:一律 200ms)。
//
// 此前每个组件各自内联一份 setTimeout,窗口期并不一致:
// ExecutionMonitor 用 120ms、Layout/EventsAudit 用 200ms,
// Dashboard/Executions/WorkflowHistory 干脆没有防抖 —— 每条事件都直接
// 触发一次全量列表拉取(高频的 node.*/agent.output 事件下会打出成串请求)。
//
// 单一来源:窗口期与尾沿语义都收在这里,组件只负责调用;
// 组件卸载时必须 cancel(),避免对已卸载组件 setState。
export const REFRESH_DEBOUNCE_MS = 200;

export interface DebouncedRefresh {
  /** 触发一次防抖刷新(连续调用只会在最后一次之后触发) */
  (): void;
  /** 取消尚未触发的刷新(组件卸载时调用) */
  cancel(): void;
}

/**
 * 尾沿防抖:连续调用只在最后一次调用后 ms 毫秒触发一次 fn。
 * 刷新是"读最新状态"的动作,尾沿语义最贴合 —— 中间状态没有展示价值。
 */
export function createRefreshDebounce(
  fn: () => void,
  ms: number = REFRESH_DEBOUNCE_MS,
): DebouncedRefresh {
  let timer: number | undefined;
  const trigger = () => {
    if (timer !== undefined) window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      timer = undefined;
      fn();
    }, ms);
  };
  trigger.cancel = () => {
    if (timer !== undefined) {
      window.clearTimeout(timer);
      timer = undefined;
    }
  };
  return trigger;
}
