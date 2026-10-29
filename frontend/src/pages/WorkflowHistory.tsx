import { useCallback, useEffect, useState } from "react";
import { usePageTitle } from "@/lib/use-page-title";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, asArray, subscribeEvents } from "@/lib/api";
import { createRefreshDebounce } from "@/lib/debounce";
import { reportError } from "@/lib/errors";
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
  // 工作流不存在(过期链接):此前 toast 一闪,页面留在"空列表"的假象里。
  const [loadFailed, setLoadFailed] = useState(false);
  const nav = useNavigate();
  usePageTitle(wf ? `历史:${wf.name}` : "执行历史");
  const [execs, setExecs] = useState<Execution[]>([]);
  const [versions, setVersions] = useState<number[]>([]);
  const [viewDSL, setViewDSL] = useState<{ version: number; text: string } | null>(null);

  const refresh = useCallback(() => {
    // 此前 getWorkflow / workflowVersions 两处是 `.catch(() => {})`:加载失败
    // 时静默渲染空态,用户以为"这个工作流没有版本、没有执行记录"。
    // 同文件下面 listExecutions 那条链已经用了 toast.error —— 口径不一致,
    // 现在统一走 reportError(同一个错误 5 秒内只提示一次)。
    api
      .getWorkflow(id)
      .then((w) => {
        setLoadFailed(false);
        setWf(w);
      })
      .catch((e: unknown) => {
        setLoadFailed(true);
        reportError(e, "加载工作流详情");
      });
    api
      .listExecutions(id, 100)
      .then((x) => setExecs(asArray<Execution>(x)))
      .catch((e: unknown) => reportError(e, "加载执行记录"));
    api
      .workflowVersions(id)
      .then((x) => setVersions(asArray<number>(x)))
      .catch((e: unknown) => reportError(e, "加载版本列表"));
  }, [id]);

  useEffect(() => {
    refresh();
    // 实时事件防抖刷新(约定 7:一律 200ms):一次事件会带起三次全量拉取
    const trigger = createRefreshDebounce(refresh);
    const unsub = subscribeEvents((ev) => {
      if (ev.type.startsWith("workflow.")) trigger();
    });
    return () => {
      unsub();
      trigger.cancel();
    };
  }, [refresh]);

  const viewVersion = async (v: number) => {
    try {
      const text = await api.versionDSL(id, v);
      setViewDSL({ version: v, text });
    } catch (e) {
      reportError(e, "查看工作流版本");
    }
  };

  if (loadFailed && !wf) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
        <p>工作流不存在,或已被删除(链接可能已过期)</p>
        <Button variant="outline" size="sm" onClick={() => nav("/workflows")}>
          返回工作流列表
        </Button>
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="flex items-center gap-2 text-2xl font-semibold">
          {wf?.name ?? "…"}
          <Badge variant="secondary">v{wf?.version ?? "?"}</Badge>
        </h1>
        <p className="text-sm text-muted-foreground">
          执行历史 · 每次执行绑定具体的工作流版本
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
              <TableHead>执行 ID</TableHead>
              <TableHead>版本</TableHead>
              <TableHead>任务</TableHead>
              <TableHead>状态</TableHead>
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
