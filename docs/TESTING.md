# 测试报告 — Agent Workflow Orchestrator

> 共 8 轮测试验证(超过最低 5 轮要求),其中轮次 2-8 为浏览器视觉测试(GUI 黑盒)。
> 每次功能完成与 Bug 修复均有独立 git 提交(见提交号)。

## 环境

| 项 | 版本 |
| --- | --- |
| Go | 1.26.0 (darwin/amd64) |
| Wails | v3.0.0-beta.20 |
| ADK Go | google.golang.org/adk v1.7.0(实际最新版;需求假设的 2.x 不存在,按需求第 43 条采用真实 API) |
| SQLite | modernc.org/sqlite(纯 Go) |
| Node | v24.13.1 / Vite 8 / React 18 / TS 5 |
| 前端 | Tailwind CSS 4 + shadcn/ui + @xyflow/react (React Flow) |

---

## 轮次 1:后端全量测试(go test / vet / 构建)

**命令**:`go vet ./...`(排除模板 build/ios 包)、`go test ./workflow/... ./agent/... ./skill/... ./persistence/... ./event/... ./template/... ./permission/... ./git/... ./contextx/...`、`go build .`、`go build -tags server`、`npm run build`

**覆盖**(对应需求第三十条):

- DSL Parser:YAML/JSON 解析、未知顶层键报错、模型往返、YAML 再序列化 ✓
- Validator:孤立节点/无效边/不存在的 Agent/Skill/多起点/无起始/不可达/环无终止条件/无结束路径/非法表达式/非法 git 操作,结构化错误 `{code,node,message}` ✓
- Compiler:线性链→seq、Review→Fix 回边→route+loop(max_iterations)、parallel→merge→par ✓
- Runtime 集成:Plan→Execute→Review(REJECTED)→Fix→Review(APPROVED)→Submit 全闭环;循环保护(永远 REJECTED → max_iterations → WAITING_USER);HITL Resume(approve → COMPLETED);状态机非法转换拒绝;节点明细/事件持久化 ✓
- Agent Registry:重名拒绝/Get/List 排序;MockAgent 决策序列、失败注入 ✓
- Permission:默认策略(Claude Code 只读、Pi 可写可 commit 禁 push)、未知 Agent 全拒绝、DSL 覆盖 ✓
- Git Service:真实临时仓库 status/diff/log/commit、不支持操作拒绝 ✓
- Template:{{task}}/{{variable.xxx}}/点路径/缺失变量报告/未闭合占位符 ✓
- Persistence:workflow CRUD+版本快照、执行实时保存、崩溃恢复查询、节点明细、事件、制品、设置 ✓

**结果**:全部通过(0 fail)。vet 干净。两种 Go 构建模式 + 前端构建全部成功。

---

## 轮次 2:视觉测试 — Dashboard(浏览器)

**方式**:browser-use 打开 `http://127.0.0.1:18080/`,1440×900 截图。

**发现 Bug #1**:页面全黑,`root` 未渲染。注入 onerror 捕获后定位:后端列表接口空结果序列化为 `null`,前端对 `null.filter()` 崩溃。

**修复**(commit `fix: 空列表接口返回 [] 而非 null…`):仓库层 List 函数返回空切片 + 前端列表兜底。

**修复后**:Dashboard 完整渲染 — 统计卡(Workflows/Running/Waiting/Completed/Failed)、最近执行、示例工作流列表、侧栏导航。✅ 见 `docs/screenshots/01-dashboard.png`

---

## 轮次 3:视觉测试 — Workflow 列表 / SPA 路由

**发现 Bug #2**:直接访问 `/workflows` 返回 404 — HTTP 模式静态服务缺少 SPA fallback。

**修复**(commit `fix: HTTP server 模式增加 SPA fallback…`):无扩展名且非 /api/、/wails/ 的路径回退 index.html。

**修复后**:列表页完整 — 名称/ID/版本/节点数/启用开关/运行/编辑/复制/导出 YAML/历史/删除。✅ 见 `docs/screenshots/02-workflows.png`

---

## 轮次 4:视觉测试 — Workflow Designer(React Flow)

**发现 Bug #3**:Designer 空白,错误 `ReactFlowProvider as an ancestor` — `useReactFlow` 缺 Provider。

