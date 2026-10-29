# 08: 辅助页面中文化(GitPanel + EventsAudit + Settings)

**What to build:** 辅助页面全部为中文:Git 面板 Tab `差异`/`提交历史 (n)`、表头 `哈希/作者/时间/说明`、占位符 `提交信息`、空态 `(无变更)`、错误 scope(状态/差异/历史查询失败);事件审计页标题 `事件`、分类按钮(工作流/节点/评审/人工/技能;Agent/Git 保留);设置页标题 `设置`、安全说明权限口径对齐 Agents 表头。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [ ] GitPanel Tab/表头/占位符/空态中文;Hash 等数据值保留
- [ ] GitPanel 查询失败的 scope 文案中文(状态/差异/历史)
- [ ] EventsAudit 页标题与分类按钮中文;Agent/Git 分类保留
- [ ] Settings 页标题中文;安全说明权限口径与 07 一致
- [ ] `npx vitest run pages/GitPanel.test.tsx pages/EventsAudit.test.tsx pages/Settings.test.tsx` 绿
