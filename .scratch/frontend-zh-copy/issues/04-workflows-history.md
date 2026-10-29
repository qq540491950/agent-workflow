# 04: 工作流管理域中文化(Workflows + WorkflowHistory)

**What to build:** 工作流列表页与工作流历史页全部为中文:列表页标题 `工作流`、新建对话框提示 "创建后进入设计器画布";历史页表头(执行 ID/任务/状态)、副标题 "执行历史 · 每次执行绑定具体的工作流版本"。表单、按钮、空态等其余文案此前已是中文,不重复改动。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [ ] Workflows 页标题为中文;Designer→设计器
- [ ] WorkflowHistory 表头(执行 ID/任务/状态)与副标题为中文;版本徽章 vN 保留
- [ ] YAML 导入/新建/运行对话框文案不回退(保持既有中文)
- [ ] `pages/Workflows.test.tsx`、`pages/WorkflowHistory.test.tsx` 如有受影响断言同步更新
- [ ] `npx vitest run pages/Workflows.test.tsx pages/WorkflowHistory.test.tsx` 绿
