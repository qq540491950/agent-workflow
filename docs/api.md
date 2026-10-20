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
| POST | `/api/workflows` | 创建/更新(保存前强制校验;更新版本 +1)。**未知顶层字段 → 400 `UNKNOWN_FIELD`**(允许的键由 `model.Workflow` 的 json tag 反射派生) |
| DELETE | `/api/workflows/{id}` | 删除工作流(执行记录保留) |
| POST | `/api/workflows/{id}/duplicate` | 复制 |
| POST | `/api/workflows/{id}/enable?value=true\|false` | 启用/禁用。**只接受显式的 `true`/`false`**,缺失或其它取值返回 400(fail closed:禁用开关缺参数不能反而打开工作流) |
| POST | `/api/workflows/validate` | 校验(返回结构化 errors/warnings)。**未知顶层字段同样 400**;节点配置里的**未知键只报警告**(`UNKNOWN_NODE_CONFIG`),不阻断保存 |
| GET | `/api/workflows/{id}/export` | 导出 YAML |
| GET | `/api/workflows/{id}/versions` | 版本号列表(降序) |
| GET | `/api/workflows/{id}/versions/{version}` | 指定版本的 DSL 快照。版本号只接受十进制数字,越界返回 400 |
| POST | `/api/workflows/import` | 从 YAML 导入(body 为 YAML 文本;ID 冲突自动换新 ID)。body 上限 1MB,超限返回 **413**。**顶层未知键 → 400 `DSL_UNKNOWN_KEY`**;`permissions` 放错层级 → 400 `DSL_MISPLACED_KEY`;其余被丢弃的键在响应顶层以 `ignored_keys` 数组回报(见"契约变化") |

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

### 错误码表(第四/五轮新增)

`code` 是程序化处理的稳定标识。除下表外,既有码(`NOT_FOUND` /
`BAD_REQUEST` / `PAYLOAD_TOO_LARGE` / `INVALID_JSON` /
`METHOD_NOT_ALLOWED` / `WORKFLOW_DISABLED` / `STATE_CONFLICT` /
`DENIED` / `INVALID_LOG_LEVEL` 等)保持不变。

| code | 出现场景 | 含义 |
| --- | --- | --- |
| `DSL_UNKNOWN_KEY` | 400(导入) | DSL **文档顶层**出现未知键。YAML 与 JSON **同源** —— 同一份定义换格式导入行为一致 |
| `DSL_MISPLACED_KEY` | 400(导入) | 某个段被放错了层级(典型:`permissions:` 写在 `workflow:` 段下)。**权限收紧被静默丢弃 = 安全语义被悄悄放开**,因此必须 fail closed |
| `UNKNOWN_FIELD` | 400(保存/校验) | 请求体含**未知顶层字段**。允许的键集合由 `model.Workflow` 的 json tag **反射派生**(模型加字段自动生效),错误信息里会列出允许的键 |
| `INVALID_NODE_ENABLED` | 校验错误 | 节点的 `enabled` 不是布尔值。运行期同样 fail closed(节点失败),两边结论一致 |
| `UNKNOWN_NODE_CONFIG` | 校验**警告** | 节点配置里有**未被识别**的键(典型:`timeout_seconds` 拼成 `timeout_second`)。该设置**不会生效**,但**不阻断保存** |
| `DEPRECATED_NODE_CONFIG` | 校验**警告** | 节点配置键已废弃(如 condition 节点的 `expression`)。仍可保存,运行期按边上的条件走 |

前两个是**硬错误**(导入直接失败);后两个是**警告**(只提示,不影响保存)——
存量工作流里确实存在拼错或已废弃的键,报错会让用户打不开自己的老工作流。
已废弃的键**只报废弃、不报未知**,避免用户看到两条互相矛盾的提示。

> **校验期的 code 与 HTTP 错误体的 code 是两套**:校验期的 code 出现在
> `POST /api/workflows/validate` 响应的 `errors[].code` / `warnings[].code`
> 数组里;HTTP 错误体的 `code` 在响应体顶层。同一个字符串在两处都可能出现。

## 契约变化(第四/五/七轮)

### 1. 导入响应带 `ignored_keys`

`POST /api/workflows/import` 的响应在**顶层**附加一个 `ignored_keys` 数组
(工作流字段的位置不变,前端把响应当 `Workflow` 用,契约不破):

```json
{
  "id": "imp-1", "name": "...", "version": 1, "nodes": [], "edges": [],
  "ignored_keys": ["workflow.settings", "settings.max_iteration"]
}
```

含义:这些键**出现在文档里但被解析器丢弃了**,对应的设置**不会生效**。
没有键被忽略时**不出现**该字段(前端据此区分"没解析到"与"没有")。

策略分层(与上表的错误码一一对应):
**顶层**未知键 → 报错 `DSL_UNKNOWN_KEY`;`permissions` 放错层级 → 报错
`DSL_MISPLACED_KEY`;其余闭合结构里的未知键 → **忽略但可观测**
(记入 `ignored_keys` + 服务端 WARN 日志),不阻断导入。

