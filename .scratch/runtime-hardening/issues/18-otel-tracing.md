# 18: OTel 链路追踪

**What to build:** 在配置了 OTLP 端点时，每次执行、每个节点、每次 Agent 调用都有可串联的 trace span；未配置时完全 no-op，不影响桌面模式。排障时能看到时间花在哪个节点、哪个 Agent、哪次重试上，而不是只看事件时间线。

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [x] 未配置 OTLP 端点时追踪为 no-op，启动、执行与事件行为逐位不变
- [x] 配置端点后产生 `workflow.execution`、`workflow.node`、`agent.call` 等 span，父子关系正确
- [x] span 属性包含 execution_id、workflow_id、node_id、节点类型、agent_id、mode、attempt、status
- [x] 节点失败/取消时 span 状态正确；错误消息经过脱敏
- [x] 并行分支各自成 span，父子关系与并发执行一致
- [x] prompt 全文、环境变量、凭据不进入 span 属性
- [x] 使用内存 exporter 的测试覆盖 no-op、父子关系、错误状态与脱敏；OTLP 端点不可达不阻塞启动