**修复**(commit `fix: Designer 缺少 ReactFlowProvider…`)。

**修复后**:画布渲染 Coding Task 全部 5 节点与条件边(APPROVED/REJECTED 标签虚线)、左侧 9 种节点拖拽面板、右侧属性面板(Max Iterations / wait_user)、顶栏校验/保存/运行。✅ 见 `docs/screenshots/03-designer.png`

---

## 轮次 5:视觉测试 — 从 UI 运行工作流 + 执行监控

**步骤**:Designer → 运行 → 输入任务"实现用户登录模块(视觉测试)" → 启动执行 → 跳转执行监控页。

**断言**:
- 执行由 UI 触发并 COMPLETED(API 查询 state=COMPLETED)✅
- 监控页节点状态:plan✓ execute✓ fix✓ review✓×2(循环!)submit✓ ✅
- 实时日志 27 条完整事件流,review.rejected 黄色 / review.approved 绿色着色 ✅
- `docs/screenshots/04-executions.png`、`05-execution-monitor.png`

---

## 轮次 6:视觉测试 — HITL 人工交互(暂停/恢复)+ 并行

**步骤**:运行 Parallel Review(首节点为 human)→ 立即 WAITING_USER → UI 出现"Workflow requires your input"面板(Approve/Reject/Continue/Provide instruction)→ 点击 Approve → 恢复 → 并行分支(security/code/test)同时执行 → merge → 裁决 → COMPLETED。

**断言**:
- 暂停面板、状态徽章 Waiting User、侧栏运行计数 1 ✅(`06-hitl.png`)
- human.input_received → workflow.resumed → 并行 skill.executed(test)→ 全部 SUCCESS ✅(`07-parallel-completed.png`)

---

## 轮次 7:视觉测试 — Agents 权限 / Skills 管理

- Agents 页:4 个 Agent 的 5 项权限开关矩阵;验证默认策略 Claude Code 只读(禁写/禁 commit/禁 push)、Pi 可写可 commit 禁 push。✅(`08-agents.png`)
- Skills 页:submit / run-test / log 内置 Skill,Provider/描述/启用/Test 按钮。✅(`09-skills.png`)

---

## 轮次 8:回归测试 — 桌面模式传输修复后

**背景**:桌面模式验证时发现 Bug #4 — 前端在 Wails runtime 注入前检测 `window._wails`,误判为浏览器模式走了 HTTP(而资产服务把 /api/* 当静态文件返回 HTML)。另发现 Bug #5 — SPA fallback 拦截了 `/wails/runtime` 导致 IPC 初始化失败。

**修复**:改为探测式检测(真实调用一次绑定,500ms 内成功即桌面模式)+ fallback 排除 `/wails/` 前缀。

**断言**:
- 桌面二进制构建成功、启动后窗口创建(1439×899)、**0 次 /api HTTP 请求**(完全走 Wails IPC)✅
- Web 模式回归:Dashboard 数据经 HTTP 正常加载、mode: web、历史执行显示 2 Completed ✅(`10-dashboard-with-runs.png`)

> 桌面窗口的像素级截图受当前系统锁屏限制无法获取;窗口存在性由窗口系统 API(`list_windows`)与进程状态证实。

---

---

## 第二阶段迭代(模型配置 + 功能完善,00:40 - 01:35)

**目标**:每个 Agent 可配置模型,且不影响用户本地配置;持续探索完善功能。

### 轮次 9:每 Agent 模型/端点/环境变量配置

- 后端:`agent.AgentConfig`(Model/BaseURL/Timeout/ExtraArgs/Env/WorkingDir)+ `agent_configs` 表持久化 +
  `Registry.Replace` 热重建适配器 + `GET/PUT /api/agents/{id}/config`。
- **隔离原则(需求核心)**:配置仅通过每次调用的 CLI 参数(`--model`)与子进程环境变量
  (`ANTHROPIC_BASE_URL`/`PI_BASE_URL`/自定义)传递;不读写 `~/.claude`、`~/.pi` 等本地配置文件;
  敏感值 API 返回以 `***` 掩码,保存时 `***`=保留原值、空串=删除;不进入日志与工作流定义。
