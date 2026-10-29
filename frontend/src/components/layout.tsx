import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import {
  LayoutDashboard,
  GitBranch,
  PlayCircle,
  Bot,
  Wrench,
  Activity,
  GitCompare,
  Settings,
  ScrollText,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { api, asArray, detectMode, subscribeEvents } from "@/lib/api";
import { createRefreshDebounce } from "@/lib/debounce";
import { reportError } from "@/lib/errors";
import { modeLabel } from "@/lib/labels";
import type { Execution } from "@/lib/types";

// 导航项用中文(用户可读);Git 等专有名词保留。此前直接用路由名
// (Dashboard/Workflows/…),是"文案从数据模型贴出来"的典型。
const nav = [
  { to: "/", label: "总览", icon: LayoutDashboard },
  { to: "/workflows", label: "工作流", icon: GitBranch },
  { to: "/executions", label: "执行记录", icon: PlayCircle },
  { to: "/events", label: "事件", icon: ScrollText },
  { to: "/agents", label: "Agent", icon: Bot },
  { to: "/skills", label: "技能", icon: Wrench },
  { to: "/git", label: "Git", icon: GitCompare },
  { to: "/settings", label: "设置", icon: Settings },
];

export default function Layout() {
  const location = useLocation();
  const [running, setRunning] = useState(0);
  const [mode, setMode] = useState("…");
  useEffect(() => {
    detectMode().then((m) => setMode(m));
  }, []);

  // 侧栏显示正在运行的执行数(实时事件驱动)
  useEffect(() => {
    let runningCount = 0;
    const refresh = () => {
      api
        .listExecutions("", 50)
        .then((execs: Execution[] | null) => {
          // asArray 而非 `?? []`:后者挡不住"被解析成 HTML 兜底页的对象"
          // 这一异常返回,execs.filter 会抛 TypeError 让整个 Layout 崩溃
          // (影响面是所有页面,比单页列表更严重)。
          const list = asArray<Execution>(execs);
          runningCount = list.filter(
            (e) => e.state === "RUNNING" || e.state === "WAITING_USER",
          ).length;
          setRunning(runningCount);
        })
        // 此前是 `.catch(() => {})`:侧栏的运行中计数加载失败时**静默**
        // 停在 0,用户看到的是"没有运行中的任务",实际是请求失败。Layout
        // 挂在所有页面上、且每次路由变化都会重新挂载,同一个错误会被报很多
        // 次 —— reportError 内部按 5 秒窗口去重,不会变成提示风暴。
        .catch((e: unknown) => reportError(e, "加载运行中执行数"));
    };
    refresh();
    // 防抖:node.* 等高频事件不应逐条触发执行列表拉取(约定 7:一律 200ms)
    const trigger = createRefreshDebounce(refresh);
    const unsub = subscribeEvents((ev) => {
      if (
        ev.type.startsWith("workflow.") ||
        ev.type.startsWith("node.") ||
        ev.type === "human.input_required"
      ) {
        trigger();
      }
    });
    return () => {
      unsub();
      trigger.cancel();
    };
  }, [location.pathname]);

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      {/* 侧栏 */}
      <aside className="flex w-56 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground">
        <div className="flex items-center gap-2 px-4 py-4">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Activity className="h-4 w-4" />
          </div>
          <div>
            <div className="text-sm font-semibold leading-tight">
              Agent Workflow
            </div>
            <div className="text-[10px] text-muted-foreground">Orchestrator</div>
          </div>
        </div>
        <nav className="flex-1 space-y-1 px-2">
          {nav.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              end={to === "/"}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
                    : "text-muted-foreground hover:bg-sidebar-accent/50 hover:text-foreground",
                )
              }
            >
              <Icon className="h-4 w-4" />
              {label}
              {to === "/executions" && running > 0 && (
                <span className="ml-auto inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-green-600/20 px-1.5 text-[10px] font-medium text-green-500">
                  {running}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
        <div className="border-t px-4 py-3 text-[10px] text-muted-foreground">
          {/* 运行模式显示中文;未知值由 modeLabel 回退原文 */}
          <div>模式:{modeLabel(mode)}</div>
          <div>ADK Go runtime · Wails 3</div>
        </div>
      </aside>

      {/* 内容区 */}
      <main className="flex-1 overflow-hidden">
        <Outlet />
      </main>
    </div>
  );
}
