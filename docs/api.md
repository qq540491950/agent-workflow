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
| POST | `/api/workflows/{id}/enable?value=true\|false` | 启用/禁用。**只接受显式的 `true`/`false`**,缺失或其它取值返回 400(fail closed:禁用开关缺参数不能反而打开工作流) |
| POST | `/api/workflows/validate` | 校验(返回结构化 errors/warnings) |
| GET | `/api/workflows/{id}/export` | 导出 YAML |
| GET | `/api/workflows/{id}/versions` | 版本号列表(降序) |
| GET | `/api/workflows/{id}/versions/{version}` | 指定版本的 DSL 快照。版本号只接受十进制数字,越界返回 400 |
| POST | `/api/workflows/import` | 从 YAML 导入(body 为 YAML 文本;ID 冲突自动换新 ID)。body 上限 1MB,超限返回 **413** |

## Executions

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/workflows/{id}/run` | 启动执行 `{task, variables}`(异步) |
| GET | `/api/executions?workflow_id=&limit=` | 执行列表(最新在前)。`limit` **上限 1000**,超出的数字按 1000 收敛(不再因整数溢出变成"不限制"而返回全表) |
| GET | `/api/executions/{id}` | 执行详情(含 state_data 快照) |
| GET | `/api/executions/{id}/nodes` | 节点执行明细(attempt/duration/output)。**output / error / result_json 已在读出口脱敏**(这些字段直接来自 Agent 的 stdout/stderr,常含凭据) |
| GET | `/api/executions/{id}/events` | 事件流 |
| GET | `/api/executions/{id}/artifacts` | 制品(如 git-diff)。**制品保持原样**,不脱敏 |
| GET | `/api/executions/{id}/export` | 导出完整记录 JSON(附件下载)。响应头 `Cache-Control: no-store`;其中的节点明细同样已脱敏 |
| POST | `/api/executions/{id}/input` | 人工输入并恢复 `{response, instruction}` |
| POST | `/api/executions/{id}/retry` | 失败执行恢复 `{skip: bool}`。空 body 合法(等价 `skip=false`);body 非空而解析失败返回 400(此前静默变 `skip=false`,「跳过失败节点」实际会重跑失败 Agent) |
| POST | `/api/executions/{id}/cancel` | 取消执行 |
| DELETE | `/api/executions/{id}` | 删除记录(级联) |

## Agents / Skills

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/agents` | Agent 注册表(含权限策略与已配置模型) |
| POST | `/api/agents/{id}/test` | 可用性测试(CLI 探测) |
| GET | `/api/agents/{id}/config` | 读取运行配置(env 值以 `***` 掩码;**`extra_args` 里的值同样被掩码**) |
| PUT | `/api/agents/{id}/config` | 保存配置并热重建适配器(env `***`=保留,空=删除) |
| POST | `/api/agents/{id}/reset` | 清空运行配置,恢复适配器内置默认 |
| POST | `/api/agents/{id}/permission` | 更新权限策略。整表读-改-写已加锁串行化,并发授权**不会互相覆盖** |
| GET | `/api/skills` | Skill 注册表(含启用状态) |
| POST | `/api/skills/{id}/enable` | 启用/禁用 `{enabled}`(缺该字段返回 400) |
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
| GET | `/api/settings` | 数据目录 / Git 工作目录 / 日志级别 / script 节点放行开关 |
| POST | `/api/settings` | 更新设置(日志级别与 script 节点开关即时生效;`allow_script_nodes` 缺省表示不修改)。**`log_level` 只接受 `debug`/`info`/`warn`/`error`**(大小写不敏感,落库归一为小写),非法值返回 400 —— 不再静默回落 info(界面显示的级别与实际生效的级别会不一致) |
| GET | `/api/backup` | 导出备份(工作流 + Agent 配置,含明文敏感环境变量)。响应头 `Cache-Control: no-store`,避免明文 API Key 留在浏览器/代理的磁盘缓存里 |
| POST | `/api/backup/restore` | 恢复备份。已存在的同 ID 工作流跳过;**校验不通过的定义被拒绝**(不落库)并计数;Agent 配置结构非法同样拒绝;**本机不存在的 Agent 也计入 `agent_configs_invalid` 且不落库**(此前计入"已恢复"是假成功:配置永远不会被应用)。响应含 `workflows_restored` / `workflows_skipped` / `workflows_invalid` / `workflows_invalid_ids` / `agent_configs_restored` / `agent_configs_invalid` / **`agent_configs_unknown_ids`** |
| GET | `/api/events` | **SSE** 实时事件流(UIEvent;连接即达响应头,15s keepalive) |
| GET | `/api/events/all?type=&limit=` | 跨执行审计事件(支持类型前缀过滤) |
| GET | `/api/stats` | 每工作流执行统计 |
| GET | `/api/version` | 应用与关键依赖版本(ADK/Wails 从构建信息读取) |

## 错误格式

非 2xx **一律**返回 JSON,形状唯一:

