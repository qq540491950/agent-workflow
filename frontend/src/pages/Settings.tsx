import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { reportError } from "@/lib/errors";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

interface SettingsInfo {
  data_dir: string;
  git_working_dir: string;
  log_level: string;
  allow_script_nodes: boolean;
}

// 后端可接受的日志级别(app/application validateLogLevel)。
// 非法值后端现在直接 400 拒绝(此前静默回落 info,界面显示与实际生效不一致),
// 前端先挡一道:level 也可能来自后端存量配置,不一定只由下拉框产生。
export const LOG_LEVELS = ["debug", "info", "warn", "error"];

export default function Settings() {
  const [info, setInfo] = useState<SettingsInfo | null>(null);
  const [ver, setVer] = useState<Record<string, string>>({});
  const [gitDir, setGitDir] = useState("");
  const [level, setLevel] = useState("info");
  const [allowScript, setAllowScript] = useState(false);
  const [saving, setSaving] = useState(false);
  const [backupBusy, setBackupBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const i = await api.getSettings();
      setInfo(i);
      setGitDir(i.git_working_dir);
      setLevel(i.log_level || "info");
      setAllowScript(!!i.allow_script_nodes);
    } catch (e) {
      toast.error((e as Error).message);
    }
  }, []);

  useEffect(() => {
    refresh();
    // 此前是 `.catch(() => {})`:版本信息加载失败时静默不显示,用户以为
    // "没有版本信息"而不是"加载失败"。与上面 refresh 的错误口径统一。
    api
      .version()
      .then((v) => setVer(v ?? {}))
      .catch((e: unknown) => reportError(e, "加载版本信息"));
  }, [refresh]);

  const save = async () => {
    // 前端先挡一道非法日志级别:后端已改为 400 拒绝,但 level 也可能来自
    // 存量配置(不是下拉框选出来的),直接发过去只会拿到一句后端原文。
    if (!LOG_LEVELS.includes(level)) {
      toast.error(`日志级别非法: ${level}(可选 ${LOG_LEVELS.join("|")})`);
      return;
    }
    setSaving(true);
    try {
      const i = await api.updateSettings({
        git_working_dir: gitDir,
        log_level: level,
        allow_script_nodes: allowScript,
      });
      setInfo(i);
      setAllowScript(!!i.allow_script_nodes);
      toast.success("设置已保存(日志级别与 script 开关即时生效)");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const exportBackup = async () => {
    setBackupBusy(true);
    try {
      // 桌面模式经 IPC 拿 JSON 文本;浏览器模式走同源下载链接
      // (桌面 AssetHandler 也把 /api 挂到同源,故链接在两种模式都可用)
      const text = await api.exportBackupText();
      const blob = new Blob([text], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `agent-workflow-backup-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("备份已导出(含明文敏感环境变量,请妥善保管)");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBackupBusy(false);
    }
  };

  const restoreBackup = async (file: File) => {
    if (
      !window.confirm(
        `从 ${file.name} 恢复?\n\n已存在同 ID 的工作流会被跳过(不覆盖你的修改);` +
          `不合法的定义会被拒绝并计数。`,
      )
    )
      return;
    setBackupBusy(true);
    try {
      const raw = JSON.parse(await file.text());
      const r = await api.restoreBackup(raw);
      const parts = [`恢复工作流 ${r.workflows_restored} 个`];
      if (r.workflows_skipped > 0) parts.push(`跳过已存在 ${r.workflows_skipped} 个`);
      if (r.workflows_invalid > 0) parts.push(`拒绝非法 ${r.workflows_invalid} 个`);
      parts.push(`恢复 Agent 配置 ${r.agent_configs_restored} 个`);
      // agent_configs_invalid 的语义已扩大:结构非法 **或** 本机不存在该 Agent
      // (配置落库也不会生效)。文案必须说清,否则用户以为凭据已恢复。
      if (r.agent_configs_invalid > 0) {
        parts.push(`拒绝 ${r.agent_configs_invalid} 个(结构非法或本机无此 Agent)`);
      }
      const unknown = r.agent_configs_unknown_ids ?? [];
      if (unknown.length > 0) parts.push(`本机无此 Agent:${unknown.join(", ")}`);
      toast.success(parts.join(","));
      if (r.workflows_invalid > 0 || r.agent_configs_invalid > 0) {
        toast.warning(
          unknown.length > 0
            ? "有内容未恢复,详见恢复结果计数(本机不存在的 Agent 配置未生效)"
            : "有内容未恢复,详见恢复结果计数",
        );
      }
      refresh();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBackupBusy(false);
    }
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Settings</h1>
        <p className="text-sm text-muted-foreground">应用设置</p>
      </div>

      <div className="max-w-2xl space-y-4">
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-base">常规</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">数据目录(SQLite 所在位置,只读)</Label>
              <Input value={info?.data_dir ?? "…"} readOnly className="font-mono text-xs" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">Git 工作目录</Label>
              <div className="flex gap-2">
                <Input
                  className="font-mono text-xs"
                  value={gitDir}
                  onChange={(e) => setGitDir(e.target.value)}
                />
              </div>
              <p className="text-[11px] text-muted-foreground">
                Git 节点与 Git 面板在此目录执行 status/diff/commit 等操作。
              </p>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs text-muted-foreground">日志级别(即时生效)</Label>
              <Select value={level} onValueChange={setLevel}>
                <SelectTrigger className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {["debug", "info", "warn", "error"].map((l) => (
                    <SelectItem key={l} value={l}>{l}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={save} disabled={saving}>
              保存设置
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-base">安全</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-start justify-between gap-4">
              <div className="space-y-1">
                <Label className="text-xs">允许执行 script 节点</Label>
                <p className="text-[11px] text-muted-foreground">
                  script 节点以 <code className="font-mono">/bin/sh -c</code>{" "}
                  执行工作流里写明的任意本地命令,而工作流可以通过 YAML 导入获得。
                  默认关闭:关闭时 script 节点执行会失败并提示开启本开关。
                </p>
              </div>
              <Switch
                checked={allowScript}
                onCheckedChange={(v) => {
                  // 开启是提权动作(任意本地命令执行):二次确认,拒绝则保持原值。
                  if (v && !allowScript) {
                    const ok = window.confirm(
                      "允许执行 script 节点?\n\n" +
                        "script 节点以 /bin/sh -c 执行工作流里写明的任意本地命令," +
                        "而工作流可以通过 YAML 导入获得 —— 导入不可信的工作流等于放开任意命令执行。\n\n" +
                        "确认只在完全可信的工作流下开启。",
                    );
                    if (!ok) return;
                  }
                  setAllowScript(v);
                }}
              />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-base">备份</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">
              备份含全部工作流与 Agent 配置。<b className="text-foreground">
                环境变量以明文导出</b>
              ,请妥善保管。
            </p>
            <div className="flex gap-2">
              <Button variant="outline" onClick={exportBackup} disabled={backupBusy}>
                导出备份
              </Button>
              <label
                className={cn(
                  "inline-flex cursor-pointer items-center rounded-md border px-4 py-2 text-sm",
                  backupBusy
                    ? "pointer-events-none opacity-50"
                    : "hover:bg-accent",
                )}
              >
                从文件恢复
                <input
                  type="file"
                  accept="application/json,.json"
                  className="hidden"
                  disabled={backupBusy}
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    e.target.value = ""; // 允许重复选同一个文件
                    if (f) restoreBackup(f);
                  }}
                />
              </label>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-base">关于</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1 font-mono text-xs text-muted-foreground">
            <div>
              app: {ver.app ?? "Agent Workflow Orchestrator"}
              {ver.version ? ` v${ver.version}` : ""}
            </div>
            {ver.adk && <div>adk: {ver.adk}</div>}
            {ver.wails && <div>wails: {ver.wails}</div>}
            {ver.commit && <div>commit: {ver.commit}</div>}
          </CardContent>
        </Card>

        <Card className="mt-4">
          <CardHeader className="py-3">
            <CardTitle className="text-base">安全说明</CardTitle>
          </CardHeader>
          <CardContent className="text-xs text-muted-foreground">
            <ul className="list-disc space-y-1 pl-4">
              <li>Agent 的模型/API 端点/环境变量保存在本应用自己的 SQLite 中,不写入 ~/.claude 等本地配置。</li>
              <li>敏感环境变量通过 API 读取时以 *** 掩码,不进入日志。</li>
              <li>权限策略(fs:write / git:commit / git:push)由 Permission Manager 强制执行。</li>
              <li>script 节点默认不放行(见上方「安全」),避免导入的工作流直接执行本地命令。</li>
            </ul>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