- 单测:假 CLI 捕获参数验证 `--model cfg-model`、环境变量注入、节点级覆盖(`--model node-model`)、
  掩码/合并优先级(请求级 > 配置级)。✅ `agent/model_config_test.go`
- E2E:保存 → GET 掩码 → **重启服务器后仍持久化** → `***` 保留语义 → 空串删除语义。✅
- 视觉:Agents 配置对话框(模型/端点/超时/附加参数/环境变量/隔离提示)。✅ 截图 `11-agent-model-config.png`

### 轮次 10:节点级 model 覆盖 + 重试策略

- DSL `config.model` → `AgentRequest.Model`(优先于 Agent 默认模型);`config.retry.{max_attempts,backoff}`
  (fixed/exponential,上限 10 次)在执行器内实现重试循环,重试发出 `agent.retry` 事件。
- 视觉:Designer 属性面板出现 Model/重试次数/退避策略字段。✅ 截图 `13-designer-node-model.png`

### 轮次 11:YAML 导入

- `POST /api/workflows/import`(ID 冲突自动换新 ID,导入前强制校验)+ 前端导入对话框。
- E2E:导入示例 → 二次导入换 ID → 非法 YAML 被结构化拒绝。✅

### 轮次 12:Git 面板页

- 状态(已暂存/已修改/未跟踪)、Diff 视图、提交历史、一键提交(commit API + 权限约束);
  Git 默认工作目录改为进程当前目录,`--git-dir` 启动参数可覆盖。✅ 截图 `12-git-panel.png`(正确显示当时未提交的改动)

### 轮次 13:设置页 + 审计事件页

- 设置:数据目录(只读)、Git 工作目录、日志级别(即时生效,持久化)。✅ 截图 `14-settings.png`
- Events:跨执行审计流,SSE 实时刷新,按类型过滤。✅ 截图 `15-events-audit.png`

### 轮次 14:Designer 自动布局(@dagrejs/dagre)

- 一键按连线方向 TB 重排节点并适配视图。✅ 截图 `16-auto-layout.png`(循环回边清晰)

### 轮次 15:失败执行的重试/跳过恢复

- 状态机新增 FAILED→RUNNING(仅用户显式重试合法);`Engine.RetryNode(skip)` + 监控页按钮。
- 集成测试:失败 → 重试(仍失败)→ 跳过(SKIPPED)→ 完成后续节点;对 COMPLETED 重试被拒绝。✅ `workflow/runtime/retry_test.go`

### 轮次 16:细节完善与 Bug 修复

- **Bug #6**:清空 Mock 行为配置后丢失内置默认演示脚本(Decision=DONE 不匹配任何路由,
  误触发循环保护)。修复:内置默认脚本 + 用户配置按 mode 合并。E2E 复验两种语义均 COMPLETED。✅
- Dashboard 每工作流执行统计(成功率条);执行监控 State 会话状态查看器(隐藏内部键);
  HITL 用户指令注入 execute/fix 上下文;工作流列表过滤;桌面通知(完成/失败/等待输入);
  监控页耗时显示;执行记录一键导出 JSON。✅ 截图 `17-dashboard-stats.png`

### 真实崩溃恢复测试

- 运行中 `pkill` 服务器 → 重启 → WAITING_USER 执行保持可恢复 → 提交 approve → 跨进程恢复 → COMPLETED。✅

### 轮次 17:执行监控增强

- **时间线视图**:甘特式节点条(按执行起点对齐,条长=耗时,颜色=状态),
  并行重叠与人工等待区间一目了然。✅ 截图 `19-monitor-timeline.png`
- 标题栏显示总耗时;循环执行显示"循环 N 轮"徽章;一键导出执行记录 JSON
  (执行 + 节点 + 事件 + 制品打包归档)。✅ E2E 验证导出内容完整

### 轮次 18:失败恢复与记录管理

- 状态机新增 FAILED→RUNNING(仅用户显式重试合法);`Engine.RetryNode(skip)`
  支持"重试失败节点"与"跳过并继续";监控页按钮直达。
- 集成测试:失败 → 重试(仍失败)→ 跳过(SKIPPED)→ 后续节点继续;对
  COMPLETED 执行重试被拒绝。✅ `workflow/runtime/retry_test.go`
