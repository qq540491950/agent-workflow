import { BrowserRouter, Route, Routes } from "react-router-dom";
import { Toaster } from "@/components/ui/sonner";
import Layout from "@/components/layout";
import Dashboard from "@/pages/Dashboard";
import Workflows from "@/pages/Workflows";
import Designer from "@/pages/Designer";
import WorkflowHistory from "@/pages/WorkflowHistory";
import Executions from "@/pages/Executions";
import ExecutionMonitor from "@/pages/ExecutionMonitor";
import Agents from "@/pages/Agents";
import Skills from "@/pages/Skills";
import GitPanel from "@/pages/GitPanel";
import Settings from "@/pages/Settings";
import EventsAudit from "@/pages/EventsAudit";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Dashboard />} />
          <Route path="/workflows" element={<Workflows />} />
          <Route path="/workflows/:id/design" element={<Designer />} />
          <Route path="/workflows/:id/history" element={<WorkflowHistory />} />
          <Route path="/executions" element={<Executions />} />
          <Route path="/executions/:id" element={<ExecutionMonitor />} />
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
