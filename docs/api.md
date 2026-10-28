# HTTP API 参考(server 模式)

> 桌面模式通过 Wails IPC 暴露同一套服务;以下端点在 `--server` 模式下可用。
> 启动:`./bin/agent-workflow-server --server --addr 127.0.0.1:8080`

> **第三十轮(迭代 34 收官,轮次 93)契约复核**:本文件是**对外契约**,
> 收官轮对它做了 **8 条声称 vs 代码**的逐条抽查 —— **8 条全部成立,
> 契约正文未改**。逐条的核对依据见 `docs/OPTIMIZATION.md` 第十四节
> 末尾"本轮复核的依据"表的最后一行:
>
> | # | 契约声称 | 核对到的代码 |
> | --- | --- | --- |
> | 1 | **凭据出口** | git 包**五个出口**全过 `logx.Mask`(`git/git.go:209/247/264/288/297`);错误文本也过(`api/server.go:536`);`state_json` 按键分类;制品走值形态规则 |
> | 2 | **错误响应形状** | 非 2xx 一律 `{error, code, kind}`(`api/server.go:524-547`);路由级 404 / 405 同形状(`:631-642`) |
> | 3 | **状态码映射** | `statusForError`(`api/server.go:578-603`)与下方状态码表**逐行一致**(先 code 再 kind) |
> | 4 | **git 出口** | `Status` / `Diff` / `Log` / `Commit` / `CurrentBranch` **五个出口**全遮 |
> | 5 | **modes 端点** | `GET /api/modes`(`api/server.go:63`)→ `workflow/model.AllModes()`(`workflow/model/model.go:156`);`modeSpecs` 实为 plan / execute / review / fix / test / submit,与本文档**逐项一致** |
> | 6 | **备份范围** | 导出带 `version` + `workflows` + `agent_configs` + `skill_configs`(`app/application/application.go:1903-1906`);恢复端 `checkBackupVersion`(`:1934`)校验版本 |
> | 7 | **取消语义** | `workflow/runtime/engine.go:505-568` 与下方"取消执行的三种语义"表**逐行一致**(终态 → 400 `INVALID_TRANSITION` / 不存在 → 404 / `CREATED`·`WAITING_USER` → CAS 裁决 + 非终态节点落 `SKIPPED`) |
> | 8 | **顶层键严格化** | `app/application/workflow_payload.go:97` 的 `UNKNOWN_FIELD`;允许集合由 `model.Workflow` 的 json tag 反射派生;服务器模式与桌面模式走同一段代码 |

## 系统

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查与版本信息 |
| GET | `/api/modes` | agent 节点的**合法 mode 列表**(JSON 字符串数组,顺序即展示顺序)。唯一来源是 `workflow/model.AllModes()`(约定 13:同一份语义只有一处定义)—— 前端的设计器下拉据此渲染,**不再自带硬编码清单**(后端加了 mode 而前端没跟上 → 用户在下拉里选不到它;前端留着后端已删的 mode → 用户选了必然被校验期判非法)。桌面模式走 Wails 绑定 `WorkflowService.Modes()`,与本端点同一份数据(约定 10) |

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
| GET | `/api/workflows/{id}/versions/{version}` | 指定版本的 DSL 文本。版本号只接受十进制数字,越界返回 400。**读的是当初存下的原文**(`workflow_versions.dsl_text`),不是从模型快照重新编码的结果 —— 快照里的整数在 JSON 往返后变成 float64,重新编码会把 `max_iter: 3` 显示成 `3.0`(**内容差异,不是排版差异**)。老库没有这一列的值时回退到重编码(轮次 85) |
| POST | `/api/workflows/import` | 从 YAML 导入(body 为 YAML 文本;ID 冲突自动换新 ID)。body 上限 1MB,超限返回 **413**。**顶层未知键 → 400 `DSL_UNKNOWN_KEY`**;`permissions` 放错层级 → 400 `DSL_MISPLACED_KEY`;其余被丢弃的键在响应顶层以 `ignored_keys` 数组回报(见"契约变化") |

