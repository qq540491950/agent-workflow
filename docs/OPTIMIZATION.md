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
> 编号说明:缺陷号沿用 `#95` 起(轮次 31 结束于 #94);轮次号因
> `docs/TESTING.md` 的轮次 32(可视化/端到端验证)已占用,从 **33** 起排。
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
>
> 本轮两条**新出现的缺陷类别**值得单列:
> ① **门禁自身不可信** —— 门禁跑的是"侥幸通过"的路径(见第四节末的 #119-#121);
> ② **校验期与运行期各写一份** —— 约定 13 此前只有文字,没有任何机制保证
>   两边不漂移(见轮次 38,#124)。


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

### 仍然开放(轮次 33-40 之后)

> 上一节(轮次 29-31)的开放项:第 1-3 条已在轮次 31 修掉,第 4 条
> (`pi` 无真机冒烟)仍然开放。以下是本轮之后的完整开放清单。

1. **`build/ios` 的编译问题(#120)** —— **仍然开放**。`go build ./...` 失败是
   **既有登记项**(轮次 1 就用"排除 build/ios 包"绕过,此后一直没修)。
   本轮的处理是**让门禁显式排除 `build/` 目录**,而不是假装它能编译 ——
   根因(Wails 脚手架的 `build/ios` 包用 `//go:build ios`,但 `go list ./...`
   在非 iOS 平台仍会枚举它)在 `build/` 属于 Wails 生成物的前提下不好单独修。
   **同类问题**:`node_modules/flatted/golang/pkg/flatted` 是根目录误装 npm
   依赖时夹带的 Go 包,同样排除;根因(根目录不该装 npm 依赖)未处理。
2. **`pi` 适配器无真机冒烟** —— **仍然开放**(自轮次 29 起)。同形替身已覆盖
   子进程层,但**真实 pi-agent CLI 的输出形态**仍未验证。
3. **桌面模式的"点击交互"未做像素级验证** —— **仍然开放**。渲染已验证
   (轮次 32),点击需要辅助功能 TCC 授权,当前环境未授。
4. **契约探针只覆盖"配置键"维度** —— **新登记**。`nodeconfig_contract_test.go`
   用 AST 扫描运行期源码,比对"节点类型 → 配置键"契约;
   `compiler/conditionenv.go`(条件表达式可用变量)只有普通单测,
   **没有同等级的探针** —— 校验期与运行期若再次分叉条件变量集合,
   没有机制会报警。
5. **前端"列表必须过 `asArray`"守卫是源码扫描,不是类型系统** —— **新登记**。
   `frontend/src/test/list-guard.test.ts` 能抓"调用点后面直接 `.map`",
   但**先赋值给变量再 `.map`** 的写法扫不到(第二个断言只检查文件里是否
   出现过 `asArray` 字样)。
6. **列表接口的 `limit` 上限 1000 是硬编码常量** —— **新登记**。
   超过 1000 条被**静默截断**,响应里没有任何"已截断"标记;真正需要
   大数据量时得引入分页契约。
7. **SSE 断连后的订阅释放只有单测覆盖** —— **新登记**。
   `api/sse_test.go` 断言了写失败即关流,但**冒烟脚本没有"断开后订阅数归零"**
   的断言(冒烟只测事件送达与 keepalive)—— 端到端层面仍可能回归。
8. **会话持久化 / ADK v2 迁移** —— **仍然暂缓**,理由与评估见
   `docs/ROADMAP.md`(本轮未触及,不调整)。
9. **`workflow/dsl` 的 7 个零引用配置结构体** —— 本轮由 `compiler/nodeconfig.go`
   标注为"过期、等待删除"(它们会**把凭据引进 DSL 的诱惑**);删除动作在
   后续轮次处理。

