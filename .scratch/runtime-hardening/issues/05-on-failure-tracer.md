# 05: 失败分支 tracer bullet

**What to build:** 工作流作者可以在连线上声明 `on_failure: true`。节点重试耗尽后不再直接终止，而是沿失败边执行通知、回滚或修复节点；失败节点仍然显示 FAILED，执行可以在接管失败后完成，但必须在事件、详情和导出里明确标注“经失败分支完成”。

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [x] DSL 与模型支持边的 `on_failure` 字段；既有工作流不写该字段时行为逐位不变
- [x] 节点重试耗尽后沿失败边执行分支；失败节点状态保持 FAILED，不被改写成 SUCCESS
- [x] 失败分支可执行 agent/script/git/human 节点，并沿用既有权限、脱敏与事件语义
- [x] 执行可以 `COMPLETED`，但 `workflow.completed` 事件、执行详情与导出都包含 `via_failure_branch`
- [x] 没有失败边时，失败仍按现有“终止执行”路径处理
- [x] 集成测试覆盖“成功不走进失败分支”与“失败走进失败分支”两条路径；事件顺序稳定
- [x] 新增错误码/字段的前端契约与文档同步
