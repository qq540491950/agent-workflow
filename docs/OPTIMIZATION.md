# 优化修改方案 — 轮次 27 全面审计

> 方法:三轮并行只读审计(workflow 运行时+编译器 / 持久化+API+应用层 /
> 前端+双模式契约),共提出 41 条,逐条人工复核源码后定级。
>
> **轮次 27 完成**:22 条当场修复并提交(6 P0 + 8 后端 P1 + 10 前端)。
> **轮次 28 完成剩余全部登记项**:批次 A(A1-A8 运行时语义)、
> B(B1-B2 并行编译正确性)、C(C1-C5 测试基建)。
> 缺陷编号与回归用例见 `docs/TESTING.md` 轮次 27/28。
>
> 与前几轮的关键差别:轮次 24-26 的缺陷多数是**崩溃/挂起**;轮次 27 的
> 6 个 P0 里有 4 个属于**静默成功**——工作流语义上没走完,系统却报告成功。
> 这类缺陷不会触发任何异常路径,是此前"门禁全绿"却仍有 P0 的根本原因。
>
> **轮次 33-40(两轮并行审计,8 个子代理)**:见本文第四、五节。
> **轮次 41-52(三轮并行复审,10 个子代理)**:见本文第六、七、八节。
> **轮次 53-56(第六轮,3 个子代理)**:见本文第九节。
> **轮次 57-60(第七轮,4 个子代理)**:见本文第十节。
> **轮次 61-70(第六至十轮补登,commit `60780a5` / `bb5cd63` / `e5971ed` /
> `795f26f`)**:见本文第十一节(#212-#244)。
> 编号说明:缺陷号沿用 `#95` 起(轮次 31 结束于 #94);轮次号因
> `docs/TESTING.md` 的轮次 32(可视化/端到端验证)已占用,从 **33** 起排。
> 缺陷号自第六轮起从 `#189` 续(轮次 49-52 结束于 #188)。
> 轮次与子代理的对应关系:
>
> | 轮次 | 子代理方向 | commit |
> | --- | --- | --- |
> | 33 | 前端(约定 7 / 8 / 10) | `3f5e68f` |
> | 34 | DSL 与模型层 | `3f5e68f` |
> | 35 | 持久化与事件总线 | `3f5e68f` |
> | 36 | API 与应用服务层 | `3f5e68f` |
> | 37 | 运行时(状态写 / 事件落库 / 告警分级) | `916a9c4` |
> | 38 | 编译器与校验器(约定 13 收口) | `916a9c4` |
> | 39 | Agent / Skill 适配层 | `916a9c4` |
> | 40 | 支撑包(logx / permission / template / git / contextx) | `916a9c4` |
> | 41 | 跨文件收口(后端) | `8520115` |
> | 42 | 前端契约同步 | `8520115` |
> | 43 | 门禁脚本重写 | `8520115` |
> | 44 | 文档与脚本同步 | `8520115` |
> | 45 | `state_json` 脱敏通道 | `4e979eb` |
> | 46 | CAS 冲突可观测 | `4e979eb` |
> | 47 | 恒真测试清理 + 死字段废弃 | `4e979eb` |
> | 48 | DSL 严格化 + builtin 参数守卫 | `4e979eb` |
> | 49 | 运行时交接三处 + 自查六处 | `59ef7d7` |
> | 50 | 前端权限透传 + 校验期未知键告警 | `59ef7d7` |
> | 51 | API 未知顶层键 400 | `59ef7d7` |
> | 52 | 累积退化复审(门禁与死代码) | `59ef7d7` |
> | 53 | 状态键分类守卫扩面 + 统一状态写入口 | `60780a5` |
> | 54 | 日志级别实际生效断言 + 校验告警不丢 | `60780a5` |
> | 55 | 门禁重写(gofmt 清单 / 用例存在性自动推导 / build 产物) | `60780a5` |
> | 56 | 死代码与注释收口 + 文档补第三至五轮 | `60780a5` |
> | 57 | 迭代 22:双模式顶层键契约下沉应用层 | `bb5cd63` |
> | 58 | 迭代 24:条件变量集合 AST 探针 + `asArray` 守卫补第三形态 | `bb5cd63` |
> | 59 | 迭代 23:终审复审(门禁可信度 / 脱敏口径 / 弱测试) | `bb5cd63` |
> | 60 | 迭代 25:门禁 9 步 + eslint 落地 + 冒烟打印端口占用者 | `bb5cd63` |
> | 61-63 | 第六轮补登:守卫防空转双下限 / 状态机一致性交叉测试 / 用例存在性判据细化 | `60780a5` |
> | 64-66 | 第七/八轮补登:门禁硬伤(eslint / `&&` 链 / embed 兜底)+ 双模式契约补全 | `bb5cd63` |
> | 67-68 | 第九轮:`output` 键 resume 口径差根治 + 终审复审 10 场景 + 全域扫描 | `e5971ed` |
> | 69-70 | 第十轮:凭据回写 / 制品脱敏 / 门禁自检 / 前端吞错可见化 / 权限单一源 | `795f26f` |
>
> 前两轮(33-40)两条**新出现的缺陷类别**值得单列:
> ① **门禁自身不可信** —— 门禁跑的是"侥幸通过"的路径(见第四节末的 #119-#121);
> ② **校验期与运行期各写一份** —— 约定 13 此前只有文字,没有任何机制保证
>   两边不漂移(见轮次 38,#124)。
>
> 第三轮起(41-52)出现第三条类别:
> ③ **"静默丢弃"是同一族缺陷的共身** —— 前三轮修的是"崩溃/挂起"与
>    "静默成功",第四、五轮才看清:**键被丢掉、状态被改写、参数变空串**
>    与"该失败却成功"是同一件事的三个切面(约定 14 的完整含义)。
>    本文的"来源"列因此新增一档:**约定收敛** = 硬性约定早就写着,
>    但既没有实现也没有守卫。


## 一、已修复清单(轮次 27)

### P0 — 静默成功 / 静默丢数据(危害最高的四类)

| 编号 | 缺陷 | 触发路径 | 修复 |
| --- | --- | --- | --- |
| P0-1 | 决策点无匹配出口即静默 COMPLETED | review 的条件出口全部不匹配且无默认分支 → 后续节点一个没跑,执行标成功 | 置 `KeyAbort` 落 FAILED,错误里带决策值与已尝试条件,并发 `node.failed` |
| P0-2 | 决策点多条回边只保留第一条 | review 同时有 REJECTED→fix 与 REFACTOR→refactor 两条回边 → refactor 子图永不执行,而校验器判定其"可达"因而通过 | 新增 `KindSwitch` IR 节点(纯条件选择、不重跑决策节点)+ 循环内决策快照键 `KeyLoopDecision` |
| P0-3 | DSL `variables:` 段运行时完全失效 | 工作流里声明的变量不参与 `{{variable.x}}` 渲染与 `variables.x == ...` 条件,条件恒假 | `Engine.Start` 以 DSL 变量打底、运行请求变量覆盖 |
| P0-4 | 校验器比运行时更严,拒绝合法表达式 | `variables.env == "prod"` 被判 `unknown name variables` 而无法保存,但运行期完全支持 | 校验期改为与 `EvalCondition` 同源的 map 形状环境 |

### P0 — 并发与历史丢失

| 编号 | 缺陷 | 危害 | 修复 |
| --- | --- | --- | --- |
| P0-5 | 节点明细按 `exec:node` upsert | 循环第二轮与节点级重试整行覆盖上一轮(attempt 恒为 1),监控页看不到任何迭代痕迹 | 主键改为 `exec:node:attempt`,每次执行独立成行 |
| P0-6 | `DisabledSkills` 裸 map 并发读写 | 执行协程读、REST 开关写 → 生产是 `concurrent map read and map write` **runtime fatal**,recover 无效,整应用退出 | 加 `RWMutex`;顺带修正 `SetEnabled` 写序(先落库再改内存)与 `LogLevel` 无锁字段 |

### P1 — 数据正确性 / 敏感信息 / 契约健壮性

| 编号 | 缺陷 | 修复 |
| --- | --- | --- |
| P1-1 | 工作流版本号由客户端请求体决定(伪造 `version:99` 落库成 v100;唯一约束被 `INSERT OR REPLACE` 静默覆盖,版本历史失真) | 版本号以库中 `MAX(version)` 为准(事务内读取),快照改普通 `INSERT` 让唯一冲突显式报错 |
| P1-2 | `SaveWorkflow` 吞掉存在性查询错误 | DB 故障时对已存在工作流走 INSERT 分支,真实根因被主键冲突掩盖;改为上抛(并加故障注入测试) |
| P1-3 | 事件流三个出口(落库 / SSE / Wails IPC)不脱敏 | 在 `Bus.Emit` 分发点统一脱敏一次,三条出口同时生效,新增订阅者不会漏 |
| P1-4 | `retry` 端点吞请求体解析错误 | `{"skip":"yes"}` 被吞后 skip 静默变 false,「跳过失败节点」实际重跑失败 Agent(可能重复写文件/重复外部调用)还返回 200 |
| P1-5 | `enable` 端点 fail open | `value != "false"` 让缺参、?value=0、?value=no 全部判为"启用"——禁用开关缺参数反而打开工作流 |
| P1-6 | JSON 请求体无上限 | 服务器模式监听 127.0.0.1 且无鉴权,一个大 body 即可 OOM;统一 `MaxBytesReader` |
| P1-7 | 备份恢复绕过校验 | 悬空边/空图的非法定义会落库并在列表里正常可见,直到运行时才炸(与 `ImportYAML` 严格度不一致) |
| P1-8 | events(type) / executions(created_at) 缺索引 | 审计页 type 前缀过滤与执行列表排序退化为全表扫描,events 是唯一无上限增长的表 |

### 前端(10 条,均已修复)

| 编号 | 缺陷 | 危害 |
| --- | --- | --- |
| F1 | `versionDSL`/`exportYAML` 绕过 `http()` 且不检查 `res.ok` | 后端 JSON 错误体被当 DSL 弹进对话框,或写进下载的 .yaml |
| F3 | `http()` 丢弃后端 `code`/`kind` | 403 权限不足与 400 参数错误在 UI 上无法区分 |
| F4 | Agents Test 按钮无 catch | 失败是 unhandled rejection,用户点了按钮完全没反馈 |
| F5 | 权限切换用陈旧闭包快照 | 快速连点最终态与用户意图相反;失败无回滚 |
| F6 | Executions 表格删除按钮在第 2 列 | 与表头错位,视觉上删除按钮落在 Workflow 列下 |
| F7 | ExecutionMonitor 三处列表未过 `asArray` | 桌面 IPC 返回 null 时整页白屏 |
| F9 | GitPanel commit 无二次确认 | `git add -A` + 回车即提交全部改动,不可逆 |
| F10 | Agent「恢复默认」能力空洞 | 后端 REST 与绑定都就绪,前端两个分支都没封装、UI 无入口 |
| F13 | 重试/跳过按钮无 busy 态 | 连点并发打多次 /retry,多个 toast 混杂 |
| F14 | layout 用 `?? []` 而非 `asArray` | 挡不住"被解析成 HTML 兜底页的对象",Layout 崩溃影响所有页面 |

## 二、已修复清单(轮次 28:批次 A/B/C)

### 批次 A:运行时语义补齐

| 项 | 缺陷 | 修复 |
| --- | --- | --- |
| A1 | Agent/Skill 返回 WAIT_USER 后,用户答复被完全丢弃,Agent 被**第二次真实调用**(execute/fix 模式重复写文件、重复跑测试);reject 更是被静默吞成 COMPLETED | 语义定为「复用已有响应」:恢复时把上次结果快照存入 `AnsweredKey`,节点入口消费 `_user_input` 后直接按已答复完成,不再调 Agent;reject 仍走 FAILED |
| A2 | 模板缺失变量静默渲染空串,`missing` 列表被 `rendered, _ :=` 丢弃 | 去重排序后发 `node.warning` 事件 + `logx.Warn`。只告警不让节点失败(部分变量确实可选);刻意不用 `node.waiting`(语义是"暂停等人工",会误导前端渲染 HITL 面板) |
| A3 | 节点级状态机 `CanTransitionNode` 从未接线(只在测试里被调用) | 写回 `StatusKey` 前校验,违反记 `runtime.error` 让问题可见(不阻断执行:状态已是既成事实) |
| A4 | 并行分支失败不级联:失败走 `yield(escalateEvent, nil)`,ADK errgroup 只在 Go error 时 cancel,兄弟分支继续跑完 | 给并行区包一层共享可取消 ctx + abort 监视协程,任一分支 `FAILED:` 即取消其余分支;用 `cascadeCtx` 包装 InvocationContext 只替换取消语义。只认 FAILED 前缀,WAIT_USER 不会误杀 |
| A5 | merge 汇总失真两处:①按入边统计而入边 ≠ 并行区分支(分支内嵌路由时条件不成立的分支本就不该跑);②"查不到状态"一律算成功 | 从"喂给 merge 的 parallel 节点"取分支清单;区分「无条件扇出无状态=被截断(失败)」「条件分支无状态=正常路由(标注)」「显式 SKIPPED(标注)」。失败或必跑分支未完成时 merge 落 FAILED |
| A6 | `Start` 重置 Mock 打断同工作流并发执行 | `AgentRequest` 带 `ExecutionID`,mock 按执行隔离计数空间;`ResetMock` 只清无执行上下文的共享空间 |
| A7 | pi 适配器 `LastIndex("{")` 命中内层 `{` 时片段不配平 → decision 静默丢失、路由恒走默认分支 | 新增 `coreagent.TrailingJSONObject` 做括号配平(跳过字符串字面量与转义引号);claude 侧同问题一并改用 `FirstJSONObject` 统一 |
| A8 | Agent stdout 无上限捕获,截断只发生在落库层 | 新增 `coreagent.BoundedBuffer`(8MB 上限,超限计数并在 `String()` 追加显式截断标记);claude/pi 双方接入 |

### 批次 B:并行编译正确性

| 项 | 缺陷 | 修复 |
| --- | --- | --- |
| B1 | `irBuilder.stopped` 是共享字段,且 `parallel()` 给内层分支的停止集合**不含外层的** → 分支一路走到并行区汇合点及其后所有节点。后果不是"多跑一次":并行分支由 ADK 并发执行,等于多 goroutine 并发写同一批状态 | 停止集合显式沿调用链传递(`chain`/`chainStop` 合并),`parallel`/`route` 把外层 stop 合并进内层分支(`copySet(outerStop) ∪ {innerJoin}`),彻底删掉共享字段 |
| B2 | 并行分支不收敛时塞一个合成的 `<parID>.merge` 占位节点(工作流里并不存在,落到 lower 变 noop),后续链被静默丢弃而执行报成功 | 新增 `KindWarning` IR 节点(只发告警不执行):标记 `Plan.Unconverged`,各分支照常跑完并发 `runtime.warning`,明确告知"没有公共汇合点,分支之后的链不会执行" |

### 批次 C:测试基建

| 项 | 缺陷 | 修复 |
| --- | --- | --- |
| C1 | 组件层交互行为零测试(二次确认、busy 防连点、乐观回滚都只有"看起来对"的代码) | 新增 `interaction.test.tsx` 11 个用例,覆盖上述三类语义 + 四种异常列表返回 |
| C2 | REST 契约测试仅覆盖少量端点 | 补齐至"每端点至少一条契约测试",4xx 一律断言错误体同时含 `error` 与 `code` |
| C3 | `api.test.ts` 把 `@bindings/*` 整个 mock 成 `{}` → 依赖它的用例**静默失败而非报错**,绑定漂移测不出来 | Proxy 桩:访问未声明导出立刻抛错;清单与 `frontend/bindings/` 下真实生成文件对齐,并加一条直接比对的用例 |
| C4 | 无故障注入,DB 故障只能靠"关掉 DB"粗略模拟 | `persistence.DB` 增加 `SetQueryFault` 语句级注入点,接在 `SaveWorkflow` 存在性查询与 `SaveSetting` |
| C5 | 备份三个 API 方法零 UI 消费 —— "看起来有能力实则不可达" | Settings 页新增「备份」卡片(导出 + 从文件恢复,带确认与恢复/跳过/拒绝三组计数) |

## 三、轮次 29:结构性风险的处理结果

第 1-3 项已在本轮处理(第 4 项在轮次 28 已完成)。

### 1. 状态三处写入 —— 已按"中间方案"收敛(未做大重构)

先澄清一个事实:**不是"三处都写"**。运行期 `exec.StateData` 完全不被写,
唯一真权威是 ADK session state;`StateData` 只在 `collectResult` 末尾整体
快照一次。真正的问题是**两个旁路写入者方向相反**。

- **float64 症状的根源已定位并消除**:`StateData` 执行期是 int,经 SQLite
  `state_json` 往返后 `encoding/json` 把数字全解成 float64 —— 同一字段的
  类型随生命周期变化,于是每个读取点都得写双类型兼容(`IntOfOk` 就是这么
  来的),漏一处就是静默 bug。新增 `model.NormalizeStateJSON`,在**唯一的
  解码入口**(`GetExecution` / `ListExecutions`)把整数值的数字统一转回 int。
  类型在整个生命周期内稳定,`IntOfOk` 退化为纵深防御而不再是必需品。
  1.5 小数等真浮点值不受影响。
- **#81 `abortNoRoute` 只写 `NodeStates` 不写 `StatusKey`(P0)**:决策节点
  自身已把 StatusKey 写成 SUCCESS(它确实成功了,只是没有出口能走),
  而 `collectResult` 结束时用 StateData **整体替换** NodeStates ——
  结果那个 FAILED 被覆盖回 SUCCESS。实测确认 UI 上"决策点失败"退化成
  成功;且 merge 读的是 StatusKey,下游同样看不到失败。改为双写。
- **#82 `syncNodeStates` 是增量覆盖,派生视图必须全量重建(P1)**:StateData
  里被 `delete` 的 key,其旧值永远留在 NodeStates 里 —— `RetryNode(skip=false)`
  正是 `delete(StateData, StatusKey)`,于是节点在 UI 上仍显示上一次的 FAILED。
  `loopResetter` 的空串哨兵也会被 `fmt.Sprint("")` 复制成空状态。
  改为新建表 + 整体替换,视图与真值同生共死。
- **#83 nil 被渲染成字面量 `"<nil>"`(P1)**:`stateAccess.Delete` 以
  `Set(key, nil)` 伪造删除(ADK 的 State 只有 Get/Set/All),`Get` 把 nil
  当缺失,但 `fmt.Sprint(nil)` 会写出 `"<nil>"` 当成节点状态。归一时跳过 nil。

**为什么不做"NodeStates 完全变派生视图"的大重构**:收益有限而风险明确 ——
`NodeStates` 的非测试消费者只有 2 处(子工作流返回值、完成事件载荷),
前端**无任何页面读取**它;而 `StateData` 有独立用途(Resume 整体回放、
RetryNode 打补丁、执行详情页的上下文面板),不能删。真正的收益(消除
float64 陷阱)已经用解码归一拿到了。

### 2. ADK 语义依赖 —— 已用契约测试钉死

新增 `workflow/compiler/adk_contract_test.go`,把本项目赖以成立的 ADK 语义
写成断言。ADK 升级**不会**让代码编译失败,只会让语义静默改变;而现有业务
测试测的是本项目自己的逻辑,不会报警。

- `parallelagent` 只在**分支返回 Go error** 时取消兄弟(errgroup 语义)——
  这正是 `parallelCascadeAgent` 存在的全部理由(节点失败走
  `yield(escalateEvent, nil)`,err 为 nil)。实测两条都确认。
- `InvocationContext` 内嵌 `context.Context` —— `cascadeCtx` 依赖这一形态。
  **重要发现:ADK 自己在接口注释里写明这是临时方案**("will be removed
  later... we plan to stop embedding go context in adk context types")。
  即级联层依赖的是 ADK 明示要移除的形态,升级时必须改为 `WithContext`
  或由本项目自管取消信号。
- `loopagent` 的 `MaxIterations` 上限语义 —— 循环保护完全依赖它。
- Agent 名字唯一性 —— 实测 ADK 当前**不拒绝**重名(会硬报错,不是静默),
  但 parentmap 按名字索引,重名会让事件路由到错误父节点,故 `uniqueName`
  仍必须保持。

### 3. 真实 Agent 端到端 —— 已补真机冒烟

新增 `agent/claude/real_smoke_test.go`。本机实测(claude 2.1.286):
真实调用返回 `OK\n\n{"decision": "DONE", "summary": "ok"}`,
`STATUS=SUCCESS` / `DECISION=DONE` / `SUMMARY=ok` 全部正确解析
—— **在真实 CLI 上验证了 A7 的尾部 JSON 提取**;1 秒超时的真实进程正确
返回 `CLAUDE_TIMEOUT` 且不挂死(验证进程组击杀 + WaitDelay)。
无 CLI / 无凭据 / 无网络时全部 skip,不阻塞 CI。

### 轮次 31:复审发现并已处理

对最近三轮改动做了"改 A 坏 B"复审(只读审计 diff),13 条结论逐条复核。
**其中两条是本项目自己引入的**:

- **#86 检查点节流写成了死代码**:`maybeCheckpoint` 用
  `Exec.CurrentNodeID` 判断"是否换了节点",但调用点在它之前一行就调了
  `SetCurrentNode(nodeID)` —— 两者恒等,这条分支永远走不到,节流退化成
  纯 2s 定时。后果:快速链里第 2..N 个节点都不落库,崩溃时
  `current_node_id` 仍指向链首,`RetryNode` 从错误位置重试 —— 恰好把
  #85 打了折扣。改用独立的 `lastCheckpointNode` 字段。
- **#87 Approve 不重新映射决策(可真实触发)**:`status=WAIT_USER` 与
  `decision=REJECTED` 可同时成立(Agent 一边要求人工确认、一边给倾向性
  结论),而 `answeredOutcome` 照搬该决策 —— 用户点 Approve 后
  `KeyDecision` 仍是 REJECTED,路由走回拒绝分支,用户的批准被无视。改为按
  答复重新映射,与 routeAgent 的恢复消费路径一致。

**一条约定违反(经人工裁决)**:并行不收敛原本只告警就落 COMPLETED,
与硬性约定 #14 逐字冲突(约定点名"并行不收敛"必须显式失败)。已改为
落 FAILED,错误写明"请把分支连到同一个 merge 节点"。

**恒真测试清理**:`TestNilStateDoesNotBecomeLiteralNil` 断言的
`node_states_json` 根本不经过 `syncNodeStates` —— 无论 #83 修没修都通过,
等于没为 #83 提供任何保护;两个 ADK 契约测试只 `t.Log` 不 `t.Error`,
意味着关键契约回归不会让 CI 变红。恒真的测试比没有测试更危险(虚假安全感),
已全部改为真断言并**实测有效性**。

**死代码清理**:`Plan.Unconverged` 只写不读;`KindWarning` 携带的
`Branches` 无人读取却带假注释;`logx` 有 `MaskMap` 幽灵注释;`runNodeGuarded`
把同一对象既当 receiver 又当参数;`mock.Agent.runs` 无界增长(加 256 上限);
`agent/output.go` 自造 `itoa`(改 `strconv.FormatInt`)。

### 仍然开放(需单独立项,不属于已登记缺陷)

1. ~~**分支 goroutine 的 panic 没有兜底**~~ —— **本轮已修(#84)**。ADK 的
   `parallelagent` 在自己的 goroutine 里跑分支(`errgroup.Go`),而
   `recover()` 在 adk 生产代码中零命中,分支内 panic 绕过 `engine.execute`
   的兜底直接杀进程(实测确认,违反约定 #12)。改为在 `RunEnv.runNodeGuarded`
   兜住:节点是 panic 的实际发生地,无论跑在执行协程还是 ADK 分支协程都覆盖;
   panic 转成 FAILED outcome 后由既有 KeyAbort 流程落库 FAILED,
   用户看到"哪个节点炸了"而不是整个应用消失。
2. ~~**执行全程无持久化**~~ —— **本轮已修(#85)**。`CurrentNodeID` 只存在
   内存,运行中从不落库:真崩溃(进程被强杀,没有任何收尾代码执行)后库里
   仍是空值,`RecoverPending` 标 FAILED 之后 `RetryNode` 恒返回
   `NO_FAILED_NODE`("无法定位失败节点")—— 崩溃恢复后**无法从失败处重试**。
   实测(无检查点时):节点执行期间 t=80/160/240ms 的 `current_node_id`
   恒为空串,直到 panic 被兜住后才被 `failExecution → save` 写进去 ——
   那条路径掩盖了真崩溃场景。新增 `RunEnv.maybeCheckpoint`(2s 节流)+
   `DB.TouchExecutionNode`(单列 UPDATE,不碰其余字段),节点开始时把
   "卡在哪个节点"写进库。
3. ~~**`ADK 契约`待补**~~ —— **已补齐(轮次 30,共 9 条)**。原有 4 条
   之外补上 5 条,每条都锁定一处本项目依赖、但升级时会**静默**失效的语义:

   | 契约 | 锁定内容 | 一旦变化的后果 |
   | --- | --- | --- |
   | `loopagent` 恰好 N 轮 | 原断言只有 `n > 3`("至多");补 `n == max`(1/3/5 三档) | 循环保护失准:变"至少"会多跑几轮,变"至多"会提前触发 |
   | `loopagent` Escalate 立即退出 | 循环体内任一子 Agent 发 Escalate 即退出整轮,不等跑完 | 人工闸门会在循环体内反复弹(每次 Resume 立刻又退出) |
   | `parallelagent` 提前停止仍 drain | 消费者 `yield` 返回 false 后仍等待所有分支 goroutine 结束(defer 里 cancel + drain) | `engine.execute` 的 break 后回写会与仍在跑的分支并发写状态 |
   | 分支错误回传消费者 | 分支 Go error 会经迭代器回传 | `engine` 的 `lastErr` 永远 nil,错误原因丢失 |
   | `session.State` Set(nil)/Get 缺失键 | `Set(k,nil)` 后 Get 返回 `(nil,nil)` 不报错;缺失键返回 `ErrStateKeyNotExist`;`All()` 仍含被置 nil 的键 | 一次性状态(`_user_input` 等)的 Delete 约定与派生视图(#83)全部出错 |
   | `temp:` 前缀语义 | 带前缀的键**影响实时状态但被从事件里剥掉** | **实测确认:本项目不能给需要 Resume 恢复的状态加 `temp:` 前缀**,否则 StateData 会静默丢失 |

   写这些契约时踩到的 API 事实(已记在测试注释里):`AppendEvent` 挂在
   service 而非 session;`All()` / `Events().All()` 都返回迭代器而非 map。

4. **`pi` 适配器无真机冒烟**:本机未安装 pi-agent CLI。新增
   `agent/pi/stub_smoke_test.go`,用"同形替身二进制"补上**子进程这一层**
   (`Bin` 可配置,替身接收与真实 pi 完全相同的参数),验证:
   全部参数送达、含嵌套对象的尾部 JSON 正确提取(A7 在真实进程形态上
   成立)、超时按 `PI_TIMEOUT` 返回、大输出被 8MB 上限截断且带显式标记
   (A8)、stderr 不混入 Output、非零退出返回 `PI_PROCESS_ERROR` 且带
   stderr 线索。

## 四、轮次 33-36:第一轮并行审计(4 个子代理,commit `3f5e68f`)

> 4 个子代理并行只读审计前端 / DSL+模型 / 持久化+事件 / API+应用层,
> 共提出 28 条,逐条复核源码后定级,**28 条全部当轮修复**(#95-#122,
> 含门禁自身 3 条)。本轮缺陷密度高于前几轮,原因是审计面第一次覆盖到
> "两处实现各写一份"(前端防抖、校验/运行契约)和"门禁自身"这两类。
>
> 表中「来源」列区分三种情况:
> **新发现** = 前几轮从未登记过的缺陷;
> **约定收敛** = 硬性约定早就写在 `AGENTS.md` 里,但没有实现或没有守卫,
> 本轮补上;
> **既有登记项** = 前几轮已登记但未修的条目,本轮修掉。

### 轮次 33:前端(约定 7 / 8 / 10)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #95 | 约定收敛 | 实时事件刷新**违反约定 7 的 200ms 防抖**:`ExecutionMonitor` 实测 120ms;`Dashboard` / `Executions` / `WorkflowHistory` **完全没有防抖** | 每条实时事件触发一次全量列表拉取 → 请求数 = 事件数(高频 `node.*` / `agent.output` 下打出成串请求,桌面模式下还会互相覆盖响应) | 新增 `frontend/src/lib/debounce.ts` 作单一来源(`REFRESH_DEBOUNCE_MS = 200`),四个组件统一改用,组件卸载 `cancel()` |
| #96 | 约定收敛 | 另 2 个组件各自内联 `setTimeout`,窗口期互不一致 | 窗口漂移(120 / 200 / 无),约定 7 无法作为一条规则执行 | 同上,全部收敛到 `debounce.ts` |
| #97 | 新发现 | `Workflows` 五处裸 `await` 吞错 | 任意 API 失败 → 界面无任何提示,用户以为操作成功(约定 14 同族:该失败却成功) | 失败一律 `toast.error` |
| #98 | 约定收敛 | `Designer` 对 `api.listAgents()` 的结果直接 `.map` | 桌面 IPC 返回 `null` 或 SPA fallback 返回 HTML 兜底页 → 抛 `TypeError` 又被 `.catch(() => {})` 吞掉 → **Agent 下拉框空的且无提示** | 过 `asArray()`;并新增**源码扫描守卫** `frontend/src/test/list-guard.test.ts`(见 `docs/TESTING.md`) |
| #99 | 约定收敛 | `api.exportBackupText` **只有桌面分支** | 服务器模式点「导出备份」→ 去调不可用的 Wails 绑定 → **必然失败**(约定 10 双分支必须一致) | 补同源 `/api/backup` 分支 |

### 轮次 34:DSL 与模型层

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #100 | 新发现 | `dsl.Node` 的节点配置是 `json:"-"` | **JSON 格式的工作流导入后,节点配置全部丢失**(导入"成功",配置静默消失) | `dsl.Node` 补 `MarshalJSON` / `UnmarshalJSON`,配置键与 YAML 路径同构 |
| #101 | 新发现 | `ToModel` 从 `Rest` 里 `delete` 字段,且与调用方**共享同一张 map** | 调用方的 DSL 文档被就地改写(第二次转换就丢字段) | 改深拷贝,不再改写调用方文档 |
| #102 | 新发现 | YAML inline 整数被解析成 `uint64`,运行期按 `int` 取值 | `timeout_seconds` / `retry.max_attempts` **静默失效**(取到 0)——用户写了超时,节点仍按默认超时跑 | `ToModel` 里对 variables / permission / 节点配置统一调 `model.NormalizeStateJSON` **把数值归一成 int**(模型里的数值语义从此稳定);运行期的读取点另在轮次 40 侧补齐(`cfgInt` 认全 int 形态) |
| #103 | 新发现 | `model.AsError` 兜底分支**丢弃错误链**(注释却声称"保持原错误链") | `errors.Is` / `errors.As` 到不了根因,排障时被截断 | 补 `Unwrap`,`model.Error` 增加 `cause` 字段 |
| #104 | 新发现 | `NormalizeStateJSON` 只归一顶层 map | 数组里嵌的 map 仍是 `float64`(约定 2 的漏网之鱼);`NaN` / `Inf` 会让 JSON 序列化失败 | 改递归归一 + `NaN`/`Inf` 守卫 |

### 轮次 35:持久化与事件总线

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #105 | 约定收敛 | `SaveExecution` 是**整行 upsert,无条件覆盖 `state` 列** | 绕过 `persistence.CasExecutionState`:CAS 落定的 `FAILED` / `CANCELLED` 被过期副本改回"运行中"(约定 4) | 加**负面终态守卫**:已落定的终态不被过期整行写改回(轮次 37 进一步把状态列彻底交给 CAS) |
| #106 | 新发现 | 迁移里的 `ALTER TABLE` 失败被**静默吞掉** | 旧库缺列 → 迁移"成功" → 运行时才炸,根因被掩盖 | 迁移失败上抛(并保留已执行的 DDL) |
| #107 | 新发现 | `wrap()` 丢弃底层错误链;`variables_json` 往返后数值类型未归一 | 排障断链;`variables` 里的整数读回来是 `float64`(约定 2) | `wrap` 保留错误链;数值类型在解码入口归一 |
| #108 | 新发现 | `event.Emit` **不隔离订阅者**:订阅者 panic 直接冒泡到发布方 | 桌面模式下发布方是**工作流执行协程** → 一个坏订阅者让**整个应用退出**(约定 12 同族) | 逐订阅者 `recover()`,坏订阅者被隔离并记日志 |

### 轮次 36:API 与应用服务层

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #109 | 新发现 | API 错误体**不统一**:普通错误只有 `error` 没有 `code`;405 与未知路由返回**纯文本** | 前端 `ApiError.code` 时而 `undefined`;405 只能退化成 "HTTP 405" | 错误体统一带 `error` + `code` + `kind`;`jsonErrorHandler` 把 ServeMux 的 404/405 换成 JSON |
| #110 | 新发现 | 超大 body 返回 **400** 而非 413 | 客户端无法区分"参数错"与"请求体过大" | 统一 413 `PAYLOAD_TOO_LARGE`;非法 JSON 统一 400 `INVALID_JSON` |
| #111 | 新发现 | `limit` / 版本号用 `int` 累加解析,**溢出成负数** | `?limit=99999999999999999999` → 仓储层把 `limit<=0` 当"不限制" → **返回全表**(内存与响应体不可控) | `limit` 收敛到上限 1000;版本号走独立解析并拒绝越界 |
| #112 | 新发现 | SSE 写失败**只记 Debug 日志并继续循环** | 客户端断开后连接不释放(订阅不注销、goroutine 常驻、keepalive 继续写) | 写失败即关流并释放订阅;建连即失败则不订阅 |
| #113 | 约定收敛 | 节点 `output` / `error` **明文凭据经 API 泄漏** | 这些字段直接来自 Agent 的 stdout/stderr,常含 `ANTHROPIC_API_KEY=sk-...`;事件流已在总线出口脱敏,但节点明细是**直写 SQLite、原样返回** | API 读出口统一脱敏(`maskNode` + 结构化 `result_json` 脱敏) |
| #114 | 约定收敛 | `SetPermission` 是**读-改-写且无锁** | 并发授权(多 Agent 分别设置权限)互相覆盖,后写者拿旧快照写回 → **先写者的策略整条丢失** | 加 `permMu` 串行化整表读-改-写 |
| #115 | 约定收敛 | `GitService.SetWorkingDir` **先改内存后落库** | 落库失败但界面已切目录(界面提示失败、Git 面板已指向新目录,重启又回旧目录) | 改为先落库再改内存 |
| #116 | 约定收敛 | `log_level` **不校验** | 写入任意值 → `logx.SetLevel` 静默回落 info → **界面显示 bogus、实际生效 info** | 只接受 `debug\|info\|warn\|error`(大小写不敏感,落库归一为小写) |
| #117 | 约定收敛 | `RestoreBackup` 把**未知 Agent** 也计入"已恢复" | 配置永远不会被应用(落库只是留一行死数据)→ "已恢复 N 个"是**假成功** | 未知 Agent 计入 `agent_configs_invalid` 并新增 `agent_configs_unknown_ids` 字段,不落库 |
| #118 | 新发现 | `main.go`:**SQLite 连接从不关闭**;`Shutdown` 无超时;`--log` / `--addr` 无校验 | 连接泄漏 + WAL 不落盘;关机被挂死请求无限拖住;非法参数默默按默认值跑(约定 14) | 退出码由 `run()` 返回(defer 一定执行);关库进 defer;关停 5s 上限;启动参数校验(`--log` 级别、`--addr` 合法性) |
| #119 | 新发现 | `workflow/model`、`workflow/dsl`、根包 `agentworkflow` **三个包零测试** | 三处是所有类型/入口的所在地,零覆盖 = 门禁在这三处形同虚设 | 补齐三个包的测试(见 `docs/TESTING.md` 轮次 33-36) |

### 门禁自身的三个缺陷(第一轮顺带发现,归入轮次 36)

| 编号 | 缺陷 | 实测证据 | 修复 |
| --- | --- | --- | --- |
| #120 | `go build ./...` **必然失败** | `go build ./...` → `exit=1`,`# agentworkflow/build/ios: function main is undeclared in the main package` | 门禁改用排除 `build/` 的包列表(`go list ./... \| grep -vE '(^\|/)(build\|node_modules)/'`) |
| #121 | `go test ./...` 捎带两个**不属于本项目**的包 | `build/ios`、`build/ios/scripts/deps`、`build/android/scripts/deps`、`node_modules/flatted/golang/pkg/flatted` 全部出现在 `go list ./...` | 同上排除;`build/` 本身的编译问题登记在"仍然开放",不在门禁里假装修好 |
| #122 | 前端构建输出被 `>/dev/null` 吞掉 | `npm run build >/dev/null` 把 `tsc` 的报错(走 stdout)一起丢掉,构建失败只留一行 `npm error` | 不再丢弃构建输出;并把 lint/build/vitest 三步的失败码显式传出 |

## 五、轮次 37-40:第二轮并行审计(4 个子代理,commit `916a9c4`)

> 4 个子代理并行审计运行时 / 编译器+校验器 / 适配层 / 支撑包,共 19 条,
> 全部修复(#123-#141)。本轮的核心不是"又发现多少条",
> 而是**把两份实现合并成一份**,并给"合并后的同源性"装上探针。

### 轮次 37:运行时(状态写 / 事件落库 / 告警分级)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #123 | 约定收敛 | runtime 的状态写**绕过 CAS**:整行 upsert 仍覆盖 `state` 列 | 拿着过期副本的写者能把刚由 CAS 落定的状态改回去("已完成"被改回"运行中"),UI 状态与实际执行不符(约定 4 / 14 同族) | 新增 `workflow/runtime/persist.go`:状态列**只**由 CAS 落定,其余列由 `saveMeta` 写;CAS 与元信息**合成一条原子 SQL**(消除"状态已完成、节点状态还是上一轮"的窗口) |
| #124 | 约定收敛 | 节点 `output` / `error` / `result_json` **明文落库** | 落库前不脱敏 → 审计文件 / 备份 DB 里留明文凭据(约定 8) | 落库前脱敏(**artifact 保持原样** —— 制品是要给用户看的原始产物) |
| #125 | 新发现 | 事件落库失败被**静默丢弃** | 审计流凭空缺一段,事后无法追查 | 计数 + 日志 + `runtime.error` 事件(**不打断执行** —— 落库失败不是执行失败) |
| #126 | 新发现 | 正常循环退出被记为 **WARN** | ADK 的 Escalate 编码了多种语义(正常循环出口 / HITL 暂停 / 节点失败 / 循环上限),此前一律记 WARN → 噪声淹没真正异常,运维与用户都对告警脱敏 | 新增 `workflow/runtime/escalate.go` 做**分级**:预期内 → Info,异常 → Warn,并发 `runtime.escalate` 事件(带 `kind=expected\|unexpected`) |

### 轮次 38:编译器与校验器(约定 13 收口)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #127 | 约定收敛 | **配置契约有两份实现**:校验期在 `validator` 里内联 `switch`,运行期在 `executor.go` 里另写一套 | 两份实现各自演化 → 方向决定后果:运行期读了表里没有的键 = 用户被自己的校验器挡住;表里要求了运行期不读的键 = 用户被迫填一个没作用的键(约定 13) | 新增 `compiler/nodeconfig.go`(节点类型→配置契约)与 `compiler/conditionenv.go`(表达式可用变量)作**唯一定义**,`validator` 改为消费 |
| #128 | 新发现 | **禁用节点仍要求必填配置** | `enabled: false` 的节点本不该执行,却因为缺必填配置导致**整个工作流无法保存** | 禁用节点跳过必填校验(但**仍报未知节点类型**) |
| #129 | 新发现 | **禁用节点引用不存在的 Agent 也报错** | 同上,禁用节点引用 `claude-code` 而注册表里没有 → 仍然挡住保存 | 禁用节点跳过 Agent 存在性校验 |
| #130 | 新发现 | condition 节点的 `expression` 被当**必填** | 运行期**从不读** `expression`(条件写在边上)→ 用户被迫填一个没作用的字段 | 移出必填;但**语法检查仍保留**(防止写了坏表达式却以为生效) |
| #131 | 新发现 | `enabled` 非布尔被**静默忽略** | 用户写 `enabled: "false"`(字符串)→ 运行期按"未配置"处理 → 节点照常执行,用户以为已禁用 | 运行期 **fail closed**(节点失败、不执行副作用),校验期报 `INVALID_NODE_ENABLED`,两边结论一致 |

### 轮次 39:Agent / Skill 适配层

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #132 | 新发现 | claude 适配器取**第一个** JSON,不是尾部 | 正文里的示例 JSON(提示词回显)会误导路由 → 决策取错(约定 1 同族) | 与 pi 统一改取**尾部** JSON(括号配平扫描) |
| #133 | 新发现 | `is_error` 为**字符串** `"true"` 时信封解析失败 | 适配器把"额度耗尽"这类错误当 SUCCESS 返回 → **该失败却成功**(约定 14,P0 级) | 信封解析兼容布尔与字符串形态;解析失败按失败处理 |
| #134 | 约定收敛 | 输出被 **8MB 上限截断后仍返回 SUCCESS** | 截断意味着内容不完整,按成功处理 = 拿半截输出继续跑(约定 14) | 改为**显式失败**并说明被截断 |
| #135 | 约定收敛 | **stderr 原样拼进错误消息** | CLI 常把 `ANTHROPIC_AUTH_TOKEN=sk-...` 打进 stderr → 凭据进执行记录与事件流(约定 8) | 按**确实注入过的值**精确脱敏(新增 `agent/redact.go`),不靠正则猜形态 |
| #136 | 新发现 | JSON 提取是 **O(n²)** | 病态输出(8MB 未配平的 `{`)曾**卡死节点数小时** | 改单趟线性扫描 |
| #137 | 新发现 | `Masked()` **漏掩 `ExtraArgs`**;mock 淘汰会**删掉仍在运行的空间** | 附加参数里的凭据明文外泄;执行中的 mock 计数空间被淘汰 → 决策错位 | 补掩 `ExtraArgs`;mock 淘汰跳过仍在运行的执行 |

### 轮次 40:支撑包(logx / permission / template / git / contextx)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #138 | 约定收敛 | logx 掩码只认 `map[string]any` / `[]any` | `map[string]string` 等容器**明文泄漏**(约定 8) | 掩码引擎重写:反射遍历全部容器类型(新增 `logx/mask.go`) |
| #139 | 约定收敛 | logx 键名判定是**整串匹配** | `user_token` / `x-api-key` / `apiKey` 全部漏掉 | 改**词级**规则:键名先归一(`_`/`-` 当分隔符、驼峰边界也切,`apiKey` → `api_key`),再分四档判定 —— ① 强敏感词(password / secret / credential…);② 相邻两词的强敏感词组(`api_key` / `client_key` / `access_key`…);③ 无分隔紧凑写法(`apikey` / `accesstoken`…);④ **以 `token` 结尾**(token / user_token / github_token)。另有弱敏感档(token 在中间)与白名单(`max_tokens` / `token_count` / `keyword` / `tokenizer` 不误伤) |
| #140 | 约定收敛 | permission 的**非法覆盖被静默忽略** | 非法策略 → 回落默认 → **fail open**(权限被放宽) | 改 **fail closed** |
| #141 | 新发现 | template 渲染 **panic 逃逸** | 模板里的 `Stringer` 崩溃会**带崩执行协程**(约定 12) | 渲染层兜住 panic,转成节点失败 |

## 六、轮次 41-44:第三轮(跨文件收口 / 前端契约同步 / 门禁重写,commit `8520115`)

> 4 个子代理并行:跨文件收口 / 前端契约同步 / 门禁脚本重写 / 文档同步。
> 产出 **21 条**(#142-#162),全部当轮修复。
>
> 本轮的性质与前两轮不同:前两轮是"找新缺陷",本轮是**"收口"**——
> 把此前几轮留下但**没有实现、没有守卫、或只写在注释里**的约定真正落到
> 代码里。典型如 #142:那些结构体零引用、编译器不报错、人眼扫不出来 ——
> 唯一的症状是"第二份契约"还躺在仓库里,等着下一个人照着它加字段。
>
> 本轮也是**第一次把门禁本身当成被审计对象**:此前门禁"全绿"这件事
> 本身从未被验证过(见轮次 43 的六条)。

### 轮次 41:跨文件收口(后端)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #142 | 约定收敛 | `workflow/dsl` 里 9 个**零引用**的配置结构体(`PromptDef` / `AgentNodeConfig` / `RetryPolicy` / `SkillNodeConfig` / `ConditionNodeConfig` / `HumanNodeConfig` / `ScriptNodeConfig` / `GitNodeConfig` / `SubWorkflowNodeConfig`)仍在 | 它们是"第二份契约"的化石:内容已过期(缺 `model` 与 `enabled`,多出运行期从不读取的 `Env`),却仍**看起来像一份可用的契约**;下一个人照着它加字段,就会再次造出第二份(约定 13)。它们还让"把凭据写进 DSL 结构体"从抽象诱惑变成具体诱惑 | 全部删除,并加 `workflow/dsl/no_config_structs_test.go`(AST 扫描:dsl 包不得再声明节点配置结构体),让"再写一份"立刻失败并指出该去哪里改 |
| #143 | 新发现 | `enabled` 的语义注释**过时**:写着"被忽略照常执行",实际早已改成 fail closed | 注释与实现相反 → 读代码的人按注释理解,形成"禁用节点照样跑"的错误预期 | 注释改写为 fail closed 的真实语义,并与校验期 `INVALID_NODE_ENABLED` 的口径对齐 |
| #144 | 约定收敛 | 运行期**不归一** Skill 返回的状态,只精确匹配 `"WAIT_USER"` 与 `{"FAILED","failed"}` | 外部实现返回 `"wait_user"` / `"Failed"` / `" wait_user "` → **不匹配 → 按成功继续往下跑**:人工确认被静默跳过、失败被当成功(约定 14) | 归一接入运行期,唯一来源 `skill.NormalizeStatus`(去空白 + 转大写),与 Agent 侧 `coreagent.StatusXxx` 口径一致。**注**:本轮只修"形态"问题;"契约外状态怎么办"当时的口径是"按成功处理(不猜)",并用一条用例把这个口径钉住 —— 该口径在第五轮被推翻(见 #176) |
| #145 | 约定收敛 | 工作流级 `permissions` 段结构非法时,运行期把该 Agent 的权限**全拒**(fail closed)但**执行继续跑**,最终失败原因变成"agent 无权执行 fs:write" | 真正的原因("permissions 段写错了")被掩盖,用户被带偏(约定 14:静默降级同族) | 改用 `ApplyChecked`:非法结构 → 执行**明确失败**,原因里直接写明"权限覆盖非法" |
| #146 | 约定收敛 | 日志级别白名单有**三份**:`logx` 一份、`app/application` 一份、`main.go` 一份 | 三份必然漂移,漂移方向是"用户能选一个后端不认的值"(设置页回显 debug、实际跑 info) | `logx` 成为唯一来源(`Levels()` / `ValidateLevel()` / `SetLevelChecked()`),删掉另两份 |
| #147 | 约定收敛 | API 读出口只脱敏节点明细的 `output` / `error` / `result_json`,**`state_data` 与 `task` 原样返回** | 会话状态快照里是 Agent 的 stdout/stderr 原文,常含 `ANTHROPIC_API_KEY=sk-…` → 凭据经 API 泄漏(约定 8) | 读出口补 `state_data` / `task` 脱敏 |
| #148 | 新发现 | 持久层的终态守卫只挡 `FAILED` / `CANCELLED`,**不挡 `COMPLETED`** | 绕过 runtime 的直写能把一条**已完成**的执行改回 `RUNNING` —— 列表里显示"运行中",它其实早就跑完了(状态与事实不符,约定 14 同族) | `COMPLETED` 一并纳入受保护终态 |
| #149 | 约定收敛 | 运行期**不复用** `persistence.SaveExecution`(自己拼 SQL 直写 `executions` 表),两边列集合靠注释提醒同步 | 持久层加列而运行期没加 → 该列静默丢失;运行期加了列而持久层没加 → 整条 UPDATE 报 `no such column`,执行记录的元信息再也落不了库 | 列清单导出为常量供契约测试使用,新增 `workflow/runtime/persist_contract_test.go`,把"注释提醒"变成断言 |

### 轮次 42:前端契约同步

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #150 | 新发现 | 错误体解析不统一:部分路径剥 `Kind(CODE): ` 前缀,部分不剥 | 用户看到 `ValidationError(WORKFLOW_INVALID): ...` 这种机器形态,信息量低于原文 | 统一到 `buildApiError` + `stripErrorPrefix`,JSON 端点与文本端点(`httpText`)走同一套 |
| #151 | 新发现 | `CODE_HINTS` 码表只有 9 个码,后端已有的其它码**没有中文兜底** | 错误体只有 `code` 没有 `error` 时(旧版后端 / 反向代理改写),用户只看到"HTTP 400" | 码表补齐;第四/五轮新增的 6 个码(`DSL_UNKNOWN_KEY` / `DSL_MISPLACED_KEY` / `UNKNOWN_FIELD` / `UNKNOWN_NODE_CONFIG` / `DEPRECATED_NODE_CONFIG` / `INVALID_NODE_ENABLED`)在迭代 21 补入,与 `docs/api.md` 的错误码表一一对应 |
| #152 | 约定收敛 | `restoreBackup` 的返回类型里**没有** `agent_configs_unknown_ids`,而后端已经在返回这个字段 | 前端拿不到"本机不存在的 Agent"清单 → Settings 页无法告诉用户哪些配置没恢复 | 类型补上并在 Settings 页展示 |
| #153 | 约定收敛 | Skills 开关**没有 in-flight 锁** | 连点两次发出两个请求,后到的覆盖先到的,最终态与用户意图相反 | 同行 in-flight 锁(与 Agents 权限开关同款) |
| #154 | 新发现 | GitPanel 的三个查询(status / diff / log)绑成"全成功或全失败" | 仓库没初始化时 status 报错,连 log 也一起没了 —— 用户想看的提交历史被无关的失败拖掉 | 三查询**独立成败**,各管各的提示 |
| #155 | 新发现 | Agents 页不滤空键 | Agent 配置里的空环境变量键渲染成空行,界面上像有配置、实际没有 | 过滤空键 |
| #156 | 约定收敛 | Settings 页打开"允许执行 script 节点"**无风险确认** | 该开关放开的是"`/bin/sh -c` 执行 DSL 里的任意命令"(约定 11),而工作流可由 YAML 导入获得 —— 一次误点就把任意命令执行打开 | 开关带风险确认文案 |

### 轮次 43:门禁脚本重写(8 步)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #157 | 新发现 | `go build ./...` **必然失败** | 实测 `go build ./...` → `exit=1`,`# agentworkflow/build/ios: function main is undeclared in the main package` | 门禁改用排除 `build/` 的包列表;`build/ios` 本身的编译问题**登记在"仍然开放"**,不在门禁里假装修好 |
| #158 | 新发现 | 门禁只有 `vet` 没有 `build` | `vet` 能发现类型/语法错,但**链接期**错误(缺 main、符号冲突、平台专属代码)只有 build 才暴露 | 补 `go build` 步骤(第 3 步),日志落盘再回显,只回显非噪声告警 |
| #159 | 新发现 | 门禁不检查"包**有用例**" | 测试文件存在但用例被注释掉 / 全部 skip → 门禁照样绿,覆盖率悄悄归零 | 用例**存在性**检查(防静默 0 用例)。**范围在第六轮扩到全项目**:清单由 `go list` 自动推导,不再是手写的"关键包"清单(见 #193) |
| #160 | 新发现 | `gofmt` 按**目录**传参 | gofmt 遇目录参数会递归整棵树,传模块根目录会把 Wails 脚手架(`build/ios`)一起卷进来 | 改为按**文件**清单传给 gofmt,清单由 `go list` 推导(新增包不会漏) |
| #161 | 新发现 | 冒烟脚本**没有端口预检** | 端口被上一次残留进程占用时,9 项断言全部打到那个旧进程上,却照样打印 `SMOKE OK` —— 门禁"全绿"但什么都没验 | 冒烟脚本加端口预检(占用即失败)。**占用者的打印在第七轮补上**(见 #211):此前只报端口号,用户得自己猜是谁占的 |
| #162 | 新发现 | 冒烟脚本的 `trap` 在中断时**泄漏临时目录** | Ctrl-C / 失败退出时临时目录留在磁盘,多次运行后堆满 | `trap` 覆盖所有退出路径 |

## 七、轮次 45-48:第四轮(state_json 脱敏 / CAS 可观测 / 恒真测试清理 / DSL 严格化,commit `4e979eb`)

> 3 个子代理并行,产出 **12 条**(#163-#174),全部当轮修复。
> 本轮补的是第三轮复审判定的两个缺口(`state_json` 这条旁路、CAS 冲突
> 静默丢弃),外加**恒真测试清理** —— 前三轮的"实测有效性"里有几条
> 其实站不住(见 #166 / #167)。

### 轮次 45:`state_json` 明文旁路

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #163 | 新发现 | `executions.state_json` 这条通道**全程无脱敏**:前几轮修了 `execution_nodes` 的 output/error 与 API 出口,唯独会话状态快照没人碰 | 落库副本与 API 响应体都带明文凭据 —— 审计文件、备份 DB 直接拿到 `ANTHROPIC_API_KEY=sk-…`(约定 8) | 新增 `workflow/runtime/state_mask.go`:**按键分类**脱敏(19 个状态键逐个核对)。结构化键(decision / status / `_abort` / `_resume_node` / `_current_node` / `_loop_*` / `node:<id>:status` / `node:<id>:attempt`)**原样保留**,**其余默认脱敏**(默认拒绝:将来新增的状态键不会因漏登记就明文落库)。落库侧与 API 读出口**复用同一个函数** —— 两份实现各自演化就会出现"落库脱敏了但 API 又吐明文" |
| #164 | 新发现 | `node:<id>:result` / `node:<id>:answered` 的值是 outcome 的 **JSON 文本**,直接正则脱敏会产出**非法 JSON** | 恢复路径要 `json.Unmarshal` 这个值,解析失败就按"未答复"处理 → **静默重跑节点**(用户白等一次,人工闸门失效) | 走"解析 JSON → 结构化脱敏 → 重新序列化",保证仍是合法 JSON;内层键名(`{"api_key":"sk-…"}`)也能被抓到,纯文本正则抓不到 |

### 轮次 46:CAS 冲突可观测

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #165 | 新发现 | 三处 `_ = e.transitionAndSave(...)` 把 CAS 冲突整个吞掉,但**终态事件照发** | CAS 失败 = 库里的状态已被别的写者裁决(用户取消 / 显式重试 / 崩溃恢复)。此前:错误连日志都没有,且 `workflow.completed` 照发 → **事件流说"已完成"、库里还是"运行中"**,UI 与审计流互相打架,事后说不清哪边说了算(约定 14) | 状态没落库就**不发终态事件**,只发 `runtime.error`(`reason=state_commit_conflict`,带 `target_state` / `current_state`)。分级:终态路径上的冲突是**真异常**(ERROR),取消路径上的冲突是**预期竞态**(Warn,不制造噪音)。另加计数 `Engine.StateCommitFailures()` |

### 轮次 47:恒真测试清理 + 死字段废弃

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #166 | 新发现 | `agent/output_depth_test.go` 的断言**逻辑上恒假** | 改坏实现测试仍然绿 → 该文件给的是虚假安全感(轮次 31 已清过一次恒真测试,这是第二处) | 改为真实不变量(线性扫描的实测复杂度、单趟不重复匹配等),每条断言都做过"改坏实现 → 变红 → 还原"的实测 |
| #167 | 新发现 | `workflow/compiler/adk_contract_test.go` 有**三处只 `t.Log` 不 `t.Error`** 的"契约测试" | 关键 ADK 契约回归**不会让 CI 变红** —— 而这恰是 `parallelCascadeAgent` 的前提 | 改为真断言(ADK 契约从"只记录"变成"会拦人") |
| #168 | 新发现 | `agent/redact_test.go` 用例不足(缺部分包含 / JSON / 多行 / 边界) | 脱敏是安全边界,边界形态没覆盖就等于没测 | 补齐四类边界用例 |
| #169 | 新发现 | condition 节点的 `config.expression` 是**死字段**:运行期从不读(条件写在边上),但校验期仍对它做**语法检查** | 用户在设计器填错一个**没有作用的**字段,就保存不了自己的工作流 —— "被自己的校验器挡住"(约定 13 的反面) | 读需求确认条件应写在边上;该节点键**降级为警告**(`DEPRECATED_NODE_CONFIG`),删掉前端死输入框;存量 DSL 仍可保存、不影响路由 |

### 轮次 48:DSL 严格化 + builtin 参数守卫

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #170 | 约定收敛 | 键集合校验**只做在 YAML 路径**,JSON 路径零检查 | 同一份 DSL 换种格式导入行为就不同(顶层未知键 YAML 报错、JSON 静默丢弃)—— 约定 13 的"同源"精神 | 两路径共用同一套键校验(`workflow/dsl/schema.go`,键集合由结构体标签**反射派生**,不手写白名单) |
| #171 | 约定收敛 | `permissions:` 段放错层级(如写在 `workflow:` 段下)曾被**静默丢弃** | 权限收紧无声失效 = 安全语义被悄悄放开,必须 fail closed | 报错 `DSL_MISPLACED_KEY`,导入直接失败 |
| #172 | 新发现 | `model.Workflow.Permission` 的 json tag 是单数 `permission`,而 DSL 根级段的正式写法是复数 `permissions` | `json.Unmarshal` 把 `permissions` 当未知字段**静默丢弃** —— 用户写了权限收紧,导入后运行期拿到的仍是默认宽松策略,没有任何提示(约定 14) | `model.Workflow.UnmarshalJSON` 读侧多认一个别名(输出侧仍写 `permission`,既有 API 响应与历史快照的键名不动);两个键同时出现时报错而不是静默挑一个 |
| #173 | 新发现 | builtin Skill 用 `req.Args["message"].(string)` 取值,**类型断言失败就静默变空串** | `args: {message: 123}` → 用户的提交信息被丢弃;`LogSkill` 更糟:输出空串 + SUCCESS —— 节点"成功"了但日志里什么都没有(约定 14) | 类型守卫:不是期望类型就报错 |
| #174 | 新发现 | 模板"缺失变量"的口径是**按值是否为 nil**,而运行期契约是**按键是否存在** | 把 nil 当"缺失" → 每个引用了未产出槽位(`task` / `plan` / `iteration`)的 prompt 都刷告警,真正拼错的变量名反而被淹没 —— 告警方向反了 | 统一为"键在数据里 = 可解析(值为 nil 渲染成空串,不算缺失)" |

## 八、轮次 49-52:第五轮(runtime 交接 / 前端与校验期 / API 严格化 / 累积退化复审,commit `59ef7d7`)

> 3 个子代理并行 + 1 个独立 verifier 做累积退化复审,产出 **14 条**
> (#175-#188)。其中 10 条当轮修复,4 条转入下一轮(见第九节)。
>
> 本轮的核心是**"静默丢弃"的最后一块**:第四轮把 DSL 层与状态层的
> 静默丢弃治住了,剩下的全在**运行期的参数与状态落地**上 ——
> 渲染失败、状态不认识、参数类型不对,统统"静默降级成能跑的样子"。

### 轮次 49:runtime 交接三处 + 自查六处

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #175 | 新发现 | 模板渲染失败(内部 panic 被兜底成 error)时,`Render` **吞掉 error 并回退原始模板** | Agent 收到的是一段**没渲染过的模板文本** → 照常执行 → 照常 SUCCESS。`{{revwie}}` 拼错时,Agent 拿着字面量 `{{revwie}}` 去干活,工作流"成功"结束(约定 14) | 改用 `RenderErr`:渲染失败 → **节点失败**并写明原因,Agent 不被调用 |
| #176 | 新发现 | 未知的 Skill 状态**落空即成功** | 契约外的状态(`"weird"` / `"BANANA"` / 空串)既不是 SUCCESS 也不是 FAILED,此前按"没报错"处理 → 节点成功。该失败却成功(约定 14,P0 级)。**注意**:这推翻了第三轮 #144 当时**刻意**钉住的口径("未知状态按成功处理,不猜")—— "不猜某个具体状态值"被误写成了"猜它是成功" | 契约外状态与空状态一律 FAILED,原因里点名状态值与契约,让用户能改。第五轮同时改写了第三轮那条用例(`TestSkillStatusUnknownIsSuccess` → `TestSkillStatusUnknownFailsNode`) |
| #177 | 新发现 | 节点 `args` 不是 map 时**静默传空参数** | `args, _ := node.Config["args"].(map[string]any)` —— 断言失败 → args = nil → 用户在节点上写的参数全部丢失,Skill 拿到空参数照常执行 | 类型守卫:不是 map 就让节点失败并写明实际类型 |
| #178 | 新发现 | Agent 状态**大小写不匹配 = 成功** | 外部 Agent 实现返回 `"failed"` / `"wait_user"` → 精确匹配落空 → 节点按成功继续 | 归一(与 #144 同一口径,唯一来源 `coreagent` 侧的归一函数) |
| #179 | 新发现 | agent 节点的 `prompt` 写成对象却没有可用的 `template` | prompt 变成**空串** —— Agent 收到空提示词照常执行 | 节点失败并写明"prompt 缺 template" |
| #180 | 新发现 | git / subworkflow 节点的 `args` / `input` 静默丢 | git 节点的操作参数、子工作流的输入,类型不对就整个消失,子工作流收到空输入 | 类型守卫(与 #177 同族) |
| #181 | 新发现 | 节点 FAILED 时**没有原因** | 用户在执行详情里只看到"失败",不知道为什么 | 失败必须带可读原因 |

### 轮次 50:前端权限透传 + 校验期未知键告警

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #182 | 新发现 | 设计器保存时**丢弃服务端返回的工作流级权限覆盖** | 用户从没碰过权限,保存一次就把服务端返回的 `permissions` 抹掉了 —— 权限收紧无声失效(约定 14) | `toModel` 抽成纯函数并**透传保留** |
| #183 | 约定收敛 | 校验期**不报**未知配置键 | `timeout_seconds` 拼成 `timeout_second` 时,运行期**照常执行节点**,只是那个设置**根本没生效** —— 用户以为超时是 30 秒,实际用默认值。拼错与没写完全无法区分 | 报**警告**(`UNKNOWN_NODE_CONFIG`)+ 编辑距离建议("是否想写 `timeout_seconds`?"),**不阻断保存**(存量工作流里有拼错的键,报错会让用户打不开自己的老工作流)。键清单唯一来源 `compiler.NodeConfigSpecFor`(约定 13);已废弃的键只报"已废弃"不报"未知" |

### 轮次 51:API 未知顶层键 400

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #184 | 约定收敛 | API 接受**未知顶层键**并**静默丢弃**(`json.Unmarshal` 默认宽松) | 用户提交了一份含多余键的 JSON,接口返回 200,用户以为那份设置生效了,实际根本没进模型(约定 14)。同一份 DSL 的顶层未知键在 `workflow/dsl` 里已报 `DSL_UNKNOWN_KEY`,JSON 保存路径却没有对应行为 —— 两条路径语义不一致 | 未知顶层键 → **400 `UNKNOWN_FIELD`**,错误信息列出允许的键。允许集合由 `model.Workflow` 的 json tag **反射派生**(模型加字段自动生效,不手写);`permissions` 与 `ignored_keys` 为特例 |

### 轮次 52:累积退化复审(独立 verifier)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #185 | 新发现 | `state_mask` 分类守卫只扫 **2 个文件**(`compiler/lower.go` 与 `runtime/executor.go`),且只认 `.Set()` 调用式(receiver 名必须是 `st` / `state`) | 直接写 map 的地方**完全不可见** —— `engine.go` 里就有 `exec.StateData[compiler.StatusKey(nodeID)] = …` 这种写法,新增的键不会被守卫发现,落库脱敏会把它当自由文本(结构化键被改写 = 执行语义变) | **第六轮已修**(见 #189) |
| #186 | 新发现 | 日志级别测试名声称"即时生效",但**从未验证 logx 的实际级别** | 测试只断言了 app 层字段与库里的值;`logx` 没有导出当前级别的读取接口,真正的全局 logger 级别没被验证 —— 改坏 `SetLevelChecked` 不会让测试变红 | **第六轮已修**(见 #191) |
| #187 | 新发现 | `execStateGuardSQL` 在**生产路径不可达**(死代码)且注释有误导 | 运行期只把它当"插入首行"用(`ON CONFLICT DO UPDATE` 分支在生产里走不到),注释却写着"绕过 runtime 的直写(备份恢复工具、运维脚本)此前能把一条已完成的执行改回 RUNNING" —— 那条路径并不存在 | **第六轮已修**(见 #196):把注释改写为**事实陈述**("作用范围"三条:只作用于 `SaveExecution` 的冲突分支 / 生产路径不会命中 / 它是导出 API 的契约而非运行期状态机的防线),并点明运行期的防线是 `CasExecutionState`。守卫本身**保留** |
| #188 | 新发现 | gofmt 的文件清单由 `go list` 的 `.GoFiles` 推导 | `go list` **只返回当前平台的文件** —— `agent/process_windows.go` 这类平台专属文件**不在 gofmt 检查范围**内。在 macOS 上改坏它,门禁全绿 | **第六轮已修**(见 #192) |

## 九、轮次 53-56:第六轮(守卫扩面 / 假测试修正 / 门禁盲区 / 文档补齐,commit `60780a5`)

> 3 个子代理并行,产出 **8 条**(#189-#196)。本轮修的是**第五轮复审抓出的
> P1/P2**,共同点是:缺陷不在"功能写错了",而在**守卫写得太弱**——
> 探针扫的范围太窄、断言断言的不是它声称的东西、门禁漏掉了整整一类文件。
>
> 本轮同时把 `docs/OPTIMIZATION.md` 的轮次 41-52 补齐(此前断档),并重写了
> "仍然开放"清单(22 条 → 见第十一节)。

### 轮次 53:状态键分类守卫扩面 + 统一状态写入口

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #189 | 新发现 | `state_mask` 分类守卫只扫 **2 个文件**(`compiler/lower.go` 与 `runtime/executor.go`),且只认 `.Set()` 调用式(receiver 名必须是 `st` / `state`) | 直接写 map 的地方**完全不可见** —— `engine.go` 里 `exec.StateData[compiler.StatusKey(nodeID)] = …` 这种写法,新增的键不会被守卫发现,落库脱敏会把它当自由文本(结构化键被改写 = 执行语义变) | 守卫改扫 `compiler` + `runtime` **全部非测试文件**;识别依据从"变量名"改为**类型名与字段名**(`StateAccess` / `StateData` / `StateDelta` 是结构性的,改变量名不影响识别);覆盖 `.Set()`、`<map>[键] = 值`、`delete(<map>, 键)` 三种形态。另加**防空转下限断言**(扫不到键 / 扫到的形态太少即失败),防扫描器自身退化后测试空转恒过 |
| #190 | 新发现 | 状态写入点**散落各处**,没有统一入口 | 守卫只能靠 AST 猜"哪里写了状态";漏一处 = 落库明文或执行语义被改写。守卫再准,也只是在补救"没有收口"的结构问题 | 新增统一写入口 `setExecState`(顺带兜住 nil map panic);`RetryNode` 等写入点改走该入口。守卫从"发现散落"变成"验证收口" |

### 轮次 54:日志级别实际生效断言 + 校验告警不丢

> 本轮同时把**校验警告**从"整个丢掉"改成"应用层显式记日志"(`logValidationWarnings`)
> —— 此前保存/运行路径只回传 `res.Errors`,`res.Warnings` 被直接丢弃
> (`DEPRECATED_NODE_CONFIG` 这类警告只有显式 validate 端点看得见)。
> 本轮只做到"不丢"(进日志);**要让调用方也拿到**需要改 API 契约,
> 该部分在第七轮完成(#198)。

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #191 | 新发现 | 日志级别测试名声称"即时生效",但**从未验证 logx 的实际级别** | 测试只断言了 app 层字段与库里的值;`logx` 没有导出当前级别的读取接口,真正的全局 logger 级别没被验证 —— 改坏 `SetLevelChecked` 不会让门禁变红 | 补 `logx` **实际生效级别**断言(`Handler.Enabled`),四档(debug/info/warn/error)逐一验证,改坏即变红 |

### 轮次 55:门禁重写(gofmt 清单 / 用例存在性自动推导 / build 产物)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #192 | 新发现 | gofmt 的文件清单由 `go list` 的 `.GoFiles` 推导 | `go list` **只返回当前平台的文件** —— `agent/process_windows.go` 这类平台专属文件**不在 gofmt 检查范围**内。在 macOS 上改坏它,门禁全绿 | 清单改用 `git ls-files`(已跟踪)+ `git ls-files --others --exclude-standard`(新增未跟踪)相加,排除 `build/` 与 `node_modules/`。**实测**:未格式化的 `//go:build windows` 文件被抓住 |
| #193 | 新发现 | 门禁"关键包有用例"是**手写清单** | 手写清单只能拦"清单里已有的包变成 0 用例",拦不住"**新增**的包 0 用例" —— 新包落地即盲区 | 改为从包列表**自动推导**:全项目包(当前 21 个)都必须有真实用例,另设显式豁免清单 `EXEMPT_PKGS`(当前为空)。**实测**:0 用例的新包被拦 |
| #194 | 新发现 | 门禁 `go build` 的产物会污染工作区 | `go build` 在**只编译一个 main 包**时把可执行文件写到项目根 → 多出一个未跟踪二进制;原写法靠"包数 ≥2"这个脆弱前提回避 | `-o` 指向临时目录,退出时统一删掉(不再依赖包数前提) |

### 轮次 56:死代码与注释收口 + 文档补第三至五轮

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #195 | 新发现 | `SaveExecution` 的 **insert 路径落原值** | 落库侧另两条路径(`saveMeta` / `casStateAndSave`)都落脱敏副本,只有 insert 路径落原值 → 首次写入的 `state_json` 是明文(约定 8) | insert 路径改落脱敏副本,三条路径口径一致 |
| #196 | 新发现 | `persistence/zz_vt_test.go` 是**零断言调试探针** | 整个文件只有 `fmt.Println` 没有断言 → 恒真测试(虚假安全感,轮次 31 / 47 已清过两批);其覆盖的场景已被带断言的用例覆盖 | 删除该文件;`execStateGuardSQL` 的注释改写为**事实陈述**("作用范围"三条 + 运行期防线是 `CasExecutionState`),不再把不存在的旁路说成真实威胁 |

### 迭代 21:文档与前端提示同步(第六轮的一部分)

本轮(文档与前端提示子代理)不改动 Go 代码,只做两件前端对齐 + 文档补全:

| 项 | 内容 |
| --- | --- |
| `frontend/src/lib/api.ts` 的 `CODE_HINTS` | 补第四/五轮新增的 6 个码:`DSL_UNKNOWN_KEY` / `DSL_MISPLACED_KEY` / `UNKNOWN_FIELD` / `UNKNOWN_NODE_CONFIG` / `DEPRECATED_NODE_CONFIG` / `INVALID_NODE_ENABLED`。错误体只有 `code` 没有 `error` 时(旧版后端 / 反向代理改写),用户不再只看到"HTTP 400" |
| `frontend/src/pages/Workflows.tsx` | 导入工作流后,若响应带 `ignored_keys` 则提示"N 个键被忽略"并列出键名。桌面模式走 Wails 绑定没有该字段 → `?? []` 兜底,不崩 |

两条都补了 vitest 用例,并做过"改坏实现 → 变红"的实测(见
`docs/TESTING.md`"迭代 21 补做的两条实测")。

## 十、轮次 57-60:第七轮(双模式一致性 / 探针补齐 / 终审复审,迭代 22-25)

> 4 个子代理并行(迭代 22 / 23 / 24 / 25),产出 **15 条**(#197-#211)。
>
> 本轮的性质与前几轮又不同:前几轮是"找新缺陷",本轮是**"审门禁本身"**。
> 终审复审(迭代 23)把第七轮之前的所有成果放在**门禁**上称重,结论是:
> **门禁前 6 步在干净环境里从来没真正跑完过前端那一步**(#202)——
> 六轮里所有前端修复,实际上从未被门禁覆盖过。
>
> 同时,本轮也是第一次有**独立的、可复现的"改坏实现 → 测试变红"实测**
> 支撑登记项(见 `docs/TESTING.md`"终审实测记录"专节:15 组变异实验 +
> 5 组门禁探针)。

### 轮次 57:迭代 22 —— 双模式一致性(顶层键契约下沉)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #197 | 约定收敛 | 顶层键严格化只在 **api 层**(`api/server.go` 的 `parseWorkflow`),桌面模式走 Wails 绑定 → `model.Workflow.UnmarshalJSON` 的宽松 `json.Unmarshal` → **未知顶层键照旧静默丢弃** | 同一份非法载荷,服务器模式报 `UNKNOWN_FIELD`、桌面模式接受并丢弃(约定 10 的双模式一致性) | 顶层键契约**下沉到应用层**(`app/application/workflow_payload.go`):允许键集合由 `model.Workflow` 的 json tag **反射派生**(模型加字段自动生效,不手写);新增 `SavePayload` / `ParseWorkflowPayload` 入口,api 层与桌面模式**走同一段代码**。桌面模式要看到未知键必须拿到**原始载荷**(键在反序列化时就没了)→ api 层三个端点统一改用 `readLimitedBody` |
| #198 | 约定收敛 | 校验**警告**在保存/运行路径被整个丢掉 | `DEPRECATED_NODE_CONFIG` 这类警告只有显式 validate 端点看得见;直连 API 的调用方看不到 → 用户以为设置生效了(约定 14 同族) | 应用层记日志不丢(第六轮);本轮进一步把 `warnings` 作为**响应附加字段**带回(`WorkflowResult.warnings`),两种模式同源;`workflows` 绑定同步新增该字段 |
| #199 | 约定收敛 | 导入的 `ignored_keys` 只有**服务器模式**有 | 服务器模式的 import 端点自己又解析了一遍 DSL 才把这个字段补到响应上,桌面模式根本没有 → "被忽略的键"在桌面模式永远看不到 | `ignored_keys` 改由**应用层**产出(解析只做一遍),两种模式的响应形状一致 |

### 轮次 58:迭代 24 —— 探针补齐(条件变量集合 / `asArray` 守卫)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #200 | 约定收敛 | 条件变量集合(`compiler/conditionenv.go`)**缺 AST 探针** | 只有普通单测 → 校验期与运行期若再次分叉条件变量集合,没有机制会报警(约定 13) | 新增 `workflow/compiler/conditionenv_contract_test.go`(4 例):AST 扫描运行期源码,比对"条件变量 → 运行期取值后端"的契约;含"探针扫够了没有"的防空转断言,以及"运行期不得注入契约未声明的变量"的方向性检查 |
| #201 | 约定收敛 | 前端 `asArray` 守卫漏"**先赋值再 `.map`**" | 守卫只看"列表接口调用点之后的那条链",结果一旦交给变量,链上就没有 `.map` 了 → 绕过 | 守卫扩到**跨语句**的赋值-使用形态,并按**语义成员**(`map`/`forEach`/`filter`/`length`/…)判定"把值当列表用",而不是按方法名;含防空转断言(绑定数下限) |

### 轮次 59:迭代 23 —— 终审复审(门禁可信度 / 脱敏口径 / 弱测试)

> 这几条**不是新写出来的**,而是把第七轮之前的成果放在门禁上称重,
> 结论是"门禁给出的绿灯有相当一部分是假的"。其中三条已在迭代 25 修掉,
> 其余转入第十一节。

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #202 | 新发现 | **P1-1** 门禁第 6 步在干净环境**必红** | eslint 未在 `frontend/package.json` 声明 → 干净 clone 里 `npm run lint` 找不到 eslint(exit 127)→ `&&` 链短路 → **前端构建与 153 条用例从未被执行**;六轮里所有前端修复实际未被门禁覆盖 | **迭代 25 已修**:声明 eslint + 新增 `frontend/eslint.config.mjs`;三个子检查**各自独立**跑完再统一判定(lint 失败时 tsc/vitest 仍会执行);`npx` 换成 `npm run`(避免 npx 联网自动安装) |
| #203 | 新发现 | **P1-2** gofmt 吞掉语法错误 + Windows 路径零覆盖 | `gofmt -l` 对含语法错误的文件:stdout 空、stderr 报解析错、退出码 2;门禁 `2>/dev/null \|\| true` 把三者一起吞掉 → "gofmt 解析不了"被当成"已格式化"。而 `//go:build windows` 文件在 macOS 上不被 `vet`/`build`/`test` 编译到 → **完整 8 步门禁可以全绿** | **迭代 25 已修**:第 1 步分离 stdout/stderr 并检查退出码;新增第 4 步 `GOOS=windows go build` 交叉编译(包集合与第 3 步同范围,任何新增的 Windows 专属文件都被覆盖)。**实测**:含语法错误的 `//go:build windows` 文件在旧门禁下全绿 |
| #204 | 新发现 | **P2-1** `MaskStateData` 顶层字符串值**不做键名判定** | 顶层字符串值走 `logx.Mask`(**按值形态**判定:`key=value` / `token=xxx`),不按**键名**判定 → `api_key` / `auth_token` / `password` 的值若不含凭据形态则**原样返回**;而容器形态走 `logx.MaskValue`(按键名判定)→ **顶层与嵌套口径不一致**。与 #163 承诺的"默认拒绝(新增状态键不会因漏登记就明文落库)"相反 | **本轮已修**:新增 `maskStateValueByKey` —— 把值包进**单键 map** 交给 `logx.MaskValue`,键名判定复用 `logx.keySensitivity`(唯一定义处,不另写一份);outcome JSON 键仍是**唯一例外**(外层键名里的节点 ID 是用户自定标识,按数据字段名判会误伤 → 整值替换会产出非法 JSON)。新增三条用例:顶层敏感键名必须被脱敏 / 键名判定不得越过结构化键 / 键名判定不得破坏 outcome JSON |
| #205 | 新发现 | **P2-2** `output` 键的 resume/实时口径差 —— **实测可达的分支改变** | 触发:skill 节点输出 `ANTHROPIC_API_KEY=sk-xyz`,边条件 `output contains "sk-"`。首次运行(实时原文)→ 走 `secret_path`;HITL 答复触发 resume → 引擎回放**已脱敏**的 `state_json` → 决策节点虽被跳过,`matchBranch` 仍用脱敏值**重新求值** → 改走 `normal_path`。在审批流里 = **人批准 A 分支,恢复后走 B 分支** | **本轮已修**:守卫改成真断言(未归属的读取点 → `t.Errorf`),补 `nodeEnabled` 的全局归属登记,并加**防空转断言**(探针一个函数都没扫到即失败)。**注**:第六轮登记时把这条写成"影响面窄(仅当输出含凭据形态子串且条件恰好查那段)",终审证明它是**可达的分支改变**,不是"窄到无所谓" |
| #206 | 新发现 | **P3** `nodeconfig_contract_test.go` 存在**恒真路径** | `TestRuntimeConfigReadsAllAttributed` 整个函数体只有 `t.Logf` 没有 `t.Error` → **永远不会失败**;`TestNodeConfigSpecMatchesRuntimeReads` 在"运行期函数改名/删除"时走 `mapped == 0` 分支(`t.Logf` + `continue`)→ 逐类型比对被整体跳过。**该文件没有防空转下限断言**(第六轮给 `state_mask` 守卫加了,这里没加) | **未修**,见第十一节 |
| #207 | 新发现 | **P3** `template.Engine.RenderErr` 的**错误出口零覆盖** | `RenderErr` 的 `err != nil` 只在 `renderAll`/`lookup`/`toString` 自身 panic 时可达;而 `toString` → `safeString` 已经兜住了 Stringer 的 panic → 正常输入**永远走不到**该出口。运行期测试用的是**注入的失败渲染器桩**(`failingRenderer`),不经过真实引擎 → 真实引擎的错误出口无任何测试 | **本轮已修**:给 `Engine` 加一个**测试接缝**(`lookupFn`,未导出字段,包外无法注入)—— 生产路径的 panic 源(`toString` 的 Stringer 兜底、fmt 内部的 `String`/`Error`/`Format` 兜底)都会自己 recover,畸形模板/空数据都触发不了 panic → 没有缝就永远验证不了这条契约。新增用例:注入会 panic 的 lookup,断言 `err != nil` + 文本可读 + 返回原始模板。**实测(修复前)**:把 `RenderErr` 改成吞掉错误、或整个删掉 `recover()`,`template` 与 `workflow/runtime` 两个包**全绿** |
| #208 | 新发现 | **P3** 并发探针依赖 `time.Sleep` 建立读写重叠窗口 | 5 个并发测试的形态是"写者做 N 次有界迭代 + 读者自旋到 stop",重叠窗口靠 `time.Sleep(100ms)` 建立 → 调度不巧时读写**零重叠**,`-race` 什么也报不出。**实测**:在受控复现里,`-race` 对 20 / 200 / 3000 次迭代的**报告数完全相同** → 真正的问题不是"迭代次数太少",而是**重叠窗口不是确定的** | **未修**,见第十一节 |
| #209 | 新发现 | **P3** 门禁看不见"所有用例都被 `t.Skip()` 跳过" | 门禁第 6 步按 `go test` 的结果行判"有没有真实用例",判据是 `no tests to run`;而**全部 skip** 时 `go test` 仍报 `ok <pkg> 0.006s` → 判为"有真实用例" → 放行。`agent/claude/real_smoke_test.go` 等文件在无 CLI 的环境里正是全 skip | **未修**,见第十一节 |
| #210 | 新发现 | **P3** 前端 `asArray` 守卫是**文件级**判定,单点绕过 | 守卫第 2 条只要求"调用了列表接口的文件里**出现过** `asArray` 字样" → 同一文件里**某一个**列表调用点丢掉 `asArray` 防御,只要该文件别处还有 `asArray`(哪怕用在非列表接口 `api.stats()` 上)就完全看不见 | **未修**,见第十一节。**实测**:把 `Dashboard.tsx` 两个列表调用点的 `asArray` 去掉(保留 `api.stats()` 上的),守卫**三条用例全绿** |

### 轮次 60:迭代 25 —— 门禁与冒烟加固

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #211 | 新发现 | 冒烟的端口预检**只报端口号,不报占用者** | 端口被占时只打印"端口 18181 已被占用",用户得自己猜是谁占的 → 排障第一步就要靠猜 | `scripts/smoke.sh` 与 `scripts/smoke_sse.sh` 增加 `print_port_owners`:依次尝试 `lsof`(macOS)→ `ss`(Linux)→ `netstat`(兜底),三者都拿不到时**明说"未能定位"**,不假装打印了占用者 |

## 十一、轮次 61-70:第六至十轮补登(门禁自检 / 全域扫描 / 凭据回写 / 权限单一源)

> **本节的来历**:正文第九、十节已登记第六轮(#189-#196)与第七轮(#197-#211),
> 但**第八、九、十轮**的成果此前只在 commit message 里,没有进正文。
> 本节按"轮次 61-70"补登,编号自 **#212** 起续(此前结束于 #211)。
>
> 四段补登的性质递进:
> - **第六轮(轮次 61-63)** —— 补登 commit `60780a5` 中**此前未单独立项**的部分
>   (第九节已登记 8 条,此处补 3 条只写在 commit message 里的)。
> - **第七/八轮(轮次 64-66)** —— commit `bb5cd63`:门禁硬伤 + 双模式一致性。
>   第七轮的**终审复审**已在第十节登记;此处的**门禁硬伤**与
>   **双模式契约补全**是同一 commit 里**尚未单独立项**的部分。
> - **第九轮(轮次 67-68)** —— commit `e5971ed`:`output` 键 resume 口径差根治
>   + 终审复审 10 场景实测 + `asArray` 守卫的调用点识别盲区 + 全域扫描抓到的
>   P0/P1/P2/P3。
> - **第十轮(轮次 69-70)** —— commit `795f26f`:ExtraArgs 凭据回写 + 制品出口脱敏
>   + 门禁补 Windows 测试文件 + 门禁自检 + 前端吞错可见化 + 权限单一源。

### 轮次 61-63:第六轮补登(commit `60780a5`)

> 第九节(#189-#196)已登记第六轮的 8 条主项。此处补 3 条**当时做了、
> 但只写进了 commit message、没单独立项**的改动 —— 它们的共同点是:
> **"守卫看起来在跑,但没人验证过它真的有效"**。

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #212 | 新发现 | `state_mask` 守卫的**行为测试近乎恒真** | 该测试用的值(`"APPROVED"` / `"success"`)**不含敏感词** → `logx.Mask` 改不动 → 键在不在 `structuredStateKeys` 里都 PASS。实测:删掉结构化键的登记,测试**照样绿** —— 虚假安全感(轮次 31 / 47 已清过两批恒真测试,这是第三批) | 改用**会被 `logx.Mask` 改动**的凭据形态值(`key=value`)→ 一旦该键被当成自由文本,值立刻变成 `token=***`,断言变红;并加**前置自检**:先断言 `logx.Mask` 确实会改动这个值,否则测试前提不成立直接失败 —— 防"连测试用的值都失效了"这种退化再次恒真 |
| #213 | 新发现 | `execStateGuardSQL` 与 `CanTransitionExecution` 是**两份独立的终态判定**,彼此不验证 | 持久层的守卫按自己的规则拦非法状态跳变,状态机按自己的规则算合法迁移 —— 两边规则一旦漂移,就会出现"持久层拦住了状态机认为合法的跳变"(执行莫名失败)或反之 | 新增 `persistence/exec_state_guard_test.go`:**状态机一致性交叉测试** —— 枚举两边的判定集合,断言**逐条一致**。实测:改动其中任一侧的规则,交叉测试变红 |
| #214 | 新发现 | 门禁的"关键包有用例"检查用 `go test` 的**结果行文本**判断,不看**用例是否真被跑到** | `go test` 对一个"测试文件里没有 Test 函数"的包报 `? pkg [no test files]`,对"有 Test 函数但被 build tag 排除"的包报 `ok pkg [no tests to run]` —— 两者都被判成"缺用例",但**第三种情况**(用例存在却被 `-short` 跳过)看不出来 | 判据细化为三种输出变体分别归类(`zero` / `nofiles` / `missing`),每种给**不同的原因文案**;`EXEMPT_PKGS` 豁免清单从"手写"改为显式登记(当前为空 = 21 个包全部要有真实用例) |

### 轮次 64-66:第七/八轮补登(commit `bb5cd63`)

> 第十节(#197-#211)已登记第七轮的**终审复审**结论与"探针补齐"。
> 此处补登同一 commit 里**尚未立项**的**门禁硬伤**与**双模式契约补全**。

#### 门禁硬伤(轮次 64)

> 这一组的共同形态:**门禁自己可以全绿,而项目是坏的**。
> 上一轮把它们评为 P1/P2,本轮全部修复。

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #215 | 新发现 | **eslint 未在 `frontend/package.json` 声明** | eslint 实际来自**被 gitignore 的仓库根 `node_modules`** → 干净 clone 里 `npm run lint` 找不到 eslint(exit 127)→ `&&` 链短路 → **前端构建与全部前端用例从未被执行**。六轮里所有前端修复,实际从未被门禁覆盖过 | 声明 5 个 eslint devDep 并重生成锁文件;新增 `frontend/eslint.config.mjs` |
| #216 | 新发现 | 前端门禁是**一条 `&&` 链**(`lint && build && vitest`) | 最前面的 lint 一失败,后面的 tsc 与 vitest **全被短路跳过** —— 失败快速用错了地方:lint 的问题不该让你看不见 tsc 和 vitest 的问题,更不该让"前端构建 + 全部前端用例"在干净环境里变成死代码 | 三个子检查**各自独立**跑完再统一判定:各自取退出码、全部执行、失败项一次性全部打印,最后统一判定(任一失败 → 整体失败) |
| #217 | 新发现 | `//go:embed all:frontend/dist` + `frontend/dist` 被 gitignore | 干净 clone 里 `go list ./...` 直接失败(`pattern all:frontend/dist: no matching files found`)→ 门禁**第 0 步**就退出,后面 9 步一步都跑不到 —— 前端构建与全部前端用例在干净环境里**仍然是死代码**(只是死在了更前面) | 门禁在 `go list` 之前加 embed 兜底:目录已存在且非空 → 一个字节都不动;缺失或为空 → 放一个占位文件(embed 不接受空目录)。前端真实构建(vite build,`emptyOutDir` 默认 true)会清掉占位换成真产物 |
| #218 | 新发现 | `CODE_HINTS` 码表只覆盖**后端码的子集**(15 码),且**没有反向守卫** | 错误体只有 `code` 没有 `error` 时(旧版后端 / 反向代理改写),用户只看到裸的"HTTP 400";而**后端新增一个码、前端忘了补**这件事没有任何机制会发现 —— 码表与后端**各写一份**,必然漂移 | 码表补到 **64 码**(后端全集);新增**双向守卫**:扫描后端 Go 源码的**全部** code 与前端码表求差集 —— 后端有前端没有 → 红;前端有后端没有 → 红(该码从未生效,留着的提示是误导)。修前实测:**缺 50 个、多 1 个**(多出来的 `DENIED` 从未生效过) |

#### 双模式契约补全(轮次 65)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #219 | 新发现 | 桌面分支的 IPC 错误**不转 `ApiError`** | 服务器模式看到的是剥掉 `Kind(CODE):` 前缀 + 中文提示的 `ApiError`;桌面模式看到的是 Wails IPC 带内部前缀的裸文本 → 同一份错误,两种模式**形态不同**,前端的 `ApiError.code` 分支在桌面模式**恒不成立** | 新增 `toApiError(e)`:桌面模式的所有 IPC 错误统一转成 `ApiError`(剥前缀 + 解析 code/kind),与服务器模式**同一形状** |
| #220 | 新发现 | 桌面分支的 `updateAgentConfig` **不更新后回读** | 桌面模式直接返回绑定的 void 结果 → 调用方拿到 `null`,**掩码展示失效**;服务器模式则回读**掩码后**的配置 → 两种模式**行为不同**(约定 10) | 桌面分支改为更新后回读 `GetConfig`,返回**掩码后**的配置,与服务器模式形状一致。`resetAgent` 同改 |
| #221 | 新发现 | 前端 `CODE_HINTS` 的守卫只做**单向**(后端 → 前端) | 前端多出的码(后端已废弃的)不会被发现 → 提示表里留着一个**永远不会出现**的码,占位且误导 | 守卫改为**双向**:两个方向的差集都必须为空(见 #218) |

### 轮次 67-68:第九轮(commit `e5971ed`)—— 终审复审 + 全域扫描

> 第九轮做了两件此前没做过的事:
> ① **终审复审** —— 把前八轮的成果放在**门禁**上称重,得出"该失败却成功"
>    专项的**实测**结论(不是看代码看出来的);
> ② **全域扫描** —— 不按模块分,按"出口"扫一遍全仓,抓到两条**此前所有轮次都漏掉的 P0**。

#### 终审复审与守卫盲区(轮次 67)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #222 | 新发现 | `output` 键在 resume 与实时路径**口径不同** —— 根因不止"凭据形态" | `output` 的语义是"最近一次成功节点的输出",**分支内后续节点会覆盖它** → 恢复时重算读到的是**完全不同的值**。此前登记为"影响面窄",终审实测证明是**可达的分支改变**:人批准 A 分支,恢复后走 B 分支(约定 14 同族:走错分支比崩溃更危险 —— 用户看不到任何异常,只看到"流程跑完了") | **根治**(详见轮次 68 的收口方式):路由依据与展示文本分离 |
| #223 | 新发现 | 前端 `asArray` 守卫的**调用点识别**有两处盲区 | ① `CALL_RE` 要求 `api.` **紧挨着**方法名 → 换行写法(`api` 换行 `.listAgents()`)的调用点**完全不可见** —— 实测仓库里有三处这种写法(`pages/Designer.tsx`、`pages/Workflows.tsx`、`components/layout.tsx`),它们只被文件级判定兜着;② 回调形参**带类型标注**时(`.then((execs: Execution[] \| null) => …)`)正则匹配不上 → 该绑定不可见(实测 `components/layout.tsx` 的 `listExecutions` 正是这种写法) | `CALL_RE` 放宽为允许 `api` 与方法名之间有空白/换行;回调形参正则允许可选的类型标注;并补**守卫自身的盲区回归用例**(用合成源码验证"换行调用点必须被看见、去掉防御必须被判违规") |

#### 全域扫描抓到的 P0(轮次 67)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #224 | 新发现 | `extra_args` 的 `***` 掩码**没有回写语义** | 用户打开配置拿到掩码 → 只改模型 → 保存 → **真实 API Key 被覆盖成字面量 `***`** → 之后每次 CLI 调用带 `--api-key ***` → 鉴权失败,而用户看到的是"已保存" | **第十轮修复**(#235):按参数名对齐合并 |
| #225 | 新发现 | 制品(`/artifacts`、`/export`)**完全未脱敏** | `git diff` 原文经 API 返回 —— 凭据形态的片段(`ANTHROPIC_API_KEY=sk-...`)直接进响应体 | **第十轮修复**(#236):按值的形态脱敏 |

#### 全域扫描抓到的 P1/P2/P3(轮次 68)

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #226 | 新发现 | `default_working_dir` 是**死设置** | 写入即生效(API 返回成功),但运行期**从不读它** → 用户以为设了默认工作目录,实际所有节点仍在默认目录跑 | **第十轮修复**(#241) |
| #227 | 新发现 | `git_working_dir` **无法清空** | 设置页把输入框清空 → 后端把空串当成"没传" → **旧值保留** → 用户以为回到默认目录,实际还在旧目录 | **第十轮修复**(#242) |
| #228 | 新发现 | `Settings.Update` **部分生效** | 先落库并应用 `git_working_dir`,**之后**才校验 `log_level` → 校验失败返回 400,但**目录已经改了** → 用户收到"失败"却发现设置变了(约定 14:该失败却成功) | **第十轮修复**(#242) |
| #229 | 新发现 | 权限判定有**双源**:全局策略与工作流 `permissions` 段各算一次 | ① 全局策略在执行**启动时**取快照 → 运行中收紧权限对**在跑的执行无效**;② `permissions` 段用"**替换**"语义 → 能**放宽**全局禁用,与适配器的全局判定结论相反 → 用户以为的收紧根本没生效 | **第十轮修复**(#240):单一源 + 只收紧 |
| #230 | 新发现 | `--git-dir` 启动参数的**错误被丢弃** | 指定一个不存在的目录 → 静默回落到默认目录,**不报错** → 用户以为工作目录设对了 | **第十轮修复**(#244) |
| #231 | 新发现 | `on_loop_limit` **不校验取值** | 拼错的 `"fl"`(想写 `fail`)→ 静默落回 `wait_user` → 循环超限时**暂停等人**而不是**失败** → 用户以为程序卡住了 | **第十轮修复**(#243) |
| #232 | 新发现 | 任务文本**明文存 localStorage** | 任务文本里常有凭据(`ANTHROPIC_API_KEY=sk-...`),`localStorage` **永久留存**且永不清理 | **第十轮修复**(#239) |
| #233 | 新发现 | SSE 的 `json.Marshal` 失败**被丢弃** | `continue` 掉这一条事件,不留任何痕迹 → 事件流里**少了一条**但没人知道是哪条 | **仍然开放**(见第十二节)—— 需要"该事件为什么发不出去"的可见口径 |
| #234 | 新发现 | `crypto/rand.Read` 的失败**被吞掉** | `_, _ = rand.Read(b)` → 熵不足时 ID 用**全零字节**生成 → ID 可预测,且没有任何迹象 | **仍然开放**(见第十二节)—— 失败必须让调用方知道(至少报错) |

### 轮次 69-70:第十轮(commit `795f26f`)—— 凭据回写 / 制品脱敏 / 门禁自检 / 权限单一源

| 编号 | 来源 | 缺陷 | 触发路径与影响 | 修复 |
| --- | --- | --- | --- | --- |
| #235 | 新发现 | `extra_args` 的回写**按下标对齐**会把 A 的凭据写到 B 的参数位 | 参数**增删**后下标全部错位 → 用户改一个参数,另一个参数的凭据被写到**错误的参数名**上 → 凭据泄漏到第三方服务,或鉴权莫名失败。**按参数名对齐**(同源归一 `--api-key` / `--api_key` / `--API-Key`)才是唯一安全的做法 | 回写改为**按参数名对齐合并**:无原值的 `***` 丢弃;已存值本身是 `***` 时视为无真值;下发 CLI 前**剔除字面量 `***`**(防旧脏数据)。**实测**:`TestMergeExtraArgsNeverCrossesOptionNames` 锁住"A 的凭据绝不进 B 的参数位" |
| #236 | 新发现 | 制品脱敏若按"**键名敏感就整值替换**",会把代码里的标识符一并抹掉 | `const token = getToken()` 整行变成 `***` → **diff 的可读性被毁**。第八轮为此把"artifact 不脱敏"钉死在测试里,本轮**重新裁决**这个取舍 | 改为**按值的形态脱敏**:**键名指向凭据且值呈凭据形态**才替换;代码标识符保留。落库 + 读出口**都过**。新增可读性回归锁(`TestMaskCredentialValuesKeepsCodeIdentifiers`) |
| #237 | 新发现 | 门禁第 4 步只有 `GOOS=windows go build`,**不含 `_test.go`** | `go build` 手册明文:"When compiling packages, build ignores files that end in `_test.go`" → 任何平台专属**测试文件**里的类型错误,在 macOS/Linux 上**永久不可见** → 整条 9 步门禁可以全绿 | 第 4 步补 `GOOS=windows go vet`(**vet 连同 `_test.go` 一起做类型检查**)。**实测**:造一个含类型错误的 `//go:build windows` **测试**文件 → 旧门禁 exit 0 全绿(79s),新门禁 exit 1 步骤 4 红 |
| #238 | 新发现 | 门禁的**每道防线都没有自检** —— 防线自己被删掉,门禁照样绿 | 步骤 4 写着 `GOOS=windows go vet`,但**没有任何用例证明它真的能抓到错误**;将来有人把 `vet` 从这一步删掉,自检不存在 → 盲区静默回来 | 新增**门禁自检**(隔离临时模块,3 个用例):① 对照组(类型正确的 windows 测试文件)**必须通过**;② 含类型错误的 windows **测试**文件**必须失败**;③ 含类型错误的 windows **源**文件**必须失败**。隔离模块放 `/tmp`(不碰工作区),`go.mod` 的 go 指令**取自仓库自己的 go.mod**(不硬编码版本号) |
| #239 | 新发现 | 前端 **11 处 `.catch(() => {})`** 把接口失败吞成空值 | 后端不可用时 Dashboard 渲染"暂无工作流",与"真的没有工作流"**完全无法区分** → 用户以为没数据,实为加载失败。而第八轮做好的 `ApiError`(错误码中文提示)因此成了**死代码** —— 错误被吞掉,提示永远到不了 UI | 逐处分类:**真吞错 11 处已修**(接上可见提示)、**刻意防御 1 处保留**(`Notification.requestPermission()`,写明理由)。区分两种失败:**接口返回非数组 → 静默降级**(合法形态);**请求失败 → 必须提示**。新增统一错误出口(`lib/errors.ts`,5 秒去重防提示风暴)+ 静态守卫(`frontend/src/test/error-guard.test.ts`,含防空转断言)。桌面分支 `import` 与 SSE `onerror` 补可见处理 |
| #240 | 新发现 | 权限判定是**双源**:全局策略(执行启动时的快照)∧ 工作流 `permissions` 段("替换"语义) | ① 运行中收紧全局权限,**在跑的执行不受影响**(快照已取);② `permissions` 段能**放宽**全局禁用 → 用户在 DSL 里写的"收紧"被反转成"放开",而适配器(只看得到全局策略)给出的结论相反 → **同一个执行里两处判定打架** | 收敛为**单一来源**:有效权限 = **全局实时** ∧ `permissions` 段(**只收紧**,求交)。放宽尝试**在校验期**报出(`PERMISSION_OVERRIDE_LOOSENS`);全局收紧**立即对在跑的执行生效**(作用域视图跟随父级实时)。`mode → 是否需要写权限` 也收敛为唯一来源 `agent.ModeRequiresWrite` |
| #241 | 新发现 | `default_working_dir` 后端**接受新值但不生效** | 用户改默认工作目录 → API 返回 200 → 运行期仍不读它 → 设置形同虚设 | 后端**拒绝新值**(返回 `UNSUPPORTED_AGENT_SETTING` 并说明原因:请改用节点级 `working_dir`,或清空该字段);**已存的老值**仍可原样回存(不阻断存量工作流) |
| #242 | 新发现 | `Settings.Update` **先落库后校验** | 非法 `log_level` → 返回 400,但 `git_working_dir` **已经改了** → 用户收到"失败"却发现设置变了 | 改为**先全量校验、后统一落库**:任一项非法则**一项都不改** |
| #243 | 新发现 | `on_loop_limit` **不校验取值** | 同 #231(第九轮登记,第十轮修复) | 校验期加**白名单**(`fail` / `wait_user`),非法值报 `INVALID_LOOP_LIMIT`。运行期靠 `p.Loop.OnLoopLimit == "fail"` 区分两条完全不同的路径 |
| #244 | 新发现 | `--git-dir` 启动参数的**错误被丢弃** | 同 #230(第九轮登记,第十轮修复) | 改为**持久化语义**:参数写入设置并在设置页可见;持久化失败则**明确回退**并说明原因;空串是**无操作**(不是"清空") |

## 十二、仍然开放(轮次 61-70 之后)

> 下面是**当前**的完整开放清单。前面各轮登记过的开放项,**已修的划掉并
> 注明去向**,真正还开着的留在下面。编号沿用正文。

### 已在本轮之前解决(留档,不再是开放项)

> 本节只留**早于本轮**就已关闭、且**下方的逐条清单没有覆盖**的条目。
> 本轮复核过的条目一律在下方"仍然开放 / 本轮复核后关闭"两节里给出。

- ~~**分支 goroutine 的 panic 没有兜底**~~ —— 轮次 31 已修(#84)。
- ~~**执行全程无持久化**~~ —— 轮次 31 已修(#85)。
- ~~**ADK 契约待补**~~ —— 轮次 30 补齐到 9 条。
- ~~**`workflow/dsl` 的 9 个零引用配置结构体** —— 轮次 41 已删(#142),
  并加了 AST 守卫防止再写一份。
- ~~**门禁自身的三个缺陷(#120 / #121 / #122)** —— 轮次 43 全面重写为
  8 步(补 `go build`、用例存在性检查、gofmt 按文件清单、
  冒烟端口预检等),第六轮把"关键包"清单改为自动推导、
  第七轮扩为 **9 步**(补 Windows 交叉编译、前端三子检查
  独立)。`build/ios` 本身的编译问题**仍开放**(见第 1 条)。

### 仍然开放(本轮逐条复核后仍开着的)

> **本清单已按当前 HEAD 逐条复核**(读代码 + 实测探针 + 跑门禁),
> 不是从上一版沿抄的。每条后面标注了复核依据。

1. **`build/ios` 的编译问题(#120 / #157)** —— **仍然开放**。`go build ./build/ios/...`
   报 `function main is undeclared in the main package`(该包用 `//go:build ios`,
   `go list ./...` 在非 iOS 平台仍会枚举它)。第三轮的处理是**让门禁显式排除
   `build/` 目录**,而不是假装它能编译 —— 根因在 `build/` 属于 Wails 生成物的前提下
   不好单独修。**同类问题**:`node_modules/flatted/golang/pkg/flatted` 是根目录误装
   npm 依赖时夹带的 Go 包,同样排除;根因(根目录不该装 npm 依赖)未处理。
2. **`pi` 适配器无真机冒烟** —— **仍然开放**(自轮次 29 起)。同形替身
   (`agent/pi/stub_smoke_test.go`)已覆盖子进程层,但**真实 pi-agent CLI 的输出
   形态**仍未验证。
3. **桌面模式的"点击交互"未做像素级验证** —— **仍然开放**。渲染已验证
   (轮次 32),点击需要辅助功能 TCC 授权,当前环境未授。
4. **列表接口的 `limit` 上限 1000 是硬编码常量** —— **仍然开放**。超过 1000 条被
   **静默截断**,响应里没有任何"已截断"标记;真正需要大数据量时得引入分页契约。
5. **SSE 断连后的订阅释放只有单测覆盖** —— **仍然开放**。`api/sse_test.go` 断言了
   写失败即关流,但**冒烟脚本没有"断开后订阅数归零"的断言**(冒烟只测事件送达与
   keepalive)—— 端到端层面仍可能回归。
6. **未知配置键只查顶层,子键不查** —— **仍然开放**(#183 的已知边界)。校验期的
   `UNKNOWN_NODE_CONFIG` 只比对节点配置的**第一层**键;`prompt.template` /
   `retry.max_attempts` 等**子键不查** —— 因为 `prompt` 既可以是字符串也可以是
   `{template:…}`,`args` / `input` 更是自由映射,逐个子键校验会误伤大量合法写法。
7. **`permissions:` 段层级错放的处理是"部分已修"** —— **部分关闭**。DSL 层已报错
   (`DSL_MISPLACED_KEY`,见 #171),但**节点级**或 `model.Workflow` 路径上的错层级
   仍可能被静默丢弃(与顶层键严格化同源)。
8. **前端 `asArray` 守卫是文件级判定,单点绕过(#210)** —— **仍然开放**
   (第九轮实测复核:仍然绕过)。守卫第 2 条只要求"调用了列表接口的文件里**出现过**
   `asArray` 字样" → 同一文件里**某一个**列表调用点丢掉防御,只要该文件别处还有
   `asArray`(哪怕用在非列表接口 `api.stats()` 上)就完全看不见。
   **本轮实测(隔离副本)**:把 `Dashboard.tsx` 的 `listWorkflows()` 与
   `listExecutions()` 两个调用点的 `asArray` 去掉(保留 `api.stats()` 上的),
   `list-guard.test.ts` **4 条用例全绿** → 盲区确认仍在。
   修法方向:判定从"文件级出现过"改为"每个列表调用点的返回值都必须过 `asArray`"。
9. **并发探针依赖 `time.Sleep` 建立读写重叠窗口(#208)** —— **仍然开放**
   (本轮实测复核:仍然开放)。并发测试的形态是"写者做 N 次有界迭代 + 读者自旋到
   stop",重叠窗口靠 `time.Sleep(150ms)` 建立 → 调度不巧时读写**零重叠**,
   `-race` 什么也报不出。**实测(受控复现)**:`-race` 对 20 / 200 / 3000 次迭代的
   **报告数完全相同** → 真正的问题不是"迭代次数太少",而是**重叠窗口不是确定的**。
   (第七轮把写侧迭代提到 3000 次只提高了**命中概率**,没有改变"窗口靠 sleep
   撞运气"这一根本形态 —— 见 `app/application/application_test.go` 的
   `TestSkillDisabledConcurrentAccess` 与 `TestLogLevelConcurrentAccess`,
   两处仍是 `time.Sleep(150 * time.Millisecond)`。)
   修法方向:用 channel / `sync.WaitGroup` 做握手,让读写必然重叠。
10. **门禁看不见"所有用例都被 `t.Skip()` 跳过"(#209)** —— **仍然开放**
    (本轮复核:门禁第 6 步仍是按 `go test` 的**结果行文本**判断,判据是
    `no tests to run`;而**全部 skip** 时 `go test` 仍报 `ok <pkg> 0.006s`
    → 判为"有真实用例" → 放行)。**实测**:一个只含 `t.Skip` 的包,`go test` 输出
    `ok skipcase 0.006s`,门禁判据不命中。`agent/claude/real_smoke_test.go` 等文件
    在无 CLI 的环境里正是全 skip。
    修法方向:第 6 步改用 `go test -json`,统计每个包的 `Test` 事件数
    (skipped 不计入)。
11. **会话持久化 / ADK v2 迁移** —— **仍然暂缓**,理由与评估见
    `docs/ROADMAP.md`。
12. **SSE 的 `json.Marshal` 失败被丢弃(#233)** —— **仍然开放**。`api/server.go`
    的 SSE 循环里 `raw, err := json.Marshal(ev)` 出错时直接 `continue` → 这一条
    事件**不送达且不留任何痕迹**,事件流里"少了一条"但没人知道是哪条。
    需要"该事件为什么发不出去"的可见口径(至少落一条告警)。
13. **`crypto/rand.Read` 的失败被吞掉(#234)** —— **仍然开放**。
    `app/application/application.go` 与 `workflow/runtime/engine.go` 都是
    `_, _ = rand.Read(b)` → 熵不足时 ID 用**全零字节**生成 → ID 可预测且毫无
    迹象。失败必须让调用方知道(至少返回错误)。
14. **前端"接口返回非数组"仍静默降级** —— **仍然开放**(有意保留,非缺陷)。
    第十轮把"接口返回非数组"(合法形态:后端返回 `null`/对象)与"请求失败"
    (必须提示)区分开:前者仍走 `asArray` 静默降级成空数组。**这是刻意的**——
    合法形态不该弹错误。但它有一个**残留**:后端**契约变了**(本该返回数组却返回了
    对象)时,用户看到的仍是"暂无数据",与"真的没有数据"无法区分。
    修法方向:区分"后端契约就是单对象"(接口声明已知)与"契约漂移"
    (声明是数组却拿到非数组)—— 后者应有一次性告警。

### 本轮复核后关闭的登记项(新登记 → 关闭的去向)

> 以下条目在本轮**逐条核实**后确认已经关闭,登记去向以供追溯。
> 第 15-18 条是本轮**新登记**的项(前几轮未进文档),第 19-42 条是此前已登记、
> 本轮复核确认关闭的项。
>
> **关于第 15-18 条的"关闭"**:这四条由**第十一轮**(并发子代理)在**工作区**
> 修掉,**尚未提交** —— 复核时读的是工作区代码,提交与否由主代理统一决定。
> 若那一轮最终没有进入主干,这四条要**回到"仍然开放"**。

15. **门禁的"孤儿包"盲区** —— **已关闭**(第十一轮,**在途改动**:该轮尚未提交)。
    此前 `go list ./...` **只列出当前平台有可编译文件的包** → 一个只含
    `//go:build windows` 文件的**全新包**根本不在列表里(实测 0 命中)→
    对整条 9 步门禁**完全隐形**(连类型错误都不报)。修法:包集合改按
    **git 跟踪的 .go 文件**推导,对 `go list` 看不见的目录逐个跑
    `GOOS=windows/linux/darwin` 交叉 `vet`;并加门禁自检(类型正确的孤儿包必须通过、
    含类型错误的孤儿包必须被抓)。
16. **`claude.Config.Perms` 死字段** —— **已关闭**(第十一轮,**在途改动**:该轮尚未提交)。
    该字段挂着 `*permission.Manager` 却**从不读它** → 调用方以为"权限已交给适配器
    兜底",实际兜底不存在(与 `default_working_dir` 同族的死设置)。现已删除,
    并在 `agent/claude/claude.go` 的注释里写明**为什么 Claude 适配器不需要这个字段**
    (CLI 层已用 `--disallowedTools` 硬禁写操作;读权限由运行期节点检查统一管)。
17. **`persistence` 的 unmarshal 错误被吞** —— **已关闭**(第十一轮,**在途改动**:该轮尚未提交)。
    此前 `repo.go` 里十余处 `_ = json.Unmarshal(...)` → 定义列损坏时**静默得到空值**:
    `permission_json` 损坏 = 权限收紧**静默失效**(安全相关);
    `nodes/edges` 损坏 = 跑的是**另一个工作流**(约定 14)。现已按列分类:
    **定义列损坏 → 显式失败**,**数据列损坏 → 降级并带原因**。
18. **`submit` 模式权限缺口** —— **已关闭**(第十一轮,**在途改动**:该轮尚未提交)。
    运行期节点检查只对 `execute|fix` 查写权限,而 `pi` 适配器自己查的是
    `execute|fix|submit` → 两份集合不一致 → `mode: submit` 的节点**不受工作流级
    `permissions` 段约束**(适配器只看得到全局策略)。现已收敛为唯一来源
    `agent.ModeRequiresWrite`,运行期与适配器共用。
19. **`on_loop_limit` 不校验** —— **已关闭**(第十轮 #243):校验期加白名单
    (`fail` / `wait_user`),非法值报 `INVALID_LOOP_LIMIT`。
20. **`default_working_dir` 死设置** —— **已关闭**(第十轮 #241):后端拒绝新值
    (`UNSUPPORTED_AGENT_SETTING`),已存老值仍可原样回存。
21. **`git_working_dir` 无法清空** —— **已关闭**(第十轮 #242):空串 =
    **清除**(落库空串 + 回到启动默认目录),与"没传该字段"严格区分。
22. **`Settings.Update` 部分生效** —— **已关闭**(第十轮 #242):先全量校验、
    后统一落库;任一项非法则**一项都不改**。
23. **权限双源** —— **已关闭**(第十轮 #240):有效权限 = **全局实时** ∧
    `permissions` 段(**只收紧**);放宽在校验期报出,全局收紧立即对在跑的执行生效。
24. **`--git-dir` 错误被丢弃** —— **已关闭**(第十轮 #244):改为持久化语义,
    错误明确回退并说明原因。
25. **`extra_args` 的 `***` 掩码无回写语义** —— **已关闭**(第十轮 #235):
    按**参数名**对齐合并(拒绝下标对齐 —— 增删参数后会把 A 的凭据写到 B 的参数位);
    无原值的 `***` 丢弃;下发 CLI 前剔除字面量 `***`。
26. **制品出口完全未脱敏** —— **已关闭**(第十轮 #236):按**值的形态**脱敏
    (键名指向凭据**且**值呈凭据形态才替换),代码标识符保留;落库 + 读出口都过。
27. **`output` 键在 resume 与实时路径的口径差(#205)** —— **已修 + 残余边界**
    (第九轮 #222 根治)。**根因不止凭据形态**:`output` 的语义是"最近一次成功节点
    的输出",**分支内后续节点会覆盖它** → 恢复时重算读到的是完全不同的值。
    **修法(路由依据与展示文本分离)**:路由一旦选定出口分支,这个选择就是本次执行的
    **既成事实** → 记进**结构化**状态键 `route:<决策节点>:branch`(值是分支下标,
    枚举,不含自由文本,不落凭据),恢复时**回放**它,不再拿脱敏副本重新求值。
    三条路都不选的原因:把 `output` 排除出脱敏 → 凭据明文落库(违反约定 8);
    另存一份未脱敏 `output` → 同样是明文落库;把 `output` 从条件变量集合去掉 →
    破坏 `compiler/conditionenv.go` 的唯一定义(违反约定 13)。
    **残余边界(两处,已在 `workflow/runtime/state_mask.go` 写明)**:① 路由在暂停前
    **从未选定过分支**时(例如循环达到上限后暂停,恢复时人的答复合法地改变了
    decision)→ 这次求值用的仍是落库的 `output` 副本;② 多回边循环里的
    `switchAgent`(选本轮走哪条回边)每轮重新求值,读到的"上一轮 output"在恢复时
    是脱敏副本。这两处要彻底消除,只能把原文留在盘上(违反约定 8)或取消 `output`
    作为条件变量(违反约定 13)—— 属于三个硬约束真正互斥的角落。
    **规避建议**:需要稳定的分支依据时用 `decision` / `status`(结构化键,原样保留),
    别用 `output` 里的自由文本。
28. **前端 11 处静默吞错** —— **已关闭**(第十轮 #239):逐处分类 —— 真吞错 11 处
    接上可见提示,刻意防御 1 处(`Notification.requestPermission()`)保留并写明理由;
    区分"接口返回非数组"(静默降级)与"请求失败"(必须提示);统一错误出口
    (`lib/errors.ts`,5 秒去重防提示风暴)+ 静态守卫(含防空转断言)。
29. **`CODE_HINTS` 码表只覆盖子集** —— **已关闭**(第八轮 #218/#221):补到 64 码,
    并加**双向守卫**(后端有前端没有 → 红;前端有后端没有 → 红)。
30. **门禁硬伤四件套** —— **已关闭**(第八轮 #215-#217):eslint 声明 + 前端三子检查
    各自独立 + `//go:embed` 兜底(干净 clone 不再在第 0 步崩溃)。
31. **门禁看不见平台专属 `_test.go` 的类型错误** —— **已关闭**(第十轮 #237):
    第 4 步补 `GOOS=windows go vet`(`go build` 手册明文忽略 `_test.go`)。
32. **门禁的防线没有自检** —— **已关闭**(第十轮 #238):隔离临时模块跑 3 个用例
    (对照组必须过 / 坏测试文件必须红 / 坏源文件必须红),防止将来有人删掉 vet
    调用而自检照样绿。
33. **顶层键严格化只在服务器模式生效** —— **已关闭**(第七轮 #197):契约下沉到
    应用层(`app/application/workflow_payload.go`),允许键集合由 `model.Workflow`
    的 json tag 反射派生;api 层与桌面模式走**同一段代码**(`SavePayload` /
    `ParseWorkflowPayload`)。同批关闭的还有 #198(校验告警)、#199(导入的
    `ignored_keys`)。
34. **`execStateGuardSQL` 生产路径不可达(#187)** —— **已澄清**(第六轮 #196):
    注释改写为**事实陈述**("作用范围"三条 + 运行期的防线是 `CasExecutionState`,
    不要把本守卫当成"过期写进不来"的保证)。守卫本身**保留**(它是导出 API 的
    契约,不是死代码)。第九轮另加**状态机一致性交叉测试**(#213):两条判定必须
    逐条一致,改动任一侧即变红。
35. **`state_mask` 分类守卫覆盖面不足(#185)** —— **已关闭**(第六轮 #189 +
    #212):守卫扩到 compiler+runtime 全部非测试文件,识别依据从"变量名"改为
    **类型名与字段名**;覆盖 `.Set()`、直接下标赋值与 `delete` 三种形态;防空转
    下限断言改为**双下限**(键数 **且** 形态数)。配套新增统一状态写入口
    `setExecState`(#190)。
36. **gofmt 的平台文件盲区(#188)** —— **已关闭**(第六轮 #192 + 第七轮 #203 +
    第十轮 #237):清单改用 `git ls-files`;补"gofmt 解析不了 ≠ 已格式化"(退出码与
    stderr 必须被检查);补 `GOOS=windows go build` + `go vet` 交叉编译。
37. **日志级别"即时生效"缺真正的断言(#186)** —— **已关闭**(第六轮 #191):
    补 `logx` 实际生效级别断言,改坏即变红。
38. **`MaskStateData` 顶层字符串值不做键名判定(#204)** —— **已关闭**(第七轮):
    脱敏改为**键名 + 值形态**两级判定,键名规则复用 `logx.keySensitivity`。
39. **`nodeconfig_contract_test.go` 的恒真路径(#206)** —— **已关闭**(第七轮):
    归属检查改成真断言,并加防空转断言。
40. **`template.Engine.RenderErr` 的错误出口零覆盖(#207)** —— **已关闭**(第七轮):
    给引擎加测试接缝,让 panic→error 出口变得可测。
41. **契约探针只覆盖"配置键"维度** —— **已关闭**(第七轮 #200):
    `workflow/compiler/conditionenv_contract_test.go` 补上同等级的 AST 探针。
42. **前端 `asArray` 守卫漏"先赋值再 `.map`"** —— **已关闭**(第七轮 #201)。
    残留的"文件级判定"问题见第 8 条(#210)。

---

### 本轮复核的依据

| 结论 | 怎么核的 |
| --- | --- |
| 门禁 **9 步全部通过(73s)** | `./scripts/verify.sh` 实跑:步骤 1-4(gofmt 154 文件 / vet / build / Windows 交叉编译 + **两组门禁自检**)通过;步骤 5(`go test -race`)**21 个包全绿**;步骤 6(每包都有真实用例)通过;步骤 7(前端 lint/build/vitest)通过;步骤 8-9(冒烟 9 项 + SSE 4 项)全通过 |
| 前端 **190/190** 用例、**20** 个测试文件 | `npx vitest run` 实跑(commit `795f26f` 状态):`Test Files 20 passed (20)`,`Tests 190 passed (190)`。(工作区含第十一轮在途改动时为 198/198) |
| Go 测试文件 **104** 个、覆盖 **21** 个包 | `git ls-tree -r HEAD --name-only \| grep '_test.go$'`(排除 `build/` 与 `node_modules/`)统计;包集合 = 21 个,与门禁第 6 步的 `CHECK_COUNT` 一致。(工作区另有第十一轮在途改动 +7 个 Go 测试文件) |
| `asArray` 文件级盲区(#210)**仍开放** | 隔离副本(`/tmp`)改坏 `Dashboard.tsx` 两个调用点 → `list-guard.test.ts` **4 条全绿** |
| 并发探针重叠窗口(#208)**仍开放** | 读 `app/application/application_test.go`:两处并发用例仍是 `time.Sleep(150 * time.Millisecond)` 建立窗口 |
| `build/ios` **仍编译失败** | `go build ./build/ios/...` → `function main is undeclared in the main package` |
| `crypto/rand` 失败**仍被吞** | `app/application/application.go` 与 `workflow/runtime/engine.go` 均是 `_, _ = rand.Read(b)` |
| `SSE` marshal 失败**仍被丢弃** | `api/server.go` 的 SSE 循环:`json.Marshal` 出错 → `continue`,无告警 |
