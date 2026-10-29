# 03: 总览页与执行列表中文化(Dashboard + Executions)

**What to build:** 总览页与执行记录页全部为中文:页标题 `总览`/`执行记录`;统计卡(工作流/运行中/等待人工/已完成/失败/已取消);表头(工作流/执行 ID/任务/状态/总执行/成功率);`N nodes · vN` → `N 个节点 · vN`;工作流启用徽章 `已启用/已禁用`;执行列表状态过滤按钮显示中文(传给后端的枚举值不变);空态文案里的 "Workflows 页面" → "工作流页面"。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [x] Dashboard 页标题、统计卡、表头、徽章、`N 个节点` 均为中文;ID/v1 保留
- [x] Executions 页标题、表头为中文;过滤按钮显示中文、点击过滤逻辑仍按枚举值比较
- [x] 状态列徽章(经 StateBadge)已由 02 中文化,此处不改
- [x] `pages/Dashboard.test.tsx`(Dashboard→总览、Cancelled→已取消)、`pages/Executions.test.tsx` 断言同步
- [x] `npx vitest run pages/Dashboard.test.tsx pages/Executions.test.tsx` 绿