### 2. 顶层字段严格校验

`POST /api/workflows` 与 `POST /api/workflows/validate` 对请求体做**未知
顶层键**检查:出现未知键 → **400 `UNKNOWN_FIELD`**,错误文本列出未知键与
允许的键。

- 允许集合的唯一来源是 `model.Workflow` 的 json tag(反射派生),不手写;
- `permissions`(DSL 根级段的正式写法)与 `ignored_keys`(导入响应的
  专用字段)是两个特例 —— 后者被拒会让"导入 → 原样回存"的往返失败;
- 前端设计器发出的 payload 只含模型里的字段,因此严格化**不打破**保存路径;
- `warnings`(保存/运行结果的附加字段)也是特例 —— 保存时忽略它,否则
  "导入 → 原样回存"的往返会失败。

> **第七轮变更(已关闭上述边界)**:该检查已从 HTTP 层**下沉到应用服务层**
> (`app/application/workflow_payload.go`),允许键集合仍由 `model.Workflow` 的
> json tag 反射派生,并新增 `warnings` 字段作为特例。
> 服务器模式与桌面模式**走同一段代码**(`SavePayload` / `ParseWorkflowPayload`),
> 同一份非法载荷在两种模式下给出**同一个 code**。
> 详见 `docs/OPTIMIZATION.md` 第十节(#197)。

### 3. `restoreBackup` 的返回形状

| 字段 | 含义 |
| --- | --- |
| `workflows_restored` / `workflows_skipped` | 已恢复 / 同 ID 已存在而跳过 |
| `workflows_invalid` / `workflows_invalid_ids` | 校验不通过而被**拒绝**(不落库)的定义与 ID 列表 |
| `agent_configs_restored` | 已落库的 Agent 配置数 |
| `agent_configs_invalid` | 未落库的 Agent 配置数(**结构非法或本机无此 Agent**) |
| `agent_configs_unknown_ids` | **本机不存在**的 Agent ID 清单(配置永远不会被应用) |

`agent_configs_invalid` 的语义已扩大:此前"本机不存在的 Agent"也被计入
"已恢复" —— 那是**假成功**(落库只是留一行死数据,配置永远不生效)。

### 4. 节点 `output` 与会话状态 `state_json` 的脱敏

脱敏的**三条独立路径**(各自都要做,漏一条就漏):

1. **事件流**:`event.Bus.Emit` 分发点统一脱敏;
2. **节点明细**:落库前脱敏 + API 读出口(`/nodes`、`/export`)再脱敏一次
   —— `output` / `error` / `result_json` 三处;
3. **会话状态快照**(`executions.state_json`):此前**完全没脱敏**,是明文
   旁路。现按**键分类**处理:
   - 结构化键(`decision` / `status` / `_abort` / `_resume_node` /
     `_current_node` / `_loop_*` / `node:<id>:status` / `node:<id>:attempt`)
     —— **原样保留**:它们是路由与决策的依据,脱敏它们等于改写执行语义;
   - 其余键(`output` / `_error` / `git_diff` / `user_instruction` /
     `node:<id>:result` / `node:<id>:answered` 等)**一律脱敏**
     (默认拒绝:将来新增的状态键不会因漏登记就明文落库);
   - `node:<id>:result` / `node:<id>:answered` 走
     **解析 JSON → 结构化脱敏 → 重新序列化**,保证仍是**合法 JSON**
     (恢复路径要 `Unmarshal`,非法 JSON 会被静默当成"未答复"→ 节点重跑)。

**制品(artifact)保持原样**,不脱敏 —— 制品是要给用户看的原始产物。

> **已知边界**:`output` 既是条件表达式可用的变量(`compiler/conditionenv.go`
> 把它映射到 `compiler.KeyOutput`),又是默认脱敏的自由文本键。**恢复路径**
> 从 `state_json` 回放,条件看到的 `output` 与实时路径不一致 —— 同一份
> 工作流恢复后与首次运行的路由可能不同。

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

**`runtime.error` 的两种来源**(第四、五轮):

| `data.reason` | 含义 |
| --- | --- |
| `state_commit_conflict` | **状态迁移没有落库**(CAS 冲突或落库错误)。库里的状态已被别的写者裁决(用户取消 / 显式重试 / 崩溃恢复)—— 本次迁移被放弃,**终态事件不再发**(否则事件流说"已完成"、库里还是"运行中",UI 与审计流互相打架)。`data` 带 `target_state`(想落的状态)/ `current_state`(库里的实际状态)/ `error` |
| (无 `reason`) | 事件落库失败、节点状态机非法转换等既有的运行时错误 |

取消路径上的同类冲突**不发** `runtime.error`(那是用户高频操作造成的预期
竞态,记 WARN 即可),以免 ERROR 噪音淹掉真正的异常。可观测性靠
`Engine.StateCommitFailures()` 计数与库里的状态。

**脱敏的路径清单**见上文"契约变化 → 4. 节点 `output` 与会话状态
`state_json` 的脱敏"(事件流 / 节点明细落库前 / 节点明细读出口 /
会话状态快照,共四条独立路径)。
