import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, asArray } from "@/lib/api";
import { reportError } from "@/lib/errors";
import type { SkillDTO } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export default function Skills() {
  const [skills, setSkills] = useState<SkillDTO[]>([]);
  // 启用开关的 in-flight 锁:此前开关无锁,连点会并发打多次
  // /api/skills/{id}/enable —— 后到的一次覆盖先到的一次,最终状态
  // 取决于请求到达顺序,而界面显示的是刷新回来的值,用户看到的开关
  // 可能与后端实际生效的不一致(与 Agents 页权限开关同类问题,
  // 那里已加同款锁)。
  const [pendingSkill, setPendingSkill] = useState<string | null>(null);

  const refresh = () => api.listSkills().then((x) => setSkills(asArray<SkillDTO>(x))).catch((e) => reportError(e, "加载 Skill 列表"));
  useEffect(() => {
    refresh();
  }, []);

  const toggle = async (s: SkillDTO, v: boolean) => {
    if (pendingSkill === s.id) return; // 同一行 in-flight 加锁
    setPendingSkill(s.id);
    // 乐观更新,失败回滚:开关状态由列表驱动,不等刷新才动会让人以为没点上
    setSkills((list) => list.map((x) => (x.id === s.id ? { ...x, enabled: v } : x)));
    try {
      await api.setSkillEnabled(s.id, v);
    } catch (e) {
      setSkills((list) => list.map((x) => (x.id === s.id ? { ...x, enabled: !v } : x)));
      reportError(e, "启用 Skill");
      return;
    } finally {
      setPendingSkill(null);
    }
    refresh();
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Skills</h1>
        <p className="text-sm text-muted-foreground">
          Skill 注册表 — Workflow Runtime 不感知 Skill 内部实现,仅通过接口调用
        </p>
      </div>
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Skill</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>描述</TableHead>
              <TableHead>Provider</TableHead>
              <TableHead>启用</TableHead>
              <TableHead className="text-right">测试</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {skills.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="font-medium">{s.name}</TableCell>
                <TableCell>
                  <Badge variant="outline" className="font-mono text-xs">{s.id}</Badge>
                </TableCell>
                <TableCell className="max-w-md text-sm text-muted-foreground">
                  {s.description}
                </TableCell>
                <TableCell className="text-sm">builtin</TableCell>
                <TableCell>
                  <Switch
                    checked={s.enabled}
                    disabled={pendingSkill === s.id}
                    onCheckedChange={(v) => toggle(s, v)}
                  />
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={async () => {
                      try {
                        const out = await api.testSkill(s.id);
                        toast.success("输出: " + out.slice(0, 120));
                      } catch (e) {
                        reportError(e, "测试 Skill");
                      }
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
    </div>
  );
}
