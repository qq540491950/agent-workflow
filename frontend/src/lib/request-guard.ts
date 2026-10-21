// 请求序号守卫:防止"旧请求的迟到响应覆盖新请求的结果"。
//
// 场景(第二轮扫描实测,真实组件):
//
//   - EventsAudit:自定义前缀每次按键发一次请求(旧实现的 refresh 依赖
//     filter,输入一变 effect 就重跑)→ 旧前缀的迟到响应覆盖新前缀的结果
//     (输入框是 "node.",列表却是 "n" 的结果);
//   - ExecutionMonitor:切换 :id 时旧执行的响应覆盖新执行 → 页面显示
//     **上一条执行**的数据;而"取消执行"/"重试失败节点"按 exec.id 操作,
//     可能作用在用户以为不是的那条执行上。
//
// 语义:每次发请求前取一个**单调递增**的序号;响应回来时只有"序号仍是
// 最新的"才允许写状态,否则整条丢弃(连错误提示也不弹 —— 旧请求的失败
// 与当前页面无关,弹出来只会误导)。
//
// 单一来源:序号语义只在这里实现,组件只负责"取号 / 比对号"。
// 与 lib/debounce.ts 并列:debounce 管"什么时候发",本守卫管"回来的
// 响应还算不算数"。
export interface RequestGuard {
  /** 取下一个请求序号(发请求前调用) */
  next(): number;
  /** 当前最新序号(用于比对) */
  latest(): number;
  /** 该序号的响应是否仍然有效(它仍是最新的一次请求) */
  isCurrent(seq: number): boolean;
  /** 作废所有在飞请求(组件卸载时调用) */
  invalidate(): void;
}

/**
 * 创建一个请求序号守卫。
 *
 * 组件里用 useMemo 固定一个实例(序号必须在多次渲染之间连续):
 *
 *   const guard = useMemo(() => createRequestGuard(), []);
 */
export function createRequestGuard(): RequestGuard {
  let seq = 0;
  return {
    next(): number {
      seq += 1;
      return seq;
    },
    latest(): number {
      return seq;
    },
    isCurrent(s: number): boolean {
      return s === seq;
    },
    invalidate(): void {
      // 序号只增不减:在飞请求拿到的都是更小的序号,自此全部失效。
      seq += 1;
    },
  };
}
