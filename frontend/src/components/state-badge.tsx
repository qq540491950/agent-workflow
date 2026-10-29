import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { stateLabel } from "@/lib/labels";
import type { ExecutionState } from "@/lib/types";
import {
  CheckCircle2,
  XCircle,
  PlayCircle,
  PauseCircle,
  CircleDashed,
  Ban,
  Circle,
} from "lucide-react";

// 状态只保留颜色与图标;文案统一走 lib/labels.ts 的 stateLabel
// (执行/节点状态中文映射的唯一来源,未知状态回退原文)。
const map: Record<
  string,
  { className: string; icon?: React.ReactNode }
> = {
  RUNNING: { className: "bg-blue-500/15 text-blue-400", icon: <PlayCircle className="h-3 w-3" /> },
  WAITING_USER: { className: "bg-amber-500/15 text-amber-400", icon: <PauseCircle className="h-3 w-3" /> },
  // PAUSED 曾在这里有一枚徽章,但它**从来不可达**:没有任何代码路径会把
  // 执行迁移到 PAUSED(该状态已从执行状态机移除)。老库里的遗留行在
  // 读出口已归一为 CANCELLED,所以这里不需要(也不该)再有它。
  COMPLETED: { className: "bg-green-500/15 text-green-400", icon: <CheckCircle2 className="h-3 w-3" /> },
  FAILED: { className: "bg-red-500/15 text-red-400", icon: <XCircle className="h-3 w-3" /> },
  CANCELLED: { className: "bg-muted text-muted-foreground", icon: <Ban className="h-3 w-3" /> },
  CREATED: { className: "bg-muted text-muted-foreground", icon: <CircleDashed className="h-3 w-3" /> },
  // node states
  SUCCESS: { className: "bg-green-500/15 text-green-400", icon: <CheckCircle2 className="h-3 w-3" /> },
  PENDING: { className: "bg-muted text-muted-foreground", icon: <Circle className="h-3 w-3" /> },
  SKIPPED: { className: "bg-muted text-muted-foreground", icon: <Ban className="h-3 w-3" /> },
  WAITING: { className: "bg-amber-500/15 text-amber-400", icon: <PauseCircle className="h-3 w-3" /> },
};

export function StateBadge({
  state,
  className,
}: {
  state: ExecutionState | string;
  className?: string;
}) {
  const cfg = map[state] ?? { className: "bg-muted text-muted-foreground" };
  return (
    <Badge variant="secondary" className={cn("gap-1", cfg.className, className)}>
      {cfg.icon}
      {stateLabel(state)}
    </Badge>
  );
}
