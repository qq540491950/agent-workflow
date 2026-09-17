import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import {
  Plus,
  Copy,
  Trash2,
  Play,
  Pencil,
  FileDown,
  History,
  Power,
  FileUp,
} from "lucide-react";
import { api, asArray } from "@/lib/api";
import type { Workflow } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { MoreHorizontal } from "lucide-react";

export default function Workflows() {
  const nav = useNavigate();
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [newDesc, setNewDesc] = useState("");
  const [filter, setFilter] = useState("");
  const [runTarget, setRunTarget] = useState<Workflow | null>(null);
  const [task, setTask] = useState("");
  const [runVars, setRunVars] = useState("{}");
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [statsMap, setStatsMap] = useState<Record<string, { running: number; total: number }>>({});

  const refresh = useCallback(() => {
    api.listWorkflows().then((x) => setWorkflows(asArray<Workflow>(x))).catch((e) => toast.error(e.message));
    api
      .stats()
      .then((arr) => {
        const m: Record<string, { running: number; total: number }> = {};
        asArray<never>(arr).forEach((s: { workflow_id: string; running: number; total: number }) => (m[s.workflow_id] = { running: s.running, total: s.total }));
        setStatsMap(m);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  const create = async () => {
    try {
      const wf = await api.saveWorkflow({
        id: "",
        name: newName || "未命名工作流",
        description: newDesc,
        version: 0,
        enabled: true,
        nodes: [
          {
            id: "start",
            name: "开始",
            type: "human",
            config: { prompt: "任务开始前确认", responses: ["approve", "continue"] },
            position: { x: 80, y: 160 },
          },
        ],
        edges: [],
        settings: { max_iterations: 5, on_loop_limit: "wait_user" },
      });
      toast.success("工作流已创建");
      setCreating(false);
      setNewName("");
      nav(`/workflows/${wf.id}/design`);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const openRun = (w: Workflow) => {
    setRunTarget(w);
    setTask(localStorage.getItem(`last-task:${w.id}`) ?? "");
  };

  const run = async () => {
    if (!runTarget) return;
    let variables: Record<string, unknown>;
    try {
      variables = JSON.parse(runVars || "{}");
    } catch {
      toast.error("变量不是合法 JSON");
      return;
    }
    try {
      const exec = await api.runWorkflow(runTarget.id, task || "默认任务", variables);
      toast.success("执行已启动: " + exec.id);
      localStorage.setItem(`last-task:${runTarget.id}`, task);
      setRunTarget(null);
      setTask("");
      nav(`/executions/${exec.id}`);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Workflows</h1>
          <p className="text-sm text-muted-foreground">
            创建、编辑、运行工作流
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => setImportOpen(true)}>
            <FileUp className="mr-1 h-4 w-4" /> 导入 YAML
          </Button>
          <Button onClick={() => setCreating(true)}>
            <Plus className="mr-1 h-4 w-4" /> 新建工作流
          </Button>
        </div>
      </div>

      <div className="mb-3">
        <Input
          className="w-64"
          placeholder="按名称 / ID / 描述过滤…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名称</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>版本</TableHead>
              <TableHead>节点</TableHead>
              <TableHead>执行</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>更新时间</TableHead>
              <TableHead className="w-40 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {workflows.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">
                  暂无工作流,点击右上角"新建工作流"开始
                </TableCell>
              </TableRow>
            )}
            {workflows
              .filter(
                (w) =>
                  !filter ||
                  w.name.toLowerCase().includes(filter.toLowerCase()) ||
                  w.id.toLowerCase().includes(filter.toLowerCase()) ||
                  (w.description ?? "").toLowerCase().includes(filter.toLowerCase()),
              )
              .map((w) => (
              <TableRow key={w.id}>
                <TableCell>
                  <Link
                    to={`/workflows/${w.id}/design`}
                    className="font-medium hover:underline"
                  >
                    {w.name}
                  </Link>
                  <div className="text-xs text-muted-foreground">
                    {w.description}
                  </div>
                </TableCell>
                <TableCell className="font-mono text-xs">{w.id}</TableCell>
                <TableCell>v{w.version}</TableCell>
                <TableCell>{w.nodes.length}</TableCell>
                <TableCell>
                  {statsMap[w.id] ? (
                    <span className="text-xs">
                      共 {statsMap[w.id].total} 次
                      {statsMap[w.id].running > 0 && (
                        <span className="ml-1 text-blue-400">● {statsMap[w.id].running} 运行中</span>
                      )}
                    </span>
                  ) : (
                    <span className="text-xs text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell>
                  <Switch
                    checked={w.enabled}
                    onCheckedChange={async (v) => {
                      await api.setEnabled(w.id, v);
                      refresh();
                    }}
                  />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {(w.updated_at ?? "").slice(0, 19).replace("T", " ")}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      size="icon"
                      variant="ghost"
                      title="运行"
                      onClick={() => openRun(w)}
                    >
                      <Play className="h-4 w-4" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      title="编辑"
                      onClick={() => nav(`/workflows/${w.id}/design`)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button size="icon" variant="ghost">
                          <MoreHorizontal className="h-4 w-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          onClick={async () => {
                            await api.duplicateWorkflow(w.id);
                            toast.success("已复制");
                            refresh();
                          }}
                        >
                          <Copy className="mr-2 h-4 w-4" /> 复制
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={async () => {
                            const yml = await api.exportYAML(w.id);
                            const blob = new Blob([yml], { type: "text/yaml" });
                            const a = document.createElement("a");
                            a.href = URL.createObjectURL(blob);
                            a.download = `${w.id}.yaml`;
                            a.click();
                          }}
                        >
                          <FileDown className="mr-2 h-4 w-4" /> 下载 YAML
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={async () => {
                            const yml = await api.copyYAML(w.id);
                            try {
                              await navigator.clipboard.writeText(yml);
                              toast.success("YAML 已复制到剪贴板");
                            } catch {
                              toast.error("剪贴板不可用");
                            }
                          }}
                        >
                          <Copy className="mr-2 h-4 w-4" /> 复制 YAML
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={() => nav(`/workflows/${w.id}/history`)}
                        >
                          <History className="mr-2 h-4 w-4" /> 执行历史
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          className="text-red-500"
                          onClick={async () => {
                            if (!confirm(`确定删除工作流 ${w.name}?`)) return;
                            await api.deleteWorkflow(w.id);
                            toast.success("已删除");
                            refresh();
                          }}
                        >
                          <Trash2 className="mr-2 h-4 w-4" /> 删除
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* 导入对话框 */}
      <Dialog open={importOpen} onOpenChange={setImportOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>从 YAML 导入工作流</DialogTitle>
          </DialogHeader>
          <Textarea
            className="min-h-64 font-mono text-xs"
            placeholder={"version: \"1\"\nworkflow:\n  id: my-flow\n  name: My Flow\nnodes:\n  - id: a\n    type: human\n…"}
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setImportOpen(false)}>
              取消
            </Button>
            <Button
              onClick={async () => {
                try {
                  const wf = await api.importYAML(importText);
                  toast.success(`已导入: ${wf.name} (v${wf.version})`);
                  setImportOpen(false);
                  setImportText("");
                  refresh();
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            >
              导入
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 新建对话框 */}
      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>新建工作流</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <Input
              placeholder="工作流名称"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
            />
            <Input
              placeholder="描述(可选)"
              value={newDesc}
              onChange={(e) => setNewDesc(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              <Power className="mr-1 inline h-3 w-3" />
              创建后进入 Designer 画布,可拖入节点并连线
            </p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreating(false)}>
              取消
            </Button>
            <Button onClick={create}>创建</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 运行对话框 */}
      <Dialog open={!!runTarget} onOpenChange={(v) => !v && setRunTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>运行 {runTarget?.name}</DialogTitle>
          </DialogHeader>
          <Input
            placeholder="任务描述(将传递给工作流)"
            value={task}
            onChange={(e) => setTask(e.target.value)}
          />
          <Textarea
            className="min-h-16 font-mono text-xs"
            placeholder='{"max_review_iterations": 3}(可选变量)'
            value={runVars}
            onChange={(e) => setRunVars(e.target.value)}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRunTarget(null)}>
              取消
            </Button>
            <Button onClick={run}>
              <Play className="mr-1 h-4 w-4" /> 运行
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
