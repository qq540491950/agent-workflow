import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { toast } from "sonner";
import { CheckCircle2, XCircle, Circle, PauseCircle, Ban } from "lucide-react";
import { api, subscribeEvents } from "@/lib/api";
import type { Artifact, Execution, ExecutionNode, UIEvent } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import { StateBadge } from "@/components/state-badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

// Timeline 把各节点执行区间画成水平条,直观展示并行与循环。
function Timeline({
  nodes,
  startedAt,
  finishedAt,
}: {
  nodes: ExecutionNode[];
  startedAt?: string;
  finishedAt?: string;
}) {
  if (!startedAt || nodes.length === 0) return null;
  const t0 = new Date(startedAt).getTime();
  // eslint-disable-next-line react-hooks/purity -- 时间线右端 = 当前时刻,渲染时取墙钟是有意行为
  const t1 = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  const span = Math.max(1, t1 - t0);
  const stateColor: Record<string, string> = {
    SUCCESS: "bg-green-500",
    FAILED: "bg-red-500",
    RUNNING: "bg-blue-400",
    WAITING: "bg-amber-400",
    SKIPPED: "bg-muted-foreground/40",
  };
  return (
    <div className="mb-3 space-y-1 rounded-md border p-2">
      <div className="text-[10px] text-muted-foreground">时间线(0 → {(span / 1000).toFixed(1)}s)</div>
      {nodes.map((n) => {
        const s0 = n.started_at ? new Date(n.started_at).getTime() : t0;
        const dur = n.duration_ms ?? (n.finished_at ? new Date(n.finished_at).getTime() - s0 : 0);
        const left = Math.max(0, Math.min(100, ((s0 - t0) / span) * 100));
        const width = Math.max(1.5, Math.min(100 - left, (dur / span) * 100));
        return (
          <div key={n.id} className="flex items-center gap-2">
            <span className="w-20 shrink-0 truncate text-[10px] text-muted-foreground" title={n.node_name}>
              {n.node_name}
            </span>
            <div className="relative h-2.5 flex-1 rounded bg-muted/40">
              <div
                className={cn("absolute h-full rounded", stateColor[n.state] ?? "bg-muted-foreground")}
                style={{ left: `${left}%`, width: `${width}%` }}
                title={`${n.node_name} ${n.state} ${n.duration_ms ?? 0}ms @+${((s0 - t0) / 1000).toFixed(1)}s`}
              />
            </div>
            <span className="w-10 shrink-0 text-right text-[10px] text-muted-foreground">
              {n.duration_ms ? `${n.duration_ms}ms` : ""}
            </span>
          </div>
        );
      })}
    </div>
  );
}

