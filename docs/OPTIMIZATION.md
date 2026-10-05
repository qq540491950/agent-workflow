# 优化修改方案 — 轮次 27 全面审计

> 方法:三轮并行只读审计(workflow 运行时+编译器 / 持久化+API+应用层 /
> 前端+双模式契约),共提出 41 条,逐条人工复核源码后定级。本轮**已修复
> 22 条**(6 P0 + 6 后端 P1 + 10 前端),每个修复与回归测试同 commit。
> 剩余 8 条列入下方"分批落地路线"。
>
> 与前几轮的关键差别:轮次 24-26 的缺陷多数是**崩溃/挂起**;本轮 6 个 P0
> 里有 4 个属于**静默成功**——工作流语义上没走完,系统却报告成功。这类缺陷
> 不会触发任何异常路径,是此前"门禁全绿"却仍有 P0 的根本原因。

## 一、已修复清单

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
| P1-2 | `SaveWorkflow` 吞掉存在性查询错误 | DB 故障时对已存在工作流走 INSERT 分支,真实根因被主键冲突掩盖;改为上抛 |
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

## 二、剩余缺陷与落地路线(按批次)

### 批次 A:运行时语义补齐(建议下一轮)

| 项 | 缺陷 | 建议 | 风险 |
| --- | --- | --- | --- |
| A1 | `WAIT_USER` 恢复时,Agent/Skill 节点被原样重跑,用户答复丢失 | 在 `runAgentNode`/`runSkillNode` 消费 `_user_input` 并置 `answered` 标记;已答复则不再调 Agent(避免 execute/fix 模式重复写文件) | 中。需先定义"答复后是否重跑"的产品语义 |
| A2 | 模板缺失变量静默渲染空串,`missing` 列表被丢弃 | 汇总进节点 outcome 与 `runtime.error` 事件;关键变量缺失是否让节点 FAILED 需产品决策 | 低(先只告警) |
| A3 | 节点级状态机 `CanTransitionNode` 从未接线 | 在 `nodeAgent` 写 `StatusKey` 前校验,非法转换记 `runtime.error`;或直接删除该死代码 | 低 |
| A4 | 并行分支失败不级联兄弟分支 | 失败分支 `yield(nil, err)` 让 ADK errgroup 取消兄弟;或入口加 abort 守卫 | 中。真实 Agent 场景下省 token,但会改变并行语义 |
| A5 | merge 汇总失真:按入边统计,缺失状态一律算成功 | 读取并行区分支清单,`StatusKey` 缺失即计入 skipped 并在摘要标注 | 低 |
| A6 | `Start` 重置 Mock 会打断同工作流的并发执行 | mock 计数按 executionID 隔离,或只在显式开启时重置 | 低 |
| A7 | `pi` 适配器用 `LastIndex("{")` 定位尾部 JSON | 含嵌套对象时 Unmarshal 失败 → 决策静默丢失、路由恒走默认分支。改为从第一个能配平到末尾的 `{` 起解析 | 低 |
| A8 | Agent stdout 无上限捕获,截断只发生在落库层 | 用 `io.LimitReader` 限制内存缓冲并在超限时显式报错 | 低 |

### 批次 B:并行编译正确性(需先补测试)

| 项 | 缺陷 | 建议 |
| --- | --- | --- |
| B1 | `irBuilder.stopped` 是共享字段,嵌套并行时内层 `chainStop` 会清掉外层 stop 集合,分支越过汇合点执行后置节点 | 把 stop 集合改为沿调用链显式传参,彻底去掉共享字段。**注意**:目前无"并行套并行"的测试用例,需先补一个能暴露该行为的用例再重构 |
| B2 | 并行分支不收敛时后续节点被静默丢弃(`join == ""` 时直接丢链) | 兜底改为在 `Plan` 里保留分支信息并在执行后发告警事件;或校验期拒绝不收敛拓扑 |

### 批次 C:测试基建(投入产出比最高的一项)

当前最大的系统性风险是**测试只覆盖正常路径**:

| 项 | 缺口 | 建议 |
| --- | --- | --- |
| C1 | 组件层无交互行为测试(焦点保持、危险操作二次确认、双击防护) | 补 jsdom 组件测试,优先覆盖本轮新增的 confirm/busy/乐观回滚逻辑 |
| C2 | REST 契约测试仅覆盖少量端点 | 按"每个端点至少一条契约测试"补齐,含状态码语义与错误体形状 |
| C3 | `api.test.ts` 把 `@bindings/*` 整个 mock 成空对象 | 依赖它的用例会静默失败而非报错;改为部分 mock,缺导出即抛错 |
| C4 | 无故障注入测试 | `SaveWorkflow` 的查询失败、`SetEnabled` 的落库失败等只能靠人工构造;建议引入可注入的 DB 接口 |
| C5 | 备份三个 API 方法(`backupURL`/`exportBackupText`/`restoreBackup`)零 UI 消费 | 要么补 Settings 页备份/恢复入口,要么删除死代码——"看起来有能力实则不可达"最容易误导 |

## 三、架构层面的中期建议

1. **状态收敛到单一写入点**。当前 `exec.StateData` / `Exec.NodeStates` /
   ADK session state 三处各写一份,已导致"JSON 往返 int 变 float64"这一类
   反复出现的缺陷(`IntOfOk` 存在本身就是症状)。建议:节点状态只以 ADK
   session state 为准,`NodeStates` 改为派生视图。

2. **"静默成功"要当成 P0 级别的设计约束**。本轮 4 个 P0 都是"该失败却
   成功"。建议在 `nodeAgent`/`routeAgent` 收尾处加一条不变式:执行结束前若
   仍有未执行的可达节点,必须显式失败或显式标记为设计意图,不允许默认收尾。

3. **校验期与运行期必须同源**。P0-4 与 P0-3 都是"两份实现各自演化"导致的
   分裂。建议把条件表达式环境、节点类型配置结构各抽成单一来源
   (`compiler` 与 `validator` 共用),从结构上消除漂移。

4. **错误信息即 UI 契约**。后端已返回 `code`/`kind`,前端此前丢弃。
   建议定义错误码枚举 + 前端按码分类渲染(权限类引导去设置页、校验类定位
   到画布节点),避免所有错误都退化成一句裸消息。

## 四、验证方式

每项修复与回归测试同 commit,`./scripts/verify.sh` 7 步全绿作为门禁
(gofmt / vet / `go test -race ./...` / 前端 lint+build+vitest / 双冒烟)。

其中并发缺陷的回归测试已实测有效性:回退 `DisabledSkills` 的锁后,
`-race` 报 3 处 DATA RACE 并 FAIL;SSE 背压用例也从 0.6s 涨到 2.3s 暴露了
事件脱敏正则的性能问题(已用廉价子串预筛解决)。
