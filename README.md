# Agent Workflow Orchestrator

可配置、可视化、可扩展的多 Agent 工作流编排桌面应用。

- **运行时**:ADK Go v1.7.0(Workflow Runtime)+ Wails 3(桌面壳)+ SQLite(持久化)
- **前端**:React + TypeScript + Vite + shadcn/ui + React Flow + Tailwind CSS 4
- **文档**:[网页版使用说明(带截图)](docs/index.html) · [测试报告(8 轮,含视觉测试)](docs/TESTING.md)

## 快速开始

```bash
# 前端
cd frontend && npm install && npm run build && cd ..

# 桌面模式
go build -o bin/agent-workflow-desktop . && ./bin/agent-workflow-desktop

# 服务器模式(浏览器访问,REST+SSE)
go build -tags server -o bin/agent-workflow-server . && ./bin/agent-workflow-server --server --addr 127.0.0.1:8080
```

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
