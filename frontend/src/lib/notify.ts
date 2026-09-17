// 执行完成/等待输入的桌面通知。
// 桌面模式:原生通知由 Go 侧 bridgeNotifications 发出(WKWebView 的
// Web Notification 权限模型不可靠),这里只监听通知点击跳转;
// 浏览器模式:走 Web Notification API。
import { api, subscribeEvents } from "@/lib/api";

let enabled = false;

function navigateToExecution(id: string) {
  window.focus();
  // 通过 popstate 触发 React Router 的客户端导航(避免整页刷新)
  history.pushState({}, "", `/executions/${id}`);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export async function initNotifications() {
  if (enabled || typeof window === "undefined") return;
  enabled = true;
  if (await api.isDesktopMode()) {
    const rt = await import("@wailsio/runtime");
    rt.Events.On("ui:open-execution", (data: unknown) => {
      const id = Array.isArray(data) ? String(data[0]) : String(data ?? "");
      if (id) navigateToExecution(id);
    });
    return;
  }
  if (!("Notification" in window)) return;
  if (Notification.permission === "default") {
    // 在用户首次交互后再请求权限,避免打扰
    const ask = () => {
      Notification.requestPermission().catch(() => {});
      window.removeEventListener("click", ask);
    };
    window.addEventListener("click", ask);
  }
  subscribeEvents((ev) => {
    if (Notification.permission !== "granted") return;
    let title = "";
    let body = "";
    switch (ev.type) {
      case "workflow.completed":
        title = "工作流已完成";
        body = `Execution ${ev.execution_id.slice(0, 12)}…`;
        break;
      case "workflow.failed":
        title = "工作流失败";
        body = String(ev.data?.error ?? ev.execution_id).slice(0, 80);
        break;
      case "human.input_required":
        title = "工作流等待你的输入";
        body = String(ev.data?.prompt ?? "").slice(0, 80);
        break;
      default:
        return;
    }
    try {
      const n = new Notification(title, { body, tag: ev.execution_id + ev.type });
      n.onclick = () => {
        navigateToExecution(ev.execution_id);
        n.close();
      };
    } catch {
      // 通知失败不影响主流程
    }
  });
}