- 执行记录删除(级联清理节点/事件/制品)+ 列表页删除按钮。✅ E2E

### 轮次 19:编排能力补强

- **节点级禁用**:`config.enabled=false` → 运行时 SKIPPED(路由直通),
  画布半透明 ⊘ 标识,属性面板开关。✅ E2E(execute SKIPPED,链路完成)
- **Merge 聚合**:merge 节点收集分支状态,输出"汇合 N 个分支,全部成功/
  失败分支"摘要与事件。✅
- **工作流级权限覆盖**:DSL `permissions` 段运行时生效(克隆全局管理器 +
  应用覆盖,不影响其他运行);持久化与列迁移。✅ E2E(mock-pi 禁写 → execute
  PERMISSION_DENIED)
- **工作流级超时**:`settings.timeout_seconds` 防止单轮挂死。✅ E2E(sleep 10
  + 2s 超时 → FAILED "工作流执行超时(2s)")
- **subworkflow 存在性警告** + **Designer Cmd/Ctrl+S 保存**。

### 轮次 20:可预测性与一致性

- **Bug #7**:Mock 决策序列游标跨执行共享,第二次运行行为漂移。修复:
  每次 Start 前自动重置 Mock(ResetMock),决策序列从头开始。✅ E2E(连续两次
  运行 review 均 attempt=2)
- **Bug #8**:清空 Mock 行为配置后丢失内置默认脚本。修复:内置默认脚本 +
  用户配置按 mode 合并(behavior=null 恢复默认,{} 清空某 mode)。✅ E2E
- **Skill 启用/禁用真实生效**:运行时拒绝调用禁用 Skill(此前仅 UI 展示)。
  ✅ E2E(禁用 submit → FAILED 明确报错 → 重新启用 → COMPLETED)
- **HITL 指令注入**:`instruction` 文本写入 `user_instruction`,
  后续 execute/fix 节点可通过 `{{user_instruction}}` 引用。✅ E2E(approval-demo
  全链路:WAITING_USER → 指令恢复 → 上下文含"重点关注密码存储安全" → COMPLETED)

### 轮次 21:运维与文档

- `/api/health` 健康端点;`--git-dir` 启动参数;优雅关闭(SIGINT/SIGTERM →
  Shutdown + WaitIdle 等待执行收尾);Events 分类快捷过滤(前缀匹配);
  Agents 列表显示已配置模型;Test 真实探测 CLI 安装状态;示例工作流按 ID 补种;
  新增 approval-demo 审批演示示例;favicon。
- **数据备份/恢复**:设置页导出全部工作流 + Agent 配置(含明文敏感环境变量,
  备份需妥善保管);恢复时同 ID 工作流跳过。✅ E2E(导出 11 工作流 + 2 配置 →
  新库恢复 8 个/跳过 3 个种子重复 → 数据完整)

### 并发与稳定性

- `go test -race` 全量通过(runtime/persistence/agent/event 等)。
- 连续 10 次执行全部 COMPLETED,server RSS 21MB 无泄漏迹象。
- **真实重启恢复**:运行中 pkill 服务器 → 重启 → WAITING_USER 执行保持可恢复
  → 提交 approve → 跨进程恢复 → COMPLETED。
- 桌面 .app 打包(productName/Identifier 修正)并启动验证:窗口创建、
  UI 渲染、**mode: desktop**(IPC 传输,0 次 HTTP /api 请求)。

### 轮次 22:真实 Claude 适配器端到端(假 CLI)

- 用假 `claude` 脚本置于 PATH,创建引用 `claude-code` 适配器 + 节点级
  `config.model` 的工作流并运行:执行 COMPLETED、输出被结构化解析、
  假 CLI 收到 `--model claude-sonnet-4-5`(节点级覆盖生效)。✅
- 结论:"每 Agent 模型配置 → 持久化 → 适配器重建 → CLI 参数传递"全链路打通,
  且全程未触碰用户本地配置文件。

### 轮次 23:最终回归(收尾)

