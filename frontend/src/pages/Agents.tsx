import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { AgentInfo } from "@/lib/types";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export default function Agents() {
  const [agents, setAgents] = useState<AgentInfo[]>([]);

  const refresh = () => api.listAgents().then(setAgents).catch((e) => toast.error(e.message));
  useEffect(() => {
    refresh();
  }, []);

  const toggle = async (a: AgentInfo, key: keyof AgentInfo["permissions"]) => {
    const p = { ...a.permissions, [key]: !a.permissions[key] };
    // 权限约束:push 依赖 commit
    if (key === "git_commit" && !p.git_commit) p.git_push = false;
    await api.setAgentPermission(a.id, p);
    refresh();
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Agents</h1>
        <p className="text-sm text-muted-foreground">
          Agent 注册表与权限策略(策略由 Permission Manager 强制执行,Agent 无法自行突破)
        </p>
      </div>
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Agent</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>fs:read</TableHead>
              <TableHead>fs:write</TableHead>
              <TableHead>git:read</TableHead>
              <TableHead>git:commit</TableHead>
              <TableHead>git:push</TableHead>
              <TableHead className="text-right">测试</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {agents.map((a) => (
              <TableRow key={a.id}>
                <TableCell className="font-medium">{a.name}</TableCell>
                <TableCell>
                  <Badge variant="outline" className="font-mono text-xs">{a.id}</Badge>
                </TableCell>
                {(
                  [
                    "filesystem_read",
                    "filesystem_write",
                    "git_read",
                    "git_commit",
                    "git_push",
                  ] as const
                ).map((k) => (
                  <TableCell key={k}>
                    <Switch checked={a.permissions[k]} onCheckedChange={() => toggle(a, k)} />
                  </TableCell>
                ))}
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={async () => {
                      const msg = await api.testAgent(a.id);
                      toast.success(msg);
                    }}
                  >
                    Test
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        默认策略:Claude Code 只读(禁止修改文件/提交);Pi Agent 可写、可 commit、禁止 push。
      </p>
    </div>
  );
}
