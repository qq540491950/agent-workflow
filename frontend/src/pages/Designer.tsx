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
import { api, asArray, describeWarnings } from "@/lib/api";
import { reportError } from "@/lib/errors";
import type { AgentInfo, ValidationError, Workflow, WFNode } from "@/lib/types";
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
import { useUnsavedGuard } from "@/lib/unsaved-guard";

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

// ---- mode 清单(后端派生) ----

/**
 * FALLBACK_MODES 是 mode 清单的**兜底**(加载中 / 加载失败时用)。
 *
 * 它**不是**清单的来源:清单的唯一来源是后端(model.AllModes,经
 * api.modes() 拿到)—— 前端此前自己硬编码了一份清单,后端加了 mode
 * 而前端没跟上,用户在下拉里**选不到**新 mode;前端留着后端已删的
 * mode,用户选了必然被校验期判非法(约定 13)。
 *
 * 为什么还要留一份兜底:后端返回前(网络慢)或失败时,下拉不能空着 ——
 * 空下拉框会让用户以为"这个节点不能设 mode",而节点配置面板此时
 * 正在被使用。
 *
 * 它必须与后端清单**逐项一致**(后端新增/删除 mode 时这里要跟着改),
 * 由 Go 侧的契约测试锁住(见 api/modes_test.go)。
 */
export const FALLBACK_MODES = [
  "plan",
  "execute",
  "review",
  "fix",
  "test",
  "submit",
];

/**
 * resolveModeList 合并"后端返回的清单"与兜底清单。
 *
 * 后端返回空(异常返回 / 空数组)→ 用兜底,控件不留空。
 * 导出以便单测锁定该行为(见 designer.test.ts)。
 */
export function resolveModeList(loaded: unknown): string[] {
  const list = asArray<string>(loaded);
  return list.length > 0 ? list : FALLBACK_MODES;
}

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

