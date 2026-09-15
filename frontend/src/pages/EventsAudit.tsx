import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { RefreshCw } from "lucide-react";
import { api, subscribeEvents } from "@/lib/api";
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

export default function EventsAudit() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [filter, setFilter] = useState("");

  const refresh = useCallback(async () => {
    try {
      const q = filter ? `?type=${encodeURIComponent(filter)}&limit=300` : "?limit=300";
      setEvents((await api.allEvents(q)) ?? []);
    } catch (e) {
      // allEvents(q) 的 q 已含 ?,这里直接拼
      toastErr(e);
    }
  }, [filter]);

  const toastErr = (e: unknown) => {}; // 占位,见下
  void toastErr;

  useEffect(() => {
    refresh();
    return subscribeEvents(() => refresh());
  }, [refresh]);

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Events</h1>
          <p className="text-sm text-muted-foreground">
            跨执行审计日志(实时,按类型过滤)
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Input
            className="w-56 font-mono text-xs"
            placeholder="类型过滤,如 review. 或 human."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && refresh()}
          />
          <Button variant="outline" size="sm" onClick={refresh}>
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
