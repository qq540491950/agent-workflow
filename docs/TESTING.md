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

## 轮次 26:静态审计驱动的缺陷收敛(P0 正确性/安全 + P1 数据与体验)

> 方法:三轮并行只读审计(workflow 运行时+编译器 / 持久化+API+应用层 /
> 前端+桌面壳)逐条人工复核后按严重度修复;每项修复与回归测试同 commit,
> 11 个提交。审计结论的前提是"门禁全绿"——缺陷全部落在未被测试覆盖的
> 语义分支上(测试集中在解析→编译→跑通→回放的正常路径)。

### P0 修复(#29-#37)

- **#29 git 节点 add_all 覆盖 commit 参数**:`gitArgs` 被整体覆盖成
  `["add","-A"]`,提交从未发生却返回成功(工作区脏、后续 diff/review 读到
  未提交状态)。同时 `branch`/`checkout` 拒绝以 `-` 开头的引用名(DSL
  参数注入)。`TestRunCommitWithAddAll`、`TestRunRejectsOptionLikeRefName`。
- **#30 人工闸门 reject 等同 continue**:`runHumanNode` 对任何响应返回
  NodeSuccess,拒绝后后续节点照跑、执行标 COMPLETED(内置示例
  approval-demo 即此拓扑)。改走通用 NodeFailed 分支,与 `lower.go` 路由
  路径的 reject→FAILED 对齐。`TestHumanRejectAbortsExecution`。
- **#31 `_user_input` 消费后不清除**:`StateAccess` 无 Delete,该键残留
  会话状态并随 StateData 持久化,同链第二个 human 闸门读到上一次的输入
  直接 SUCCESS。`StateAccess` 增加 Delete(nil 即不存在)。实测修复前第二道
  闸门直接跳到 submit 并 COMPLETED。`TestSecondHumanGatePausesAgain`。
- **#32 script 节点无任何权限门**:以 `/bin/sh -c` 执行 DSL 任意命令,而
  工作流可由 YAML 导入获得,叠加未鉴权 REST 层即任意命令执行。新增显式
  开关 `AllowScriptNodes`(设置项 `allow_script_nodes`,默认 false,
  fail closed)+ 设置页「安全」卡片。`TestScriptNodeDeniedByDefault`、
  `TestScriptNodesGateDefaultsOff`。
- **#33 执行协程无 recover**:全仓零 `recover()`,任一节点适配器/ADK
  panic 会终止整个进程且执行永久停留 RUNNING。`execute` 顶层兜底 +
  快照非空校验(修 `snapshot_json` 损坏时的 nil 解引用根因)。
  `TestNodePanicDoesNotCrashProcess`、`TestMissingSnapshotFailsCleanly`。

### P1 修复(#38-#45)

- **#34 logx 只脱敏 msg**:args 原样落盘,api 层 `error=` 里的 CLI stderr
  会带出 token。现对 string/error/Stringer 参数值脱敏。
- **#35 SSE 背压反压执行引擎**:订阅回调在产生事件的协程里同步写客户端,
  一个不读数据的连接即可卡住工作流。改为每连接 256 条缓冲队列 + 写超时
  10s,投递非阻塞(满则丢弃并告警,回放走 SQLite)。
  `TestSSESlowClientDoesNotBlockProducer`(旧实现实测 10s 超时失败)。
- **#36 属性面板每次按键重挂载**:key 含 `JSON.stringify(cfg)`,cfg 每次
  按键都是新对象 → 面板卸载重建、输入框失焦,配置字段只能敲一个字符。
- **#37 新节点 ID 与画布冲突**:自增计数器从不扫描已 hydrate 的节点,
  打开含 `agent-1` 的工作流再拖 agent 节点必然重复 ID,保存被
  DUPLICATE_NODE 拒绝。改为 `nextNodeId` 扫描取最小可用值。
- **#38 保存无视校验结果**:校验不通过仍提交,用户只看到后端裸 JSON 错误;
  "已保存但存在校验问题"分支不可达。改为校验不通过即阻止保存。