// 把画布状态重建为提交给后端的工作流定义。
//
// **保存路径会用它整体替换工作流对象**:凡是这里没有的字段,都会被丢掉。
// 因此凡是"服务端有、设计器不改写"的字段,都必须在这里原样带回 ——
// 否则用户在画布上改点别的再点保存,那些字段就被静默抹掉(约定 14:
// 静默丢弃 = 用户以为设置还在,实际已经没了)。
//
// 当前原样带回的字段:
//
//   - permission:工作流级权限覆盖。设计器**不提供**编辑入口(UI 不呈现),
//     但服务端返回的工作流里带着它(YAML 导入 / 备份恢复都会写进去),
//     漏掉就等于把用户没碰过的权限收紧抹掉。
//
// 导出以便单测锁定该行为(见 designer.test.ts)。
export function toWorkflowModel(
  wf: Workflow | null,
  nodes: RFNode[],
  edges: RFEdge[],
): Workflow {
  const wfNodes: WFNode[] = nodes.map((n) => ({
    id: n.id,
    name: String(n.data.label ?? n.id),
    type: n.data.wtype as WFNode["type"],
    position: { x: n.position.x, y: n.position.y },
    config: (n.data.cfg as Record<string, unknown>) ?? {},
  }));
  const model: Workflow = {
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
  // 权限覆盖:有就原样带回(不解析、不改写),没有就不放这个键 ——
  // 放一个 undefined 进去在桌面 IPC 路径上会变成"显式清空"。
  if (wf?.permission !== undefined) {
    model.permission = wf.permission;
  }
  return model;
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
  const [runVars, setRunVars] = useState("{}");
  const [agents, setAgents] = useState<string[]>([]);
  // mode 清单:后端派生(约定 13)。加载中/失败时用 FALLBACK_MODES 兜底,
  // 控件不留空(空下拉框 = 用户以为"这个节点不能设 mode")。
  const [modes, setModes] = useState<string[]>(FALLBACK_MODES);
  const [saving, setSaving] = useState(false);
  // 未保存守卫:savedFp 是"最后一次与画布一致的模型指纹"。
  // 加载完成 / 保存成功后通过 fpTick 触发 effect 重打指纹(直接在
  // hydrate 后取 toModel() 会读到未刷新的旧 state);dirty = 当前画布
  // 模型 ≠ 指纹。此前画布编辑只存在内存里,误点侧边栏 = 半小时编排
  // 静默蒸发 —— 这不是"提示不友好",是数据丢失。
  const [savedFp, setSavedFp] = useState<string | null>(null);
  const [fpTick, setFpTick] = useState(0);
  useEffect(() => {
    if (!wf) return;
    setSavedFp(JSON.stringify(toModel()));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fpTick 是刻意的重打指纹信号
  }, [fpTick]);
  const dirty = wf != null && savedFp != null && JSON.stringify(toModel()) !== savedFp;
  useUnsavedGuard(dirty, "画布上有未保存的更改,离开将丢失。确定离开?");
  const wrapRef = useRef<HTMLDivElement>(null);
  const { fitView } = useReactFlow();

  useEffect(() => {
    // asArray 而非直接 .map:桌面 IPC 与 SPA fallback 异常路径下
    // api.listAgents() 可能返回 null 或 HTML 兜底页对象,
    // 直接 .map 抛 TypeError 且被下面的 catch 吞掉 —— 界面只剩一个空的
    // Agent 下拉框,没有任何提示。
    //
    // catch 也从 `.catch(() => {})` 改成 reportError:请求失败时同样只留
    // 一个空下拉框,用户以为"没有可选的 Agent"。同一个错误 5 秒内只提示
    // 一次(Designer 会被反复挂载)。
    api
      .listAgents()
      .then((as) => setAgents(asArray<AgentInfo>(as).map((a) => a.id)))
      .catch((e: unknown) => reportError(e, "加载 Agent 列表"));
  }, []);

  useEffect(() => {
    // mode 清单来自后端:后端加了 mode 而前端没跟上,用户就选不到它;
    // 前端留着后端已删的 mode,用户选了必然被校验期判非法(约定 13)。
    //
    // 失败时保留兜底清单(下拉不留空)并提示 —— 不静默:用户至少知道
    // 清单没拿到(否则会以为"只有这几个 mode")。
    api
      .modes()
      .then((m) => setModes(resolveModeList(m)))
      .catch((e: unknown) => reportError(e, "加载 mode 清单"));
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

  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    api.getWorkflow(id).then((w) => {
      setWf(w);
      hydrate(w);
      setFpTick((t) => t + 1); // 画布就绪后打"已保存"指纹
      setTimeout(() => fitView({ padding: 0.15 }), 60);
      // 打开即静默校验一次:导入/保存时带回的警告(如 SUBWORKFLOW_NOT_FOUND
      // "运行时将失败")此前只在用户点「校验」或保存时可见 —— 带着已知问题
      // 的工作流每次打开都装作没事,用户要等到运行失败才知道(约定 14:
      // 该报告却没报告)。静默 = 不弹 toast,错误面板照常显示。
      api
        .validateWorkflow(w)
        .then((res) => {
          if (!res.valid) setErrors(res.errors);
          else if (res.warnings?.length)
            setErrors(res.warnings.map((x) => ({ ...x, code: "⚠ " + x.code })));
        })
        .catch((e) => {
          // 预校验失败不阻断打开画布(用户仍可查看/编辑,点「校验」会看到
          // 原因),但要留痕 —— 静默吞错守卫(error-guard)也要求不得是空 catch。
          console.warn("打开工作流时的预校验失败(点「校验」可重试)", e);
        });
    }).catch((e) => {
      setLoadError(e.message);
      reportError(e, "加载工作流");
    });
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
    return toWorkflowModel(wf, nodes, edges);
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
      const nid = nextNodeId(nodes, type);
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
            cfg: defaultConfigForType(type),
            running: false,
            done: false,
            failed: false,
            waiting: false,
          },
        },
      ]);
      setSelectedNodeId(nid);
    },
    [nodes, setNodes],
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
      if (!silent) reportError(`校验失败: ${res.errors.length} 个问题`);
      return false;
    } catch (e) {
      if (!silent) reportError(e, "校验工作流");
      return false;
    }
  };

  const save = async () => {
    setSaving(true);
    try {
      // 先本地校验再提交:后端 Save 同样会校验,并把 validator 错误数组
      // 原样塞进 error 字段(用户只看到一坨裸 JSON)。此前无论校验是否
      // 通过都提交,既拿不到可读提示,后端的 warning 分支也不可达。
      if (!(await validate(true))) {
        reportError("校验未通过,已阻止保存(见画布下方错误列表)");
        return;
      }
      const saved = await api.saveWorkflow(toModel());
      setWf(saved);
      hydrate(saved);
      setFpTick((t) => t + 1); // 保存即新基线,守卫不再拦截
      toast.success(`已保存 (v${saved.version})`);
      // 校验警告必须让用户看到:此前只进后端日志,用户以为"已废弃的
      // 配置键"仍然生效(两种模式现在都带回 warnings)。
      const warns = describeWarnings(saved.warnings);
      if (warns) toast.warning(warns);
    } catch (e) {
      reportError(e, "保存工作流");
    } finally {
      setSaving(false);
    }
  };

  saveRef.current = save;
  // 打开运行对话框时预填工作流变量
  const openRun = () => {
    setRunVars(JSON.stringify(wf?.variables ?? {}, null, 2));
    setRunOpen(true);
  };
  const run = async () => {
    if (!wf) return;
    let variables: Record<string, unknown>;
    try {
      variables = JSON.parse(runVars || "{}");
    } catch {
      reportError("变量不是合法 JSON");
      return;
    }
    try {
      const exec = await api.runWorkflow(wf.id, task || "默认任务", variables);
      toast.success("执行已启动");
      setRunOpen(false);
      nav(`/executions/${exec.id}`);
    } catch (e) {
      reportError(e, "运行工作流");
    }
  };

  const selectedNode = nodes.find((n) => n.id === selectedNodeId);
  const selectedEdge = edges.find((e) => e.id === selectedEdgeId);

  if (loadError) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
        <p>工作流加载失败:{loadError}</p>
        <Button variant="outline" size="sm" onClick={() => nav("/workflows")}>返回工作流列表</Button>
      </div>
    );
  }
  if (!wf) {
    return <div className="flex h-full items-center justify-center text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="flex h-full flex-col">
      <datalist id="common-models">
        <option value="claude-sonnet-4-5" />
        <option value="claude-opus-4-1" />
        <option value="claude-haiku-4-5" />
        <option value="glm-4.7" />
        <option value="glm-4.7-flash" />
        <option value="gpt-5.1-codex" />
      </datalist>
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
          {/* Cmd/Ctrl+S 快捷保存存在但不可见 —— 按钮上注明,快捷键才有被发现的可能 */}
          <Button
            variant="outline"
            size="sm"
            onClick={save}
            disabled={saving}
            title="保存(⌘S / Ctrl+S)"
          >
            <Save className="mr-1 h-4 w-4" /> 保存
          </Button>
          <Button size="sm" onClick={openRun}>
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
            colorMode="dark"
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
            {nodes.length === 0 && (
              <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
                <div className="rounded-xl border border-dashed px-6 py-4 text-center text-sm text-muted-foreground">
                  画布为空:从左侧拖入节点开始编排
                  <div className="mt-1 text-xs">拖到画布后连线,顶部「校验」检查结构,「保存」并「运行」</div>
                </div>
              </div>
            )}
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
              // 注意:key 只能随"选中的节点"变化。此前把 cfg 也拼进 key,
              // 而每次按键都会产生新的 cfg 对象 → 整个面板被卸载重建,
              // 输入框失焦,model / prompt / command 等字段只能敲一个字符。
              key={selectedNode.id}
              node={selectedNode}
              agents={agents}
              modes={modes}
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
                const nid = nextNodeId(nodes, String(selectedNode.data.wtype ?? "node"));
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
          {/* 任务框回车 = 启动执行:填完任务直接回车是最自然的路径 */}
          <Input
            placeholder="任务描述(将作为 {{task}} 注入 Prompt)"
            value={task}
            onChange={(e) => setTask(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                run();
              }
            }}
          />
          <Textarea
            className="min-h-16 font-mono text-xs"
            placeholder='{"key": "value"}(可选变量,{{variable.key}} 引用)'
            value={runVars}
            onChange={(e) => setRunVars(e.target.value)}
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