```json
{"error": "NOT_FOUND(...): workflow \"x\" not found", "code": "NOT_FOUND", "kind": "WorkflowError"}
```

三个字段的约定:

- `error`:人类可读文本(前端直接展示);
- `code`:程序化处理的稳定标识(前端按它分支,不解析文本);
- `kind`:错误分类,取值见 `workflow/model` 的 `ErrorKind`
  (`ValidationError` / `AgentError` / `ProcessError` / `TimeoutError` /
  `PermissionError` / `WorkflowError` / `StateError` / `PersistenceError` /
  `GitError` / `CancelledError`)。

**普通错误(参数校验失败等)此前只有 `error` 字段**,同一套 API 出现两种
错误体形状,前端 `ApiError.code` 时而 `undefined` —— 现在普通错误统一补上
`code: "BAD_REQUEST"` 与 `kind: "ValidationError"`。

路由层的 404 / 405 也走同一形状(`net/http` 的 ServeMux 原本写纯文本):

| 情况 | code | 错误文本 |
| --- | --- | --- |
| 路由不存在 | `NOT_FOUND` | `接口不存在` |
| 方法不匹配 | `METHOD_NOT_ALLOWED` | `HTTP 方法不被允许` |

HTTP 状态码语义:

| 状态码 | 场景 |
| --- | --- |
| 400 | 校验失败(WORKFLOW_INVALID 等)/ 参数错误 / 请求体不是合法 JSON(`INVALID_JSON`) |
| 403 | 权限拒绝(PERMISSION_DENIED) |
| 404 | 资源不存在(NOT_FOUND) |
| 405 | 方法不被允许(METHOD_NOT_ALLOWED) |
| 413 | 请求体超过 1MB 上限(`PAYLOAD_TOO_LARGE`) |
| 200 | DELETE 为幂等语义:删除不存在的资源同样返回 200 |

所有 JSON 请求体上限 1MB(`http.MaxBytesReader`):超限直接拒绝,不会把
整个 body 读进内存。服务器模式监听 `127.0.0.1` 且**无鉴权**,本机任意
进程都能打这些端点,故该上限是必要的。
**超限返回 413 `PAYLOAD_TOO_LARGE`**(此前是 400,客户端无法区分"参数错"
与"请求体过大");`/api/workflows/import` 的 YAML body 同样按 1MB 计,
超限不再被静默截断成一条与真实原因无关的 YAML 解析错误。

`limit` 查询参数上限 **1000**:此前用 `int` 累加解析,超长数字会整数溢出成
负数,而仓储层把 `limit<=0` 解释为"不限制" —— 一个
`?limit=99999999999999999999` 的请求会变成"返回全表"(内存与响应体都不可控)。
现在超出的数字按 1000 收敛(注意:是**静默截断**,响应里没有"已截断"标记)。

文本类端点(`/export`、`/versions/{n}`)失败时同样返回上述 JSON 错误体,
客户端应按状态码判断而非直接使用响应内容。

## SSE 实时事件流(GET /api/events)

- 连接建立立即返回 `text/event-stream` 响应头与注释行(`: connected`),
  EventSource 立即进入 OPEN 状态;
- 之后每个 UIEvent 以 `data: {JSON}\n\n` 推送;
- 每 15 秒发送 keepalive 注释行(`: keepalive`)防止代理断开空闲连接;
- 客户端断开由 EventSource 自动重连。
- **写失败即关流**:此前写失败只记一条 Debug 日志并继续循环 → 连接不释放
  (订阅不注销、goroutine 常驻、keepalive 继续写)。现在写失败立即结束流
  并注销订阅;建连即失败(对端不可达)则根本不订阅。

## 实时事件类型(UIEvent.type)

`workflow.started / paused / resumed / waiting_user / completed / failed / cancelled`
`node.started / completed / failed / waiting`
`agent.started / output / completed / retry`
`review.approved / review.rejected`
`human.input_required / human.input_received`
`skill.executed / git.executed / merge.completed`
`node.warning`(模板变量缺失,渲染为空串)/ `runtime.error` / `runtime.warning`
(如并行分支不收敛:各分支终点互不相同,分支之后的链不会执行)
`runtime.escalate`(ADK Escalate 的分级:`kind=expected` 是预期内的循环出口
或 HITL 暂停,`kind=unexpected` 是节点失败 / 循环上限 / 循环停滞)

所有事件的 `data` 在分发点统一脱敏(键名为 token/api_key/password 等
时整值替换为 `***`,值含 `key=value` 形态时就地替换),落库 / SSE /
Wails IPC 三条出口一致。

**脱敏的三条独立路径**(各自都要做,漏一条就漏):

1. **事件流**:`event.Bus.Emit` 分发点统一脱敏(本节描述的这条);
2. **节点明细**:直写 SQLite,不经总线 → **读出口**(`/nodes`、`/export`)
   再脱敏一次,`output` / `error` / `result_json` 三处;
3. **落库副本**:节点明细落库前即脱敏(审计文件 / 备份 DB 里不留明文),
   **制品(artifact)保持原样** —— 制品是要给用户看的原始产物。