- **#39 吞错导致权限被清空/设置假成功**:`SetPermission` 读失败后重建空
  map → 整表写清空其他 Agent 权限策略;`Settings.Update` 先改内存再忽略
  写错误;`Export` 丢三次查询错误;`parseWorkflow` 丢 Unmarshal 错误落库
  残缺工作流。四处均改为上抛/先落库后改内存。
- **#40 404 语义不齐**:`wrapNotFound` 是无人调用的死代码,
  `GetWorkflowVersion`/`GetSetting` 走 `wrap()` → 400。接入后返回 404 +
  `code: NOT_FOUND`。
- **#41 script 节点配置静默失效**:`timeout_seconds` 被硬编码 120s 覆盖、
  `working_dir` 被全局 Git 目录覆盖。

### 回归汇总(轮次 26 收尾)

- `gofmt`/`go vet` 干净;`go test -count=1 -race ./...` 全部通过(新增
  HITL/panic/script 门禁共 8 个新回归用例)。✅
- 前端 `tsc` + `eslint` + `vite build` 通过;vitest 27/27(新增
  `designer.test.ts` 4 组 `nextNodeId` 用例)。✅
- `smoke.sh` 9 项 + `smoke_sse.sh` 4 项全部通过。✅

### 仍未覆盖(留待后续)

- 属性面板连续输入的焦点保持、危险操作二次确认等交互行为无 jsdom 用例。
- REST 端点契约测试仍只覆盖 6 处(30+ 端点),`api.test.ts` 尚未覆盖
  agents/skills/git/settings/backup 等分支。
- 校验器表达式环境缺 `variables`(运行时 `EvalCondition` 支持)、
  并行分支不收敛时后续节点被静默丢弃、节点明细按 `exec:node` 覆盖导致
  重试/循环历史丢失 —— 已记录,**均在轮次 27 修复**。

## 轮次 27:三轮并行审计的全面收敛(P0 静默成功 + P1 数据/契约 + 前端 10 项)

> 方法:三轮并行只读审计(workflow 运行时+编译器 / 持久化+API+应用层 /
> 前端+双模式契约)共提出 41 条,逐条人工复核源码后定级;22 条已修复,
> 每个修复与回归测试同 commit(3 个提交)。
>
> 与前几轮的关键差别:轮次 24-26 的缺陷多是崩溃/挂起;本轮 6 个 P0 里
> 有 4 个属于**静默成功**——工作流语义上没走完,系统却报告成功。这类
> 缺陷不触发任何异常路径,是"门禁全绿"却仍有 P0 的根本原因。

### P0 修复(#42-#47)

- **#42 决策点无匹配出口即静默 COMPLETED**:`review` 的条件出口全部不匹配
  且无默认分支时,路由直接收尾 → 后续节点一个没跑,执行落 COMPLETED,
  UI 显示"成功"。改为置 `KeyAbort` 落 FAILED,错误里带决策值与已尝试条件
  并发 `node.failed`。`TestNoMatchingBranchFailsExecution`、
  `TestSingleUnconditionalExitStillWorks`(补默认分支仍走通)。
- **#43 决策点多条回边只保留第一条**:其余回边既不进 exits 也不进 loop,
  对应子图永不执行,而校验器的可达性是纯图连通性判断,该节点"可达"
  因而通过 —— 用户既看不到错误也查不到原因。新增 `KindSwitch` IR 节点
  (纯条件选择、不重跑决策节点)+ 循环内决策快照键 `KeyLoopDecision`
  (循环体每轮会清空 `KeyDecision`,不回存的话所有回边条件恒不成立)。
  `TestSecondBackEdgeBranchIsCompiled`、`TestRefactorBackEdgeActuallyRuns`。
- **#44 DSL `variables:` 段运行时完全失效**:`Engine.Start` 只取运行请求
  传入的变量,工作流里声明的 variables 完全不生效(`{{variable.x}}` 渲染
  空串、`variables.x == ...` 条件恒假),而 DSL 解析与导出都保留着这段。
  改为以 DSL 变量打底、请求变量覆盖。`TestWorkflowVariablesReachRuntime`、
  `TestRunVariablesOverrideWorkflowVariables`。
