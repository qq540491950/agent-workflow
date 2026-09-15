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
import { api, detectMode, subscribeEvents } from "@/lib/api";
import type { Execution } from "@/lib/types";

const nav = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard },
  { to: "/workflows", label: "Workflows", icon: GitBranch },
  { to: "/executions", label: "Executions", icon: PlayCircle },
  { to: "/events", label: "Events", icon: ScrollText },
  { to: "/agents", label: "Agents", icon: Bot },
  { to: "/skills", label: "Skills", icon: Wrench },
  { to: "/git", label: "Git", icon: GitCompare },
  { to: "/settings", label: "Settings", icon: Settings },
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
          execs = execs ?? [];
          runningCount = execs.filter(
            (e) => e.state === "RUNNING" || e.state === "WAITING_USER",
          ).length;
          setRunning(runningCount);
        })
        .catch(() => {});
    };
    refresh();
    const unsub = subscribeEvents((ev) => {
      if (
        ev.type.startsWith("workflow.") ||
        ev.type.startsWith("node.") ||
        ev.type === "human.input_required"
      ) {
        refresh();
      }
    });
    return unsub;
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
          <div>mode: {mode}</div>
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