function durationStr(start?: string, end?: string): string {
  if (!start) return "-";
  const t0 = new Date(start).getTime();
  const t1 = end ? new Date(end).getTime() : Date.now();
  const ms = Math.max(0, t1 - t0);
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60000)}m${Math.round((ms % 60000) / 1000)}s`;
}

const nodeIcon: Record<string, React.ReactNode> = {
  SUCCESS: <CheckCircle2 className="h-4 w-4 text-green-500" />,
  FAILED: <XCircle className="h-4 w-4 text-red-500" />,
  RUNNING: <Circle className="h-4 w-4 animate-pulse text-blue-400" />,
  WAITING: <PauseCircle className="h-4 w-4 text-amber-400" />,
  SKIPPED: <Ban className="h-4 w-4 text-muted-foreground" />,
  PENDING: <Circle className="h-4 w-4 text-muted-foreground" />,
};

export default function ExecutionMonitor() {
  const { id = "" } = useParams();
  const [exec, setExec] = useState<Execution | null>(null);
  const [nodes, setNodes] = useState<ExecutionNode[]>([]);
  const [events, setEvents] = useState<UIEvent[]>([]);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [instruction, setInstruction] = useState("");
  // 提交互斥:防止双击重复提交(后端另有 CAS 兜底)
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [e, ns, evs, arts] = await Promise.all([
        api.getExecution(id),
        api.executionNodes(id),
        api.executionEvents(id),
        api.executionArtifacts(id),
      ]);
      setExec(e);
      setNodes(ns);
      setEvents(
        evs.map((x) => ({
          type: String(x.type),
          execution_id: String(x.execution_id),
          node_id: String(x.node_id ?? ""),
          timestamp: String(x.created_at),
          data: (x.data as Record<string, unknown>) ?? {},
        })),
      );
      setArtifacts(arts);
    } catch (err) {
      toast.error((err as Error).message);
    }
  }, [id]);

  useEffect(() => {
    refresh();
    // 实时事件:匹配当前执行则刷新
    const pending: UIEvent[] = [];
    let timer: number | undefined;
    const unsub = subscribeEvents((ev) => {
      if (ev.execution_id !== id) return;
      pending.push(ev);
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(refresh, 120);
    });
    return () => {
      unsub();
      if (timer) window.clearTimeout(timer);
    };
  }, [id, refresh]);

  if (!exec) {
    return <div className="flex h-full items-center justify-center text-muted-foreground">加载中…</div>;
  }

  const waiting =
    exec.state === "WAITING_USER" || nodes.some((n) => n.state === "WAITING");
  const waitingNode = nodes.find((n) => n.state === "WAITING");
  const prompt =
    (events.find((e) => e.type === "human.input_required")?.data?.prompt as string) ??
    waitingNode?.error ??
    "工作流需要人工确认";

  const provide = async (response: string, extra?: string) => {
    if (busy) return;
    setBusy(true);
    try {
      await api.provideInput(exec.id, {
        response,
        instruction: extra ?? "",
      });
      toast.success("已提交,执行恢复中");
      setInstruction("");
      setTimeout(refresh, 300);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-4 flex items-center gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold">
            Execution
            <span className="font-mono text-sm text-muted-foreground">{exec.id}</span>
            <StateBadge state={exec.state} />
            {(exec.iterations?.loop ?? 0) > 0 && (
              <Badge variant="secondary" className="text-[11px]">循环 {exec.iterations?.loop} 轮</Badge>
            )}
          </h1>
          <p className="text-sm text-muted-foreground">
            <Link className="hover:underline" to={`/workflows/${exec.workflow_id}/design`}>
              {exec.workflow_name}
            </Link>{" "}
            · v{exec.workflow_version} · {exec.task}
            {exec.started_at && (
              <span className="ml-2 font-mono text-[11px]">
                · 耗时 {durationStr(exec.started_at, exec.finished_at)}
              </span>
            )}
          </p>
        </div>
        <div className="ml-auto flex gap-2">
          <a
            className="inline-flex h-8 items-center rounded-md border px-3 text-xs hover:bg-accent"
            href={`/api/executions/${exec.id}/export`}
            download={`execution-${exec.id}.json`}
          >
            导出记录
          </a>
          {(exec.state === "RUNNING" || exec.state === "WAITING_USER") && (
            <Button
              variant="outline"
              size="sm"
              className="text-red-500"
              onClick={async () => {
                await api.cancelExecution(exec.id);
                refresh();
              }}
            >
              取消执行
            </Button>
          )}
        </div>
      </div>

      {exec.error && (
        <div className="mb-4 flex items-center justify-between rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          <span>{exec.error}</span>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={async () => {
                await api.retryExecution(exec.id, false);
                toast.success("重试失败节点");
                setTimeout(refresh, 300);
              }}
            >
              重试失败节点
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={async () => {
                await api.retryExecution(exec.id, true);
                toast.success("已跳过失败节点");
                setTimeout(refresh, 300);
              }}
            >
              跳过并继续
            </Button>
          </div>
        </div>
      )}

      {/* HITL 面板 */}
      {waiting && (
        <Card className="mb-4 border-amber-500/50">
          <CardHeader className="py-3">
            <CardTitle className="flex items-center gap-2 text-amber-400">
              <PauseCircle className="h-4 w-4" /> Workflow requires your input
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm">{prompt}</p>
            <div className="flex gap-2">
              <Button size="sm" disabled={busy} onClick={() => provide("approve")}>Approve</Button>
              <Button size="sm" variant="outline" className="text-red-400" disabled={busy} onClick={() => provide("reject")}>
                Reject
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => provide("continue")}>
                Continue
              </Button>
            </div>
            <div className="flex gap-2">
              <Textarea
                className="min-h-16"
                placeholder="提供补充指令后点击 Provide instruction…"
                value={instruction}
                onChange={(e) => setInstruction(e.target.value)}
              />
              <Button
                size="sm"
                variant="secondary"
                disabled={busy}
                onClick={() => provide("instruction", instruction)}
              >
                Provide instruction
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4 lg:grid-cols-5">
        {/* 节点状态 */}
        <Card className="lg:col-span-2">
          <CardHeader className="py-3">
            <CardTitle className="text-base">节点状态</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1">
            {/* 时间线(甘特式):按执行起点对齐,条长=耗时 */}
            {nodes.length > 0 && <Timeline nodes={nodes} startedAt={exec.started_at} finishedAt={exec.finished_at} />}
            {nodes.length === 0 && (
              <p className="py-6 text-center text-sm text-muted-foreground">等待节点执行…</p>
            )}
            {nodes.map((n) => (
              <div
                key={n.id}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5",
                  n.state === "RUNNING" && "bg-blue-500/10",
                )}
              >
                {nodeIcon[n.state] ?? nodeIcon.PENDING}
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm">
                    {n.node_name}
                    {n.attempt > 1 && (
                      <Badge variant="secondary" className="ml-1 px-1 text-[10px]">
                        ×{n.attempt}
                      </Badge>
                    )}
                  </div>
                  <div className="text-[10px] text-muted-foreground">
                    {n.node_type}
                    {n.duration_ms ? ` · ${n.duration_ms}ms` : ""}
                  </div>
                </div>
                {n.state === "FAILED" && (
                  <div className="max-w-40 truncate text-xs text-red-400" title={n.error}>
                    {n.error}
                  </div>
                )}
              </div>
            ))}
          </CardContent>
        </Card>

        {/* 日志 / 事件 / 制品 */}
        <Card className="lg:col-span-3">
          <CardContent className="pt-4">
            <Tabs defaultValue="events">
              <TabsList>
                <TabsTrigger value="events">实时日志 ({events.length})</TabsTrigger>
                <TabsTrigger value="nodes">节点输出</TabsTrigger>
                <TabsTrigger value="artifacts">Artifacts ({artifacts.length})</TabsTrigger>
                <TabsTrigger value="state">State</TabsTrigger>
              </TabsList>
              <TabsContent value="events" className="mt-2">
                <div className="max-h-[420px] space-y-0.5 overflow-auto rounded-md bg-muted/40 p-2 font-mono text-[11px]">
                  {events.map((e, i) => (
                    <div key={i} className="flex gap-2">
                      <span className="text-muted-foreground">
                        {(e.timestamp ?? "").slice(11, 19)}
                      </span>
                      <span
                        className={cn(
                          e.type.includes("failed") && "text-red-400",
                          e.type.includes("approved") && "text-green-400",
                          e.type.includes("rejected") && "text-amber-400",
                          e.type.includes("input_required") && "text-amber-300",
                        )}
                      >
                        {e.type}
                      </span>
                      {e.node_id && <span className="text-muted-foreground">{e.node_id}</span>}
                    </div>
                  ))}
                  {events.length === 0 && (
                    <div className="py-6 text-center text-muted-foreground">暂无日志</div>
                  )}
                </div>
              </TabsContent>
              <TabsContent value="nodes" className="mt-2">
                <div className="max-h-[420px] space-y-2 overflow-auto">
                  {nodes.map((n) => (
                    <details key={n.id} className="rounded-md border">
                      <summary className="cursor-pointer px-3 py-1.5 text-sm">
                        {n.node_name}
                        <span className="ml-2 text-xs text-muted-foreground">
                          {n.state}
                        </span>
                      </summary>
                      <pre className="max-h-40 overflow-auto whitespace-pre-wrap bg-muted/40 p-2 text-[11px]">
                        {n.output || n.error || "(无输出)"}
                      </pre>
                    </details>
                  ))}
                  {nodes.length === 0 && (
                    <div className="py-6 text-center text-sm text-muted-foreground">暂无</div>
                  )}
                </div>
              </TabsContent>
              <TabsContent value="state" className="mt-2">
                <pre className="max-h-[420px] overflow-auto rounded-md bg-muted/40 p-3 font-mono text-[11px]">
                  {exec.state_data
                    ? JSON.stringify(
                        Object.fromEntries(
                          Object.entries(exec.state_data).filter(
                            ([k, v]) =>
                              ((!k.startsWith("node:") &&
                                !k.startsWith("revisit:") &&
                                !k.startsWith("_")) ||
                                k === "_user_input") &&
                              v !== null &&
                              v !== "" &&
                              v !== false,
                          ),
                        ),
                        null,
                        2,
                      )
                    : "(无状态数据)"}
                </pre>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  运行时会话状态(task/plan/review/git_diff/decision 等),供恢复与调试;内部键已省略。
                </p>
              </TabsContent>
              <TabsContent value="artifacts" className="mt-2">
                <div className="max-h-[420px] space-y-2 overflow-auto">
                  {artifacts.map((a) => (
                    <details key={a.id} className="rounded-md border">
                      <summary className="cursor-pointer px-3 py-1.5 text-sm">
                        {a.name}
                        <span className="ml-2 text-xs text-muted-foreground">
                          {a.node_id} · {a.content_type}
                        </span>
                      </summary>
                      <pre className="max-h-48 overflow-auto whitespace-pre-wrap bg-muted/40 p-2 text-[11px]">
                        {a.content}
                      </pre>
                    </details>
                  ))}
                  {artifacts.length === 0 && (
                    <div className="py-6 text-center text-sm text-muted-foreground">
                      暂无制品(git diff 等会出现在这里)
                    </div>
                  )}
                </div>
              </TabsContent>
            </Tabs>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