- **#45 校验器比运行时更严**:validator 用 struct env 只暴露 4 个字段,
  `variables.env == "prod"` 被判 unknown name 而拒绝保存,运行期完全
  支持。两侧改为同源的 map 形状环境。`expression_env_test.go` 4 组
  (含"两侧环境键集合必须一致"的直接比对)。
- **#46 节点明细按 `exec:node` 覆盖**:循环第二轮与节点级重试整行覆盖上一轮
  (attempt 恒为 1),监控页看不到任何迭代/重试痕迹。主键改为
  `exec:node:attempt`,每次执行独立成行。`TestFullLoop` 的断言同步改写 ——
  **此前它把"覆盖"当成期望值固化了**(`len==5` 且 `attempt==2`)。
- **#47 `DisabledSkills` 裸 map 并发读写**:执行协程经 `IsSkillDisabled` 读、
  Skills 页开关经 `SetEnabled` 写,生产环境是 `concurrent map read and map
  write` **runtime fatal**(recover 无效,整应用退出,违反约定 #12 的意图)。
  加 `RWMutex`;顺带修正 `SetEnabled` 写序(先落库再改内存)与 `LogLevel`
  无锁字段。`TestSkillDisabledConcurrentAccess` **实测有效**:回退锁后
  `-race` 报 3 处 DATA RACE 并 FAIL。

### P1 修复(#48-#55)

- **#48 工作流版本号由客户端请求体决定**:`model.Workflow.Version` 带 json
  tag,`POST /api/workflows` 直接反序列化,伪造 `version:99` 被 ++ 后落库成
  v100 并留下伪造快照;重复提交还会因 `workflow_versions` 唯一约束被
  `INSERT OR REPLACE` 静默覆盖,版本历史失真且无告警。改为版本号以库中
  `MAX(version)` 为准(事务内读取),快照改普通 `INSERT` 让唯一冲突报错。
- **#49 `SaveWorkflow` 吞存在性查询错误**:DB 故障时对已存在工作流走 INSERT
  分支,真实根因被一句主键冲突掩盖。改为上抛。
- **#50 事件流三个出口不脱敏**:节点失败的 error 常含 CLI stderr,而 stderr
  里常带 `ANTHROPIC_API_KEY=sk-…`。日志侧已由 `logx.Mask` 覆盖,但事件有
  三条下游(SQLite 落库 / SSE / Wails IPC)此前是旁路。在 `Bus.Emit`
  分发点统一脱敏一次,三条出口同时生效,新增订阅者不会漏。
  脱敏走**廉价子串预筛后再进正则** —— Emit 是每节点每次执行的热路径,
  裸正则在 `-race` 下让 SSE 背压用例从 0.6s 涨到 2.3s(2000 事件×1.1ms)。
- **#51 `retry` 端点吞请求体解析错误**:`{"skip":"yes"}` 被吞后 skip 静默
  变 false,用户点「跳过失败节点」实际重跑那个失败的 Agent(可能重复写
  文件/重复外部调用)还返回 200 假装成功。
- **#52 `enable` 端点 fail open**:`value != "false"` 让缺参、?value=0、
  ?value=no 全部判为"启用" —— 一个禁用开关缺参数反而打开工作流。改为只
  接受显式 true/false,其余 400。
- **#53 JSON 请求体无上限**:服务器模式监听 127.0.0.1 且无鉴权,本机任意
  进程都能打这些端点;此前只有 import 端点有 1MB 限制。统一
  `MaxBytesReader`(空 body 端点用 optional 变体,保持 `/run` 可不带 body)。
- **#54 备份恢复绕过校验**:`RestoreBackup` 直接 `Repo.SaveWorkflow`,不走
  `WorkflowService.Save` 与 `Engine.Validate`,悬空边/空图的非法定义会落库
  并在列表里正常可见,直到运行时才炸。改为过校验 + 跳过计数 + 回报非法 ID;
  Agent 配置解不出结构也不再落库(此前形成"库里有、运行不生效"的分裂态)。
- **#55 补 `events(type)` 与 `executions(created_at)` 索引**:审计页 type
  前缀过滤与执行列表排序此前都退化为全表扫描,events 是唯一无上限增长的表。

### 前端修复(#56-#65)

- **#56 `versionDSL`/`exportYAML` 把后端错误 JSON 当成内容**:两者绕过
  `http()` 自建 fetch 且不检查 `res.ok`,`wrap()` 的 JSON 错误体被
  `res.text()` 原样返回 —— 404 的错误弹进版本对话框,或写进下载的
  `.yaml`。新增 `httpText()`;`WorkflowHistory` 的 try/catch 此前永远
  不触发,因为 fetch 并不抛错。
- **#57 `http()` 丢弃后端 `code`/`kind`**:403 权限不足与 400 参数错误在
  UI 上完全无法区分。新增 `ApiError` 携带 `code`/`kind`/`status`。
- **#58 Agents Test 按钮吞异常**:无 catch → unhandled rejection,用户点了
  按钮完全没有反馈。补 catch + busy 态。
- **#59 权限切换用陈旧闭包快照**:快速连点同一 Switch 时两次都基于同一份
  `a.permissions` 计算新值,最终态与用户意图相反;失败无回滚。改为以最新
  state 为基准 + 乐观更新 + 失败回滚 + 同行 in-flight 锁。
- **#60 Executions 表格列错位**:删除按钮单元格被插在第 2 位而表头第 2 列
  是 Workflow;空态 `colSpan={7}` 也与 8 列表头不符。按钮移到行尾。
- **#61 ExecutionMonitor 三处列表未过 `asArray`**:桌面 IPC 允许返回
  null,`setNodes(null)` 会在 `nodes.length` 抛 TypeError 导致整页白屏。
  全项目 12 处列表都用了 `asArray`,唯独这三处漏掉。
- **#62 layout 用 `?? []` 而非 `asArray`**:挡不住"被解析成 HTML 兜底页的
  对象",`execs.filter` 抛错让整个 Layout 崩溃 —— 影响面是所有页面。
- **#63 GitPanel 渲染处读 `.length` 崩溃**:`fileChips` 内部有 `files ?? []`
  但调用点的 `status.staged.length` 先执行,防御位置错了。改为在 refresh
  里用 `asArray` 归一整个 status 形状。
- **#64 GitPanel commit 无二次确认**:后端走 `git add -A` + commit,输入框
  回车即提交全部改动(含未跟踪),不可逆。补 confirm 并展示文件数。
- **#65 Agent「恢复默认」能力空洞**:后端 REST 与 Wails 绑定都就绪,但
  `api.ts` 两个分支都没封装、UI 无入口 —— 配错配置后没有逃生口。补双分支
  封装 + 按钮(带确认)。另:重试/跳过按钮接入 busy 态防连点。

### 回归汇总(轮次 27 收尾)

- `gofmt`/`go vet` 干净;`go test -count=1 -race ./...` 全部通过。✅
- 前端 `tsc --noEmit` + `eslint --max-warnings=0` + `vite build` 通过;
  vitest 32/32(新增 `httpText` 与 `ApiError` 共 5 个用例)。✅
- `./scripts/verify.sh` 7 步全绿(含 smoke 9 项 / SSE 4 项)。✅
- 新增回归测试:validator 4 组 + runtime 8 组 + application 5 组 +
  api 3 组 + logx 3 组 + event 2 组 + persistence 2 组。✅

### 仍未覆盖(留待后续,详见 `docs/OPTIMIZATION.md`)

- 组件层交互行为仍无 jsdom 用例(本轮新增的 confirm/busy/乐观回滚)。
- `irBuilder.stopped` 共享字段在"并行套并行"下会丢失外层 stop 集合 ——
  目前没有能暴露该行为的用例,需先补测试再重构。
- `WAIT_USER` 恢复时 Agent/Skill 节点被原样重跑、用户答复丢失。
- 备份三个 API 方法零 UI 消费(能力看起来存在但不可达)。

## 轮次 28:收敛 OPTIMIZATION.md 的全部登记项(批次 A / B / C)

> 承接轮次 27:把方案里登记但未处理的缺陷全部落地。分 4 个提交,
> 每项修复与回归测试同 commit。全部缺陷在**修复前先用测试暴露**。

### 批次 A:运行时语义补齐(#66-#73)

- **#66 `WAIT_USER` 恢复时用户答复被丢弃**:lower.go 只把 `_user_input`
  塞回状态就放行,runAgentNode/runSkillNode **从不读它** → 用户答复
  完全丢失、Agent 被第二次真实调用。execute/fix 模式下等于重复写文件、
  重复跑测试、重复 git 操作;reject 更是被静默吞成 COMPLETED。
  **语义已与产品确认**:复用 WAIT_USER 那次的响应,答复作为上下文注入,
  节点按"已答复"完成,不再重复调 Agent。新增 `AnsweredKey` 存上次快照 +
  `answeredOutcome` 消费。`waituser_test.go` 4 个用例(approve /
  instruction / reject / Skill 节点),含副作用计数断言。
  **实测有效性**:回退修复后 4 个用例全 FAIL,分别报出"Agent 被重复调用
  1→2""副作用发生 2 次""用户答复未写入上下文""reject 应 FAILED 实际
  COMPLETED""Skill 被重复执行 1→2"。
- **#67 模板缺失变量静默为空串**:`rendered, _ :=` 丢弃 missing 列表 ——
  `{{revwie}}` 拼错时 Agent 收到残缺 prompt 照常返回 SUCCESS,无任何
  告警。改为发 `node.warning` + `logx.Warn`(只告警不让节点失败)。
  `TestMissingTemplateVarEmitsWarning` / `TestNoWarningWhenTemplateComplete`。
- **#68 节点级状态机从未接线**:`CanTransitionNode` 只在测试里被调用。
  写回 `StatusKey` 前校验,违反记 `runtime.error`。
- **#69 并行分支失败不级联**:失败走 `yield(escalateEvent, nil)`,
  ADK errgroup 只在 Go error 时 cancel → 兄弟分支继续跑完(继续烧 token、
  继续改文件)。给并行区包一层共享可取消 ctx + abort 监视协程,任一分支
  `FAILED:` 即取消其余分支;用 `cascadeCtx` 包装 InvocationContext 只替换
  取消语义(ADK 的 Run 收的是 InvocationContext 而非 context.Context)。
  只认 FAILED 前缀,WAIT_USER 不会误杀兄弟分支。
  **实测有效性**:关闭级联后 `TestParallelFailureCancelsSiblings` FAIL ——
  慢分支(30s)不被取消,执行 10s 内一直 RUNNING;恢复后 PASS。
- **#70 merge 汇总失真**:①按入边统计而入边 ≠ 并行区分支(分支内嵌路由时,
  条件不成立的分支本就不该跑,却是 merge 的一条入边);②"查不到状态"
  一律算成功(分支被跳过/根本没跑到时输出"全部成功")。改为从 parallel
  节点取分支清单,并区分「无条件扇出无状态=被截断(失败)」「条件分支
  无状态=正常路由」「显式 SKIPPED」。`TestMergeReportsSkippedBranch`。
  *注:首版实现把"无状态"一律判失败,被
  `TestRouteInsideParallelBranch` 抓出条件分支不跑是正常路由,据此重写。*
