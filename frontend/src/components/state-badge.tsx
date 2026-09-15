import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
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

const map: Record<
  string,
  { label: string; className: string; icon?: React.ReactNode }
> = {
  RUNNING: { label: "Running", className: "bg-blue-500/15 text-blue-400", icon: <PlayCircle className="h-3 w-3" /> },
  WAITING_USER: { label: "Waiting User", className: "bg-amber-500/15 text-amber-400", icon: <PauseCircle className="h-3 w-3" /> },
  PAUSED: { label: "Paused", className: "bg-amber-500/15 text-amber-400", icon: <PauseCircle className="h-3 w-3" /> },
  COMPLETED: { label: "Completed", className: "bg-green-500/15 text-green-400", icon: <CheckCircle2 className="h-3 w-3" /> },
  FAILED: { label: "Failed", className: "bg-red-500/15 text-red-400", icon: <XCircle className="h-3 w-3" /> },
  CANCELLED: { label: "Cancelled", className: "bg-muted text-muted-foreground", icon: <Ban className="h-3 w-3" /> },
  CREATED: { label: "Created", className: "bg-muted text-muted-foreground", icon: <CircleDashed className="h-3 w-3" /> },
  // node states
  SUCCESS: { label: "Success", className: "bg-green-500/15 text-green-400", icon: <CheckCircle2 className="h-3 w-3" /> },
  PENDING: { label: "Pending", className: "bg-muted text-muted-foreground", icon: <Circle className="h-3 w-3" /> },
  SKIPPED: { label: "Skipped", className: "bg-muted text-muted-foreground", icon: <Ban className="h-3 w-3" /> },
  WAITING: { label: "Waiting", className: "bg-amber-500/15 text-amber-400", icon: <PauseCircle className="h-3 w-3" /> },
};

export function StateBadge({
  state,
  className,
}: {
  state: ExecutionState | string;
  className?: string;
}) {
  const cfg = map[state] ?? { label: state, className: "bg-muted text-muted-foreground" };
  return (
    <Badge variant="secondary" className={cn("gap-1", cfg.className, className)}>
      {cfg.icon}
      {cfg.label}
    </Badge>
  );
}
