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