// 新建节点的默认配置。
//
// condition 节点**不再**预填 expression:路由条件写在**连线**上
// (edge.condition),运行期与编译器都不读节点上的表达式 —— 预填一个
// 不生效的表达式,只会让人以为"填了就生效"(约定 13:校验期与运行期同源,
// UI 也不得呈现运行期不读的配置)。
//
// 导出以便单测锁定该行为(见 designer.test.ts)。
export function defaultConfigForType(type: string): Record<string, unknown> {
  switch (type) {
    case "agent":
      return { agent: "mock-claude", mode: "plan" };
    case "git":
      return { operation: "diff" };
    case "human":
      return { prompt: "请确认", responses: ["approve", "reject", "continue"] };
    default:
      return {};
  }
}

// 遗留的 condition 节点配置里若还留着 expression,返回它的值(空串 = 没有)。
//
// 存量 DSL 里可能已经写了 config.expression(旧版 UI 会预填)。该键已废弃:
// 运行期不读取。UI 给出提示,而不是继续渲染一个填了也没用的输入框 ——
// 留着输入框会让人以为路由由它驱动。
//
// 导出以便单测锁定(见 designer.test.ts)。
export function legacyConditionExpression(cfg: Record<string, unknown>): string {
  const v = cfg.expression;
  return typeof v === "string" ? v : "";
}

