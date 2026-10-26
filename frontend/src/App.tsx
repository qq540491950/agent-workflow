import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { lazy, Suspense, useEffect } from "react";
import { initNotifications } from "@/lib/notify";
import { Toaster } from "@/components/ui/sonner";
import Layout from "@/components/layout";
import Dashboard from "@/pages/Dashboard";
import Workflows from "@/pages/Workflows";
import Executions from "@/pages/Executions";
import Agents from "@/pages/Agents";
import Skills from "@/pages/Skills";
import GitPanel from "@/pages/GitPanel";
import Settings from "@/pages/Settings";
import EventsAudit from "@/pages/EventsAudit";

// 路由级代码分割:React Flow/dagre 等重组件只在进入对应页面时加载
const Designer = lazy(() => import("@/pages/Designer"));
const WorkflowHistory = lazy(() => import("@/pages/WorkflowHistory"));
const ExecutionMonitor = lazy(() => import("@/pages/ExecutionMonitor"));

// createBrowserRouter(data router):设计器的未保存守卫(unsaved-guard)
// 依赖 useBlocker,而 useBlocker 只在 data router 下可用 —— BrowserRouter +
// <Routes> 会直接抛错。路由形状与原来一一对应,只是搬进了 children。
const router = createBrowserRouter([
  {
    element: <Layout />,
    children: [
      { path: "/", element: <Dashboard /> },
      { path: "/workflows", element: <Workflows /> },
      {
        path: "/workflows/:id/design",
        element: (
          <Suspense fallback={<PageLoading />}>
            <Designer />
          </Suspense>
        ),
      },
      {
        path: "/workflows/:id/history",
        element: (
          <Suspense fallback={<PageLoading />}>
            <WorkflowHistory />
          </Suspense>
        ),
      },
      { path: "/executions", element: <Executions /> },
      {
        path: "/executions/:id",
        element: (
          <Suspense fallback={<PageLoading />}>
            <ExecutionMonitor />
          </Suspense>
        ),
      },
      { path: "/agents", element: <Agents /> },
      { path: "/skills", element: <Skills /> },
      { path: "/git", element: <GitPanel /> },
      { path: "/settings", element: <Settings /> },
      { path: "/events", element: <EventsAudit /> },
    ],
  },
]);

export default function App() {
  useEffect(() => {
    initNotifications();
  }, []);
  return (
    <>
      <RouterProvider router={router} />
      <Toaster position="top-center" richColors />
    </>
  );
}

function PageLoading() {
  return (
    <div className="flex h-full items-center justify-center text-muted-foreground">
      加载中…
    </div>
  );
}
