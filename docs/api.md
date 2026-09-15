# HTTP API 参考(server 模式)

> 桌面模式通过 Wails IPC 暴露同一套服务;以下端点在 `--server` 模式下可用。
> 启动:`./bin/agent-workflow-server --server --addr 127.0.0.1:8080`

## 系统

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查与版本信息 |

## Workflows

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/workflows` | 列出全部工作流 |
| GET | `/api/workflows/{id}` | 读取工作流定义 |
| POST | `/api/workflows` | 创建/更新(保存前强制校验;更新版本 +1) |
| DELETE | `/api/workflows/{id}` | 删除工作流(执行记录保留) |
| POST | `/api/workflows/{id}/duplicate` | 复制 |
| POST | `/api/workflows/{id}/enable?value=true\|false` | 启用/禁用 |
| POST | `/api/workflows/validate` | 校验(返回结构化 errors/warnings) |
| GET | `/api/workflows/{id}/export` | 导出 YAML |
| GET | `/api/workflows/{id}/versions` | 版本号列表(降序) |
| GET | `/api/workflows/{id}/versions/{version}` | 指定版本的 DSL 快照 |
| POST | `/api/workflows/import` | 从 YAML 导入(body 为 YAML 文本;ID 冲突自动换新 ID) |

## Executions

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/workflows/{id}/run` | 启动执行 `{task, variables}`(异步) |
| GET | `/api/executions?workflow_id=&limit=` | 执行列表(最新在前) |
| GET | `/api/executions/{id}` | 执行详情(含 state_data 快照) |
| GET | `/api/executions/{id}/nodes` | 节点执行明细(attempt/duration/output) |
| GET | `/api/executions/{id}/events` | 事件流 |
| GET | `/api/executions/{id}/artifacts` | 制品(如 git-diff) |
| GET | `/api/executions/{id}/export` | 导出完整记录 JSON(附件下载) |
| POST | `/api/executions/{id}/input` | 人工输入并恢复 `{response, instruction}` |
| POST | `/api/executions/{id}/retry` | 失败执行恢复 `{skip: bool}` |
| POST | `/api/executions/{id}/cancel` | 取消执行 |
| DELETE | `/api/executions/{id}` | 删除记录(级联) |

## Agents / Skills

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/agents` | Agent 注册表(含权限策略与已配置模型) |
| POST | `/api/agents/{id}/test` | 可用性测试(CLI 探测) |
| GET | `/api/agents/{id}/config` | 读取运行配置(env 值以 `***` 掩码) |
| PUT | `/api/agents/{id}/config` | 保存配置并热重建适配器(env `***`=保留,空=删除) |
| POST | `/api/agents/{id}/permission` | 更新权限策略 |
| GET | `/api/skills` | Skill 注册表(含启用状态) |
| POST | `/api/skills/{id}/enable` | 启用/禁用 `{enabled}` |
| POST | `/api/skills/{id}/test` | 测试执行 |

## Git

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/git/status` | 结构化状态(分支/暂存/修改/未跟踪) |
| GET | `/api/git/diff?staged=` | 未提交改动 |
| GET | `/api/git/log?limit=` | 提交历史 |
| POST | `/api/git/commit` | 提交全部改动 `{message}` |

## Settings / Events / Stats

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/settings` | 数据目录 / Git 工作目录 / 日志级别 |
| POST | `/api/settings` | 更新设置(日志级别即时生效) |
| GET | `/api/backup` | 导出备份(工作流 + Agent 配置,含明文敏感环境变量) |
| POST | `/api/backup/restore` | 恢复备份(已存在工作流跳过) |
| GET | `/api/events` | **SSE** 实时事件流(UIEvent) |
| GET | `/api/events/all?type=&limit=` | 跨执行审计事件(支持类型前缀过滤) |
| GET | `/api/stats` | 每工作流执行统计 |

## 错误格式

非 2xx 返回 `{"error": "..."}`。校验/权限/状态机错误为结构化类型,
例如:`PermissionError(PERMISSION_DENIED): agent "mock-pi" 无权执行 fs:write`。

## 实时事件类型(UIEvent.type)

`workflow.started / paused / resumed / waiting_user / completed / failed / cancelled`
`node.started / completed / failed / waiting`
`agent.started / output / completed / retry`
`review.approved / review.rejected`
`human.input_required / human.input_received`
`skill.executed / git.executed / merge.completed`