- **#71 mock 并发执行决策错位**:`Start` 调 `ResetMock()` 清空共享计数,
  同工作流并发两次时第二次清掉第一次的序列。新增 `AgentRequest.ExecutionID`,
  mock 按执行隔离;`ResetMock` 只清共享空间。
  `TestMockIsolatedPerExecution` / `TestMockSharedSpaceStillResets`。
- **#72 pi 适配器嵌套 JSON 解析失败致决策丢失**:`LastIndex("{")` 命中内层
  `{` 时片段不配平,Unmarshal 必失败 → decision 静默丢失、路由恒走默认
  分支。新增 `coreagent.TrailingJSONObject` 做括号配平(跳过字符串字面量
  与转义引号);claude 侧同问题一并改用 `FirstJSONObject`。
- **#73 Agent stdout 无上限捕获**:`cmd.Output()` 全量读进内存,截断只发生
  在落库层。新增 `coreagent.BoundedBuffer`(8MB,超限计数 + `String()`
  追加显式截断标记)。`agent/output_test.go` 13 个用例。

### 批次 B:并行编译正确性(#74-#75)

- **#74 并行分支越过汇合点**:`irBuilder.stopped` 是共享字段,且
  `parallel()` 给内层分支的停止集合**不含外层的** → 分支一路走到汇合点
  及其后所有节点。后果不是"多跑一次":并行分支由 ADK 并发执行,等于多
  goroutine 并发写同一批状态。停止集合改为显式沿调用链传递,
  `parallel`/`route` 把外层 stop 合并进内层
  (`copySet(outerStop) ∪ {innerJoin}`),删掉共享字段。
  `TestNestedParallelKeepsOuterStopSet`:修复前 merge/after 各出现 2 次。
