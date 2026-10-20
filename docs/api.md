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
| GET | `/api/agents/{id}/config` | 读取运行配置(env 值以 `***` 掩码;**`extra_args` 里的值同样被掩码**)。**`behavior` 字段目前未掩码(登记为 #255,第十三轮修复中)** |
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
| POST | `/api/settings` | 更新设置(日志级别与 script 节点开关即时生效;`allow_script_nodes` 缺省表示不修改)。**`log_level` 只接受 `debug`/`info`/`warn`/`error`**(大小写不敏感,落库归一为小写),非法值返回 400 —— 不再静默回落 info(界面显示的级别与实际生效的级别会不一致)。**`git_working_dir` 的"缺省"与"空串"是两种语义**:请求体里**没有该字段** = 保持原值不变;**显式传空串** = **清除**(落库空串 + 回到启动默认目录)。**先全量校验、后统一落库** —— 任一项非法则一项都不改 |
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

### 错误码表(第四/五/十轮新增)

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
| `UNSUPPORTED_AGENT_SETTING` | 400(保存 Agent 配置) | 该 Agent 设置**不生效**(运行期从不读取它)。典型:`default_working_dir` —— 后端**拒绝新值**并说明原因(请改用节点级 `working_dir`,或清空该字段)。**已存的老值仍可原样回存**(不阻断存量工作流) |
| `INVALID_LOOP_LIMIT` | 校验错误 | `settings.on_loop_limit` 的取值不在白名单(`fail` / `wait_user`)。校验期就报出,不再静默落回 `wait_user`(那会让"循环超限"变成"暂停等人",用户以为程序卡住了) |
| `PERMISSION_OVERRIDE_LOOSENS` | 校验错误 / 运行错误 | `permissions:` 段试图**放宽**全局已禁用的能力。**有效权限 = 全局实时策略 ∧ permissions 段(只收紧)** —— 放宽尝试在**校验期**就报出;运行期遇到历史工作流里的放宽写法同样报出并按拒绝处理 |
| `INVALID_PERMISSION_OVERRIDE` | 校验错误 | `permissions:` 段的**结构**非法(如未知字段、类型不对)。与 `PERMISSION_OVERRIDE_LOOSENS` 区分:前者是"写错了",后者是"想放开" |

> **第十轮新增的三个码**(`UNSUPPORTED_AGENT_SETTING` / `INVALID_LOOP_LIMIT` /
> `PERMISSION_OVERRIDE_LOOSENS`)均已进前端 `CODE_HINTS` 码表,并受**双向守卫**
> 约束(后端有码而前端没有 → 门禁变红;前端有码而后端没有 → 门禁变红)。

前两个是**硬错误**(导入直接失败);后两个是**警告**(只提示,不影响保存)——
存量工作流里确实存在拼错或已废弃的键,报错会让用户打不开自己的老工作流。
已废弃的键**只报废弃、不报未知**,避免用户看到两条互相矛盾的提示。

> **校验期的 code 与 HTTP 错误体的 code 是两套**:校验期的 code 出现在
> `POST /api/workflows/validate` 响应的 `errors[].code` / `warnings[].code`
> 数组里;HTTP 错误体的 `code` 在响应体顶层。同一个字符串在两处都可能出现。

## 契约变化(第四/五/七/十/十二轮)

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

4. **制品(artifact)**:见下一节(第十轮补上的第四条路径)。

### 5. 制品(artifact)出口的脱敏(第十轮变更)

**制品此前完全未脱敏** —— `git diff` 原文经 `/api/executions/{id}/artifacts`
与 `/api/executions/{id}/export` 返回,凭据形态的片段直接进响应体。

**现在的口径**:按**值的形态**脱敏 —— **键名指向凭据且值呈凭据形态**才替换。

| 情况 | 处理 |
| --- | --- |
| `ANTHROPIC_API_KEY=sk-...`(键名指向凭据 + 值呈凭据形态) | **替换**(值 → `***`) |
| `const token = getToken()`(键名像凭据,但值是**代码标识符**) | **原样保留** —— 否则 diff 的可读性被毁 |
| `output: cli stderr: ...`(自由文本里嵌着凭据形态) | **替换**(按值形态识别) |

- 落库侧与读出口**都过**脱敏(库里那份可能是本轮修复之前的历史数据);
- 脱敏**幂等**:同一份内容连续脱敏 1/2/3 次,逐字节相同;
- `git diff` 的**结构**(增删行的标记)不被破坏 —— 只改值,不改行结构。

> **注意**:第八轮曾把"artifact 不脱敏"作为**刻意取舍**钉进测试,第十轮
> **重新裁决**了这个取舍 —— 凭据泄漏的代价高于 diff 可读性的损失,而
> "按值的形态"这个口径让两者可以兼得。旧的"不脱敏"断言已改写并附理由。

### 6. `output` 键在 resume 与实时路径的口径差(第九轮变更)

`output` 既是条件表达式可用的变量(`compiler/conditionenv.go` 把它映射到
`compiler.KeyOutput`),又是默认脱敏的自由文本键。**恢复路径**从 `state_json`
回放,条件看到的 `output` 与实时路径不一致 —— 同一份工作流恢复后与首次运行
的路由可能不同。

**这是可达的分支改变**(不是"窄到无所谓"):skill 节点输出
`ANTHROPIC_API_KEY=sk-xyz` + 边条件 `output contains "sk-"` → 首次运行走
`secret_path` → HITL 答复触发 resume → 引擎回放**已脱敏**的 `state_json` →
条件用脱敏值**重新求值** → 改走 `normal_path`。**在审批流里 = 人批准 A 分支,
恢复后走 B 分支**。

**根因不止凭据形态**:`output` 的语义是"最近一次成功节点的输出",
**分支内后续节点会覆盖它** → 恢复时重算读到的是完全不同的值。

**现在的口径:路由依据与展示文本分离**。

- 路由一旦选定出口分支,这个选择就是本次执行的**既成事实** → 记进**结构化**
  状态键 `route:<决策节点>:branch`(值是分支下标,**枚举、不含自由文本、
  不落凭据**);
- 恢复时**回放**这个选择,不再拿脱敏副本重新求值;
- 落库的 `state_json` 里仍然**只有脱敏后的 `output`**(约定 8 不破),
  但恢复路径不再依赖它做路由。

**残余边界**(两处,已在 `workflow/runtime/state_mask.go` 写明):① 路由在
**暂停前从未选定过分支**时(例如循环达到上限后暂停,恢复时人的答复合法地
改变了 decision)→ 这次求值用的仍是落库的 `output` 副本;② 多回边循环里的
`switchAgent`(选本轮走哪条回边)每轮重新求值,读到的"上一轮 output"在恢复时
是脱敏副本。这两处要彻底消除,只能把原文留在盘上(违反约定 8)或取消 `output`
作为条件变量(违反约定 13)。

**给写条件表达式的人的规避建议**:需要稳定的分支依据时,用
`decision` / `status`(结构化键,原样保留),别用 `output` 里的自由文本。

### 7. Agent 配置的 `extra_args` 回写语义(第十轮变更)

`extra_args` 里的凭据值在 GET 时**掩码**为 `***`(约定 8)。此前**没有回写
语义** → 用户打开配置拿到掩码 → 只改模型 → 保存 → **真实 API Key 被覆盖成
字面量 `***`** → 之后每次 CLI 调用带 `--api-key ***` → 鉴权失败,而用户看到的
是"已保存"。

**现在的口径:按参数名对齐合并**(不是按下标对齐)。

| 情形 | 处理 |
| --- | --- |
| 客户端传 `***`,而库里该参数**有真值** | 保留**库里的真值** |
| 客户端传 `***`,而库里该参数**无真值** | **丢弃**该参数(不写回字面量 `***`) |
| 库里存的值**本身就是 `***`** | 视为**无真值** |
| 客户端传了**新值** | 用**新值**(用户明确填的优先) |
| 参数**增删或重排** | 凭据**跟着参数名走**,绝不跟着位置走 |

> **为什么拒绝按下标对齐**:增删一个参数后,下标全部错位 → A 的凭据会被写到
> B 的**参数名**上 → 凭据泄漏到第三方服务,或鉴权莫名失败。参数名对齐时
> 同源归一 `--api-key` / `--api_key` / `--API-Key` 视为同一个参数。

下发 CLI 前会**剔除字面量 `***`**(防旧脏数据)。

### 8. 设置与权限的语义(第十轮变更)

| 项 | 语义 |
| --- | --- |
| `git_working_dir` **空串** | = **清除**(落库空串 + 回到启动默认目录)。与"**没传该字段**"严格区分 —— 后者保持原值不变 |
| `Settings.Update` | **先全量校验、后统一落库** —— 任一项非法(如 `log_level`)则**一项都不改**(此前先落库后校验,返回 400 但目录已经改了) |
| `default_working_dir` | **死设置**,运行期从不读取 → 后端**拒绝新值**(`UNSUPPORTED_AGENT_SETTING`);**已存的老值仍可原样回存** |
| `on_loop_limit` | 白名单 `fail` / `wait_user`,非法值在校验期报 `INVALID_LOOP_LIMIT` |
| `--git-dir` 启动参数 | **持久化语义**:写入设置并在设置页可见;持久化失败则**明确回退并说明原因**(此前错误被丢弃,静默回落到默认目录);**空串是无操作**(不是"清空") |
| **有效权限** | = **全局实时策略** ∧ 工作流 `permissions` 段(**只收紧**,求交)。① 全局策略**实时**读取 → 收紧立即对在跑的执行生效;② `permissions` 段**永远无法放宽**全局禁用 → 放宽尝试在校验期报 `PERMISSION_OVERRIDE_LOOSENS` |
| `permissions:` 段错层级 | 顶层 `permissions:` 是正式写法;错层级(如写在 `workflow:` 段下)报 `DSL_MISPLACED_KEY` |

### 9. 校验警告与被忽略的键(两种模式的同源契约)

| 字段 | 出现位置 | 含义 |
| --- | --- | --- |
| `warnings` | `POST /api/workflows`、`POST /api/workflows/{id}/run`、`POST /api/workflows/validate` 的**响应** | 校验期的**警告**(如 `DEPRECATED_NODE_CONFIG`)。**没有警告时该字段不出现** —— 前端据此区分"没解析到"与"没有" |
| `ignored_keys` | `POST /api/workflows/import` 的**响应** | 这些键**出现在文档里但被解析器丢弃了**,对应的设置**不会生效**。没有键被忽略时**不出现**该字段 |

两者都由**应用服务层**产出(解析只做一遍),**服务器模式与桌面模式走同一段
代码**(`SavePayload` / `ParseWorkflowPayload`)—— 同一份非法载荷在两种模式下
给出**同一个 code**。详见 `docs/OPTIMIZATION.md` 第十一节(#197 / #198 / #199)。

### 10. 凭据脱敏的覆盖矩阵(第十二轮变更)

> 登记于 `docs/OPTIMIZATION.md` #245-#247;回归锁见
> `docs/TESTING.md` 的"可执行覆盖矩阵"专节。

**问题**:此前**两套掩码规则并存** —— `logx.Mask`(按**键名**判定)与
`agent.MaskCredentialValues`(按**值形态**判定)各覆盖一半,"出口用哪套就漏
另一半"。终审实测:`Bearer eyJ…` / `sk-ant-…` / `ghp_…` / `AKIA…` /
`Basic <base64>` / PEM 私钥块 / 高熵裸串,在节点 `output` / `error` /
`result_json`、`state_json`、事件表、**实时 SSE**、API 出口(执行详情 /
节点明细 / 导出 / 事件流)、日志、制品**全部明文泄漏**。

**现在的口径**:值的形态规则**只有一份**(`logx/credential.go`),
`agent` 侧降为薄封装;`logx.Mask` 升级为"**键名 ∪ 值形态**"。
落库侧与读出口**各过一次** → 两条规则的终态都是 `***` → **幂等**
(连续脱敏 1 次与 2 次逐字节相同)。

**用户可见的变化**:同一个凭据,在**任何**读出口(`GET /api/executions/{id}`、
`GET /api/executions/{id}/nodes`、`GET /api/executions/{id}/export`、
`GET /api/events`、`GET /api/events/all`、`GET /api/executions/{id}/artifacts`)
都不再以明文出现 —— 之前它只在**部分**出口被替换。

**形态 × 出口的覆盖矩阵**(两个出口 = `Mask` 与 `MaskCredentialValues`):

| 形态 | `Mask`(日志 / 节点输出 / `state_json` / 事件流 / 详情 / 导出) | `MaskCredentialValues`(制品 `git diff`) |
| --- | --- | --- |
| `API_KEY=sk-xxx`(key=value) | 脱敏 | 脱敏 |
| `sk-ant-` / `ghp_` / `AKIA…`(已知前缀裸值) | 脱敏 | 脱敏 |
| `eyJ…`(裸 JWT)、`Bearer eyJ…`、`Basic <base64>` | 脱敏 | 脱敏 |
| PEM 私钥块 | 脱敏 | 脱敏 |
| 高熵裸串(无前缀无键名,≥24 字符) | 脱敏 | 脱敏 |
| `password: hunter2`、`token=abc123`(低熵) | 脱敏 | **不脱敏**(有意取舍) |
| 代码标识符(`const token = getToken();`)、英文句子、文件路径 | **不脱敏** | **不脱敏** |

**两条刻意保留的取舍**(不是漏网,登记在开放清单里):

- **制品出口的低熵值不脱敏** —— diff 里的 `password = "xxx"` 多半是**代码或
  占位符**,抹掉会毁掉**审计证据**;DB / API / 日志出口走**键名规则**,照常脱敏。
- **高熵裸值的 24 字符门槛** —— 裸值语境没有键名线索,门槛必须比键名语境
  (≥16 或 ≥32)**严**,否则 40 位 git SHA、base64 测试夹具会被抹掉。
  驼峰标识符另有护栏(大小写起伏 ≥4 次不脱敏)。

> **为什么这值得单列一节**:此前"规则覆盖了哪些形态 × 哪些出口"**从来没被
> 断言过** —— 改规则不会让测试变红,少一条规则也不会。现在矩阵是**可执行的**
> (`TestCredentialShapeCoverageMatrix`),13 形态 × 2 出口**逐格**登记期望,
> **未覆盖的格子必须写清理由**。将来谁改门槛,矩阵立刻变红,取舍必须被
> **重新论证**一遍。

### 11. `behavior` 字段尚未掩码(第十三轮修复中,登记为 #255)

> **这是当前仍开放的一条**,写在这里是为了让"字段级的掩码覆盖面"可查。

`GET /api/agents/{id}/config` 返回的 `AgentConfig` 里,`env` 的值与
`extra_args` 里的凭据**都**被掩码为 `***`,但 **`behavior`(自由映射
`map[string]any`)没有走掩码** → 该字段里若含凭据形态的值,会**原样返回**
(约定 8 要求"API 返回掩码")。

**已知结论(第十三轮修)**:掩码要覆盖**全部**可能承载凭据的字段;
`behavior` 是自由映射,不能按值形态**盲抹**(会毁掉合法配置),但**必须过
一遍值的形态规则**。

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
