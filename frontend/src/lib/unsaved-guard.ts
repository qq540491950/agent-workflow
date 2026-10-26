import { useEffect } from "react";
import { useBlocker } from "react-router-dom";

/**
 * 未保存更改守卫:dirty 时拦住两条丢失路径。
 *
 *   1. **应用内跳转**(侧边栏 / 返回 / 任意 Link):useBlocker 拦下,
 *      confirm 确认后放行,取消则留在原地。
 *   2. **窗口关闭 / 刷新**:beforeunload(浏览器原生的"离开页面?"提示;
 *      桌面模式经 Wails 关窗是否触发由 Wails 决定,服务器模式必然生效)。
 *
 * 为什么要这个守卫:设计器画布上的编辑(拖节点、改配置)只存在于内存,
 * 误点侧边栏 = 半小时的编排静默蒸发(走查实测:没有任何提示)。这不是
 * "提示不够友好",是**数据丢失**。
 *
 * confirm(而不是自定义对话框)与仓库既有习惯一致(删除工作流、
 * script 开关都用 window.confirm),且它天然阻塞两条路径的处理逻辑。
 */
export function useUnsavedGuard(dirty: boolean, message: string): void {
  useEffect(() => {
    if (!dirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = message; // 旧式浏览器要求的赋值形态
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty, message]);

  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );

  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm(message)) {
      blocker.proceed();
    } else {
      blocker.reset();
    }
  }, [blocker, message]);
}