- **#75 并行分支不收敛时静默丢弃后续链**:`join == ""` 时塞合成的
  `<parID>.merge` 占位节点(工作流里并不存在,落到 lower 变 noop),
  后续链一个都没进 Plan 而执行报成功。新增 `KindWarning` IR 节点
  (只发告警不执行),标记 `Plan.Unconverged` 并发 `runtime.warning`。
  `TestNonConvergingParallelIsReported` / `TestNonConvergingParallelWarns`。

### 批次 C:测试基建(#76-#80)

- **#76 组件层交互行为零测试**:新增 `interaction.test.tsx` 11 个用例 ——
  乐观更新成功/失败回滚、busy 期间连点只提交一次、Git commit 二次确认
  (回车同样走确认)、重试/跳过按钮互斥、四种异常列表返回不崩。
- **#77 REST 契约测试补齐 + 顺带修掉 4 处契约偏差**:`api/server_test.go`
  新增 22 个 `TestContract_*`,覆盖 30+ 端点中此前零覆盖的部分;4xx 一律
  断言错误体同时含 `error` 与 `code`。补测试时暴露出 4 处后端契约偏差,
  一并修复:
  1. `GET/PUT /api/agents/{id}/config` + `reset` + `permission` 对未知
     Agent 返回 200(零值配置);**PUT 还会为不存在的 ID 凭空落库一行配置**,
     而 `applyAgentConfig` 对未知 ID 静默返回 —— 配置永远不生效。
     新增 `App.requireAgent`,四个端点统一 404 NOT_FOUND。
  2. `GET /api/executions/{id}/{nodes,events,artifacts}` 对不存在的执行返回
     200 + `[]`,前端无法区分"这个执行没有节点明细"与"执行 ID 打错了"。
     三个子资源端点先确认父执行存在。
  3. `GET /api/workflows/{id}/versions` 空结果返回 `null` 而非 `[]` ——
     违反 AGENTS.md 约定 #7(列表端点一律返回数组)。`ListWorkflowVersions`
     改为非 nil 初始化,并加 `TestContract_EmptyListIsArrayNotNull` 覆盖
     7 个列表端点。
  4. `POST /api/agents/{id}/test` 与 `POST /api/skills/{id}/enable` 对未知
     ID 返回 400 且 body 无 `code` —— registry 抛的是裸 `fmt.Errorf`,
     wrap() 只能映射成 400。两个 registry 的 `Get` 改为结构化
     `model.NewError(..., "NOT_FOUND", ...)`,与 404 语义约定对齐。