## Executions

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/workflows/{id}/run` | 启动执行 `{task, variables}`(异步)。工作流声明了 `inputs` 段时(迭代 36):必填入参缺失 → **400 `INPUT_REQUIRED`**;传入值类型不符 → **400 `INPUT_TYPE_MISMATCH`**;缺省项自动兜 `default` 落入执行变量。同一工作流的未结束执行数达到 `settings.max_concurrent` → **409 `WORKFLOW_BUSY`**(fail fast,不排队) |
| GET | `/api/executions?workflow_id=&limit=` | 执行列表(最新在前)。`limit` **上限 1000**,超出的数字按 1000 收敛并打 `X-Limit-Truncated` / `X-Limit-Applied` 响应头(不再因整数溢出变成"不限制"而返回全表) |
| GET | `/api/executions/{id}` | 执行详情(含 state_data 快照) |
| GET | `/api/executions/{id}/nodes` | 节点执行明细(attempt/duration/output)。**output / error / result_json 已在读出口脱敏**(这些字段直接来自 Agent 的 stdout/stderr,常含凭据) |
| GET | `/api/executions/{id}/events` | 事件流 |
| GET | `/api/executions/{id}/artifacts` | 制品(如 git-diff)。**按值的形态脱敏**(第十轮):凭据形态的值被替换为 `***`,代码标识符原样保留(保住"改了哪一行"这个审计证据)。详见第 10 节 |
| GET | `/api/executions/{id}/export` | 导出完整记录 JSON(附件下载)。响应头 `Cache-Control: no-store`;其中的节点明细同样已脱敏 |
| POST | `/api/executions/{id}/input` | 人工输入并恢复 `{response, instruction}` |
| POST | `/api/executions/{id}/retry` | 失败执行恢复 `{skip: bool}`。空 body 合法(等价 `skip=false`);body 非空而解析失败返回 400(此前静默变 `skip=false`,「跳过失败节点」实际会重跑失败 Agent) |
| POST | `/api/executions/{id}/cancel` | 取消执行。**三种语义**(第十五轮变更):**运行中** → 200;**已终态** → **400 `INVALID_TRANSITION`**;**不存在** → 404。详见"第 12 节" |
| DELETE | `/api/executions/{id}` | 删除记录(级联) |

## Agents / Skills

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/agents` | Agent 注册表(含权限策略与已配置模型) |
| POST | `/api/agents/{id}/test` | 可用性测试(CLI 探测) |
| GET | `/api/agents/{id}/config` | 读取运行配置(env 值以 `***` 掩码;**`extra_args` 里的值同样被掩码**;**`behavior` 字段也已纳入掩码**,走键名 + 值形态规则,#255 已关闭) |
| PUT | `/api/agents/{id}/config` | 保存配置并热重建适配器。**env 的第四态契约**:**缺省(字段不存在)= 不修改**(保留库里已存的环境变量)/ **`{}` = 显式清空全部** / **`{k:v}` = 整表替换**(逐键按约定 5 合并:`***`=保留原值、空串=删除该键、库里没有的 `***` 丢弃;**表里没有的键会被删除**)。env **内部**仍是约定 5 的三态 |
| POST | `/api/agents/{id}/reset` | 清空运行配置,恢复适配器内置默认 |
| POST | `/api/agents/{id}/permission` | 更新权限策略 —— **局部更新**:只改载荷里**显式给出**的能力位,**缺省的保持原值**(基线 = 当前**生效**的策略)。**非布尔值返回 400**(静默当作 false 等于吊销该能力)。**未知键忽略**(载荷可能来自比本端点更宽的客户端契约)。整表读-改-写已加锁串行化,并发授权**不会互相覆盖** |
| GET | `/api/skills` | Skill 注册表(含启用状态) |
| POST | `/api/skills/{id}/enable` | 启用/禁用 `{enabled}`(缺该字段返回 400) |
| POST | `/api/skills/{id}/test` | 测试执行 |

## Git

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/git/status` | 结构化状态(分支/暂存/修改/未跟踪)。**文本已脱敏**(文件路径同样可能带凭据) |
| GET | `/api/git/diff?staged=` | 未提交改动。**文本已脱敏**(见下方"git 出口的脱敏") |
| GET | `/api/git/log?limit=` | 提交历史。**提交信息已脱敏**(commit message 可能含凭据) |
| POST | `/api/git/commit` | 提交全部改动 `{message}`。**输出已脱敏** |

### git 出口的脱敏(轮次 88 / 迭代 30)

Git 工作目录是**用户自己的代码库**:里面常有 `.env`、带密钥的配置文件、
凭据写死在配置里的脚本。此前 `GET /api/git/diff` **原样**返回 diff 文本 ——
凭据明文直接进 API 响应体,再由前端 GitPanel 渲染到屏幕。

**现在的口径**:git 包的**五个出口**(`Status` / `Diff` / `Log` / `Commit` /
`CurrentBranch`)的命令输出**一律过 `logx.Mask`**(键名 ∪ 值形态)——
与**节点 output 出口同源**(`logx/credential.go` 是形态规则的唯一来源)。

| 情况 | 处理 |
| --- | --- |
| `ANTHROPIC_API_KEY=sk-ant-…`(键名 + 凭据形态的值) | **替换**(值 → `***`,键名保留) |
| `DB_PASSWORD=hunter2`(**低熵**的值) | **替换** —— 键名规则抓得住;`.env` 里的密码多半是这种形态,只靠值形态规则会漏 |
| `const token = getToken();`(键名像凭据、值是代码) | **替换**(键名规则)—— 与节点 output 出口同一个取舍:**宁可多遮** |
| 普通代码 / 文件路径 / 英文句子 | **逐字不变**(可读性回归锁:`git/mask_test.go` 的 `TestDiffKeepsOrdinaryContentByteIdentical` 拿 `git diff` 原文逐字节比对) |

**与"制品出口"的差异(刻意)**:制品(`/api/executions/{id}/artifacts`)走
**只按值形态**的规则(`logx.MaskCredentialValues`)—— 制品是**已落库的历史
快照**,要保住"改了哪一行"这个审计证据;git 面板是**用户当前工作区**的
实时视图,里面的 `.env` 低熵密码只有键名规则抓得住。两者规则**同源**
(同一个 `logx/credential.go`),只是**严格度**按出口不同。

**工作流 git 节点**(`Service.Run`)的输出**不在本包内脱敏**:节点 output
由应用层统一过 `logx.Mask`,制品由执行器过值形态规则(两处口径都是既有
契约,`workflow/runtime/masking_test.go` 钉着"制品里 `const token =
getToken();` 逐字保留")。在本包再叠一层键名规则会与制品出口打架。


## Settings / Events / Stats

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/settings` | 数据目录 / Git 工作目录 / 日志级别 / script 节点放行开关。**对应设置行读不出来时多一个 `*_read_failed: true` 标记**(仅在读失败时出现,三个标记:`git_working_dir_read_failed` / `log_level_read_failed` / `allow_script_nodes_read_failed`):此刻该字段的值是**内存态生效值**,**不是库里的值** —— 消费方必须把它标给用户,不能当"库里那个"展示或回存(回存即覆盖,损坏行就此消失) |
| POST | `/api/settings` | 更新设置(日志级别与 script 节点开关即时生效;`allow_script_nodes` 缺省表示不修改)。**`log_level` 只接受 `debug`/`info`/`warn`/`error`**(大小写不敏感,落库归一为小写),非法值返回 400 —— 不再静默回落 info(界面显示的级别与实际生效的级别会不一致)。**`git_working_dir` 的"缺省"与"空串"是两种语义**:请求体里**没有该字段** = 保持原值不变;**显式传空串** = **清除**(落库空串 + 回到启动默认目录)。**先全量校验、后统一落库** —— 任一项非法则一项都不改 |
| GET | `/api/backup` | 导出备份(工作流 + Agent 配置 + **技能启用状态**)。**含明文,范围是三类**:`agent_configs` 整份原样(`env` / `behavior` / `extra_args`)+ **工作流定义本体**(含工作流 `variables`)。约定 8 允许备份导出含明文(它是唯一例外),但**请按"整个文件都是敏感的"对待**。响应头 `Cache-Control: no-store`,避免明文 API Key 留在浏览器/代理的磁盘缓存里。`skill_configs` 段(id → enabled)是轮次 85 补上的:此前备份里根本没有它 → 换机器恢复后,此前**禁用**的技能又全部可用了 |
| POST | `/api/backup/restore` | 恢复备份。**先校验 `version`**(轮次 85):缺失或不认识 → **400 `BAD_REQUEST`**(此前 `version` 只写不读,任何 JSON 都被当备份照单全收,用户看到"恢复成功"却什么也没恢复);`version: 1` 是现有备份的版本号 → 正常路径(向后兼容)。已存在的同 ID 工作流跳过;**校验不通过的定义被拒绝**(不落库)并计数;Agent 配置结构非法同样拒绝;**本机不存在的 Agent 也计入 `agent_configs_invalid` 且不落库**(此前计入"已恢复"是假成功:配置永远不会被应用)。**技能启用状态**按 `skill_configs` 段恢复(只对本机确实存在的 Skill 写回,其余跳过并计入 `skill_configs_unknown_ids`);**老备份没有这一段 → 不恢复技能状态、不报错**。响应含 `workflows_restored` / `workflows_skipped` / `workflows_invalid` / `workflows_invalid_ids` / `agent_configs_restored` / `agent_configs_invalid` / **`agent_configs_unknown_ids`** / `agent_configs_apply_failed_ids` / **`skill_configs_restored`** / **`skill_configs_unknown_ids`** |
| GET | `/api/events` | **SSE** 实时事件流(UIEvent;连接即达响应头,15s keepalive) |
| GET | `/api/sse/stats` | 当前 SSE 订阅数(只读,`{"subscribers": N}`,无敏感信息)。运维排查"连接泄漏 / 订阅常驻"的可观测点;冒烟脚本(`smoke_sse.sh`)靠它端到端断言"断开后订阅释放" |
| GET | `/api/events/all?type=&limit=` | 跨执行审计事件(支持类型前缀过滤) |
| GET | `/api/stats` | 每工作流执行统计。**`failed` 与 `cancelled` 分列**(轮次 84):`failed` 只算 `FAILED`,`cancelled` 只算 `CANCELLED`;各列之和 == `total`(列的划分穷尽所有执行状态)。消费方算成功率时**分母要排除 `cancelled`** —— 被用户叫停的执行不是"没做成" |
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
| 路由不存在 | `NOT_FOUND` | `请求的资源不存在(链接可能已过期,或地址有误)` |
| 方法不匹配 | `METHOD_NOT_ALLOWED` | `HTTP 方法不被允许` |

> 路由级 404 的文案此前是"接口不存在" —— 写给开发者看的,普通用户看到
> 它不知道该做什么(轮次 88 / 迭代 30 改为面向用户但仍可定位的措辞)。

HTTP 状态码语义(轮次 88 / 迭代 30 调整过内部类的状态码):

| 状态码 | 场景 |
| --- | --- |
| 400 | 校验失败(WORKFLOW_INVALID 等)/ 参数错误 / 请求体不是合法 JSON(`INVALID_JSON`)/ **状态不允许**(`INVALID_RESUME` / `INVALID_TRANSITION` / `INVALID_RETRY` / `NO_FAILED_NODE`)/ git 命令失败(`GIT_*`)/ 已取消(`*_CANCELLED`) |
| 403 | 权限拒绝(PERMISSION_DENIED) |
| 404 | 资源不存在(NOT_FOUND) |
| 405 | 方法不被允许(METHOD_NOT_ALLOWED) |
| 413 | 请求体超过 1MB 上限(`PAYLOAD_TOO_LARGE`) |
| 500 | **内部错误**:持久化失败(`DB_ERROR` / `AGENT_CONFIG_CORRUPT`)/ 其它内部错误(`INTERNAL`)/ handler panic |
| 502 | **外部依赖失败**:Agent 调用失败(`AgentError`)/ 外部进程异常退出(`CLAUDE_PROCESS_ERROR` / `PI_PROCESS_ERROR`) |
| 504 | **外部依赖超时**(`CLAUDE_TIMEOUT` / `PI_TIMEOUT`) |
| 200 | DELETE 为幂等语义:删除不存在的资源同样返回 200 |

**为什么内部错误要报 5xx(而不是此前一律 400)**:数据库锁死、Agent 崩溃、
超时 —— 这些与"用户填错了参数"无关。此前它们**一律报 400**,用户与监控
无法区分"我填错了"与"后端炸了";监控侧的 4xx 告警会被这类噪声淹没。

**向后兼容**:既有 **4xx 映射一律不变**(`NOT_FOUND`→404、
`PAYLOAD_TOO_LARGE`→413、`PermissionError`→403、其余校验类/状态类→400)——
前端与第三方客户端可能依赖。新增的 5xx **只覆盖内部类错误**。

> **一处刻意不改的**:`INVALID_RESUME` / `INVALID_TRANSITION` 语义上更像
> "409 Conflict"(状态冲突),但它们是**既有对外契约** —— 本文件"取消执行"
> 一节写明"已终态 → 400 `INVALID_TRANSITION`",`api/cancel_semantics_test.go`
> 也钉着 400。前端**不按状态码分支**(只按 `code`),但第三方客户端与文档
> 契约都在 → 本轮**保持 400**,把"改 409"登记为待统一裁决的开放项。

**错误文本一律脱敏**:`error` 字段的文本过 `logx.Mask` —— 错误消息常夹带
命令输出 / 子进程 stderr / SQL 文本,里面可能带凭据(约定 8:"API 返回
掩码")。**原始错误只进日志**(`logx.Warn`),用户看到的是脱敏后的人话。

**错误文本不含 Go 内部细节**:此前 `badRequest` 直接把 Go 的错误原文透出
(如 `json: cannot unmarshal string into Go struct field AgentConfig.extra_args
of type []string` —— 泄漏 Go 结构体名与类型名)。现在这类调用点给**人话**
(字段名保留 —— 那是用户自己 payload 里的 JSON 键;Go 类型名不保留)。

**panic 兜底**:handler 里的 panic 此前**一路冒到 net/http** —— 服务器模式
客户端收到 EOF(前端误报"后端未启动"),桌面模式则把 Go 包路径泄漏到界面。
现在 `wrap` 兜住 panic,统一回 **500 + 标准错误体**:

```json
{"error": "服务内部错误", "code": "INTERNAL", "kind": "WorkflowError"}
```

panic 原文**只进日志**,不进响应体(`http.ErrAbortHandler` 除外 —— 那是
net/http 的主动中止信号,按官方要求放行)。


所有 JSON 请求体上限 1MB(`http.MaxBytesReader`):超限直接拒绝,不会把
整个 body 读进内存。服务器模式监听 `127.0.0.1` 且**无鉴权**,本机任意
进程都能打这些端点,故该上限是必要的。
**超限返回 413 `PAYLOAD_TOO_LARGE`**(此前是 400,客户端无法区分"参数错"
与"请求体过大");`/api/workflows/import` 的 YAML body 同样按 1MB 计,
超限不再被静默截断成一条与真实原因无关的 YAML 解析错误。

`limit` 查询参数上限 **1000**:此前用 `int` 累加解析,超长数字会整数溢出成
负数,而仓储层把 `limit<=0` 解释为"不限制" —— 一个
`?limit=99999999999999999999` 的请求会变成"返回全表"(内存与响应体都不可控)。
现在超出的数字按 1000 收敛;**发生收敛时**响应带两个头,
消费方据此区分"就这么多"与"被截断了"(不发生时两个头都不存在):

- `X-Limit-Truncated: true` —— 显式 `limit` 超过上限被收敛;
- `X-Limit-Applied: 1000` —— 实际生效的条数上限。

标记走响应头而非响应体字段:列表端点的响应体是裸数组,塞字段等于
改所有列表接口的响应形状;头是可选读取的附加语义,不读不受影响。
三个限列表端点(`/api/executions`、`/api/events/all`、`/api/git/log`)同口径。

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
| `INVALID_NODE`(mode 取值) | 校验错误 | agent 节点的 `mode` **不在取值域内**(唯一来源:`model.AllModes()` —— `plan` / `execute` / `review` / `fix` / `test` / `submit`)。此前只查"必填"不查"取值",拼错的 mode(`exectue`)能存能跑,还会**跳过写权限检查**。运行期同样 fail closed(节点失败),两边结论一致(轮次 84) |
| `INVALID_NODE`(数值非有限 / timeout 越界) | 校验错误 | ① 工作流里的数值**不是有限数**(NaN / ±Inf)—— 落库会连坐清空整张 map(#306 / #307,轮次 86);② 节点的 `timeout_seconds` **不在 0..86400** 或不是整数秒(#311,轮次 86) |
| `UNKNOWN_NODE_CONFIG` | 校验**警告** | 节点配置里有**未被识别**的键(典型:`timeout_seconds` 拼成 `timeout_second`)。该设置**不会生效**,但**不阻断保存** |
| `DEPRECATED_NODE_CONFIG` | 校验**警告** | 节点配置键已废弃(如 condition 节点的 `expression`)。仍可保存,运行期按边上的条件走 |
| `UNSUPPORTED_AGENT_SETTING` | 400(保存 Agent 配置) | 该 Agent 设置**不生效**(运行期从不读取它)。典型:`default_working_dir` —— 后端**拒绝新值**并说明原因(请改用节点级 `working_dir`,或清空该字段)。**已存的老值仍可原样回存**(不阻断存量工作流) |
| `INVALID_LOOP_LIMIT` | 校验错误 | `settings.on_loop_limit` 的取值不在白名单(`fail` / `wait_user`)。校验期就报出,不再静默落回 `wait_user`(那会让"循环超限"变成"暂停等人",用户以为程序卡住了) |
| `PERMISSION_OVERRIDE_LOOSENS` | 校验错误 / 运行错误 | `permissions:` 段试图**放宽**全局已禁用的能力。**有效权限 = 全局实时策略 ∧ permissions 段(只收紧)** —— 放宽尝试在**校验期**就报出;运行期遇到历史工作流里的放宽写法同样报出并按拒绝处理 |
| `INVALID_PERMISSION_OVERRIDE` | 校验错误 | `permissions:` 段的**结构**非法(如未知字段、类型不对)。与 `PERMISSION_OVERRIDE_LOOSENS` 区分:前者是"写错了",后者是"想放开" |
| `BAD_REQUEST` | 400(更新 Agent 权限) | 权限载荷里的能力位**不是布尔值**(错误信息点名是哪个字段、实际类型是什么)。**静默当作 false 等于吊销该能力**,而客户端以为自己改的是别的能力(约定 14)。注意:此路径**没有**专属错误码,与其它 `BAD_REQUEST` 共用 |
| `INPUT_REQUIRED` | 400(启动执行) | 工作流声明了必填入参(`inputs[].required: true`)而请求的 `variables` 里没有提供。执行**不创建**(校验发生在任何副作用之前) |
| `INPUT_TYPE_MISMATCH` | 400(启动执行) | 传入的入参值与声明的 `type`(`string` / `number` / `boolean`)不符。类型判定与校验期查 `default` 走**同一个函数**(`model.InputValueMatchesType`,约定 13) |
| `WORKFLOW_BUSY` | **409**(启动执行) | 同一工作流的非终态执行数(CREATED / RUNNING / WAITING_USER)达到 `settings.max_concurrent`(0 = 不限,迭代 36)。请求本身合法,只是"现在不能跑"—— 所以是 409 而不是 400;**不排队**,调用方决定重试或放弃 |
| `INVALID_MAX_CONCURRENT` | 校验错误 | `settings.max_concurrent` 不在 0..100(唯一来源:`compiler.MaxConcurrentRange`)。负数会静默变成"不限",必须报出 |
| `INVALID_TRANSITION` | 400(取消执行) | **状态迁移非法**。取消一个**已终态**(`COMPLETED` / `FAILED` / `CANCELLED`)的执行时返回 —— 取消一个已经结束的运行是"取消失败",不是幂等删除。**约定 6 的"幂等返回 200"只适用于删除资源**,不适用于状态迁移。详见"第 12 节" |

> **第十轮新增的三个码**(`UNSUPPORTED_AGENT_SETTING` / `INVALID_LOOP_LIMIT` /
> `PERMISSION_OVERRIDE_LOOSENS`)均已进前端 `CODE_HINTS` 码表,并受**双向守卫**
> 约束(后端有码而前端没有 → 门禁变红;前端有码而后端没有 → 门禁变红)。

前两个是**硬错误**(导入直接失败);后两个是**警告**(只提示,不影响保存)——
存量工作流里确实存在拼错或已废弃的键,报错会让用户打不开自己的老工作流。
已废弃的键**只报废弃、不报未知**,避免用户看到两条互相矛盾的提示。

> **校验期的 code 与 HTTP 错误体的 code 是两套**:校验期的 code 出现在
> `POST /api/workflows/validate` 响应的 `errors[].code` / `warnings[].code`
> 数组里;HTTP 错误体的 `code` 在响应体顶层。同一个字符串在两处都可能出现。

## 契约变化(第四/五/七/十/十二/十三/十五/八十一/八十二轮)

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
| `agent_configs_apply_failed_ids` | **已落库但运行时应用失败**的 Agent ID 清单(轮次 81 / 第十七轮变更)。落库 ≠ 生效:配置写进库了,但套用时失败(CLI 不存在 / 参数非法等)→ 用户只看到"已恢复 N 个",不知道哪几个**没有生效**。前端必须读这个字段并展示 |

`agent_configs_invalid` 的语义已扩大:此前"本机不存在的 Agent"也被计入
"已恢复" —— 那是**假成功**(落库只是留一行死数据,配置永远不生效)。

**`agent_configs_apply_failed_ids` 与 `agent_configs_invalid` 的区别**:前者
是"**落库了但没生效**"(套用运行时失败),后者是"**根本没落库**"。两者都必须
上报 —— 只报前者会让用户以为配置生效了,只报后者会让用户以为库里的配置
也一并没了。

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

> **刻意**明文落库的两个字段(第十八轮补登记,#285):`executions.task` 与
> `executions.variables` —— **落库不脱敏,但读出口脱敏**。
>
> **为什么**:它们是**恢复输入**。恢复路径把 `exec.Task` / `exec.Variables`
> 重新灌进会话(task 进模型消息、variables 进条件表达式求值)—— 落库副本
> 一旦被改写,恢复后的执行语义就变了(条件走错边 = 约定 14 的"该失败却
> 成功"同族)。**比明文落库的伤害更直接**。
>
> **读出口仍然掩码**:`GET /api/executions/{id}` 等出口对 task / variables
> 都做脱敏(与 `output` 键的口径差 #205 / #222 **同源**)。
>
> **所以"库里全是脱敏的"这个理解是错的**:库里的 `task` / `variables`
> 是**明文**,只有 API 响应里的不是。`docs/OPTIMIZATION.md` 第十四节
> 第 72 条有同一取舍的登记。

### 5. 制品(artifact)出口的脱敏(第十轮变更)

**制品此前完全未脱敏** —— `git diff` 原文经 `/api/executions/{id}/artifacts`
与 `/api/executions/{id}/export` 返回,凭据形态的片段直接进响应体。

**现在的口径**:按**值的形态**脱敏 —— **键名指向凭据且值呈凭据形态**才替换。

| 情况 | 处理 |
| --- | --- |
| `ANTHROPIC_API_KEY=sk-...`(键名指向凭据 + 值呈凭据形态) | **替换**(值 → `***`) |
| `const token = getToken()`(键名像凭据,但值是**代码标识符**) | **原样保留** —— 否则 diff 的可读性被毁 |
| `output: cli stderr: ...`(自由文本里嵌着凭据形态) | **替换**(按值形态识别) |
| `AK=AKIAIOSFODNN7EXAMPLE`(**键值粘连**:短键名把凭据"粘"住) | **替换**(值 → `***`,键名 `AK=` 保留)—— 迭代 27 #318 |
| `+AKIAIOSFODNN7EXAMPLE`(diff **行前缀**把凭据"粘"住) | **替换**(值 → `***`,行标记 `+` 保留)—— 迭代 27 #318 |

- 落库侧与读出口**都过**脱敏(库里那份可能是本轮修复之前的历史数据);
- 脱敏**幂等**:同一份内容连续脱敏 1/2/3 次,逐字节相同;
- `git diff` 的**结构**(增删行的标记)不被破坏 —— 只改值,不改行结构。

> **注意**:第八轮曾把"artifact 不脱敏"作为**刻意取舍**钉进测试,第十轮
> **重新裁决**了这个取舍 —— 凭据泄漏的代价高于 diff 可读性的损失,而
> "按值的形态"这个口径让两者可以兼得。旧的"不脱敏"断言已改写并附理由。

#### 凭据 token 化的两条硬约束(迭代 27,#318)

凭据形态判定先把文本切成"候选 token",再逐个判形态。切分规则有两条
**不能违反**的约束 —— 违反任一条都会造成**新的明文泄漏**:

1. **`=` 必须留在 token 的字符集里**:`=` 是 base64 的**合法字符**
   (PEM 块、`Basic <base64>` 的填充)。把它删掉会把 base64 **截断** →
   长串 base64 跌破裸值语境的高熵门槛(≥24)→ 整串明文泄漏。
   回归锁:`logx/credential_test.go` 的
   `TestBareBase64WithPaddingIsNotTruncated`(用一串**恰好 24 字符**
   (含两个 `=` 填充)的 base64 —— 去掉 `=` 就跌破门槛)。
2. **"键值粘连"要切开**:`AK=AKIA…` 的 token 整体**不呈**任何凭据形态
   (AWS 密钥 ID 的规则锚定 `^…$`、已知前缀用 `HasPrefix`、高熵规则要求
   小写 → **四路全不命中**)。修法不是"把 `=` 从字符集里删掉",而是
   **按首个 `=` 切开再判形态**(且**只有右侧非空才切** —— base64 的填充
   在末尾,右侧为空 → 不切)。切口两侧**都**参与判定,所以
   `AKIA…=foo` 这种"凭据在左"的形态也盖得住。

> 同源的第二个口子(迭代 27 #319):预筛提示词原先只列了 `akia` / `asia` /
> `aiza` / `abia`,**漏了** `aida` / `aroa` / `anpa` / `acca` → 这四种
> AWS 密钥 ID 连**裸值**形态都判不到(连预筛都过不去)。

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

### 7. Agent 配置的 `extra_args` 回写语义(第十轮变更;第十五轮补"值形态"那一半)

`extra_args` 里的凭据值在 GET 时**掩码**为 `***`(约定 8)。此前**没有回写
语义** → 用户打开配置拿到掩码 → 只改模型 → 保存 → **真实 API Key 被覆盖成
字面量 `***`** → 之后每次 CLI 调用带 `--api-key ***` → 鉴权失败,而用户看到的
是"已保存"。

**掩码的判据:参数名 ∪ 值形态**(第十五轮,登记为 #265)。

| 判据 | 例子 | 处理 |
| --- | --- | --- |
| **参数名**指向凭据 | `--api-key sk-x`、`--token=sk-x` | 整个值 → `***`(名字就是线索,与值长什么样无关) |
| **值的形态**是凭据(名字不敏感) | `--header "Authorization: Bearer ghp_xxx"`、`--foo sk-ant-xxx` | 整个值 → `***` |
| 两者都不是 | `--description "some text"`、`--model claude-sonnet-4-5` | **原样保留**(不误伤) |

> **值形态的规则只有一份**:`logx/credential.go`(约定 13 的同源精神)。
> `extra_args` 侧只取它的**判断**,不另写一份形态判定。
> **判定对象是整个参数值**(值里含凭据形态 → 整个值替换为 `***`),
> 不是就地替换其中的子串 —— 全仓的 `***` 只表示"这个值被掩了,回写时沿用
> 已存值";半掩半明的值回写时无法判断"这一段是不是掩码",而且就地替换会
> 改写用户自己的原文(`rotate ghp_xxx soon` 变成 `rotate *** soon`)。

**回写的口径:按参数名对齐合并**(不是按下标对齐)。

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
>
> **掩码与合并必须是同一个口径**:掩码认得的,合并也必须认得 —— 否则
> "按形态被掩的 `***`"回传时找不到原值 → 凭据被丢弃(Agent 每次调用都不带
> 鉴权参数,而用户只看到"已保存")。

**已存值按原文保存(含引号)**:参数不经 shell,引号是参数值的一部分 ——
`--api-key "sk-x"` 回来必须是 `--api-key "sk-x"`,不是 `--api-key sk-x`。

**布尔开关不是值**:`--api-key --verbose` 里 `--verbose` 是**另一个选项**
(不带值),不得被当成 `--api-key` 的值掩掉或合并掉;`-5` 这类**负数**同理
(它常是 `--offset` 的值)。

下发 CLI 前会**剔除字面量 `***`**(防旧脏数据)—— **所有会被掩码的值位**
(不只凭据名)都剔。

### 8. 设置与权限的语义(第十轮变更;第十八轮(轮次 82)补"读失败标注"与"Set 白名单")

| 项 | 语义 |
| --- | --- |
| `git_working_dir` **空串** | = **清除**(落库空串 + 回到启动默认目录)。与"**没传该字段**"严格区分 —— 后者保持原值不变 |
| 设置行**读失败** | 库里那一行损坏或 DB 读失败时,`GET /api/settings` 额外返回对应的 **`*_read_failed: true`**(`git_working_dir_read_failed` / `log_level_read_failed` / `allow_script_nodes_read_failed`),且该字段的值是**内存态生效值**而不是库值(此前读错误被吞掉,界面显示的不是库里那个,用户一保存就把损坏行覆盖了)。**"从未设置过"不算失败**:库里没有这一行是正常初始状态,响应形状与此前完全一致 |
| `Settings.Update` | **先全量校验、后统一落库** —— 任一项非法(如 `log_level`)则**一项都不改**(此前先落库后校验,返回 400 但目录已经改了) |
| `Settings.Set`(仅 Wails IPC) | **白名单键**:`git_working_dir` / `log_level` / `allow_script_nodes`;其余键**一律拒绝**(400 `BAD_REQUEST`)。**复用 `Update` 的同一套校验**(目录存在性、级别合法性),并做**值类型校验** —— 不能成为绕过校验的后门。`agent_policies` **刻意不在白名单里**:它由 `AgentService` 在 `permMu` 下"读整表→改一项→写整表",绕过去直写会丢并发更新 |
| `default_working_dir` | **死设置**,运行期从不读取 → 后端**拒绝新值**(`UNSUPPORTED_AGENT_SETTING`);**已存的老值仍可原样回存** |
| `on_loop_limit` | 白名单 `fail` / `wait_user`,非法值在校验期报 `INVALID_LOOP_LIMIT` |
| `--git-dir` 启动参数 | **持久化语义**:写入设置并在设置页可见;持久化失败则**明确回退并说明原因**(此前错误被丢弃,静默回落到默认目录);**空串是无操作**(不是"清空") |
| **有效权限** | = **全局实时策略** ∧ 工作流 `permissions` 段(**只收紧**,求交)。① 全局策略**实时**读取 → 收紧立即对在跑的执行生效;② `permissions` 段**永远无法放宽**全局禁用 → 放宽尝试在校验期报 `PERMISSION_OVERRIDE_LOOSENS` |
| `permissions:` 段错层级 | 顶层 `permissions:` 是正式写法;错层级(如写在 `workflow:` 段下)报 `DSL_MISPLACED_KEY` |
| `git_working_dir` **存在性校验**(轮次 81 / 第十七轮变更) | 落库前校验**目录必须存在且是目录**;不合法 → **400**,**一项都不改**。此前存一个不存在的目录也"保存成功",之后每个 git / script / agent 节点都因 `cmd.Dir` 不存在而失败,报错与设置页毫无关联。**空串 = 清除**,不需要存在性校验。`Settings.Set` 复用**同一套**校验 |
| `POST /api/agents/{id}/permission` 的**三态**(轮次 81 / 第十七轮变更) | **局部更新**:载荷里**显式给出**的能力位被改写,**缺省的保持原值**。此前按"整表覆盖"解释(缺键 → false):客户端只发 `{"filesystem_read":true}` 会把其余能力**静默置 false** —— 能力被吊销而用户什么都没收到。**非布尔值 400**;**未知键忽略** |
| Agent 配置 `env` 的**第四态**(轮次 81 / 第十七轮变更) | **缺省 = 不修改** / **`{}` = 显式清空全部** / **`{k:v}` = 整表替换**。此前"没给"被当成"清空全部":一个只想改模型的请求会顺手删掉全部凭据,而用户只看到"已保存"。env **表内部**仍是约定 5 的三态(`***`=保留 / 空串=删除 / 无原值的 `***`=丢弃) |

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

> **第二十九轮(#345)补的一条**:PEM 嵌在**键值赋值形态**时,掩码结果里会留下
> `…-----***`(占位符紧跟 BEGIN 行)—— 旧正则**不认这个已掩形态**,二次脱敏
> 会**再追加**一段占位符 → `…-----******` → **库里那份与接口返回那份对不上**
> (实测:`TestMaskIsIdempotent` 此前只覆盖**裸** PEM,没覆盖赋值内 PEM)。
> 现在 PEM 正则接受"已掩形态"(占位符紧跟 BEGIN 行 / 单独成行)→ 二次脱敏
> 成为**恒等变换**。回归锁:`TestMaskIsIdempotentForPEMInAssignment`(8 种形态)。

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
| **随机 base64 裸值**(≥24 字符 + 大小写混合 + **含数字**) | 脱敏 | 脱敏 |
| `++AKIA…` / `--AKIA…`(**双 diff 行前缀**) | 脱敏 | 脱敏 |
| `password: hunter2`、`token=abc123`(低熵) | 脱敏 | **不脱敏**(有意取舍) |
| 代码标识符(`const token = getToken();`)、英文句子、文件路径 | **不脱敏** | **不脱敏** |

**三条刻意保留的取舍**(不是漏网,登记在开放清单里):

- **制品出口的低熵值不脱敏** —— diff 里的 `password = "xxx"` 多半是**代码或
  占位符**,抹掉会毁掉**审计证据**;DB / API / 日志出口走**键名规则**,照常脱敏。
- **高熵裸值的 24 字符门槛** —— 裸值语境没有键名线索,门槛必须比键名语境
  (≥16 或 ≥32)**严**,否则 40 位 git SHA、base64 测试夹具会被抹掉。
- **驼峰标识符护栏(第二十九轮 / #342 收窄)** —— 护栏的**本意**是保护
  `getToken` / `apiKey` 这类**代码标识符**不被当凭据抹掉(否则 diff 的
  审计价值被毁)。旧口径只有"大写后紧跟小写 ≥4 次"一条 —— 随机字符里
  这个形态的概率约 16.5%,32~64 字符的串**几乎必然**满足 → 护栏事实上
  等于"高熵串豁免",随机 base64 **194/200 原样输出**。新口径要求**五条
  同时成立**才豁免:① 起伏 ≥4 ② 串是**驼峰词序列**(每词 = 大写段+小写段+
  数字段,大写段只允许出现在首词)③ 每个词 ≥2 字符 ④ 每个词**含小写字母**
  ⑤ **数字段 ≤1 处**(标识符的数字是版本号 / 时间戳这类**单一后缀**;
  随机 base64 的数字散落各处)。**实测**:200 个随机 base64 样本全部遮住,
  22 个代码标识符(含仓库里真实的 `TestScriptNodeHonorsTimeoutAndWorkingDir1637892132`)
  全部保留。**残余边界**(登记在开放清单第 78 条):不含数字的随机 base64
  (约 1.6%)不满足高熵门槛(门槛要求"含数字");构造得恰好像驼峰词序列的
  随机 base64 仍有约 0.02%~0.09% 的残余豁免。

> **为什么这值得单列一节**:此前"规则覆盖了哪些形态 × 哪些出口"**从来没被
> 断言过** —— 改规则不会让测试变红,少一条规则也不会。现在矩阵是**可执行的**
> (`TestCredentialShapeCoverageMatrix`),**17 形态** × 2 出口**逐格**登记期望,
> **未覆盖的格子必须写清理由**。将来谁改门槛,矩阵立刻变红,取舍必须被
> **重新论证**一遍。

### 11. `behavior` 字段的掩码(第十三轮已修,#255)

> **已关闭**(commit `aa88aee`)。写在这里是为了让"字段级的掩码覆盖面"可查 ——
> 将来再加新字段时,这一节就是对照表。

`GET /api/agents/{id}/config` 返回的 `AgentConfig` 里,`env` 的值与
`extra_args` 里的凭据**都**被掩码为 `***`;**`behavior`(自由映射
`map[string]any`)此前没有走掩码** → 该字段里若含凭据形态的值,会**原样返回**
(违反约定 8"API 返回掩码")。

**现在的行为**:`AgentConfig.Masked()` 把 `behavior` 一并纳入掩码,走
`logx.MaskValue` 的**键名 + 值形态**规则 —— `behavior` 是自由映射,不能按值
形态**盲抹**(会毁掉合法配置),但**必须过一遍值的形态规则**。

**回写语义(约定 5)**:前端打开配置拿到的是掩码副本,保存时会把整份配置
原样回传 → `***` 哨兵必须按**同路径**换回已存值(`MergeMaskedBehavior`),
否则用户只改了个模型名,就把凭据覆盖成了哨兵。同路径的必要性:`behavior`
是嵌套的 `map[mode]map[field]value`,同名字段在不同 mode 下互相独立。

**验证**:`agent/config_behavior_test.go`、`app/application/behavior_mask_test.go`。

### 12. 取消执行的三种语义(第十五轮变更)

> 登记于 `docs/OPTIMIZATION.md` #264(取消)与 #268(取消先到、协程后启动)。
> **第十五轮已进主干**(commit `908ba0b`),接口层的语义已按本节实现。

`POST /api/executions/{id}/cancel` 的返回值由**库里执行记录的状态**决定
(约定 4 的唯一裁决来源),**不看进程内的句柄**:

| 库里的状态 | HTTP | `code` | 含义 |
| --- | --- | --- | --- |
| `RUNNING`(且执行协程还活着) | **200** | — | 取消成功。状态由执行协程收尾时 CAS 落成 `CANCELLED`,`workflow.cancelled` 事件也由它发出 |
| `COMPLETED` / `FAILED` / `CANCELLED` | **400** | `INVALID_TRANSITION` | **取消失败**。取消一个**已经结束**的运行不是幂等删除 —— 约定 6 的"幂等返回 200"只适用于**删除资源**,不适用于状态迁移 |
| 不存在 | **404** | `NOT_FOUND` | 执行 ID 不存在 |
| `CREATED` / `WAITING_USER` | **200** | — | 走 CAS 裁决:落成 `CANCELLED` 并发 `workflow.cancelled` 事件。并发取消**只产生一次**状态变更与事件。**非终态节点同时落成 `SKIPPED`**(节点级视图 + `execution_nodes` 明细)—— 这条路径没有协程会再来收尾它们,不收尾就是"状态与事实不符"(轮次 84) |

> **`PAUSED` 已不在这个表里**:它曾是一个执行状态,但**从来不可达**
> (没有任何路径会把执行迁移到它),轮次 84 已把它从状态机里移除。
> **老库**里可能留有 `state='PAUSED'` 的行 → **读出口归一为 `CANCELLED`**
> (`persistence.NormalizeExecutionState`),所以 API 永远不会返回 `PAUSED`。

> **为什么不能用"句柄在不在"判断**:终态是**先写库**的,执行协程的句柄由
> `defer` 摘除 —— 两者之间有 ≈**210µs** 的窗口(终态事件同步落库那段)。
> 窗口内取消一个**已经完成**的执行会走"运行中"快路径 → API 回 200"已取消",
> 而执行**早已 COMPLETED**(实测:紧密轮询 **40/40** 次都能落进窗口,
> 修前 40/40 拿到 200 假成功;修后 40/40 命中窗口但 **0/40** 假成功)。
> **修法**:快路径也**先读库里的状态**。

> **库里 `RUNNING` 但句柄已摘除**(执行协程收尾时终态提交 CAS 冲突,状态停在
> `RUNNING`)→ 仍然**可以被取消**。否则用户只能看着一个永远"运行中"的执行
> 干瞪眼。

**取消先到、协程后启动**:若取消请求在执行协程**启动之前**到达(例如
Resume / Retry 的 CAS 已把状态落成 `RUNNING`、但句柄还没注册),协程
**启动即核对库里的状态** —— 已是 `CANCELLED` 就立刻收尾,**不做任何节点
执行**。否则库里写着"已取消",副作用却已经发生(约定 14 同族的"状态与事实
不符")。

**并发取消**:状态变更走 CAS,只产生一次 `CANCELLED` 与一次
`workflow.cancelled` 事件(约定 4)。

### 13. CLI 参数(argv)是凭据的**本机可见**出口(轮次 81 / 第十七轮变更)

**问题**:Agent 配置里的 `extra_args` 会被原样拼进子进程的**命令行参数**。
而 argv 对**本机所有进程**可见 —— `ps aux`、`/proc/<pid>/cmdline`、各类
进程监控与审计工具都能读到。日志与 API 出口的脱敏**管不到它**:凭据在
argv 里就是半公开的。

**现在的行为**:两个 CLI 适配器(`claude-code` / `pi-agent`)**启动 CLI 之前**
检查自己带来的那部分参数(Agent 配置的 `extra_args`;pi 侧对应
`pi.Config.DefaultArgs`),只要有**按值形态判定为凭据**的项就发一条
WARN 日志:

- 判定口径**唯一来源**:`logx/credential.go`("值的形态"规则全仓只有一份,
  `logx.MaskCredentialValues(v) != v` ⇔ v 呈凭据形态);
- 告警只报**位置**(第几个参数),**不回显值** —— 告警本身不能成为新的
  泄漏面。

**为什么不把 argv 里的值脱敏后再下发**:掩码哨兵 `***` 不是真凭据,下发过去
CLI 鉴权**必然失败**(约定 5:哨兵只表示"这里曾经被掩过")。所以这里
**只告警、不改写**,由用户自己把凭据挪到环境变量。

**建议**:凭据放 Agent 的 `env`(只在子进程里生效,别的进程读不到它的
environ);`extra_args` 只放非敏感参数。

> **本轮不做的事**:prompt / task 全文也进 argv(claude 是 `-p <prompt>`,
> pi 是 `--task <task>`)。改成 stdin 需要先确认两个 CLI 是否支持、以及
> 大段文本走 stdin 的行为差异 —— 风险不可控,本轮**不动**,登记为**长期方案**
> (`docs/OPTIMIZATION.md` 第十四节第 72 条,#284)。

**验证**:`agent/claude/claude_argv_test.go`、`agent/pi/pi_argv_test.go`
(告警必须发出 + **下发的仍是原值**,防止将来有人"顺手"加一层脱敏把鉴权改坏)。

### 14. Mock 行为脚本(`behavior`)的结构契约(轮次 81 / 第十七轮变更)

`behavior` 是 Mock Agent 的演示脚本,前端给的是**自由 JSON 文本框**。
它的结构契约现在**明确写死**:

```
behavior := { <mode>: { decisions?: string[], summary_template?: string, fail_with?: string } }
```

**两条变化**:

1. **逐键合并**:用户只写 `decisions` 时,内置默认脚本里的
   `summary_template` **保留**。此前是 `merged[mode] = raw` **整体替换** ——
   用户写一个键,默认脚本的其余键全丢(约定 14:不该动的却动了)。
2. **未知键 / 错类型在保存期报错**:`PUT /api/agents/{id}/config` 返回
   **400**、`code=BAD_REQUEST`,`error` 里指出出问题的 mode 与字段名。
   此前未知键被 `continue` **静默丢弃** —— 用户把 `decisions` 写成
   `decision`,脚本不生效,界面却提示"已保存"(约定 14:静默丢弃与崩溃同样危险)。

**例外:凭据位放行**。`behavior` 是自由 JSON 框,用户可能把 `api_key` 这类
凭据放进去 —— 那是既有契约(GET 掩码、往返按同路径合并,见第 11 节)。
判定口径与掩码**同源**:`logx.MaskValue` 会把它的值换成 `***` 的键就是
凭据位,这类键**不报"未知"**。

**适用范围**:只对 Mock Agent(`mock-claude` / `mock-pi` / `mock`)生效 ——
`behavior` 只被 `buildMock` 读,别的 Agent 上它是**死配置**,不该因为它
挡住保存。

**验证**:`app/application/mock_behavior_test.go`。

### 15. 节点 `retry` 子键的类型契约(轮次 81 / 第十七轮变更)

```
retry := { max_attempts?: 非负整数, backoff?: string }
```

- `max_attempts` 必须是非负整数。合法形态:YAML 整数、JSON 整数、
  `json.Number`;**字符串不合法**(`"3"` 是笔误,不是"重试 3 次")。
  上限 **10 次**(运行期钳制),`0` 与缺省同义(不重试)。
- `backoff` 必须是字符串(`fixed` / `exponential`)。

**两层结论一致**(约定 13:校验期与运行期同源):

| 层 | 位置 | 行为 |
| --- | --- | --- |
| 校验期 | `workflow/validator` | 报 `INVALID_NODE`(节点定义非法) |
| 运行期 | `workflow/runtime` | **fail closed**:节点失败并说明原因(`retry.max_attempts 必须是整数(实际是 string)`) |

此前运行期用 `toInt` 读 `max_attempts`,字符串 `"3"` 认不出来 → `0` →
钳到 `1` → **重试被静默关闭**:用户以为重试 3 次,实际只跑 1 次,节点照常
SUCCESS(约定 14:该失败却成功)。

> `workflow/compiler/nodeconfig.go` 的契约表(唯一来源)未变:`retry` 的子键
> 仍是 `max_attempts` / `backoff`;子键的**类型规则**写在
> `workflow/validator/validator.go`,运行期实现写在
> `workflow/runtime/executor.go` 的 `retryPolicyOf`。

**验证**:`workflow/runtime/retry_policy_test.go`、
`workflow/validator/retry_config_test.go`。

### 16. 备份的 `version` 校验与 `skill_configs` 段(轮次 85 / 第二十一轮变更)

```
备份 := {
  version?: number,          // 必填:缺失或不认识 → 400 BAD_REQUEST
  exported_at?: string,
  workflows?: [...],
  agent_configs?: { [agentID]: AgentConfig },
  skill_configs?: { [skillID]: boolean },   // 可选:skillID → 是否启用
}
```

| 变化 | 修前 | 修后(轮次 85) |
| --- | --- | --- |
| `version` | **只写不读**(导出恒为 1,恢复时从不看)→ 任何 JSON 都被当备份照单全收,用户看到"恢复成功"却什么也没恢复 | **恢复端校验**:缺失 → `400 BAD_REQUEST`;不认识(如未来的 2)→ `400 BAD_REQUEST`(fail closed:当前版本读不了新格式,硬读会静默丢字段) |
| `version: 1` | — | **正常路径**(向后兼容:现有备份的值恒为 1) |
| `skill_configs` | **不存在**(技能禁用状态在备份里彻底消失 → 换机器恢复后此前禁用的技能又全部可用,而禁用的技能在运行期会被拒绝调用) | 导出时带上**本机全部技能**的启用状态;恢复时**只对本机确实存在的 Skill 写回**,其余跳过并计入 `skill_configs_unknown_ids` |
| 老备份(无 `skill_configs`) | — | **视为"不恢复技能状态"**:不报错、**不改动本机的技能状态** |

**恢复响应新增两个字段**:`skill_configs_restored`(已写回的技能数)与
`skill_configs_unknown_ids`(本机不存在的技能 ID 列表)。老备份恢复时
`skill_configs_restored` 为 `0`。

> 导出格式**版本号不变**(仍是 1):新增的 `skill_configs` 段是**可选**的,
> 老版本读到它会忽略 —— 双向兼容。

**验证**:`app/application/backup_skill_test.go`(7 例)。

### 17. `GET /api/modes`:mode 取值域的对外出口(轮次 85 / 第二十一轮新增)

```
GET /api/modes  →  ["plan", "execute", "review", "fix", "test", "submit"]
```

- 唯一来源是 `workflow/model.AllModes()`(约定 13)—— 端点里**不另写**
  一份字面量清单(`api/modes_test.go` 的守卫会立刻变红)。
- 桌面模式走 Wails 绑定 `WorkflowService.Modes()`,与本端点**同一份数据**
  (约定 10;`api/modes_test.go` 的跨模式用例锁住)。
- 前端的设计器下拉据此渲染;加载中/失败时用**兜底清单**兜底(控件不留
  空),兜底清单同样必须与 `model.AllModes()` 逐项一致(跨语言守卫)。

**验证**:`api/modes_test.go`(3 例)、`frontend/src/lib/api.test.ts`(+2 例)、
`frontend/src/pages/designer.modes.test.tsx`(8 例)。

### 18. 落库前的数值必须**有限**(轮次 86 / 第二十二轮变更)

工作流定义里出现 **NaN / ±Inf** 时,导入与保存**一律拒绝**。

```
POST /api/workflows/import
  DSL: variables: { env: .nan, limit: 10 }
→ 400
  { "code": "WORKFLOW_INVALID",
    "kind": "ValidationError",
    "error": "ValidationError(WORKFLOW_INVALID): [{\"code\":\"INVALID_NODE\",\"message\":\"variables.env 的值必须是有限数值(不能是 NaN 或 Inf)\"}]" }
```

| 变化 | 修前 | 修后(轮次 86) |
| --- | --- | --- |
| 含 `.nan` / `.inf` 的工作流 | **导入成功(200)**,但 `variables_json` 被写成**空串** → 读回来是**空 map**(旁边合法的 `limit: 10` 一起丢)→ 运行期条件恒假 → **静默走错分支**,执行仍 `COMPLETED` | **400 `WORKFLOW_INVALID`**,错误信息指出出问题的变量路径;工作流**根本不被创建**(`GET /api/workflows/{id}` → 404) |
| 错误码 | — | `INVALID_NODE`(复用既有码:前端 `CODE_HINTS` 已有该码,不必同步前端) |
| 检查范围 | — | `variables` / 每个节点的 `config` / `permissions` 段,**递归**查 `float32` / `float64` / `json.Number` 里的 NaN/Inf |
| 持久层兜底 | `json.Marshal` 的错误被吞 → 列写成空串 | **序列化失败即报错**(`persistence.marshalJSON`):`SaveWorkflow` / `SaveExecution` 失败且**一行都不落库** |
| 老库兼容 | — | 老库里上一轮 bug 写进去的**空串 `variables_json`** 读成**空 map**,**不报错**(读出口不放宽,写出口收紧) |

> **为什么是 400 而不是 500**:这是**用户输入**的问题(DSL 里写了非法数值),
> 与"内部错误"无关 —— 与既有的 `WORKFLOW_INVALID` 同口径。

**验证**:`persistence/finite_json_test.go`(7 例)、
`workflow/validator/finite_test.go`(6 例)。

### 19. `timeout_seconds` 的取值范围(轮次 86 / 第二十二轮变更)

节点级 `timeout_seconds`(agent / script 节点)的取值必须是 **0..86400 的整数秒**。

```
节点配置: { timeout_seconds: 10000000000 }
→ 校验失败: INVALID_NODE
  "节点 \"n1\" 的 timeout_seconds 必须在 0..86400 之间(实际是 1e+10)"
```

| 值 | 含义 | 修前的后果 |
| --- | --- | --- |
| `0` | **不设节点超时**(运行期按 `req.Timeout > 0` 判断,走适配器默认超时) | — |
| `1..86400` | 节点级超时秒数 | — |
| `> 86400` | — | `time.Duration(raw) * time.Second` **溢出成负数** → **立即超时** |
| `1e20`(float) | — | `int(1e20)` 溢出成负 → 运行期 `req.Timeout > 0` 判假 → **静默改用适配器默认 600s**(用户配的值根本没生效,无任何告警) |
| 非整数 / 非数值 | — | 运行期静默取 0 / 截断 |

- 范围常量只有一份来源:`workflow/compiler` 的 `TimeoutSecondsMin/Max`
  (约定 13:校验期与运行期同源)。**下界取 0** 是刻意的:0 的语义是
  "不设节点超时",判成非法就是"用户被自己的校验器挡住"。
- **运行期钳制**(把超出上界的值钳到 24h)尚未落地 —— 精确改法见
  `docs/OPTIMIZATION.md` 轮次 86 的开放项 **#312**。

**验证**:`workflow/validator/timeout_seconds_test.go`(4 例)。

## SSE 实时事件流(GET /api/events)

- 连接建立立即返回 `text/event-stream` 响应头与注释行(`: connected`),
  EventSource 立即进入 OPEN 状态;
- 之后每个 UIEvent 以 `data: {JSON}\n\n` 推送;
- 每 15 秒发送 keepalive 注释行(`: keepalive`)防止代理断开空闲连接;
- 客户端断开由 EventSource 自动重连。
- **写失败即关流**:此前写失败只记一条 Debug 日志并继续循环 → 连接不释放
  (订阅不注销、goroutine 常驻、keepalive 继续写)。现在写失败立即结束流
  并注销订阅;建连即失败(对端不可达)则根本不订阅。
- **失败的事件不会以正常事件的形态发出**:载荷不可 JSON 化(NaN/Inf)的事件
  在写流前被跳过(记一条 WARN:事件类型 / 执行 ID / 节点 / 原因),**流本身
  继续**(一条坏事件不掐断整条流);运行期改发一条 `runtime.error`
  (`reason=event_persist_failed`)作为显式失败信号。详见下文
  "`runtime.error` 的三种来源"。

## 实时事件类型(UIEvent.type)

`workflow.started / resumed / waiting_user / completed / failed / cancelled`
`node.started / completed / failed / waiting`
`agent.started / output / completed / retry`
`review.approved / review.rejected`
`human.input_required / human.input_received`
`skill.executed / git.executed / merge.completed`

> **`workflow.paused` 已删除**(轮次 84):`PAUSED` 那个执行状态**从来不可达**,
> 这个事件**从未被发出过** —— 留着它等于对外承诺一个不会发生的事
> (订阅它的前端会永远等不到)。
`node.warning`(模板变量缺失,渲染为空串)/ `runtime.error` / `runtime.warning`
(如并行分支不收敛:各分支终点互不相同,分支之后的链不会执行)
`runtime.escalate`(ADK Escalate 的分级:`kind=expected` 是预期内的循环出口
或 HITL 暂停,`kind=unexpected` 是节点失败 / 循环上限 / 循环停滞)

**`node.warning` 的两种 `data.reason`**(第十九轮新增第二种):

| `data.reason` | 含义 | `data` 字段 |
| --- | --- | --- |
| `missing_template_vars` | **键不存在**:提示词里引用的变量**在数据表里根本没有**(例:拼错的 `{{revwie}}`) | `missing`(变量名列表)/ `message` |
| `empty_context_slots` | **键存在但值为空**:上下文链的槽位**存在**,但**上游没产出内容**(例:`{{plan}}` 的上游节点成功却没有产出)→ 渲染成空串,下游拿到的是**空上下文** | `slots`(槽位名列表)/ `message` |

两者**都不让节点失败**(空上下文下 Agent 仍可能凭自身知识给出有效结论,
硬失败会把"可选引用"变成硬约束),但**都必须可见** —— 用户要能分清
"**我拼错了变量名**"与"**上游没给我东西**"。第十九轮之前只有第一种:
第二种(上游没产出)是**静默**的,用户以为"方案传给下游了"。
判据的来源与边界见 `docs/OPTIMIZATION.md` #280。

所有事件的 `data` 在分发点统一脱敏(键名为 token/api_key/password 等
时整值替换为 `***`,值含 `key=value` 形态时就地替换),落库 / SSE /
Wails IPC 三条出口一致。

**`runtime.error` 的三种来源**(第四、五轮;第三种见下):

| `data.reason` | 含义 |
| --- | --- |
| `state_commit_conflict` | **状态迁移没有落库**(CAS 冲突或落库错误)。库里的状态已被别的写者裁决(用户取消 / 显式重试 / 崩溃恢复)—— 本次迁移被放弃,**终态事件不再发**(否则事件流说"已完成"、库里还是"运行中",UI 与审计流互相打架)。`data` 带 `target_state`(想落的状态)/ `current_state`(库里的实际状态)/ `error` |
| `event_persist_failed` | **事件本身没有落库**(载荷不可 JSON 化 —— NaN/Inf;或 DB 故障)。`data` 带 `event_type`(没落库的那条事件的类型)/ `persist_error`。**带上执行 ID 正是它的价值**:运维据此知道是**哪一次执行**的审计流缺了记录 |
| (无 `reason`) | 节点状态机非法转换等既有的运行时错误 |

> **`event_persist_failed` 的两级防护**(迭代 26 收口,#313):事件落库失败后
> 运行期发这条 `runtime.error` 作为**显式失败信号**(计数 + ERROR 日志 +
> 这条事件);它自己也要落库 —— 载荷是普通字符串,落库**成功** → 链路到此为止;
> 万一它也落库失败(DB 整体坏了),运行期的 `persistFailGuard` 仍在位 →
> **不再**发第二条,最多一级嵌套,不级联。
>
> 因此 SSE 流里**失败的事件不会以正常事件的形态出现**(它的载荷序列化失败,
> 写流前被跳过并记一条 WARN),取而代之的是这条 `runtime.error`。
> 判据是**顶层 `type` 字段**(`"type":"node.started"` 不得出现),**不是**
> "执行 ID 不得出现" —— 后者过粗:失败信号本来就该带执行 ID。

取消路径上的同类冲突**不发** `runtime.error`(那是用户高频操作造成的预期
竞态,记 WARN 即可),以免 ERROR 噪音淹掉真正的异常。可观测性靠
`Engine.StateCommitFailures()` 计数与库里的状态。

**脱敏的路径清单**见上文"契约变化 → 4. 节点 `output` 与会话状态
`state_json` 的脱敏"(事件流 / 节点明细落库前 / 节点明细读出口 /
会话状态快照,共四条独立路径)。