- `go test -count=1 -race` 全部 11 个测试包通过;vet 干净;gofmt 0 未格式化文件。
- 前端 `tsc && vite build` 通过;桌面/服务器双模式 Go 构建通过。
- 冒烟脚本 7 项断言全部通过;10 个页面视觉扫描 0 错误。
- 边界语义复查:运行不存在工作流 / 恢复 COMPLETED / 删除不存在记录 / 空 commit /
  空工作流校验 / 非法重试目标 —— 全部返回明确结构化错误(NOT_FOUND / INVALID_RESUME / 参数校验)。✅

---

## 轮次 24:权威资料对照审计与可靠性强化(ADK v1.7.0 / Wails v3 beta.20)

> 方法:并行开展两路**官方资料对照审计**(ADK Go v1.7.0 源码 + adk.dev 官方文档;
> Wails v3 beta.20 源码 + v3.wails.io 官方文档),与实现逐条对照,产出改进
> backlog 后按价值排序逐项"实现 → 测试 → 提交"。全程 10 个提交,每项独立回归。

### 测试盲区补齐(需求 §30)

- 新增 skill 注册表、skill/builtin、event 总线(并发 -race)、mock agent
  (决策序列/Reset/模板/取消)、claude/pi 适配器(假 CLI 真进程:超时/取消/
  环境合并/模型传递/结构化解析/权限前置)测试。✅
- 前端 vitest 基础设施 + api.ts 12 项测试(双模式探测竞速与缓存、asArray
  防御、HTTP 错误提取、SPA fallback 回退、SSE 分发/退订)。✅
- application 服务层 8 项测试(掩码合并语义、备份恢复往返、复制/导入/启停)。✅

### 修复的 Bug(#9-#16)

- **#9 孤儿进程阻塞**:exec.CommandContext 默认只杀直接子进程,CLI Agent
  派生的进程持有 stdout 管道,超时后调用方多等 4s+(实测)。Unix 下
  Setpgid 进程组击杀 + cmd.WaitDelay 兜底(Windows);三平台交叉编译。✅
- **#10 恢复后循环计数丢失**(ADK 审计):计数经 JSON 持久化往返变 float64,
  loopResetter/syncNodeStates 只断言 int → Resume 后计数回落,过早触发循环
  保护。新增 compiler.IntOfOk 双类型兼容 + 回归测试。✅
- **#11 超时路径读会话必失败**(ADK 审计):超时/取消后用已取消的 ctx 调
  sessSvc.Get,必然落入兜底分支;改用 context.WithoutCancel。✅
- **#12 并行分支数据竞争**(ADK 审计):parallel 分支并发写共享
  Exec.CurrentNodeID / Exec.NodeStates(无锁 map)。RunEnv 加锁 +
  TestParallelBranchesRace(-race 验证旧代码确实触发 DATA RACE)。✅
- **#13 桌面模式下载断链**(Wails 审计):桌面 AssetServer 无 /api 路由,
  导出/备份链接落入 SPA fallback 下载到 HTML。api.AssetHandler 挂载
  Route:/api + 补前缀适配(单元测试覆盖三种路径形态)。✅
- **#14 双开写库**:数据目录仅一个 SQLite,双开并发写;启用 Wails
  SingleInstance,二次启动聚焦已有窗口。✅
- **#15 示例工作流打包后不种子**:seedExamples 用相对路径读盘,打包 .app
  从 Finder 启动(CWD=/)静默跳过;改 go:embed 嵌入。✅
- **#16 进程内备份恢复静默失效**:RestoreBackup 只认 JSON 归一形态,
  ExportBackup 直出形态会被静默恢复 0 条;统一 JSON 归一。✅

### 功能增强

- **Go 侧原生通知**:完成/失败/等待输入系统级提醒(WKWebView 的 Web
  Notification 权限模型不可靠),点击聚焦窗口并导航到执行;桌面模式前端
  跳过 Web 通知避免双重提醒。✅(编译 + 单测;通知真机弹窗待打包签名验证)
- 桌面模式 OnShutdown 给运行中执行最多 5s 收尾(与服务器模式对齐)。✅
- /api/version、/api/health 版本信息从构建信息动态读取(不再硬编码)。✅

### 杂项

- 事件流不再整体丢弃:Escalate(循环退出/HITL 暂停)记 Warn 日志。✅
- 吞错清理:nodeAgent/loopGuard/loopResetter/noopAgent 的 agent.New 错误
  经 Compile 上抛(替换 panic)。✅
