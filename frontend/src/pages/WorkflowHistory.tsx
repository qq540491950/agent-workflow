import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { toast } from "sonner";
import { api, asArray, subscribeEvents } from "@/lib/api";
import type { Execution, Workflow } from "@/lib/types";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { StateBadge } from "@/components/state-badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

// 工作流的执行历史(版本绑定关系)
function dur(e: Execution): string {
  if (!e.started_at) return "-";
  const t0 = new Date(e.started_at).getTime();
  const t1 = e.finished_at ? new Date(e.finished_at).getTime() : Date.now();
  const ms = Math.max(0, t1 - t0);
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60000)}m${Math.round((ms % 60000) / 1000)}s`;
}

export default function WorkflowHistory() {
  const { id = "" } = useParams();
  const [wf, setWf] = useState<Workflow | null>(null);
  const [execs, setExecs] = useState<Execution[]>([]);
  const [versions, setVersions] = useState<number[]>([]);
  const [viewDSL, setViewDSL] = useState<{ version: number; text: string } | null>(null);

  const refresh = useCallback(() => {
    api.getWorkflow(id).then(setWf).catch(() => {});
    api.listExecutions(id, 100).then((x) => setExecs(asArray<Execution>(x))).catch((e) => toast.error(e.message));
    api.workflowVersions(id).then((x) => setVersions(asArray<number>(x))).catch(() => {});
  }, [id]);

  useEffect(() => {
    refresh();
    return subscribeEvents((ev) => {
      if (ev.type.startsWith("workflow.")) refresh();
    });
  }, [refresh]);

  const viewVersion = async (v: number) => {
    try {
      const text = await api.versionDSL(id, v);
      setViewDSL({ version: v, text });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="flex items-center gap-2 text-2xl font-semibold">
          {wf?.name ?? "…"}
          <Badge variant="secondary">v{wf?.version ?? "?"}</Badge>
        </h1>
        <p className="text-sm text-muted-foreground">
          执行历史 · Execution 绑定具体 Workflow 版本
        </p>
      </div>
      {versions.length > 0 && (
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <span className="text-xs text-muted-foreground">版本历史:</span>
          {versions.map((v) => (
            <Button key={v} size="sm" variant={v === wf?.version ? "default" : "outline"} onClick={() => viewVersion(v)}>
              v{v}
            </Button>
          ))}
        </div>
      )}

      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Execution</TableHead>
              <TableHead>版本</TableHead>
              <TableHead>Task</TableHead>
              <TableHead>State</TableHead>
              <TableHead>开始时间</TableHead>
              <TableHead>耗时</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {execs.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">
                  该工作流还没有执行记录
                </TableCell>
              </TableRow>
            )}
            {execs.map((e) => (
              <TableRow key={e.id}>
                <TableCell className="font-mono text-xs">
                  <Link className="hover:underline" to={`/executions/${e.id}`}>
                    {e.id}
                  </Link>
                </TableCell>
                <TableCell>v{e.workflow_version}</TableCell>
                <TableCell className="max-w-64 truncate text-sm text-muted-foreground">
                  {e.task}
                </TableCell>
                <TableCell>
                  <StateBadge state={e.state} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {(e.started_at ?? "").slice(0, 19).replace("T", " ")}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{dur(e)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog open={!!viewDSL} onOpenChange={(v) => !v && setViewDSL(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>版本 v{viewDSL?.version} 的 DSL 快照</DialogTitle>
          </DialogHeader>
          <Textarea readOnly className="min-h-80 font-mono text-xs" value={viewDSL?.text ?? ""} />
        </DialogContent>
      </Dialog>
    </div>
  );
}
