import { useEffect } from "react";

/**
 * 设置当前页面的 document.title。
 *
 * 桌面模式(Wails)的窗口标题跟随 document.title:此前所有页面共用
 * 一个静态标题,深链打开设计器/监控页时窗口标题分不清是哪条工作流、
 * 哪次执行。浏览器模式则是标签页标题。
 *
 * 卸载时恢复应用名,避免从设计器(带名字)跳回列表后残留旧标题。
 */
export function usePageTitle(title?: string): void {
  useEffect(() => {
    document.title = title
      ? `${title} · Agent Workflow`
      : "Agent Workflow Orchestrator";
    return () => {
      document.title = "Agent Workflow Orchestrator";
    };
  }, [title]);
}
