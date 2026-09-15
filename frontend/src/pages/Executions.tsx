import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { api, subscribeEvents } from "@/lib/api";
import type { Execution } from "@/lib/types";
import { StateBadge } from "@/components/state-badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export default function Executions() {
  const [execs, setExecs] = useState<Execution[]>([]);

  const refresh = useCallback(() => {
    api.listExecutions("", 100).then((x) => setExecs((x ?? []) as never)).catch((e) => toast.error(e.message));
  }, []);

  useEffect(() => {
    refresh();
    return subscribeEvents((ev) => {
      if (ev.type.startsWith("workflow.")) refresh();
    });
  }, [refresh]);

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Executions</h1>
        <p className="text-sm text-muted-foreground">全部执行记录(持久化于 SQLite)</p>
      </div>
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Workflow</TableHead>
              <TableHead>Task</TableHead>
              <TableHead>State</TableHead>
              <TableHead>当前节点</TableHead>
              <TableHead>开始时间</TableHead>
              <TableHead>结束时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {execs.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                  暂无执行
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
                <TableCell className="text-sm">{e.workflow_name}</TableCell>
                <TableCell className="max-w-48 truncate text-sm text-muted-foreground">
                  {e.task}
                </TableCell>
                <TableCell>
                  <StateBadge state={e.state} />
                  {e.error && (
                    <div className="mt-1 max-w-56 truncate text-[10px] text-red-400" title={e.error}>
                      {e.error}
                    </div>
                  )}
                </TableCell>
                <TableCell className="font-mono text-xs">{e.current_node_id || "-"}</TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {(e.started_at ?? "").slice(0, 19).replace("T", " ")}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {(e.finished_at ?? "").slice(0, 19).replace("T", " ") || "-"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
