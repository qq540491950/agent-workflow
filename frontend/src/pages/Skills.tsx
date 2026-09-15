import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
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

  const refresh = () => api.listSkills().then((x) => setSkills((x ?? []) as SkillDTO[])).catch((e) => toast.error(e.message));
  useEffect(() => {
    refresh();
  }, []);

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
                  <Switch checked={s.enabled} />
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
                        toast.error((e as Error).message);
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
