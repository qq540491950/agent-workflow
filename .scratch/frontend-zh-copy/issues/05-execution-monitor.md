# 05: 执行监控中文化(ExecutionMonitor)

**What to build:** 执行监控页全部为中文:页标题 `执行`;HITL 面板 `工作流等待人工输入`、按钮 `批准/拒绝/继续`、补充指令区 `提交指令`(发送给后端的 response 值 approve/reject/continue/instruction 不变);Tab `制品`/`状态`;节点输出的状态文本经 `stateLabel()` 中文化;decision 徽章(APPROVED/REJECTED 等协议值)保留英文。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [ ] 页标题、HITL 面板文案为中文;provide() 提交的 response 值仍为英文枚举
- [ ] Tab 制品/状态为中文;节点状态文本经 stateLabel 中文化;decision 徽章保留英文
- [ ] 导出记录/取消执行/重试失败节点/跳过并继续等既有中文文案不回退
- [ ] `pages/ExecutionMonitor.test.tsx` 断言同步(状态徽章 5 组 label、Approve→批准、Workflow requires your input→工作流等待人工输入)
- [ ] `npx vitest run pages/ExecutionMonitor.test.tsx` 绿
