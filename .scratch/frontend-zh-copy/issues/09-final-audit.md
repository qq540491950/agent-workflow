# 09: 收尾:残留英文扫描 + 全量门禁 + i18n 后续决策

**What to build:** 全部页面迁移完成后做整体验收:grep 清点界面剩余英文(应只剩白名单项:产品名、实体名、协议枚举值、技术栈名);跑完整门禁 `./scripts/verify.sh`(Go vet + race + 前端 build + vitest + 双冒烟)确认全绿;在 docs/ROADMAP.md 记录"是否引入 i18n 框架"的评估结论(当前规模:共享 labels 模块已够用)。

**Blocked by:** 02 全局壳与状态徽章中文化(Layout + StateBadge), 03 总览页与执行列表中文化(Dashboard + Executions), 04 工作流管理域中文化(Workflows + WorkflowHistory), 05 执行监控中文化(ExecutionMonitor), 06 设计器中文化(Designer), 07 Agent 与技能注册表中文化(Agents + Skills), 08 辅助页面中文化(GitPanel + EventsAudit + Settings).

**Status:** ready-for-agent

- [ ] 残留英文扫描通过:界面文案仅剩白名单项(逐项可解释)
- [ ] `./scripts/verify.sh` 全绿(vet + race + 前端 + 双冒烟)
- [ ] docs/ROADMAP.md 增补 i18n 评估小节(引入/不引入的理由)