- 循环保护默认上限统一 compiler.DefaultMaxIterations;detectMode 魔数提取
  常量;npm @wailsio/runtime 精确对齐 Go 模块 beta.20(消除协议漂移)。✅
- @wailsio/runtime 升级坑记录:Windows 下 `wails3 doctor` 提示的
  configLoader 告警抑制(3774232)。

### 回归汇总(轮次 24 收尾)

- `go test -count=1 -race ./...` 全部通过;vet 干净;gofmt 干净。✅
- 前端 `tsc && vite build` + vitest 12/12。✅
- 冒烟脚本 7 项通过(覆盖服务器模式 REST + SSE + HITL + 导出)。✅
- Go/Windows/Linux 交叉编译通过(agent 包及主程序)。✅

---

## 轮次 25:持续审计迭代(并发/资源/实时流)

> 方法:延续轮次 24 的审计驱动模式,精读 executor/persistence/api 层源码 +
> 回归测试先行,共 12 个提交。
> 收尾状态(06:42):本夜合计 74 个提交;最终门禁 verify.sh
> (gofmt/vet/race/前端 lint+构建+23 用例/双冒烟 13 项)全部通过。

### 修复的 Bug(#17-#22)

- **#17 服务器模式 SSE 从未接通**:ConnectEvents(Bus→UI 桥接)只在桌面模式
  调用,/api/events 客户端收不到任何实时事件(历史走 SQLite 查询,浏览器
  E2E 未暴露)。ConnectEvents 幂等化 + NewServer 顺接通;
  `scripts/smoke_sse.sh` 端到端验证(: connected 立即达 / 执行事件经流送达)。✅
- **#18 git 钩子挂起阻塞**:commit 触发的钩子挂起时持有输出管道,
  CombinedOutput 阻塞到钩子自行退出(实测取消后仍阻塞 30s);
  进程组击杀 + WaitDelay,回归测试验证新旧行为(30s→0.39s)。✅
- **#19 ErrWaitDelay 误判失败**:进程成功退出、孤儿进程延迟关管道时,
  Output/CombinedOutput 返回 exec.ErrWaitDelay,script/git/claude/pi 四处
  曾误判为节点失败;输出已完整捕获应视为成功。✅
- **#20 Skill WAIT_USER 被吞**:Skill 接口承诺 WAIT_USER,runSkillNode 只映射
  FAILED,WAIT_USER 静默当 SUCCESS;现映射 NodeWaiting 暂停(测试:暂停→
  恢复→完成)。✅
- **#21 生产 Bus 无界内存增长**:KeepHistory=true 但 History() 生产零消费者,
  所有 UIEvent 永久累积;关闭(事件已实时落库,回放走 API)。✅
- **#22 logx SetLevel 数据竞争**:运行中替换全局 logger 与并发写日志竞争;
  atomic.Pointer + 并发回归测试。✅
- **#23 并发 Resume/Retry 双重放**:读-检查-保存非原子,双击批准会并发
  产生两份 goroutine 重放同一执行;persistence.CasExecutionState 数据库级
  CAS,并发时恰好一路成功。回归:TestConcurrentResumeSingleWinner。✅
- **#24 Start 全量重置 Mock**:嵌套工作流启动会把并发运行中的父工作流
  Mock 决策序列中途清零;按 wf.Nodes 引用过滤,只重置被引用的 Agent。✅

### 加固与改进

- handleSSE:连接立即下发响应头(此前 EventSource 首事件前一直 CONNECTING)、
  15s keepalive、写锁+closed 防退出竞争(-race 抓到)。✅
- SaveExecution/SaveExecutionNode 改原子 upsert(消除两步 check-then-write)。✅
- API 状态码语义化:NOT_FOUND→404、权限→403(此前一律 400);body 附
  code/kind;DELETE 保持幂等 200。6 组 httptest 契约测试。✅
