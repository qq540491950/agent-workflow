# 后续方向 — 基于权威资料对照审计的路线图

> 生成于 2026-09-18 凌晨迭代(轮次 24)。来源:ADK Go v1.7.0 源码 + adk.dev 官方文档、
> Wails v3 beta.20 源码 + v3.wails.io 官方文档的逐条对照审计。
> 已修复项见 `docs/TESTING.md` 轮次 24(#9-#16)。

## 一、ADK 侧(按相关性排序)

### 1. 会话持久化:`session/database`(GORM + SQLite)
- **现状**:每个 execute 新建 `session.InMemoryService()`(engine.go),ADK 会话事件
  历史不跨进程;Resume 靠自研 StateData 快照回放重建状态。
- **方向**:v1.7.0 自带 `database.NewSessionService(dialector)` + AutoMigrate,
  sessionID=exec.ID 的会话可天然跨重启存活;Resume 可重入原会话,
  StateData 手工回放与 syncNodeStates 兜底可大部分删除。
- **代价**:需引入 gorm + 纯 Go sqlite dialector(glebarez)依赖;引擎执行/恢复
  路径深度改动。
- **决策**:暂缓。现有恢复链路已经过跨进程崩溃恢复 E2E 验证(轮次 17/21),
  收益(会话历史查询)不抵回归风险;待 ADK v2 迁移时一并考虑。

### 2. ADK v2 升级(需求文档假设的 "ADK 2.x" 现已真实存在)
- v2 模块(`google.golang.org/adk/v2`,至 v2.4.0)提供 Graph Workflows
  (`workflow.NewFunctionNode/NewJoinNode/Chain/StringRoute`)与 HITL
  `RequestInput` 节点 —— 与本项目"人工节点"语义一一对应。
- 迁移后 lower.go 的 routeAgent/loopGuard/自研 WAIT_USER 状态机可收缩为
  图节点 + RequestInput;项目仅需保留 Execution 级状态机与持久化。
- **决策**:单列大版本任务,不与日常迭代混合;迁移前保持 v1.7.0(用法已审计正确)。

### 3. 其他可引入能力(低优先级)
- `artifact.Service`:版本化工件存储;自建 Artifact 表可平移到该接口(无现成
  SQLite 实现,需自实现小接口)。
- `telemetry`:一行接入 OpenTelemetry(每次 Agent.Run 自带 invoke_agent span),
  调试/审计增值。
- `platform.WithTimeProvider/WithUUIDProvider`:产出确定性、可重放事件
  (崩溃恢复审计回放对口)。
- Session State 前缀作用域(`temp:` 键持久化时被剥离,适合 `_error` 等一次性数据)。

## 二、Wails 侧

### 1. 服务器模式统一到官方 `-tags server`(评估后暂缓)
- beta.20 内置服务器模式(bindings 走 HTTP transport + WebSocket 事件)理论上
  可替换自研 `api/server.go`(REST+SSE),消除双模式前端胶水。
- **暂缓原因**:自研 REST API 是已文档化的对外契约(docs/api.md、smoke.sh、
  外部脚本);内置模式的端点形态不同,替换是破坏性变更。折中已落地:
  桌面模式经 `AssetHandler` 挂载 /api(轮次 24 #13),三种形态同源。
- 若未来需要容器部署(WAILS_SERVER_PORT 等),再评估迁移。

### 2. 系统托盘 + 关窗最小化(产品决策待定)
- 现状 `ApplicationShouldTerminateAfterLastWindowClosed: true`,关窗即退出,
  与"长时执行"场景冲突(已有原生通知兜底提醒)。
- 方向:`SystemTray.SetMenu`(运行中列表/暂停/退出)+ 关窗最小化到托盘 +
  `ShouldQuit` 确认。改动退出语义需产品确认,暂缓。

### 3. 其他
- 原生对话框:备份导出/恢复、Git 目录选择(`app.Dialog.SaveFile/OpenFile`)。
- `SingleInstance.AdditionalData` + `FileAssociations: [".yaml"]`:双击导入工作流。
- `Options.Logger` 接入 logx(统一 Wails 内部日志管道)。

## 三、已确认的语义决定(勿当 Bug "修复")

- **DELETE 幂等**:删除不存在的资源返回 200(api/server_test.go 有断言)。
- **全局状态键(decision/status/output)在并行分支内可能互相覆盖**:
  并行体决策请用 merge 节点读取节点级键(StatusKey/ResultKey);
  全局键的 ADK 会话合并由分支隔离机制处理,Go 层只保证无数据竞争。
- **非 JSON 的 Claude 输出不猜测 decision**(需求 §36 字符串匹配禁令)。
