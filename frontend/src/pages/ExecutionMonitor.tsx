import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";
import { CheckCircle2, XCircle, Circle, PauseCircle, Ban } from "lucide-react";
import { api, asArray, subscribeEvents } from "@/lib/api";
import { reportError } from "@/lib/errors";
import { createRefreshDebounce } from "@/lib/debounce";
import { createRequestGuard } from "@/lib/request-guard";
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
import { usePageTitle } from "@/lib/use-page-title";

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

function durationStr(start?: string, end?: string, running = false): string {
  if (!start) return "-";
  const t0 = new Date(start).getTime();
  if (!end) {
    // 没有结束时间:运行中 → 实时走表;终态(如后端补齐 finished_at 之前
    // 的存量行)→ 显示"—"。终态还拿"当前时间-开始时间"补,时钟会永远
    // 走下去,用户会把"已完成"误读成"还在跑"。
    if (!running) return "—";
    // eslint-disable-next-line react-hooks/purity -- 运行中的耗时本来就是"当前时刻-开始时刻"
    const ms = Math.max(0, Date.now() - t0);
    return ms < 60000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.floor(ms / 60000)}m${Math.round((ms % 60000) / 1000)}s`;
  }
  const t1 = new Date(end).getTime();
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

// 节点 output/error 落库前已脱敏(约定 8):形如 key=value 且 key 含
/**
 * 实时日志:新事件到达时自动滚到底部(跟随模式)。
 *
 * 此前是纯静态容器:执行进行中日志一直往下长,最新的事件永远在可视区外,
 * 用户要不停手动滚动。跟随规则 —— 用户本来就在底部(或从未滚动)→
 * 跟随到底;用户向上翻历史时不拽人,滚回底部即恢复跟随。
 */
function EventLog({ events }: { events: UIEvent[] }) {
  const boxRef = useRef<HTMLDivElement | null>(null);
  const stickRef = useRef(true);
  useEffect(() => {
    const box = boxRef.current;
    if (!box || !stickRef.current) return;
    box.scrollTop = box.scrollHeight;
  }, [events.length]);
  return (
    <div
      ref={boxRef}
      onScroll={() => {
        const box = boxRef.current;
        if (!box) return;
        stickRef.current = box.scrollHeight - box.scrollTop - box.clientHeight < 48;
      }}
      className="max-h-[420px] space-y-0.5 overflow-auto rounded-md bg-muted/40 p-2 font-mono text-[11px]"
    >
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
  );
}

// key/token/password/secret/authorization/credential 的值被替换为 ***。
// 直接把 *** 摆出来,用户会以为节点只输出了三个星号;这里补一句说明,
// 免得把"已脱敏"误读成"出错/输出为空"。
const MASK_MARK = "***";

function maskNote(text: string | undefined): string | null {
  return text && text.includes(MASK_MARK) ? "(含脱敏内容,部分值以 *** 显示)" : null;
}

// NodeResult 是 agent 节点 result_json 的结构化形态(宽松解析:任何字段
// 缺失/类型不对都落到零值,展示层自行取舍)。skill / git 等节点的
// result_json 是别的形状 —— 解析失败时 result 为 null,只展示 output/error。
export interface NodeResult {
  summary?: string;
  decision?: string;
  error?: string;
  output?: string;
  data?: Record<string, unknown>;
}

export function parseNodeResult(raw: string | undefined): NodeResult | null {
  if (!raw) return null;
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  const str = (k: string) => (typeof o[k] === "string" && o[k] ? (o[k] as string) : undefined);
  const data =
    typeof o.Data === "object" && o.Data !== null && Object.keys(o.Data).length > 0
      ? (o.Data as Record<string, unknown>)
      : undefined;
  const r: NodeResult = {
    summary: str("Summary"),
    decision: str("Decision"),
    error: str("Error"),
    output: str("Output"),
    data,
  };
  return r.summary || r.decision || r.error || r.output || r.data ? r : null;
}

// nodeDecisionBadge 的语义:Decision 是 agent 的结构化路由依据(REJECTED /
// APPROVED / WAIT_USER 等),"DONE" 是"普通完成",不差这一个徽标。
// 徽标颜色跟语义走:拒绝 = 琥珀(不是错误,是路由),等待 = 琥珀,其余 = 中性。
export function decisionTone(decision: string): string {
  if (decision === "REJECTED") return "border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400";
  if (decision === "WAIT_USER") return "border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400";
  if (decision === "APPROVED") return "border-green-500/50 bg-green-500/10 text-green-600 dark:text-green-400";
  return "";
}

export default function ExecutionMonitor() {
  const { id = "" } = useParams();
  const [exec, setExec] = useState<Execution | null>(null);
  // 加载失败(典型:过期深链指向已删除的执行):此前只有一条 toast
  // (4 秒后消失),页面永远停在"加载中…" —— 明确失败必须给出明确的
  // 失败态与出口,而不是无限加载。
  const [loadFailed, setLoadFailed] = useState(false);
  const nav = useNavigate();
  usePageTitle(exec ? `执行:${exec.workflow_name}` : "执行详情");
  const [nodes, setNodes] = useState<ExecutionNode[]>([]);
  const [events, setEvents] = useState<UIEvent[]>([]);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [instruction, setInstruction] = useState("");
  // 提交互斥:防止双击重复提交(后端另有 CAS 兜底)
  const [busy, setBusy] = useState(false);
  // 重试/跳过互斥:此前这两个按钮无 disabled,连点会并发打多次
  // /retry —— 后端 CAS 只让一路成功,用户却看到多个 toast 混杂。
  const [recoveryBusy, setRecoveryBusy] = useState(false);
  // 请求序号守卫(第二轮 P2-3):切换 :id 时,旧执行的迟到响应会把
  // **上一条执行**的数据覆盖上来 —— 页面显示的不是用户正在看的那条执行,
  // 而"取消执行"/"重试失败节点"是按 exec.id 操作的 → 可能作用在用户
  // 以为不是的那条执行上。守卫让旧响应直接丢弃。
  const guard = useMemo(() => createRequestGuard(), []);

  const refresh = useCallback(async () => {
    const seq = guard.next();
    try {
      const [e, ns, evs, arts] = await Promise.all([
        api.getExecution(id),
        api.executionNodes(id),
        api.executionEvents(id),
        api.executionArtifacts(id),
      ]);
      // 已经切换到别的执行(或发起了更新的请求):旧响应丢弃,不得覆盖。
      if (!guard.isCurrent(seq)) return;
      setLoadFailed(false);
      setExec(e);
      // 一律过 asArray:桌面 IPC 与 SPA fallback 异常路径下可能返回
      // null 或被解析成 HTML 兜底页的对象,直接 setNodes(ns) 会在
      // nodes.length / nodes.map 处抛 TypeError 导致整页白屏。
      setNodes(asArray<ExecutionNode>(ns));
      setEvents(
        asArray<Record<string, unknown>>(evs).map((x) => ({
          type: String(x.type),
          execution_id: String(x.execution_id),
          node_id: String(x.node_id ?? ""),
          timestamp: String(x.created_at),
          data: (x.data as Record<string, unknown>) ?? {},
        })),
      );
      setArtifacts(asArray<Artifact>(arts));
    } catch (err) {
      if (!guard.isCurrent(seq)) return;
      setLoadFailed(true);
      reportError(err, "加载执行详情");
    }
  }, [id, guard]);

  useEffect(() => {
    // 切换执行:先清空上一条执行的数据。留着它的话,"取消执行"/
    // "重试失败节点"按钮(按 exec.id 操作)在新的响应到达之前仍然
    // 作用在**上一条执行**上 —— 用户以为操作的是当前这条。
    setExec(null);
    setNodes([]);
    setEvents([]);
    setArtifacts([]);
    refresh();
    // 实时事件:匹配当前执行则刷新。
    // 防抖窗口统一走 createRefreshDebounce(约定 7:一律 200ms)——
    // 此处此前是 120ms,与 Layout/EventsAudit 不一致,高频 node.* 事件下
    // 会打出比预期更密的刷新。
    const trigger = createRefreshDebounce(() => {
      void refresh();
    });
    const unsub = subscribeEvents((ev) => {
      if (ev.execution_id !== id) return;
      trigger();
    });
    return () => {
      unsub();
      trigger.cancel();
      // 组件卸载/切换执行:作废在飞请求。
      guard.invalidate();
    };
  }, [id, refresh, guard]);

  if (loadFailed && !exec) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
        <p>执行不存在,或已被删除(链接可能已过期)</p>
        <Button variant="outline" size="sm" onClick={() => nav("/executions")}>
          返回执行列表
        </Button>
      </div>
    );
  }
  if (!exec) {
    return <div className="flex h-full items-center justify-center text-muted-foreground">加载中…</div>;
  }

  // 人工节点的"等待"判定:同一节点可能有多行(attempt 递增)——
  // Resume 之后旧行保持 WAITING(历史),新行才是现状。只看
  // nodes.some(WAITING) 会让"执行已完成、审批面板却还挂着" ——
  // 以执行状态为准,节点行兜底时排除已被更高 attempt 取代的旧行。
  const isStaleWaitRow = (n: ExecutionNode) =>
    nodes.some(
      (m) => m.node_id === n.node_id && m.attempt > n.attempt,
    );
  const waitingNode = nodes.find(
    (n) => n.state === "WAITING" && !isStaleWaitRow(n),
  );
  const waiting = exec.state === "WAITING_USER" || !!waitingNode;
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
      reportError(e, "提交人工输入");
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
                · 耗时 {durationStr(exec.started_at, exec.finished_at, exec.state === "RUNNING")}
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
                try {
                  await api.cancelExecution(exec.id);
                } catch (e) {
                  reportError(e, "取消执行");
                }
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
              disabled={recoveryBusy}
              onClick={async () => {
                if (recoveryBusy) return;
                setRecoveryBusy(true);
                try {
                  await api.retryExecution(exec.id, false);
                  // 此前成功提示就是按钮文案"重试失败节点" —— 读起来像报错。
                  // 说清"已提交、会发生什么"(恢复执行,结果刷新见页面)。
                  toast.success("已提交重试,执行恢复中");
                  setTimeout(refresh, 300);
                } catch (e) {
                  reportError(e, "重试失败节点");
                } finally {
                  setRecoveryBusy(false);
                }
              }}
            >
              {recoveryBusy ? "处理中…" : "重试失败节点"}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={recoveryBusy}
              onClick={async () => {
                if (recoveryBusy) return;
                setRecoveryBusy(true);
                try {
                  await api.retryExecution(exec.id, true);
                  toast.success("已跳过失败节点");
                  setTimeout(refresh, 300);
                } catch (e) {
                  reportError(e, "跳过失败节点");
                } finally {
                  setRecoveryBusy(false);
                }
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
                    {/* 结构化路由结果(评审 APPROVED/REJECTED、人工 WAIT_USER):
                        左侧列表是用户扫视执行路径的主视图,"代码评审 ×2"这种
                        只报次数的展示让用户看不出第几次评审判了什么。 */}
                    {(() => {
                      const d = parseNodeResult(n.result_json)?.decision;
                      return d && d !== "DONE" ? (
                        <span
                          className={cn(
                            "ml-1 rounded border px-1 text-[10px]",
                            decisionTone(d),
                          )}
                        >
                          {d}
                        </span>
                      ) : null;
                    })()}
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
                <EventLog events={events} />
              </TabsContent>
              <TabsContent value="nodes" className="mt-2">
                <div className="max-h-[420px] space-y-2 overflow-auto">
                  {nodes.map((n) => {
                    const result = parseNodeResult(n.result_json);
                    const body = n.output || n.error || "";
                    const note = maskNote(body);
                    const hasData = result?.data !== undefined;
                    return (
                      <details key={n.id} className="rounded-md border">
                        <summary className="cursor-pointer px-3 py-1.5 text-sm">
                          {n.node_name}
                          <span className="ml-2 text-xs text-muted-foreground">
                            {n.state}
                          </span>
                          {/* Decision 是结构化路由依据(评审 APPROVED/REJECTED、
                              人工 WAIT_USER):一眼看到路由结果,不用展开找。
                              "DONE" 不显示 —— 它只表示"正常完成",没有信息量。 */}
                          {result?.decision && result.decision !== "DONE" && (
                            <span
                              className={cn(
                                "ml-2 rounded border px-1 text-[10px]",
                                decisionTone(result.decision),
                              )}
                            >
                              {result.decision}
                            </span>
                          )}
                        </summary>
                        <div className="space-y-1.5 bg-muted/40 p-2 text-[11px]">
                          {result?.summary && (
                            <div className="whitespace-pre-wrap">{result.summary}</div>
                          )}
                          {n.output && (
                            <pre className="max-h-40 overflow-auto whitespace-pre-wrap">
                              {n.output}
                            </pre>
                          )}
                          {n.error && (
                            <pre className="max-h-40 overflow-auto whitespace-pre-wrap text-red-400">
                              {n.error}
                            </pre>
                          )}
                          {hasData && (
                            <pre className="max-h-40 overflow-auto whitespace-pre-wrap text-muted-foreground">
                              {JSON.stringify(result?.data, null, 2)}
                            </pre>
                          )}
                          {!result?.summary && !body && !hasData && (
                            <span className="text-muted-foreground">(无输出)</span>
                          )}
                          {note && <span className="text-muted-foreground">{note}</span>}
                        </div>
                      </details>
                    );
                  })}
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
