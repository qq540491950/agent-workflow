import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { api, asArray, subscribeEvents } from "@/lib/api";
import { createRefreshDebounce } from "@/lib/debounce";
import { createRequestGuard } from "@/lib/request-guard";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface AuditEvent {
  seq: number;
  execution_id: string;
  node_id: string;
  type: string;
  data: Record<string, unknown>;
  created_at: string;
}

const categories = [
  { prefix: "", label: "全部" },
  { prefix: "workflow.", label: "Workflow" },
  { prefix: "node.", label: "Node" },
  { prefix: "agent.", label: "Agent" },
  { prefix: "review.", label: "Review" },
  { prefix: "human.", label: "Human" },
  { prefix: "skill.", label: "Skill" },
  { prefix: "git.", label: "Git" },
];

export default function EventsAudit() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  // draft = 输入框里**正在编辑**的前缀;filter = **已生效**的前缀。
  //
  // 为什么要拆成两个(第二轮 P2-1):此前只有一个 filter,输入一变它就变,
  // 而 refresh 依赖 filter → useEffect 重跑 → **每次按键都发一次请求**,
  // 且旧前缀的迟到响应会覆盖新前缀的结果(输入框是 "node.",列表却是
  // "n" 的结果)。拆开后:输入只改 draft(不发请求),回车/点分类才把它
  // 提交成 filter 并发一次请求。
  const [draft, setDraft] = useState("");
  const [filter, setFilter] = useState("");
  // filterRef 持有**已生效**的前缀:实时事件触发的刷新要用它,又不能
  // 让 refresh 依赖 filter(依赖它 → 改前缀就重建 refresh → effect 重跑
  // → 多发一次请求)。在事件处理器里更新,不在渲染期写 ref。
  const filterRef = useRef("");
  // 请求序号守卫:迟到的旧响应直接丢弃(见 lib/request-guard.ts)。
  const guard = useMemo(() => createRequestGuard(), []);

  const refresh = useCallback(
    async (prefix?: string) => {
      const f = prefix ?? filterRef.current;
      const seq = guard.next();
      try {
        const q = f ? `?type=${encodeURIComponent(f)}&limit=300` : "?limit=300";
        const data = asArray<AuditEvent>(await api.allEvents(q));
        // 旧响应(已经有更新的请求在飞):丢弃,否则会覆盖新前缀的结果。
        if (!guard.isCurrent(seq)) return;
        setEvents(data);
      } catch (e) {
        if (!guard.isCurrent(seq)) return;
        toast.error((e as Error).message);
      }
    },
    [guard],
  );

  // applyFilter 让一个前缀**立即生效**:提交状态 + 按新前缀拉一次。
  const applyFilter = useCallback(
    (prefix: string) => {
      filterRef.current = prefix;
      setFilter(prefix);
      setDraft(prefix);
      void refresh(prefix);
    },
    [refresh],
  );

  useEffect(() => {
    void refresh();
    // 实时事件防抖刷新:agent.output 等高频事件不应逐条触发全量拉取
    // (约定 7:一律 200ms)
    const trigger = createRefreshDebounce(() => {
      void refresh();
    });
    const unsub = subscribeEvents(() => {
      trigger();
    });
    return () => {
      unsub();
      trigger.cancel();
      // 组件卸载:作废在飞请求,避免对已卸载组件 setState。
      guard.invalidate();
    };
  }, [refresh, guard]);

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Events</h1>
          <p className="text-sm text-muted-foreground">
            跨执行审计日志(实时,按类型过滤)
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {categories.map((c) => (
            <button
              key={c.prefix}
              onClick={() => applyFilter(c.prefix)}
              className={
                "rounded-md border px-2.5 py-1.5 text-xs " +
                (filter === c.prefix
                  ? "border-accent bg-accent/10 text-foreground"
                  : "text-muted-foreground hover:bg-accent/40")
              }
            >
              {c.label}
            </button>
          ))}
          <Input
            className="w-48 font-mono text-xs"
            placeholder="自定义前缀…"
            value={draft}
            // 输入**不发请求**(此前每次按键都发一次,见上面的说明);
            // 回车才提交。
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== "Enter") return;
              applyFilter(draft);
            }}
          />
          <Button variant="outline" size="sm" onClick={() => void refresh()}>
            <RefreshCw className="mr-1 h-4 w-4" /> 刷新
          </Button>
        </div>
      </div>

      <div className="rounded-lg border">
        <div className="max-h-[calc(100vh-220px)] overflow-auto p-3 font-mono text-[11.5px]">
          {events.length === 0 && (
            <p className="py-10 text-center text-muted-foreground">暂无事件</p>
          )}
          {events.map((e, i) => (
            <div key={e.seq} className="flex items-baseline gap-2 rounded px-1 py-0.5 hover:bg-accent/40">
              <span className="w-16 shrink-0 text-muted-foreground">
                {(e.created_at ?? "").slice(11, 19)}
              </span>
              <span
                className={cn(
                  "w-44 shrink-0",
                  e.type.includes("failed") && "text-red-400",
                  e.type.includes("approved") && "text-green-400",
                  e.type.includes("rejected") && "text-amber-400",
                  e.type.includes("input_required") && "text-amber-300",
                  e.type.startsWith("workflow.") && "text-violet-300",
                )}
              >
                {e.type}
              </span>
              <Link
                className="shrink-0 text-[11px] text-accent/80 hover:underline"
                to={`/executions/${e.execution_id}`}
              >
                {e.execution_id.slice(0, 16)}…
              </Link>
              {e.node_id && <Badge variant="secondary" className="shrink-0 px-1 text-[10px]">{e.node_id}</Badge>}
              <span className="truncate text-muted-foreground">
                {JSON.stringify(e.data).slice(0, 120)}
              </span>
              {i === events.length - 1 && <span className="ml-auto text-[10px] text-muted-foreground">{events.length} 条</span>}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
