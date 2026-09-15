// 执行完成/等待输入的桌面通知(浏览器 Notification API;桌面模式同样可用)。
import { subscribeEvents } from "@/lib/api";

let enabled = false;

export function initNotifications() {
  if (enabled || typeof window === "undefined" || !("Notification" in window)) return;
  enabled = true;
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
        window.focus();
        // 通过 popstate 触发 React Router 的客户端导航(避免整页刷新)
        history.pushState({}, "", `/executions/${ev.execution_id}`);
        window.dispatchEvent(new PopStateEvent("popstate"));
        n.close();
      };
    } catch {
      // 通知失败不影响主流程
    }
  });
}
