import { useCallback, useEffect, useRef, useState } from "react";
import {
  addEdge,
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Connection,
  type Edge as RFEdge,
  type Node as RFNode,
  useEdgesState,
  useNodesState,
  Handle,
  Position,
  MarkerType,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import Dagre from "@dagrejs/dagre";
import { useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";
import {
  Bot,
  Wrench,
  GitBranch as ConditionIcon,
  Layers,
  Merge,
  UserRound,
  TerminalSquare,
  GitCommitHorizontal,
  Workflow as WorkflowIcon,
  Save,
  Play,
  ShieldCheck,
  LayoutGrid,
} from "lucide-react";
import { api } from "@/lib/api";
import type { ValidationError, Workflow, WFNode } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

export const nodeMeta: Record<
  string,
  { label: string; icon: React.ReactNode; color: string }
> = {
  agent: { label: "Agent", icon: <Bot className="h-4 w-4" />, color: "border-violet-500/50 bg-violet-500/10" },
  skill: { label: "Skill", icon: <Wrench className="h-4 w-4" />, color: "border-amber-500/50 bg-amber-500/10" },
  condition: { label: "Condition", icon: <ConditionIcon className="h-4 w-4" />, color: "border-sky-500/50 bg-sky-500/10" },
  parallel: { label: "Parallel", icon: <Layers className="h-4 w-4" />, color: "border-pink-500/50 bg-pink-500/10" },
  merge: { label: "Merge", icon: <Merge className="h-4 w-4" />, color: "border-pink-500/50 bg-pink-500/10" },
  human: { label: "Human", icon: <UserRound className="h-4 w-4" />, color: "border-green-500/50 bg-green-500/10" },
  script: { label: "Script", icon: <TerminalSquare className="h-4 w-4" />, color: "border-slate-500/50 bg-slate-500/10" },
  git: { label: "Git", icon: <GitCommitHorizontal className="h-4 w-4" />, color: "border-orange-500/50 bg-orange-500/10" },
  subworkflow: { label: "Sub Workflow", icon: <WorkflowIcon className="h-4 w-4" />, color: "border-cyan-500/50 bg-cyan-500/10" },
};

// ---- 自定义节点视图 ----

export function WFNodeView({ data, selected }: { data: Record<string, unknown>; selected?: boolean }) {
  const type = String(data.wtype ?? "agent");
  const meta = nodeMeta[type] ?? nodeMeta.agent;
  const running = data.running === true;
  const done = data.done === true;
  const failed = data.failed === true;
  const waiting = data.waiting === true;
  const disabled = (data.cfg as Record<string, unknown> | undefined)?.enabled === false;
  return (
    <div
      className={cn(
        "w-44 rounded-lg border bg-card px-3 py-2 shadow-sm",
        meta.color,
        selected && "ring-2 ring-ring",
        running && "animate-pulse ring-2 ring-blue-400",
        failed && "ring-2 ring-red-400",
        waiting && "ring-2 ring-amber-400",
        disabled && "opacity-50 grayscale",
      )}
    >
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground" />
      <div className="flex items-center gap-2">
        <span className="text-foreground/80">{meta.icon}</span>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{String(data.label ?? "")}</div>
          <div className="truncate text-[10px] text-muted-foreground">
            {String(data.sublabel ?? meta.label)}
          </div>
        </div>
        <div className="ml-auto text-xs">
          {disabled ? "⊘" : running ? "●" : failed ? "✕" : waiting ? "⏸" : done ? "✓" : ""}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground" />
    </div>
  );
}

const nodeTypes = { wf: WFNodeView };

function sublabelOf(n: WFNode): string {
  switch (n.type) {
    case "agent":
      return `${n.config?.agent ?? "?"} · ${n.config?.mode ?? "?"}`;
    case "skill":
      return `skill: ${n.config?.skill ?? "?"}`;
    case "git":
      return `git ${n.config?.operation ?? "?"}`;
    case "subworkflow":
      return `→ ${n.config?.workflow_id ?? "?"}`;
    default:
      return nodeMeta[n.type]?.label ?? n.type;
  }
}

// ---- Designer 页面 ----

export default function Designer() {
  return (
    <ReactFlowProvider>
      <DesignerInner />
    </ReactFlowProvider>
  );
}

function DesignerInner() {
  const { id = "" } = useParams();
  const nav = useNavigate();
  const [wf, setWf] = useState<Workflow | null>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<RFNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<RFEdge>([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null);
  const [errors, setErrors] = useState<ValidationError[] | null>(null);
  const [runOpen, setRunOpen] = useState(false);
  const [task, setTask] = useState("");
  const [agents, setAgents] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const idCounter = useRef(1);
  const wrapRef = useRef<HTMLDivElement>(null);
  const { fitView } = useReactFlow();

  useEffect(() => {
    api.listAgents().then((as) => setAgents(as.map((a) => a.id))).catch(() => {});
  }, []);

  // Cmd/Ctrl+S 快捷保存
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        saveRef.current?.();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const saveRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    if (!id) return;
    api.getWorkflow(id).then((w) => {
      setWf(w);
      hydrate(w);
      setTimeout(() => fitView({ padding: 0.15 }), 60);
    }).catch((e) => toast.error(e.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  function hydrate(w: Workflow) {
    setNodes(
      w.nodes.map((n) => ({
        id: n.id,
        type: "wf",
        position: { x: n.position.x, y: n.position.y },
        data: {
          wid: n.id,
          label: n.name || n.id,
          sublabel: sublabelOf(n),
          wtype: n.type,
          cfg: n.config ?? {},
          running: false,
          done: false,
          failed: false,
          waiting: false,
        },
      })),
    );
    setEdges(
      w.edges.map((e, i) => ({
        id: `e-${e.from}-${e.to}-${i}`,
        source: e.from,
        target: e.to,
        label: e.condition || undefined,
        animated: !!e.condition,
        style: { stroke: e.condition ? "#38bdf8" : "#64748b" },
        labelStyle: { fill: "#94a3b8", fontSize: 10 },
        labelBgStyle: { fill: "#1e293b" },
        markerEnd: { type: MarkerType.ArrowClosed },
        data: { condition: e.condition ?? "" },
      })),
    );
  }

  function toModel(): Workflow {
    const wfNodes: WFNode[] = nodes.map((n) => ({
      id: n.id,
      name: String(n.data.label ?? n.id),
      type: n.data.wtype as WFNode["type"],
      position: { x: n.position.x, y: n.position.y },
      config: (n.data.cfg as Record<string, unknown>) ?? {},
    }));
    return {
      id: wf?.id ?? "",
      name: wf?.name ?? "",
      description: wf?.description ?? "",
      version: wf?.version ?? 0,
      enabled: wf?.enabled ?? true,
      variables: wf?.variables ?? {},
      nodes: wfNodes,
      edges: edges.map((e) => ({
        from: e.source,
        to: e.target,
        condition: (e.data?.condition as string) || undefined,
      })),
      settings: wf?.settings ?? { max_iterations: 5, on_loop_limit: "wait_user" },
    };
  }

  // autoLayout 用 dagre 按连线方向重排节点(自上而下)
  const autoLayout = useCallback(() => {
    const g = new Dagre.graphlib.Graph();
    g.setDefaultEdgeLabel(() => ({}));
    g.setGraph({ rankdir: "TB", nodesep: 60, ranksep: 90 });
    nodes.forEach((n) => g.setNode(n.id, { width: 176, height: 56 }));
    edges.forEach((e) => g.setEdge(e.source, e.target));
    Dagre.layout(g);
    setNodes((nds) =>
      nds.map((n) => {
        const pos = g.node(n.id);
        if (!pos) return n;
        return {
          ...n,
          position: { x: pos.x - 88, y: pos.y - 28 },
        };
      }),
    );
    setTimeout(() => fitView({ padding: 0.15 }), 60);
  }, [nodes, edges, setNodes, fitView]);

  const onConnect = useCallback(
    (c: Connection) => {
      setEdges((eds) =>
        addEdge(
          {
            ...c,
            id: `e-${c.source}-${c.target}-${Date.now()}`,
            style: { stroke: "#64748b" },
            markerEnd: { type: MarkerType.ArrowClosed },
            data: { condition: "" },
          },
          eds,
        ),
      );
    },
    [setEdges],
  );

  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }, []);

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      const type = e.dataTransfer.getData("application/wf-node");
      if (!type || !wrapRef.current) return;
      const bounds = wrapRef.current.getBoundingClientRect();
      const position = {
        x: e.clientX - bounds.left - 80,
        y: e.clientY - bounds.top - 20,
      };
      const nid = `${type}-${idCounter.current++}`;
      setNodes((nds) => [
        ...nds,
        {
          id: nid,
          type: "wf",
          position,
          data: {
            wid: nid,
            label: nodeMeta[type]?.label ?? type,
            sublabel: "",
            wtype: type,
            cfg: defaultConfig(type),
            running: false,
            done: false,
            failed: false,
            waiting: false,
          },
        },
      ]);
      setSelectedNodeId(nid);
    },
    [setNodes],
  );

  const validate = async (silent = false) => {
    try {
      const res = await api.validateWorkflow(toModel());
      if (res.valid) {
        setErrors([]);
        if (res.warnings?.length) {
          // 警告以错误面板形式提示但不阻断
          setErrors(res.warnings.map((w) => ({ ...w, code: "⚠ " + w.code })));
          if (!silent) toast.warning(`校验通过,但有 ${res.warnings.length} 个建议`);
        } else if (!silent) {
          toast.success("校验通过");
        }
        return true;
      }
      setErrors(res.errors);
      if (!silent) toast.error(`校验失败: ${res.errors.length} 个问题`);
      return false;
    } catch (e) {
      if (!silent) toast.error((e as Error).message);
      return false;
    }
  };

  const save = async () => {
    setSaving(true);
    try {
      const ok = await validate(true);
      const saved = await api.saveWorkflow(toModel());
      setWf(saved);
      hydrate(saved);
      if (ok) toast.success(`已保存 (v${saved.version})`);
      else toast.warning(`已保存 (v${saved.version}),但存在校验问题`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  saveRef.current = save;
  const run = async () => {
    if (!wf) return;
    try {
      const exec = await api.runWorkflow(wf.id, task || "默认任务");
      toast.success("执行已启动");
      setRunOpen(false);
      nav(`/executions/${exec.id}`);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const selectedNode = nodes.find((n) => n.id === selectedNodeId);
  const selectedEdge = edges.find((e) => e.id === selectedEdgeId);

  if (!wf) {
    return <div className="flex h-full items-center justify-center text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="flex h-full flex-col">
      {/* 顶栏 */}
      <div className="flex items-center gap-3 border-b px-4 py-2.5">
        <Input
          className="w-56 font-medium"
          value={wf.name}
          onChange={(e) => setWf({ ...wf, name: e.target.value })}
        />
        <Badge variant="secondary">v{wf.version}</Badge>
        <Badge variant="outline" className="font-mono text-[10px]">{wf.id}</Badge>
        <div className="ml-auto flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={autoLayout} title="按连线方向自动排列节点">
            <LayoutGrid className="mr-1 h-4 w-4" /> 自动布局
          </Button>
          <Button variant="outline" size="sm" onClick={() => validate()}>
            <ShieldCheck className="mr-1 h-4 w-4" /> 校验
          </Button>
          <Button variant="outline" size="sm" onClick={save} disabled={saving}>
            <Save className="mr-1 h-4 w-4" /> 保存
          </Button>
          <Button size="sm" onClick={() => setRunOpen(true)}>
            <Play className="mr-1 h-4 w-4" /> 运行
          </Button>
        </div>
      </div>

      <div className="flex min-h-0 flex-1">
        {/* 左侧节点面板 */}
        <div className="w-44 shrink-0 space-y-1 overflow-auto border-r p-3">
          <div className="mb-2 text-xs font-medium text-muted-foreground">拖拽添加节点</div>
          {Object.entries(nodeMeta).map(([type, meta]) => (
            <div
              key={type}
              draggable
              onDragStart={(e) => e.dataTransfer.setData("application/wf-node", type)}
              className={cn(
                "flex cursor-grab items-center gap-2 rounded-md border px-2.5 py-2 text-sm active:cursor-grabbing",
                meta.color,
              )}
            >
              {meta.icon}
              {meta.label}
            </div>
          ))}
        </div>

        {/* 画布 */}
        <div className="relative min-w-0 flex-1" ref={wrapRef}>
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onDragOver={onDragOver}
            onDrop={onDrop}
            onNodeClick={(_, n) => {
              setSelectedNodeId(n.id);
              setSelectedEdgeId(null);
            }}
            onEdgeClick={(_, e) => {
              setSelectedEdgeId(e.id);
              setSelectedNodeId(null);
            }}
            onPaneClick={() => {
              setSelectedNodeId(null);
              setSelectedEdgeId(null);
            }}
            nodeTypes={nodeTypes}
            deleteKeyCode={["Backspace", "Delete"]}
            fitView
          >
            <Background gap={20} />
            <Controls />
            <MiniMap pannable zoomable />
          </ReactFlow>

          {/* 校验错误浮层 */}
          {errors && errors.length > 0 && (
            <div className="absolute right-4 top-4 z-10 max-w-sm space-y-1 rounded-lg border bg-popover p-3 shadow-lg">
              <div className="mb-1 flex items-center justify-between text-sm font-medium">
                <span className="text-red-500">校验问题 ({errors.length})</span>
                <button className="text-muted-foreground hover:text-foreground" onClick={() => setErrors(null)}>×</button>
              </div>
              {errors.map((err, i) => (
                <div
                  key={i}
                  className="cursor-pointer rounded border px-2 py-1.5 text-xs hover:bg-accent"
                  onClick={() => {
                    if (err.node) setSelectedNodeId(err.node);
                  }}
                >
                  <span className="font-mono text-red-400">{err.code}</span>
                  {err.node && <span className="text-muted-foreground"> @{err.node}</span>}
                  <div className="text-foreground/90">{err.message}</div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* 右侧属性面板 */}
        <div className="w-72 shrink-0 overflow-auto border-l p-4">
          {selectedNode ? (
            <NodePropertyPanel
              key={selectedNode.id + JSON.stringify(selectedNode.data.cfg)}
              node={selectedNode}
              agents={agents}
              onChange={(patch) =>
                setNodes((nds) =>
                  nds.map((n) =>
                    n.id === selectedNode.id
                      ? {
                          ...n,
                          data: { ...n.data, ...patch.data },
                        }
                      : n,
                  ),
                )
              }
              onDelete={() => {
                setNodes((nds) => nds.filter((n) => n.id !== selectedNode.id));
                setEdges((eds) => eds.filter((e) => e.source !== selectedNode.id && e.target !== selectedNode.id));
                setSelectedNodeId(null);
              }}
              onDuplicate={() => {
                const nid = `${selectedNode.data.wtype}-${idCounter.current++}`;
                setNodes((nds) => [
                  ...nds,
                  {
                    ...selectedNode,
                    id: nid,
                    position: {
                      x: selectedNode.position.x + 40,
                      y: selectedNode.position.y + 40,
                    },
                    selected: true,
                    data: { ...selectedNode.data, wid: nid },
                  },
                ]);
                setSelectedNodeId(nid);
              }}
            />
          ) : selectedEdge ? (
            <EdgePropertyPanel
              edge={selectedEdge}
              onChange={(condition) =>
                setEdges((eds) =>
                  eds.map((e) =>
                    e.id === selectedEdge.id
                      ? {
                          ...e,
                          label: condition || undefined,
                          animated: !!condition,
                          style: { stroke: condition ? "#38bdf8" : "#64748b" },
                          data: { condition },
                        }
                      : e,
                  ),
                )
              }
              onDelete={() => {
                setEdges((eds) => eds.filter((e) => e.id !== selectedEdge.id));
                setSelectedEdgeId(null);
              }}
            />
          ) : (
            <WorkflowPropertyPanel
              wf={wf}
              onChange={(patch) => setWf({ ...wf, ...patch })}
            />
          )}
        </div>
      </div>

      {/* 运行对话框 */}
      <Dialog open={runOpen} onOpenChange={setRunOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>运行工作流</DialogTitle>
          </DialogHeader>
          <Input
            placeholder="任务描述(将作为 {{task}} 注入 Prompt)"
            value={task}
            onChange={(e) => setTask(e.target.value)}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRunOpen(false)}>取消</Button>
            <Button onClick={run}>
              <Play className="mr-1 h-4 w-4" /> 启动执行
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function defaultConfig(type: string): Record<string, unknown> {
  switch (type) {
    case "agent":
      return { agent: "mock-claude", mode: "plan" };
    case "condition":
      return { expression: 'decision == "APPROVED"' };
    case "git":
      return { operation: "diff" };
    case "human":
      return { prompt: "请确认", responses: ["approve", "reject", "continue"] };
    default:
      return {};
  }
}

// ---- 属性面板组件 ----

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}

function NodePropertyPanel({
  node,
  agents,
  onChange,
  onDelete,
  onDuplicate,
}: {
  node: RFNode;
  agents: string[];
  onChange: (patch: { data?: Record<string, unknown> }) => void;
  onDelete: () => void;
  onDuplicate: () => void;
}) {
  const type = String(node.data.wtype);
  const cfg = (node.data.cfg as Record<string, unknown>) ?? {};
  const setCfg = (k: string, v: unknown) => {
    onChange({ data: { cfg: { ...cfg, [k]: v } } });
  };
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="text-sm font-medium">节点配置</div>
        <div className="space-x-1">
          <Button variant="ghost" size="sm" onClick={onDuplicate}>
            复制
          </Button>
          <Button variant="ghost" size="sm" className="text-red-500" onClick={onDelete}>
            删除
          </Button>
        </div>
      </div>
      <Row label="名称">
        <Input
          value={String(node.data.label ?? "")}
          onChange={(e) => onChange({ data: { label: e.target.value } })}
        />
      </Row>

      <div className="flex items-center justify-between rounded-md border px-3 py-2">
        <span className="text-xs text-muted-foreground">启用节点(关闭则运行时跳过)</span>
        <Switch
          checked={cfg.enabled !== false}
          onCheckedChange={(v) => setCfg("enabled", v)}
        />
      </div>

      {type === "agent" && (
        <>
          <Row label="Agent">
            <Select value={String(cfg.agent ?? "")} onValueChange={(v) => setCfg("agent", v)}>
              <SelectTrigger><SelectValue placeholder="选择 Agent" /></SelectTrigger>
              <SelectContent>
                {agents.map((a) => (
                  <SelectItem key={a} value={a}>{a}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Row>
          <Row label="Model(可选,覆盖 Agent 默认模型)">
            <Input
              className="font-mono text-xs"
              placeholder="留空 = 使用 Agent 配置的默认模型"
              value={String(cfg.model ?? "")}
              onChange={(e) => setCfg("model", e.target.value)}
            />
          </Row>
          <Row label="Mode">
            <Select value={String(cfg.mode ?? "")} onValueChange={(v) => setCfg("mode", v)}>
              <SelectTrigger><SelectValue placeholder="执行模式" /></SelectTrigger>
              <SelectContent>
                {["plan", "execute", "review", "fix", "test", "submit"].map((m) => (
                  <SelectItem key={m} value={m}>{m}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Row>
          <Row label="Prompt 模板(支持 {{task}} {{plan}} {{git_diff}} 等变量)">
            <Textarea
              className="min-h-28 font-mono text-xs"
              value={String((cfg.prompt as Record<string, unknown>)?.template ?? "")}
              onChange={(e) => setCfg("prompt", { template: e.target.value })}
            />
          </Row>
          <Row label="Working Directory(可选)">
            <Input value={String(cfg.working_dir ?? "")} onChange={(e) => setCfg("working_dir", e.target.value)} />
          </Row>
          <Row label="Timeout(秒,0=默认)">
            <Input
              type="number"
              value={String(cfg.timeout_seconds ?? 0)}
              onChange={(e) => setCfg("timeout_seconds", Number(e.target.value))}
            />
          </Row>
          <div className="grid grid-cols-2 gap-2">
            <Row label="重试次数">
              <Input
                type="number"
                value={String((cfg.retry as Record<string, unknown>)?.max_attempts ?? 1)}
                onChange={(e) =>
                  setCfg("retry", {
                    ...(cfg.retry as Record<string, unknown>),
                    max_attempts: Number(e.target.value),
                  })
                }
              />
            </Row>
            <Row label="退避策略">
              <Select
                value={String((cfg.retry as Record<string, unknown>)?.backoff ?? "fixed")}
                onValueChange={(v) =>
                  setCfg("retry", {
                    ...(cfg.retry as Record<string, unknown>),
                    backoff: v,
                  })
                }
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="fixed">fixed</SelectItem>
                  <SelectItem value="exponential">exponential</SelectItem>
                </SelectContent>
              </Select>
            </Row>
          </div>
        </>
      )}

      {type === "condition" && (
        <Row label="Expression(可用: decision / status / iteration)">
          <Input
            className="font-mono text-xs"
            placeholder={'decision == "APPROVED"'}
            value={String(cfg.expression ?? "")}
            onChange={(e) => setCfg("expression", e.target.value)}
          />
        </Row>
      )}

      {type === "skill" && (
        <>
          <Row label="Skill">
            <Input className="font-mono text-xs" value={String(cfg.skill ?? "")} onChange={(e) => setCfg("skill", e.target.value)} />
          </Row>
          <Row label="Arguments(JSON)">
            <Textarea
              className="min-h-20 font-mono text-xs"
              value={JSON.stringify(cfg.args ?? {}, null, 2)}
              onChange={(e) => {
                try {
                  setCfg("args", JSON.parse(e.target.value || "{}"));
                } catch {
                  // 输入过程中允许暂不合法的 JSON
                }
              }}
            />
          </Row>
        </>
      )}

      {type === "human" && (
        <>
          <Row label="提示语">
            <Textarea
              className="min-h-16"
              value={String(cfg.prompt ?? "")}
              onChange={(e) => setCfg("prompt", e.target.value)}
            />
          </Row>
          <Row label="可选响应(逗号分隔)">
            <Input
              value={Array.isArray(cfg.responses) ? (cfg.responses as string[]).join(",") : ""}
              onChange={(e) =>
                setCfg(
                  "responses",
                  e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
                )
              }
            />
          </Row>
        </>
      )}

      {type === "script" && (
        <Row label="Shell 命令">
          <Textarea
            className="min-h-20 font-mono text-xs"
            value={String(cfg.command ?? "")}
            onChange={(e) => setCfg("command", e.target.value)}
          />
        </Row>
      )}

      {type === "git" && (
        <Row label="Operation">
          <Select value={String(cfg.operation ?? "")} onValueChange={(v) => setCfg("operation", v)}>
            <SelectTrigger><SelectValue placeholder="git 操作" /></SelectTrigger>
            <SelectContent>
              {["status", "diff", "log", "branch", "checkout", "commit"].map((o) => (
                <SelectItem key={o} value={o}>{o}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
      )}

      {type === "subworkflow" && (
        <Row label="Workflow ID">
          <Input className="font-mono text-xs" value={String(cfg.workflow_id ?? "")} onChange={(e) => setCfg("workflow_id", e.target.value)} />
        </Row>
      )}
    </div>
  );
}

function EdgePropertyPanel({
  edge,
  onChange,
  onDelete,
}: {
  edge: RFEdge;
  onChange: (condition: string) => void;
  onDelete: () => void;
}) {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="text-sm font-medium">连线配置</div>
        <Button variant="ghost" size="sm" className="text-red-500" onClick={onDelete}>
          删除连线
        </Button>
      </div>
      <div className="text-xs text-muted-foreground">
        {edge.source} → {edge.target}
      </div>
      <Row label="条件表达式(留空=无条件)">
        <Input
          className="font-mono text-xs"
          placeholder={'decision == "APPROVED"'}
          value={(edge.data?.condition as string) ?? ""}
          onChange={(e) => onChange(e.target.value)}
        />
      </Row>
      <p className="text-xs text-muted-foreground">
        条件按声明顺序求值,第一个成立的分支被执行。可用变量: decision, status, output, iteration。
      </p>
    </div>
  );
}

function WorkflowPropertyPanel({
  wf,
  onChange,
}: {
  wf: Workflow;
  onChange: (patch: Partial<Workflow>) => void;
}) {
  return (
    <div className="space-y-4">
      <div className="text-sm font-medium">工作流设置</div>
      <Row label="描述">
        <Textarea
          className="min-h-16"
          value={wf.description}
          onChange={(e) => onChange({ description: e.target.value })}
        />
      </Row>
      <div className="flex items-center justify-between rounded-md border px-3 py-2">
        <span className="text-xs text-muted-foreground">启用工作流(禁用后不可运行)</span>
        <Switch
          checked={wf.enabled}
          onCheckedChange={(v) => onChange({ enabled: v })}
        />
      </div>
      <Row label="Variables(JSON,可用 {{variable.xxx}} 引用)">
        <Textarea
          className="min-h-20 font-mono text-xs"
          value={JSON.stringify(wf.variables ?? {}, null, 2)}
          onChange={(e) => {
            try {
              onChange({ variables: JSON.parse(e.target.value || "{}") });
            } catch {
              // 输入过程中允许暂不合法的 JSON
            }
          }}
        />
      </Row>
      <Row label="Max Iterations(循环上限)">
        <Input
          type="number"
          value={String(wf.settings.max_iterations ?? 5)}
          onChange={(e) =>
            onChange({
              settings: { ...wf.settings, max_iterations: Number(e.target.value) },
            })
          }
        />
      </Row>
      <Row label="达到上限后">
        <Select
          value={wf.settings.on_loop_limit ?? "wait_user"}
          onValueChange={(v) => onChange({ settings: { ...wf.settings, on_loop_limit: v } })}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="wait_user">等待人工处理 (wait_user)</SelectItem>
            <SelectItem value="fail">直接失败 (fail)</SelectItem>
          </SelectContent>
        </Select>
      </Row>
      <p className="text-xs text-muted-foreground">
        选中节点或连线可编辑其属性;拖动左侧节点类型到画布添加新节点。
      </p>
    </div>
  );
}