- **#78 绑定桩形同虚设**:`api.test.ts` 把 `@bindings/*` mock 成 `{}`,
  依赖它的用例**静默失败而非报错**,绑定漂移测不出来。改为 Proxy 桩:
  未声明导出立刻抛错,`then/catch/finally` 协议属性放行(踩到:`Promise.all`
  会把每个桩当 thenable)。清单与 `frontend/bindings/` 下 wails 生成的
  真实文件对齐,并加一条直接比对的用例防手写清单漂移。
- **#79 无故障注入**:`persistence.DB` 新增 `SetQueryFault` 语句级注入点
  (仅测试用),接在 `SaveWorkflow` 存在性查询与 `SaveSetting`。
  **实测有效性**:回退 `SaveWorkflow` 的错误上抛后
  `TestSaveWorkflowSurfacesExistenceQueryError` FAIL,恢复后 PASS。
- **#80 备份功能"看起来有实则不可达"**:后端 REST 与 Wails 绑定都就绪、
  前端也有封装,但 **UI 零入口**。Settings 页新增「备份」卡片(导出 +
  从文件恢复,带确认与恢复/跳过/拒绝三组计数)。

### 回归汇总(轮次 28 收尾)

- `gofmt`/`go vet` 干净;`go test -count=1 -race ./...` 全部通过。✅
- 前端 `tsc --noEmit` + `eslint --max-warnings=0` + `vite build` 通过;
  vitest 46/46(新增 16 个:绑定守卫 3 + 交互行为 11 + 契约 2)。✅
