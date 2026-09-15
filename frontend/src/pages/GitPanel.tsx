import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { RefreshCw, GitCommitHorizontal } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

interface GitStatus {
  branch: string;
  modified: string[];
  staged: string[];
  untracked: string[];
}

interface LogEntry {
  hash: string;
  author: string;
  date: string;
  subject: string;
}

export default function GitPanel() {
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [diff, setDiff] = useState("");
  const [log, setLog] = useState<LogEntry[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [st, d, lg] = await Promise.all([
        api.gitStatus(),
        api.gitDiff(false),
        api.gitLog(20),
      ]);
      setStatus(st);
      setDiff(d);
      setLog(lg ?? []);
    } catch (e) {
      toast.error((e as Error).message);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const commit = async () => {
    if (!message.trim()) return;
    setBusy(true);
    try {
      const out = await api.gitCommit(message.trim());
      toast.success("已提交: " + out.split("\n")[0]);
      setMessage("");
      refresh();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const fileChips = (files: string[], tone: string) =>
    (files ?? []).map((f) => (
      <Badge key={f} variant="secondary" className={`mr-1 mb-1 font-mono text-[11px] ${tone}`}>
        {f}
      </Badge>
    ));

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Git</h1>
          <p className="text-sm text-muted-foreground">
            工作目录 Git 面板 — 操作受权限策略约束
            {status?.branch && (
              <Badge variant="outline" className="ml-2 font-mono text-[11px]">
                {status.branch}
              </Badge>
            )}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={refresh}>
          <RefreshCw className="mr-1 h-4 w-4" /> 刷新
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-5">
        <Card className="lg:col-span-2">
          <CardHeader className="py-3">
            <CardTitle className="text-base">状态</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            {!status && <p className="text-muted-foreground">加载中…</p>}
            {status && (
              <>
                <div>
                  <div className="mb-1 text-xs text-muted-foreground">已暂存</div>
                  {status.staged.length ? fileChips(status.staged, "text-green-400") : <span className="text-xs text-muted-foreground">无</span>}
                </div>
                <div>
                  <div className="mb-1 text-xs text-muted-foreground">已修改</div>
                  {status.modified.length ? fileChips(status.modified, "text-amber-400") : <span className="text-xs text-muted-foreground">无</span>}
                </div>
                <div>
                  <div className="mb-1 text-xs text-muted-foreground">未跟踪</div>
                  {status.untracked.length ? fileChips(status.untracked, "text-muted-foreground") : <span className="text-xs text-muted-foreground">无</span>}
                </div>
                <div className="space-y-2 border-t pt-3">
                  <Input
                    placeholder="commit message"
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && commit()}
                  />
                  <Button size="sm" className="w-full" onClick={commit} disabled={busy || !message.trim()}>
                    <GitCommitHorizontal className="mr-1 h-4 w-4" /> 提交全部改动
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>

        <Card className="lg:col-span-3">
          <CardContent className="pt-4">
            <Tabs defaultValue="diff">
              <TabsList>
                <TabsTrigger value="diff">Diff</TabsTrigger>
                <TabsTrigger value="log">Log ({log.length})</TabsTrigger>
              </TabsList>
              <TabsContent value="diff" className="mt-2">
                <pre className="max-h-[440px] overflow-auto rounded-md bg-muted/40 p-3 font-mono text-[11px]">
                  {diff || "(no diff)"}
                </pre>
              </TabsContent>
              <TabsContent value="log" className="mt-2">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Hash</TableHead>
                      <TableHead>Author</TableHead>
                      <TableHead>Date</TableHead>
                      <TableHead>Subject</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {log.map((e) => (
                      <TableRow key={e.hash}>
                        <TableCell className="font-mono text-xs">{e.hash}</TableCell>
                        <TableCell className="text-xs">{e.author}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">{e.date}</TableCell>
                        <TableCell className="text-sm">{e.subject}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TabsContent>
            </Tabs>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
