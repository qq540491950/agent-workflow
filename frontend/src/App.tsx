import { BrowserRouter, Route, Routes } from "react-router-dom";
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

export default function App() {
  useEffect(() => {
    initNotifications();
  }, []);
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Dashboard />} />
          <Route path="/workflows" element={<Workflows />} />
          <Route
            path="/workflows/:id/design"
            element={
              <Suspense fallback={<PageLoading />}>
                <Designer />
              </Suspense>
            }
          />
          <Route
            path="/workflows/:id/history"
            element={
              <Suspense fallback={<PageLoading />}>
                <WorkflowHistory />
              </Suspense>
            }
          />
          <Route path="/executions" element={<Executions />} />
          <Route
            path="/executions/:id"
            element={
              <Suspense fallback={<PageLoading />}>
                <ExecutionMonitor />
              </Suspense>
            }
          />
          <Route path="/agents" element={<Agents />} />
          <Route path="/skills" element={<Skills />} />
          <Route path="/git" element={<GitPanel />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/events" element={<EventsAudit />} />
        </Route>
      </Routes>
      <Toaster position="top-center" richColors />
    </BrowserRouter>
  );
}

function PageLoading() {
  return (
    <div className="flex h-full items-center justify-center text-muted-foreground">
      加载中…
    </div>
  );
}
