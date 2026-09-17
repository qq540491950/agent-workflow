# AGENTS.md — AI 协作开发指南

面向在本仓库工作的 AI 编码代理与人类开发者。先读 `需求.md`(架构宪法)
与 `docs/TESTING.md`(回归史),再动代码。

## 项目一句话

可配置、可视化、可扩展的多 Agent 工作流编排**桌面应用**:
Go + ADK Go v1.7.0(Workflow Runtime)+ Wails 3 beta.20(桌面壳)+
SQLite;前端 React + TS + Vite + React Flow + shadcn/ui。

## 常用命令

```bash
# 后端全量回归(vet + race 测试)
go vet ./workflow/... ./agent/... ./skill/... ./persistence/... ./event/... \
  ./template/... ./permission/... ./git/... ./contextx/... ./logx/... ./api/... ./app/... .
go test -race ./...

# 前端(在 frontend/ 下)
npm run build        # tsc + vite
npx vitest run       # 单测(含 jsdom 组件测试)

# 端到端冒烟(起真实 server 进程)
./scripts/smoke.sh       # REST+SSE+HITL+导出+负路径语义,8 项
./scripts/smoke_sse.sh   # SSE 实时流,3 项

# 桌面打包(wails3 CLI 需在 ~/go/bin)
export PATH=$PATH:~/go/bin && wails3 task package   # → bin/agent-workflow.app

# 交叉编译检查(agent/workflow 等纯 Go 包;GUI 主程序 Linux 需 CGO 环境)
GOOS=windows go build ./agent/... ./workflow/...
```

## 目录速览

- `workflow/{dsl,validator,compiler,runtime,model}` — DSL→校验→编译(ADK Agent 树)→运行时
- `agent/{claude,pi,mock}` + `skill/` — 可注册的 Agent/Skill 适配器
- `workflow/runtime/engine.go` — 执行生命周期/状态机/HITL/CAS 守卫
- `persistence/` — SQLite(WAL + 单连接);`event/` — 进程内总线
- `api/` — REST+SSE(服务器模式);桌面模式经 `AssetHandler` 挂载到 `/api`
- `app/application/` — 应用服务层(双模式统一后端);`frontend/` — React SPA

## 硬性约定(违反即 Bug,改动需三思)

1. **Agent 互不调用**,全部调度经 Workflow Runtime;结构化 decision 驱动路由,
   禁止字符串猜测。
2. **JSON 往返类型**:任何经 SQLite `state_json` 持久化再读回的 int 都是
   float64。读取用 `compiler.IntOfOk`(executor 的 attempt、loop 计数、
   mock 模板 iteration 均已处理)。
3. **进程组击杀**:所有 `exec.CommandContext` 必须
   `coreagent.ConfigureProcess(cmd)` + `cmd.WaitDelay`(claude/pi/git/script
   已覆盖);并区分 `exec.ErrWaitDelay`(进程已成功退出,输出完整,不算失败)。
4. **执行状态转换用 CAS**:`persistence.CasExecutionState` 守卫并发
   Resume/Retry/Cancel(双击批准只允许一路成功);直接 read-check-save 会被
   -race 之外的逻辑竞争击穿。
5. **掩码语义**:Agent 环境变量 GET 一律 `***`;PUT 时 `***`=保留原值、
   空串=删除、无原值的 `***` 丢弃(`AgentSvc.UpdateConfig`)。
6. **DELETE 幂等**:删除不存在资源返回 200(是有意设计,见
   `api/server_test.go`);其余资源缺失 404+`code:"NOT_FOUND"`。
7. **前端列表防御**:所有列表接口先过 `asArray()` 再 `.map`;
   实时事件刷新一律 200ms 防抖(ExecutionMonitor/Layout/EventsAudit 模式)。
8. **敏感信息**:API_KEY/TOKEN 等不进日志(logx.Mask)、不进工作流 DSL、
   API 返回掩码;备份导出含明文是唯一例外(文档已注明)。
9. **生产总线不保留历史**(`event.Bus.KeepHistory` 仅测试用),回放走
   SQLite 持久化事件。
10. **双模式**:改动 REST API 时必须同步检查 `frontend/src/lib/api.ts`
    双分支与 vitest 契约测试;桌面模式 `/api` 经 AssetHandler 挂载,补前缀
    逻辑见 `api.AssetHandler.ServeHTTP`。

## 测试基建位置

- Go:各包 `*_test.go`;运行时集成测试在 `workflow/runtime/engine_test.go`
  (fixture 模式:`newFixture`/`loadWf`/`waitForState`)与
  `executor_node_test.go`(`newMinimalEngine`)。
- 前端:`src/lib/api.test.ts`(api 层,mock @wailsio/runtime 与 @bindings)、
  `src/components/state-badge.test.tsx`(jsdom 组件)。
- 冒烟:`scripts/smoke.sh`、`scripts/smoke_sse.sh`(自起进程、自清理)。

## 提交规范

每次功能/修复独立提交;回归测试与修复同 commit;commit message 用中文、
首行 ≤72 字、正文说明"行为变化 + 验证方式"。格式:`gofmt -w` 后再提交;
`gofmt -l` 应为空(build/ 目录的 Wails 脚手架除外)。

## 已知边界与后续方向

见 `docs/ROADMAP.md`(ADK v2 迁移、session 持久化、托盘等评估与暂缓理由)。