- `./scripts/verify.sh` 7 步全绿。✅
- `docs/api.md` 同步本轮契约变化:`enable` 只接受显式 true/false、
  `retry` 的 body 语义、`restore` 的六个计数字段、1MB 请求体上限、
  新增事件类型与事件脱敏说明。✅
- 新增回归测试计数:runtime 10 组、compiler 2 组、agent 13 组、
  persistence 4 组、api 22 组、前端 16 组。✅

## 轮次 29:结构性风险的收敛(OPTIMIZATION 第三节)

> 承接轮次 28:处理方案里登记的 3 项结构性风险。先只读审计两个主题
> (状态三处写入 / ADK 依赖点),逐条复核后按"中间方案"落地,不做高风险
> 大重构。2 个提交。

### 状态一致性(#81-#83)

- **#81 `abortNoRoute` 只写 `NodeStates` 不写 `StatusKey`(P0)**:决策节点
  自身已把 StatusKey 写成 SUCCESS(它确实成功了,只是没有出口能走),而
  `collectResult` 末尾用 StateData **整体替换** NodeStates —— 那个 FAILED
  被覆盖回 SUCCESS。**实测:UI 上"决策点失败"退化成成功**,且 merge 读的是
  StatusKey,下游同样看不到失败。改为双写。
  `TestAbortNoRouteKeepsStateDataConsistent`。
- **#82 `syncNodeStates` 增量覆盖(P1)**:StateData 里被 `delete` 的 key,
  旧值永远留在 NodeStates —— `RetryNode(skip=false)` 正是
  `delete(StateData, StatusKey)`,节点在 UI 上仍显示上一次的 FAILED;
  `loopResetter` 的空串哨兵也被 `fmt.Sprint("")` 复制成空状态。
  改为全量重建。**实测有效性**:回退成增量覆盖后
  `TestSyncNodeStatesRebuildsFromScratch` 3 处断言同时 FAIL。
- **#83 nil 被渲染成字面量 `"<nil>"`(P1)**:`stateAccess.Delete` 以
  `Set(key, nil)` 伪造删除(ADK 的 State 只有 Get/Set/All),`Get` 把 nil
  当缺失,但 `fmt.Sprint(nil)` 会写出 `"<nil>"` 当节点状态。
  `TestNilStateDoesNotBecomeLiteralNil`。
- **float64 症状根治**:定位到 `StateData` 执行期是 int、经 SQLite
  `state_json` 往返后变 float64(实测确认)—— 同一字段类型随生命周期变化,
  于是每个读取点都得写双类型兼容(`IntOfOk` 的由来),漏一处就是静默 bug。
  新增 `model.NormalizeStateJSON`,在唯一的解码入口
  (`GetExecution` / `ListExecutions`)把整数值数字转回 int;1.5 等真浮点
  不受影响。`TestStateJSONNumbersAreIntAfterRead` /
  `TestListExecutionsAlsoNormalizes` / `TestStateTypesStableAcrossPersistence`
  **直接断言 int 而不经过 IntOfOk**,归一失效会立刻失败。

