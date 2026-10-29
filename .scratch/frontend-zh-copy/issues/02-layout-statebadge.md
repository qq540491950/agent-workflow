# 02: 全局壳与状态徽章中文化(Layout + StateBadge)

**What to build:** 用户打开应用看到的第一层界面全部为中文:侧栏导航(总览/工作流/执行记录/事件/Agent/技能/设置,模型/端点等专有名词保留)、底部 `模式:` 显示(desktop/server 映射中文,未知值回退原文)、产品副标题等;所有执行/节点状态徽章经 `stateLabel()` 显示中文(运行中/等待人工/已完成/失败/已取消/成功/待执行/已跳过/等待中),未知状态回退原文。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [ ] 侧栏导航项为中文;Git 保留;`Orchestrator` 与 `ADK Go runtime · Wails 3` 等产品/技术名保留
- [ ] `mode:` 行显示中文(桌面/服务器),未知模式回退原文
- [ ] StateBadge 所有已知状态显示中文;未知状态(如 PAUSED、SOME_FUTURE_STATE)回退原文
- [ ] `components/layout.test.tsx`、`components/state-badge.test.tsx` 断言同步更新
- [ ] `npx vitest run components` 绿
