// 统一服务层:桌面模式走 Wails bindings(IPC),浏览器模式走 HTTP+SSE。
// 页面只依赖本模块,不感知底层传输方式。
import type {
  AgentConfig,
  AgentInfo,
  Artifact,
  Execution,
  ExecutionNode,
  PermissionPolicy,
  SkillDTO,
  UIEvent,
  ValidationResult,
  Workflow,
} from "./types";

// 传输模式探测:调用一次真实绑定,500ms 内成功 → 桌面模式(IPC),
// 否则回退 HTTP+SSE(Wails runtime 注入时机晚于模块加载,不能用存在性判断)。
let modePromise: Promise<"desktop" | "web"> | null = null;

export function detectMode(): Promise<"desktop" | "web"> {
  if (!modePromise) {
    modePromise = (async () => {
      try {
        const rt = await import("@wailsio/runtime");
        const probe = rt.Call.ByID(1924388702); // WorkflowService.List
        const winner = await Promise.race([
          probe.then(
            () => "desktop" as const,
            () => "web" as const,
          ),
          new Promise<"web">((r) => setTimeout(() => r("web"), 500)),
        ]);
        return winner;
      } catch {
        return "web";
      }
    })();
  }
  return modePromise;
}

// Wails bindings 返回生成器类型(带 null),统一断言为本项目的领域类型。
function nn<T>(v: unknown): T {
  return (v ?? null) as T;
}

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = text;
  }
  if (!res.ok) {
    const msg =
      (data as { error?: string })?.error ?? `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return data as T;
}

// Wails bindings 按需加载(仅桌面模式)。
async function wailsBindings() {
  const [wf, ex, ag, sk, gt, st] = await Promise.all([
    import("@bindings/agentworkflow/app/application/workflowservice.js"),
    import("@bindings/agentworkflow/app/application/executionservice.js"),
    import("@bindings/agentworkflow/app/application/agentservice.js"),
    import("@bindings/agentworkflow/app/application/skillservice.js"),
    import("@bindings/agentworkflow/app/application/gitservice.js"),
    import("@bindings/agentworkflow/app/application/settingsservice.js"),
  ]);
  return { wf, ex, ag, sk, gt, st };
}

export const api = {
  
  // ---- Workflows ----
  async listWorkflows(): Promise<Workflow[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.List());
    return http("/api/workflows");
  },
  async getWorkflow(id: string): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.Get(id));
    return http(`/api/workflows/${id}`);
  },
  async saveWorkflow(wf: Workflow): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.Save(wf as unknown as never));
    return http("/api/workflows", { method: "POST", body: JSON.stringify(wf) });
  },
  async deleteWorkflow(id: string): Promise<void> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).wf.Delete(id);
    return http(`/api/workflows/${id}`, { method: "DELETE" });
  },
  async duplicateWorkflow(id: string): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.Duplicate(id));
    return http(`/api/workflows/${id}/duplicate`, { method: "POST" });
  },
  async setEnabled(id: string, enabled: boolean): Promise<Workflow> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).wf.SetEnabled(id, enabled));
    return http(`/api/workflows/${id}/enable?value=${enabled}`, { method: "POST" });
  },
  async importYAML(content: string): Promise<Workflow> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).wf.ImportYAML(content));
    }
    return http("/api/workflows/import", {
      method: "POST",
      body: content,
      headers: { "Content-Type": "application/yaml" },
    });
  },
  async validateWorkflow(wf: Workflow): Promise<ValidationResult> {
    if ((await detectMode()) === "desktop") {
      return nn(
        await (await wailsBindings()).wf.Validate(wf as unknown as never),
      );
    }
    return http("/api/workflows/validate", {
      method: "POST",
      body: JSON.stringify(wf),
    });
  },
  async exportYAML(id: string): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).wf.ExportYAML(id);
    const res = await fetch(`/api/workflows/${id}/export`);
    return res.text();
  },

  // ---- Executions ----
  async runWorkflow(
    id: string,
    task: string,
    variables?: Record<string, unknown>,
  ): Promise<Execution> {
    if ((await detectMode()) === "desktop")
      return nn(
        await (await wailsBindings()).ex.Run(id, task, variables ?? {}),
      );
    return http(`/api/workflows/${id}/run`, {
      method: "POST",
      body: JSON.stringify({ task, variables }),
    });
  },
  async listExecutions(workflowID = "", limit = 50): Promise<Execution[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.List(workflowID, limit));
    const q = workflowID
      ? `?workflow_id=${encodeURIComponent(workflowID)}&limit=${limit}`
      : `?limit=${limit}`;
    return http(`/api/executions${q}`);
  },
  async getExecution(id: string): Promise<Execution> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Get(id));
    return http(`/api/executions/${id}`);
  },
  async executionNodes(id: string): Promise<ExecutionNode[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Nodes(id));
    return http(`/api/executions/${id}/nodes`);
  },
  async executionEvents(id: string): Promise<Record<string, unknown>[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Events(id));
    return http(`/api/executions/${id}/events`);
  },
  async executionArtifacts(id: string): Promise<Artifact[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.Artifacts(id));
    return http(`/api/executions/${id}/artifacts`);
  },
  async provideInput(
    id: string,
    response: Record<string, unknown>,
  ): Promise<Execution> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ex.ProvideInput(id, response));
    return http(`/api/executions/${id}/input`, {
      method: "POST",
      body: JSON.stringify(response),
    });
  },
  async retryExecution(id: string, skip: boolean): Promise<Execution> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ex.RetryNode(id, skip));
    }
    return http(`/api/executions/${id}/retry`, {
      method: "POST",
      body: JSON.stringify({ skip }),
    });
  },
  async exportExecutionURL(id: string): Promise<string> {
    // 浏览器与桌面均用下载链接(桌面由资产处理器旁路到同源)
    return `/api/executions/${id}/export`;
  },
  async cancelExecution(id: string): Promise<void> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).ex.Cancel(id);
    return http(`/api/executions/${id}/cancel`, { method: "POST" });
  },

  // ---- Agents / Skills ----
  async listAgents(): Promise<AgentInfo[]> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).ag.List());
    return http("/api/agents");
  },
  async getAgentConfig(id: string): Promise<AgentConfig> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).ag.GetConfig(id));
    }
    return http(`/api/agents/${id}/config`);
  },
  async updateAgentConfig(id: string, cfg: AgentConfig): Promise<AgentConfig> {
    if ((await detectMode()) === "desktop") {
      return nn(
        await (await wailsBindings()).ag.UpdateConfig(id, cfg as unknown as never),
      );
    }
    return http(`/api/agents/${id}/config`, {
      method: "PUT",
      body: JSON.stringify(cfg),
    });
  },
  async testAgent(id: string): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).ag.Test(id);
    const r = await http<{ output: string }>(`/api/agents/${id}/test`, {
      method: "POST",
    });
    return r.output;
  },
  async setAgentPermission(id: string, p: PermissionPolicy): Promise<void> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).ag.SetPermission(id, p);
    return http(`/api/agents/${id}/permission`, {
      method: "POST",
      body: JSON.stringify(p),
    });
  },
  async listSkills(): Promise<SkillDTO[]> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).sk.List();
    return http("/api/skills");
  },
  async testSkill(id: string): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).sk.Test(id);
    const r = await http<{ output: string }>(`/api/skills/${id}/test`, {
      method: "POST",
    });
    return r.output;
  },

  async stats(): Promise<
    { workflow_id: string; name: string; total: number; completed: number; failed: number; waiting: number; running: number }[]
  > {
    return http("/api/stats");
  },
  async allEvents(q: string): Promise<
    {
      seq: number;
      execution_id: string;
      node_id: string;
      type: string;
      data: Record<string, unknown>;
      created_at: string;
    }[]
  > {
    return http(`/api/events/all${q}`);
  },

  // ---- Settings ----
  async getSettings(): Promise<{
    data_dir: string;
    git_working_dir: string;
    log_level: string;
  }> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).st.Info());
    }
    return http("/api/settings");
  },
  async updateSettings(patch: {
    git_working_dir?: string;
    log_level?: string;
  }): Promise<{
    data_dir: string;
    git_working_dir: string;
    log_level: string;
  }> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).st.Update(patch.git_working_dir ?? "", patch.log_level ?? ""));
    }
    return http("/api/settings", { method: "POST", body: JSON.stringify(patch) });
  },

  // ---- Git ----
  async gitStatus(): Promise<{
    branch: string;
    modified: string[];
    staged: string[];
    untracked: string[];
  }> {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).gt.Status());
    return http("/api/git/status");
  },
  async gitDiff(staged = false): Promise<string> {
    if ((await detectMode()) === "desktop") return (await wailsBindings()).gt.Diff(staged);
    const r = await http<{ diff: string }>(`/api/git/diff?staged=${staged}`);
    return r.diff;
  },
  async gitCommit(message: string): Promise<string> {
    if ((await detectMode()) === "desktop") {
      return nn(await (await wailsBindings()).gt.Commit(message));
    }
    const r = await http<{ output: string }>(`/api/git/commit`, {
      method: "POST",
      body: JSON.stringify({ message }),
    });
    return r.output;
  },
  async gitLog(limit = 10): Promise<
    { hash: string; author: string; date: string; subject: string }[]
  > {
    if ((await detectMode()) === "desktop") return nn(await (await wailsBindings()).gt.Log(limit));
    return http(`/api/git/log?limit=${limit}`);
  },
};

// ---- 实时事件(统一订阅层) ----

type EventHandler = (ev: UIEvent) => void;
const handlers = new Set<EventHandler>();
let started = false;

export function subscribeEvents(h: EventHandler): () => void {
  handlers.add(h);
  ensureEventStream();
  return () => handlers.delete(h);
}

function dispatch(ev: UIEvent) {
  handlers.forEach((h) => h(ev));
}

async function ensureEventStream() {
  if (started || typeof window === "undefined") return;
  started = true;
  if ((await detectMode()) === "desktop") {
    import("@wailsio/runtime").then((runtime) => {
      runtime.Events.On("ui:event", (data: unknown) => {
        dispatch(data as UIEvent);
      });
    });
  } else {
    const es = new EventSource("/api/events");
    es.onmessage = (m) => {
      try {
        dispatch(JSON.parse(m.data) as UIEvent);
      } catch {
        // 忽略无法解析的事件
      }
    };
    es.onerror = () => {
      // 断线由 EventSource 自动重连
    };
  }
}
