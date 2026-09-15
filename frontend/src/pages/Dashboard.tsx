import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  Activity,
  CheckCircle2,
  XCircle,
  CircleDashed,
  GitBranch,
  ArrowRight,
} from "lucide-react";
import { api, subscribeEvents } from "@/lib/api";
import type { Execution, Workflow } from "@/lib/types";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { StateBadge } from "@/components/state-badge";

export default function Dashboard() {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [executions, setExecutions] = useState<Execution[]>([]);

  useEffect(() => {
    api.listWorkflows().then(setWorkflows).catch(() => {});
    api.listExecutions("", 8).then(setExecutions).catch(() => {});
    const unsub = subscribeRefresh(() => {
      api.listExecutions("", 8).then(setExecutions).catch(() => {});
    });
    return unsub;
  }, []);

  const running = executions.filter((e) => e.state === "RUNNING").length;
  const waiting = executions.filter((e) => e.state === "WAITING_USER").length;
  const completed = executions.filter((e) => e.state === "COMPLETED").length;
  const failed = executions.filter(
    (e) => e.state === "FAILED" || e.state === "CANCELLED",
  ).length;

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <p className="text-sm text-muted-foreground">
          多 Agent 工作流编排总览
        </p>
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-5">
        <StatCard
          icon={<GitBranch className="h-4 w-4" />}
          label="Workflows"
          value={workflows.length}
        />
        <StatCard
          icon={<Activity className="h-4 w-4" />}
          label="Running"
          value={running}
          tone="running"
        />
        <StatCard
          icon={<CircleDashed className="h-4 w-4" />}
          label="Waiting User"
          value={waiting}
          tone="waiting"
        />
        <StatCard
          icon={<CheckCircle2 className="h-4 w-4" />}
          label="Completed"
          value={completed}
          tone="ok"
        />
        <StatCard
          icon={<XCircle className="h-4 w-4" />}
          label="Failed"
          value={failed}
          tone="bad"
        />
      </div>

      <div className="mt-6 grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex-row items-center justify-between space-y-0">
            <CardTitle className="text-base">最近执行</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link to="/executions">
                全部 <ArrowRight className="ml-1 h-3 w-3" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {executions.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">
                还没有执行记录,去 Workflows 页面运行一个工作流吧
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Execution</TableHead>
                    <TableHead>Workflow</TableHead>
                    <TableHead>Task</TableHead>
                    <TableHead>State</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {executions.map((e) => (
                    <TableRow key={e.id}>
                      <TableCell className="font-mono text-xs">
                        <Link
                          className="hover:underline"
                          to={`/executions/${e.id}`}
                        >
                          {e.id.slice(0, 18)}…
                        </Link>
                      </TableCell>
                      <TableCell className="text-sm">{e.workflow_name}</TableCell>
                      <TableCell className="max-w-40 truncate text-sm text-muted-foreground">
                        {e.task}
                      </TableCell>
                      <TableCell>
                        <StateBadge state={e.state} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex-row items-center justify-between space-y-0">
            <CardTitle className="text-base">Workflows</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link to="/workflows">
                管理 <ArrowRight className="ml-1 h-3 w-3" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent className="space-y-2">
            {workflows.length === 0 && (
              <p className="py-8 text-center text-sm text-muted-foreground">
                暂无工作流
              </p>
            )}
            {workflows.map((w) => (
              <Link
                key={w.id}
                to={`/workflows/${w.id}/design`}
                className="flex items-center justify-between rounded-md border px-3 py-2 text-sm hover:bg-accent"
              >
                <div>
                  <div className="font-medium">{w.name}</div>
                  <div className="text-xs text-muted-foreground">
                    {w.nodes.length} nodes · v{w.version}
                  </div>
                </div>
                <Badge variant={w.enabled ? "default" : "secondary"}>
                  {w.enabled ? "enabled" : "disabled"}
                </Badge>
              </Link>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function subscribeRefresh(fn: () => void): () => void {
  return subscribeEvents((ev) => {
    if (ev.type.startsWith("workflow.")) fn();
  });
}

function StatCard({
  icon,
  label,
  value,
  tone,
}: {
  icon: React.ReactNode;
  label: string;
  value: number;
  tone?: "running" | "ok" | "bad" | "waiting";
}) {
  const toneClass =
    tone === "ok"
      ? "text-green-500"
      : tone === "bad"
        ? "text-red-500"
        : tone === "running"
          ? "text-blue-400"
          : tone === "waiting"
            ? "text-amber-400"
            : "text-foreground";
  return (
    <Card>
      <CardContent className="flex items-center gap-3 p-4">
        <div className={cn("rounded-md bg-muted p-2", toneClass)}>{icon}</div>
        <div>
          <div className="text-2xl font-semibold leading-none">{value}</div>
          <div className="mt-1 text-xs text-muted-foreground">{label}</div>
        </div>
      </CardContent>
    </Card>
  );
}