- mock 模板 {iteration} 兼容 float64(JSON 往返);清理 executor 死代码。✅
- 移除未使用依赖 zustand;@wailsio/runtime 精确对齐 Go beta.20。✅
- 桌面 .app 真机验证:全新数据目录启动,3 个示例工作流经嵌入 FS 种子成功
  (#15 的真机复核)。✅
- SaveExecution/SaveExecutionNode 原子 upsert;前端组件测试基础设施
  (jsdom + testing-library)+ StateBadge 8 项。✅
- **#26 Dashboard 状态卡片口径**:从最近 8 条执行计数,长时场景下
  RUNNING 被新记录挤出窗口后错误归零;改 stats 全量聚合。✅
- **#27 取消父执行不级联**:取消阻塞在子工作流上的父执行时,父 goroutine
  在轮询里最长阻塞 10 分钟且子执行继续跑;等待循环感知 ctx 并级联取消
  子执行。✅
- **#28 执行 goroutine 与 API 响应的数据竞争**:/run、/input、/retry 把
  存活 Execution 指针交给 writeJSON 序列化,与并发改写竞争(新写的
  HTTP 恢复存活测试在 -race 下首次抓到);Start/Resume/RetryNode 改为
  返回派发前快照。✅
- version() 双模式统一走 /api(桌面关于卡片显示 ADK/Wails 版本);
  EventsAudit 实时刷新 200ms 防抖;contextx 死代码 IterationOf 清理。✅
- 测试扩充:goroutine 泄漏回归(20 次执行后协程回落)、嵌套工作流
  集成(父→子真实链路 + 缺失子失败 + 取消级联)、崩溃恢复回归
  (共享 SQLite 双引擎:RUNNING→FAILED 可重试 / WAITING_USER 可恢复)、
  HTTP 层 HITL 恢复存活(锁定 ctx 脱离语义)、matchBranch/EvalCondition
  变体、git 路由契约(临时仓库)、绑定 ID 漂移守卫、并行分支内嵌
  条件路由组合。✅
- run:server 任务补 --server 标志(此前无参启动进入桌面分支);
  Designer 设置面板补 timeout_seconds 输入;HITL 按钮 busy 态防重复;
  Layout/EventsAudit 实时刷新防抖;Wails 内部日志接入 logx。✅
- **进程级浸泡**:35 次真实执行(30 串行 + 5 并发)全部 COMPLETED、
  无残留未终态,RSS 19.5MB → 24.5MB(正常区间)。✅
- 覆盖率快照(`-cover`):event/skill 100%、claude 96%/pi 96%/mock 95%、
  template 89%/dsl 87%/agent 83%/logx 81%/contextx 77%/permission 66%/api 66%
  /persistence 58%/git 51%/app 48%/compiler 31%(lower 生成器由 runtime
  集成测试间接覆盖)。✅

### 回归汇总(轮次 25 收尾)

- `go test -count=1 -race ./...` 全部通过;vet/gofmt 干净。✅
- 前端构建 + vitest 12/12。✅
- smoke.sh 7 项 + smoke_sse.sh 3 项全部通过。✅

## 汇总

| 轮次 | 类型 | 结果 | 修复的 Bug |
| --- | --- | --- | --- |
| 1 | 后端全量 go test/vet/build | ✅ 全部通过 | — |
| 2 | 视觉:Dashboard | ✅ | #1 空列表 null 崩溃 |
| 3 | 视觉:列表/SPA 路由 | ✅ | #2 SPA 404 |
| 4 | 视觉:Designer | ✅ | #3 ReactFlowProvider |
| 5 | 视觉:UI 运行+监控 | ✅ | — |
| 6 | 视觉:HITL+并行 | ✅ | — |
| 7 | 视觉:Agents/Skills | ✅ | — |
| 8 | 回归:双模式传输 | ✅ | #4 传输误判 #5 /wails/ 路径拦截 |
| 24 | 官方资料对照审计 + 可靠性强化(见上) | ✅ | #9 孤儿进程 #10 循环计数 #11 取消 ctx #12 并行竞态 #13 桌面下载断链 #14 双开 #15 种子路径 #16 备份恢复 |
| 25 | 持续审计迭代:并发/资源/实时流 | ✅ | #17 SSE 未接通 #18 git 钩子阻塞 #19 ErrWaitDelay #20 Skill WAIT_USER #21 Bus 内存增长 #22 logx 竞争 |

**需求第四十五条(第一阶段完成标准)逐条核对**见 `docs/index.html` 使用说明附录。
