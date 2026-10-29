import { useEffect, useState } from "react";
import { usePageTitle } from "@/lib/use-page-title";
import { toast } from "sonner";
import { Settings2 } from "lucide-react";
import { api, asArray, formatExtraArgs, parseExtraArgs } from "@/lib/api";
import { reportError } from "@/lib/errors";
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
  usePageTitle("Agent 注册表");
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [configTarget, setConfigTarget] = useState<AgentInfo | null>(null);
  const [cfg, setCfg] = useState<AgentConfig | null>(null);
  // cfgError:配置读取失败的原因。读取失败时**不给**可保存的配置 ——
  // 空配置回传等于"清空全部环境变量"(迭代 20 / P1-2)。
  const [cfgError, setCfgError] = useState<string | null>(null);
  const [savingCfg, setSavingCfg] = useState(false);
  const [testingAgent, setTestingAgent] = useState<string | null>(null);

  const refresh = () => api.listAgents().then((x) => setAgents(asArray<AgentInfo>(x))).catch((e) => reportError(e, "加载 Agent 列表"));
  useEffect(() => {
    refresh();
  }, []);

  // toggle 权限:以最新 state 为基准 + 失败回滚。
  // 此前用渲染闭包里的 a.permissions 算新值并直接整表提交:快速连点
  // 同一个 Switch 时两次都基于同一份陈旧快照,第二次写入的值与用户
  // 意图相反(开了又关却停在开),且失败路径没有回滚,UI 与后端分裂。
  const [pendingAgent, setPendingAgent] = useState<string | null>(null);
  const toggle = async (a: AgentInfo, key: keyof AgentInfo["permissions"]) => {
    if (pendingAgent === a.id) return; // 同一行 in-flight 加锁
    const current = agents.find((x) => x.id === a.id);
    const base = current?.permissions ?? a.permissions;
    const p = { ...base, [key]: !base[key] };
    if (key === "git_commit" && !p.git_commit) p.git_push = false;
    const prev = current ?? a;
    // 乐观更新,失败回滚
    setAgents((list) => list.map((x) => (x.id === a.id ? { ...x, permissions: p } : x)));
    setPendingAgent(a.id);
    try {
      await api.setAgentPermission(a.id, p);
    } catch (e) {
      setAgents((list) => list.map((x) => (x.id === a.id ? prev : x)));
      reportError(e, "更新 Agent 权限");
      return;
    } finally {
      setPendingAgent(null);
    }
    refresh();
  };

  const openConfig = async (a: AgentInfo) => {
    setConfigTarget(a);
    // 先清掉上一份配置:GET 还没回来时不能显示(或保存)别的 Agent 的配置。
    setCfg(null);
    setCfgError(null);
    try {
      const c = await api.getAgentConfig(a.id);
      setCfg({ ...c, env: c.env ?? {} });
    } catch (e) {
      // GET 失败时**不得**回传一份空配置:后端契约里 env 缺省 = 不修改,
      // 而前端是"整表回传"—— 若这里回传 env:{} ,后端会把它当成
      // "显式清空全部环境变量",库里的 ANTHROPIC_AUTH_TOKEN 等凭据
      // 全被删掉,而用户只看到"配置已保存"(迭代 20 / P1-2,约定 14)。
      //
      // 现在:不给出可保存的配置(保存按钮禁用)+ 弹窗里显示原因。
      const msg = (e as Error).message;
      setCfg(null);
      setCfgError(msg);
      reportError(e, "读取 Agent 配置");
    }
  };

  const saveConfig = async () => {
    if (!configTarget || !cfg) return;
    // "+ 添加环境变量" 插入的行键名为空:直接提交会在后端存下一条空名环境变量
    // (第二个空名行还会覆盖第一个)。保存前过滤空名,并如实告知丢了几个。
    const env = cfg.env ?? {};
    const blank = Object.entries(env).filter(([k]) => !k.trim()).length;
    const payload = blank
      ? { ...cfg, env: Object.fromEntries(Object.entries(env).filter(([k]) => k.trim())) }
      : cfg;
    setSavingCfg(true);
    try {
      await api.updateAgentConfig(configTarget.id, payload);
      toast.success(`${configTarget.name} 配置已保存(仅作用于本应用调用,不影响本地配置)`);
      if (blank > 0) toast.warning(`已忽略 ${blank} 个未填写名称的环境变量`);
      setConfigTarget(null);
    } catch (e) {
      reportError(e, "保存 Agent 配置");
    } finally {
      setSavingCfg(false);
    }
  };

  // resetConfig 恢复适配器内置默认:配错模型/端点/环境变量后唯一的逃生口。
  // 后端能力此前就绪(REST 端点 + Wails 绑定),但前端两个分支都没封装,
  // UI 也没有入口 —— 属于"能力空洞"而非崩溃,更隐蔽。
  const resetConfig = async () => {
    if (!configTarget || !cfg) return;
    if (
      !window.confirm(
        `恢复 ${configTarget.name} 的默认配置?\n\n将清空模型、端点、超时、环境变量与额外参数。`,
      )
    )
      return;
    setSavingCfg(true);
    try {
      const c = await api.resetAgentConfig(configTarget.id);
      setCfg({ ...c, env: c.env ?? {} });
      toast.success("已恢复默认配置");
    } catch (e) {
      reportError(e, "恢复默认配置");
    } finally {
      setSavingCfg(false);
    }
  };

  // 已存在未命名(空键)的环境变量行时不能再加一行:同名空键会互相覆盖,
  // 用户填好的一行也可能被后插入的空行顶掉。
  const hasBlankEnvKey = Object.keys(cfg?.env ?? {}).some((k) => !k.trim());

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Agent</h1>
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
              {/* 权限表头用中文;对应的后端键名(filesystem_read 等)
                  保留在各行 Switch 的 aria-label 里,读屏与后端口径一致 */}
              <TableHead>文件读取</TableHead>
              <TableHead>文件写入</TableHead>
              <TableHead>Git 读取</TableHead>
              <TableHead>Git 提交</TableHead>
              <TableHead>Git 推送</TableHead>
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
                    {/* 权限开关的语义在列表头(fs:read 等)与所在行(Agent),
                        读屏只会念"开关" —— 组合出完整名称 */}
                    <Switch
                      checked={a.permissions[k]}
                      aria-label={`${a.id} 的 ${k} 权限`}
                      onCheckedChange={() => toggle(a, k)}
                    />
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
                    disabled={testingAgent === a.id}
                    onClick={async () => {
                      if (testingAgent) return;
                      setTestingAgent(a.id);
                      try {
                        const msg = await api.testAgent(a.id);
                        toast.success(msg);
                      } catch (e) {
                        // 此前没有 catch:失败时是 unhandled rejection,
                        // 用户点了按钮完全没有反馈,看起来像按钮坏了
                        reportError(e, "测试 Agent");
                      } finally {
                        setTestingAgent(null);
                      }
                    }}
                  >
                    {testingAgent === a.id ? "测试中…" : "测试"}
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
          {cfgError && (
            <div className="rounded-md border border-red-500/40 bg-red-500/10 px-2.5 py-2 text-xs text-red-300">
              读取配置失败:{cfgError}
              <br />
              为避免覆盖现有配置(含凭据),<b className="text-foreground">保存已禁用</b>
              。请关闭后重试。
            </div>
          )}
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
              <div className="space-y-1.5">
                <Label className="text-xs text-muted-foreground">超时(秒,0=默认)</Label>
                <Input
                  type="number"
                  value={String(cfg.timeout_seconds ?? 0)}
                  onChange={(e) => setCfg({ ...cfg, timeout_seconds: Number(e.target.value) })}
                />
              </div>
              {/* 此前这里有"默认工作目录(可选)"输入框,但该设置运行期从不读取
                  (后端会拒绝任何新值)—— 用户填了、界面提示"已保存",Agent 仍在
                  全局 Git 目录下读写文件(约定 14)。输入框已删除,这里留一行
                  说明,指向真正生效的地方:节点级 config.working_dir。 */}
              <p className="text-xs text-muted-foreground">
                工作目录在<b className="text-foreground">节点级 config.working_dir</b>
                里设置(留空则用全局 Git 工作目录);Agent 级不提供工作目录设置。
              </p>
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
                <Label className="text-xs text-muted-foreground">
                  附加 CLI 参数(空格分隔;含空格的参数用引号包起来)
                </Label>
                <Input
                  placeholder='--verbose  --header "Authorization: Bearer ghp_xxx"'
                  value={formatExtraArgs(cfg.extra_args ?? [])}
                  onChange={(e) =>
                    setCfg({
                      ...cfg,
                      // shell 式分词:引号内的空格属于同一个参数
                      // (此前按空格硬切,带引号的值会被切碎 —— 迭代 20 / P2-1)。
                      extra_args: parseExtraArgs(e.target.value),
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
                      aria-label={`删除环境变量 ${k}`}
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
                  title={
                    hasBlankEnvKey
                      ? "请先填写当前环境变量名称(同名行会互相覆盖)"
                      : "新增一行环境变量"
                  }
                  disabled={hasBlankEnvKey}
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
            <Button
              variant="outline"
              onClick={resetConfig}
              disabled={savingCfg || !cfg}
            >
              恢复默认
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
