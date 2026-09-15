# Agent Workflow Orchestrator

可配置、可视化、可扩展的多 Agent 工作流编排桌面应用。

- **运行时**:ADK Go v1.7.0(Workflow Runtime)+ Wails 3(桌面壳)+ SQLite(持久化)
- **前端**:React + TypeScript + Vite + shadcn/ui + React Flow + Tailwind CSS 4
- **文档**:[网页版使用说明(带截图)](docs/index.html) · [测试报告(22+ 轮,含视觉测试)](docs/TESTING.md) · [HTTP API 参考](docs/api.md)

## 快速开始

```bash
# 前端
cd frontend && npm install && npm run build && cd ..

# 桌面模式
go build -o bin/agent-workflow-desktop . && ./bin/agent-workflow-desktop

# 服务器模式(浏览器访问,REST+SSE)
go build -tags server -o bin/agent-workflow-server . && ./bin/agent-workflow-server --server --addr 127.0.0.1:8080

# 打包桌面 .app
wails3 task package   # 产物 build/bin/agent-workflow.app
```

启动参数:`--server`(HTTP 模式)· `--addr` · `--data`(数据目录)· `--git-dir`(Git 工作目录)· `--log`(级别)

## 功能亮点

- **可视化编排**:React Flow 画布,9 种可注册节点类型,拖拽/连线/自动布局/复制,结构化校验并定位问题节点
- **每 Agent 模型配置**:模型 / API 端点 / 超时 / 环境变量逐 Agent 配置,仅通过调用级参数与环境变量生效,**不影响本地配置文件**;节点可再逐节点覆盖
- **循环与保护**:Review→Fix 循环自动识别,`max_iterations` + 停滞检测,达到上限转人工(wait_user)或失败
- **Human-in-the-loop**:执行暂停等待输入(Approve / Reject / Continue / Instruction),指令注入后续上下文;失败执行可重试或跳过
- **并行编排**:parallel → merge 分支并行,汇合后继续
- **持久化与恢复**:执行实时写 SQLite,重启后可恢复;Execution 绑定工作流版本;数据一键备份/恢复
- **可靠性**:工作流级超时、节点级重试(次数+退避)、循环上限与停滞检测、失败节点重试/跳过
- **可观测**:实时事件流(SSE/IPC)、跨执行审计页、节点输出、会话 State 查看器、Artifacts(git diff)、桌面通知
- **Git 集成**:status/diff/log/branch/checkout/commit 节点 + Git 面板,权限策略约束
- **配置即代码**:YAML 导入导出;Mock Agent 决策脚本可配置,无需真实模型即可演示完整闭环

## 架构原则

- UI → Application API → Workflow DSL(Parser/Validator)→ Compiler → ADK Runtime
- Agent 之间禁止互相调用,全部调度经 Workflow Runtime
- Agent 结构化输出(decision 字段)驱动路由,禁止字符串猜测
- 静态结构用 ADK Graph(Sequential/Parallel),运行时决策用动态代理,循环受 max_iterations 保护
- Claude Code 默认只读(权限策略 + CLI 参数双重强制);权限模型统一管理
- 执行实时持久化 SQLite,支持重启恢复与版本绑定

## 测试

```bash
go vet ./workflow/... ./agent/... ./skill/... ./persistence/... ./event/... ./template/... ./permission/... ./git/... ./contextx/... ./api/... ./app/... .
go test ./workflow/... ./agent/... ./skill/... ./persistence/... ./event/... ./template/... ./permission/... ./git/... ./contextx/...
```
