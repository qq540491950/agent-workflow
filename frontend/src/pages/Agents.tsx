import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Settings2 } from "lucide-react";
import { api, asArray } from "@/lib/api";
import type { AgentConfig, AgentInfo } from "@/lib/types";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
  const [configTarget, setConfigTarget] = useState<AgentInfo | null>(null);
  const [cfg, setCfg] = useState<AgentConfig | null>(null);
  const [savingCfg, setSavingCfg] = useState(false);

  const refresh = () => api.listAgents().then((x) => setAgents(asArray<AgentInfo>(x))).catch((e) => toast.error(e.message));
  useEffect(() => {
    refresh();
  }, []);

  const toggle = async (a: AgentInfo, key: keyof AgentInfo["permissions"]) => {
    const p = { ...a.permissions, [key]: !a.permissions[key] };
    if (key === "git_commit" && !p.git_commit) p.git_push = false;
    try {
      await api.setAgentPermission(a.id, p);
    } catch (e) {
      toast.error((e as Error).message);
    }
    refresh();
  };

  const openConfig = async (a: AgentInfo) => {
    setConfigTarget(a);
    try {
      const c = await api.getAgentConfig(a.id);
      setCfg({ ...c, env: c.env ?? {} });
    } catch (e) {
      toast.error((e as Error).message);
      setCfg({ model: "", base_url: "", timeout_seconds: 0, extra_args: [], env: {}, default_working_dir: "" });
    }
  };

  const saveConfig = async () => {
    if (!configTarget || !cfg) return;
    setSavingCfg(true);
    try {
      await api.updateAgentConfig(configTarget.id, cfg);
      toast.success(`${configTarget.name} 配置已保存(仅作用于本应用调用,不影响本地配置)`);
      setConfigTarget(null);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSavingCfg(false);
    }
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Agents</h1>
        <p className="text-sm text-muted-foreground">
          Agent 注册表、权限策略与模型配置。模型/端点/环境变量仅通过
          <b className="text-foreground">调用级 CLI 参数与环境变量</b>
          传给子进程,<b className="text-foreground">不读写任何本地配置文件</b>。
        </p>
      </div>
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Agent</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>模型</TableHead>
              <TableHead>fs:read</TableHead>
              <TableHead>fs:write</TableHead>
              <TableHead>git:read</TableHead>
              <TableHead>git:commit</TableHead>
              <TableHead>git:push</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {agents.map((a) => (
              <TableRow key={a.id}>
                <TableCell className="font-medium">{a.name}</TableCell>
                <TableCell>
                  <Badge variant="outline" className="font-mono text-xs">{a.id}</Badge>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {a.model || <span className="text-muted-foreground">默认</span>}
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
                <TableCell className="space-x-1 text-right">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => openConfig(a)}
                    title="模型 / 端点 / 超时 / 环境变量"
                  >
                    <Settings2 className="mr-1 h-3.5 w-3.5" /> 配置
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
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
        环境变量保存于本地 SQLite,回显时以 *** 掩码,不会出现在日志与工作流定义中。
      </p>

      {/* Agent 配置对话框 */}
      <Dialog open={!!configTarget} onOpenChange={(v) => !v && setConfigTarget(null)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>
              配置 {configTarget?.name}
              <span className="ml-2 font-mono text-xs text-muted-foreground">{configTarget?.id}</span>
            </DialogTitle>
          </DialogHeader>
          {cfg && (
            <div className="max-h-[60vh] space-y-4 overflow-auto pr-1">
              <div className="space-y-1.5">
                <Label className="text-xs text-muted-foreground">
                  默认模型(节点可在 config.model 中覆盖)
                </Label>
                <Input
                  placeholder="例如 claude-sonnet-4-5 / glm-4.7(留空=使用 CLI 默认)"
                  value={cfg.model}
                  onChange={(e) => setCfg({ ...cfg, model: e.target.value })}
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs text-muted-foreground">
                  API 端点 Base URL(可选,映射 ANTHROPIC_BASE_URL / PI_BASE_URL)
                </Label>
                <Input
                  placeholder="https://...(留空=官方默认)"
                  value={cfg.base_url}
                  onChange={(e) => setCfg({ ...cfg, base_url: e.target.value })}
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-xs text-muted-foreground">超时(秒,0=默认)</Label>
                  <Input
                    type="number"
                    value={String(cfg.timeout_seconds ?? 0)}
                    onChange={(e) => setCfg({ ...cfg, timeout_seconds: Number(e.target.value) })}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs text-muted-foreground">默认工作目录(可选)</Label>
                  <Input
                    value={cfg.default_working_dir}
                    onChange={(e) => setCfg({ ...cfg, default_working_dir: e.target.value })}
                  />
                </div>
              </div>
              {configTarget?.id.startsWith("mock") && (
                <div className="space-y-1.5">
                  <Label className="text-xs text-muted-foreground">
                    Mock 决策脚本(JSON,mode → decisions 序列;演示循环/保护用)
                  </Label>
                  <Textarea
                    className="min-h-24 font-mono text-xs"
                    placeholder={'{"review": {"decisions": ["REJECTED", "APPROVED"]}}'}
                    value={JSON.stringify(cfg.behavior ?? {}, null, 2)}
                    onChange={(e) => {
                      try {
                        setCfg({ ...cfg, behavior: JSON.parse(e.target.value || "{}") });
                      } catch {
                        // 输入过程中允许暂不合法的 JSON
                      }
                    }}
                  />
                </div>
              )}
              <div className="space-y-1.5">
                <Label className="text-xs text-muted-foreground">附加 CLI 参数(空格分隔)</Label>
                <Input
                  placeholder="--verbose"
                  value={(cfg.extra_args ?? []).join(" ")}
                  onChange={(e) =>
                    setCfg({
                      ...cfg,
                      extra_args: e.target.value.split(" ").map((s) => s.trim()).filter(Boolean),
                    })
                  }
                />
              </div>
              <div className="space-y-2">
                <Label className="text-xs text-muted-foreground">
                  环境变量(值显示为 *** 表示已保存;修改则覆盖,清空则删除)
                </Label>
                {Object.entries(cfg.env ?? {}).map(([k, v]) => (
                  <div key={k} className="flex items-center gap-2">
                    <Input
                      className="w-44 font-mono text-xs"
                      value={k}
                      onChange={(e) => {
                        const env = { ...(cfg.env ?? {}) };
                        delete env[k];
                        env[e.target.value] = v;
                        setCfg({ ...cfg, env });
                      }}
                    />
                    <Input
                      className="flex-1 font-mono text-xs"
                      value={v}
                      onChange={(e) => setCfg({ ...cfg, env: { ...(cfg.env ?? {}), [k]: e.target.value } })}
                    />
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-red-500"
                      onClick={() => {
                        const env = { ...(cfg.env ?? {}) };
                        delete env[k];
                        setCfg({ ...cfg, env });
                      }}
                    >
                      ×
                    </Button>
                  </div>
                ))}
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setCfg({ ...cfg, env: { ...(cfg.env ?? {}), "": "" } })}
                >
                  + 添加环境变量
                </Button>
              </div>
              <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-xs text-amber-300">
                隔离保证:以上配置仅通过每次调用的参数与环境变量传给子进程,
                不会写入或修改 ~/.claude、~/.pi 等本地配置,也不会进入工作流定义与日志。
              </p>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfigTarget(null)}>
              取消
            </Button>
            <Button onClick={saveConfig} disabled={savingCfg || !cfg}>
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