### ADK 契约测试与真实 CLI 冒烟

见上一提交(6597dc2):4 条 ADK 行为契约 + claude 真实 CLI 端到端。
其中一条重要发现:**ADK 自己在 interface 注释里写明
"InvocationContext 内嵌 context.Context" 是临时方案
("will be removed later... we plan to stop embedding go context in adk
context types")** —— 即 `cascadeCtx` 依赖的是 ADK 明示要移除的形态。

### 回归汇总(轮次 29)

- `gofmt`/`go vet` 干净;`go test -count=1 -race ./...` 全部通过。✅
- 新增回归:runtime 6 组(含状态一致性 3 组 + 类型稳定 2 组 + IntOfOk
  纵深防御)、persistence 2 组、compiler 4 组(ADK 契约)、claude 2 组(真机)。✅

### 仍然开放(结构性风险,非登记缺陷)

- **分支 goroutine 的 panic 没有兜底(P0 候选)**:`parallelagent` 在 ADK
  自己的 goroutine 里跑分支,而 `recover()` 在 adk 生产代码中零命中;
  本项目的兜底只对执行协程生效,分支内 panic 会直接杀进程。
- **执行全程无持久化**:运行中崩溃时 `current_node_id` 仍是 Start 时的
  值(空),`RecoverPending` 标 FAILED 后 `RetryNode` 恒返回
  `NO_FAILED_NODE`("无法定位失败节点")。现有崩溃模拟用例覆盖不到。
- **ADK 契约待补**:`loopagent` 恰好 N 轮(现有断言只是"至多")、
  `yield` 返回 false 的提前停止语义、`session.State` 的 Set(nil)/
  Get 缺失键/StateDelta 提交路径、`temp:` 前缀语义。

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
| 26 | 静态审计收敛:P0 正确性/安全 + P1 数据与体验 | ✅ | #29 git add_all 不提交 #30 reject 等同 continue #31 第二个闸门被跳过 #32 script 节点无权限门 #33 执行 panic 杀进程 #34 logx args 不脱敏 #35 SSE 背压反压引擎 #36 属性面板重挂载 #37 节点 ID 冲突 #38 保存无视校验 #39 吞错清空权限/设置假成功 #40 404 语义不齐 #41 script 配置失效 |
| 28 | 收敛 OPTIMIZATION.md 全部登记项:批次 A 运行时语义 / B 并行编译 / C 测试基建 | ✅ | #66 WAIT_USER 答复被丢弃且 Agent 重跑 #67 模板缺失变量无告警 #68 节点状态机未接线 #69 并行失败不级联 #70 merge 报假成功 #71 mock 并发决策错位 #72 pi 嵌套 JSON 解析失败 #73 stdout 无上限 #74 分支越过汇合点 #75 不收敛丢链 #76 组件交互零测试 #77 REST 契约缺口(+4 处契约偏差) #78 绑定桩形同虚设 #79 无故障注入 #80 备份能力不可达 |
| 27 | 三轮并行全面审计(运行时/持久化+API/前端):P0 静默成功 + P1 数据/契约 | ✅ | #42 无出口静默 COMPLETED #43 多回边丢弃 #44 DSL variables 失效 #45 校验器拒绝合法表达式 #46 节点明细按 exec:node 覆盖 #47 DisabledSkills 并发 fatal #48 版本号越权 #49 吞存在性查询错 #50 事件流泄密 #51 retry 吞 body 错 #52 enable fail open #53 请求体无上限 #54 备份绕过校验 #55 缺索引 #56-#65 前端 10 项 |

**需求第四十五条(第一阶段完成标准)逐条核对**见 `docs/index.html` 使用说明附录。