// 生成不与现有节点冲突的节点 ID。
// 此前使用组件内自增计数器且从不扫描已 hydrate 的节点:打开一个含
// agent-1 的工作流再拖入 agent 节点,ID 必然重复,保存时被后端
// DUPLICATE_NODE 拒绝,而界面无法解释原因。
// 导出以便单测锁定该行为(见 designer.test.ts)。
export function nextNodeId(nodes: RFNode[], type: string): string {
  const used = new Set(nodes.map((n) => n.id));
  let i = 1;
  while (used.has(`${type}-${i}`)) i++;
  return `${type}-${i}`;
}

// 节点级开关 enabled 的合法性:只接受布尔(缺省 = 启用)。
// 后端 compiler.NodeEnabledStateOf / validator 的 INVALID_NODE_ENABLED
// 是同一套语义:非布尔值(如 YAML 里的 enabled: "false")会被判非法并拒绝
// 保存。开关只会写出布尔,但导入/恢复的定义可能带非布尔值。
// 导出以便单测锁定该行为(见 designer.test.ts)。
export function nodeEnabledInvalid(cfg: Record<string, unknown>): boolean {
  return cfg.enabled !== undefined && typeof cfg.enabled !== "boolean";
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

export function NodePropertyPanel({
  node,
  agents,
  modes,
  onChange,
  onDelete,
  onDuplicate,
}: {
  node: RFNode;
  agents: string[];
  /** mode 下拉的选项(后端派生;见 FALLBACK_MODES)。 */
  modes: string[];
  onChange: (patch: { data?: Record<string, unknown> }) => void;
  onDelete: () => void;
  onDuplicate: () => void;
}) {
  const type = String(node.data.wtype);
  const cfg = (node.data.cfg as Record<string, unknown>) ?? {};
  const setCfg = (k: string, v: unknown) => {
    onChange({ data: { cfg: { ...cfg, [k]: v } } });
  };
  // enabled 只接受布尔:非布尔值(如 YAML 里的 enabled: "false")会被后端
  // 校验判为 INVALID_NODE_ENABLED 并拒绝保存(约定 13:校验期与运行期同源)。
  // 开关本身只会写出布尔,但导入/恢复的定义可能带非布尔值 —— 这里给出提示,
  // 免得用户被自己的校验器挡住却看不出原因。
  const enabledInvalid = nodeEnabledInvalid(cfg);
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
          aria-label="启用节点"
          onCheckedChange={(v) => setCfg("enabled", v)}
        />
      </div>
      {enabledInvalid && (
        <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-300">
          enabled 当前不是布尔值(来自导入/备份的定义):后端校验会报
          INVALID_NODE_ENABLED。切换上面的开关即可修正为布尔值。
        </p>
      )}

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
              list="common-models"
              placeholder="留空 = 使用 Agent 配置的默认模型"
              value={String(cfg.model ?? "")}
              onChange={(e) => setCfg("model", e.target.value)}
            />
          </Row>
          <Row label="Mode">
            <Select value={String(cfg.mode ?? "")} onValueChange={(v) => setCfg("mode", v)}>
              <SelectTrigger><SelectValue placeholder="执行模式" /></SelectTrigger>
              <SelectContent>
                {modes.map((m) => (
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
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">
            条件节点本身无副作用:路由由**连线上的条件**决定。
            选中一条连线 → 「条件表达式」,按声明顺序求值,第一个成立的分支被执行。
            可用变量:decision, status, output, iteration。
          </div>
          {legacyConditionExpression(cfg) !== "" && (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-300">
              该节点残留 expression 配置(已废弃):运行期不读取它,不影响路由。
              条件请写到连线上;保存不受影响。
            </p>
          )}
        </div>
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
          aria-label="启用工作流"
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
      <Row label="Timeout(秒,0=不限,防单轮挂死)">
        <Input
          type="number"
          min={0}
          value={String(wf.settings.timeout_seconds ?? 0)}
          onChange={(e) =>
            onChange({
              settings: { ...wf.settings, timeout_seconds: Number(e.target.value) },
            })
          }
        />
      </Row>
      <p className="text-xs text-muted-foreground">
        选中节点或连线可编辑其属性;拖动左侧节点类型到画布添加新节点。
      </p>
    </div>
  );
}
