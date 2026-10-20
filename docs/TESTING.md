# 测试报告 — Agent Workflow Orchestrator

> 共 8 轮测试验证(超过最低 5 轮要求),其中轮次 2-8 为浏览器视觉测试(GUI 黑盒)。
> 每次功能完成与 Bug 修复均有独立 git 提交(见提交号)。
>
> **当前状态(轮次 57-60 之后)**:Go 测试文件 **94** 个 / 有测试的包 **21** 个;
> 前端测试文件 **16** 个 / 用例 **153** 条。完整清单与逐文件说明见
> "轮次 41-44 / 45-48 / 49-52 / 53-56 / 57-60"五节;"某条测试在什么改动下
> 会变红"的实测记录见"改坏实现 → 测试变红"专节与"终审实测记录"专节;
> 门禁自身的盲区见"门禁盲区"一节。

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

### 本节遗留项的最终去向(全部已在后续轮次解决)

> 下面是**轮次 28 当时的快照**,不是现状。三项均已处理:
> - 组件层交互行为 → 轮次 28 C1:`interaction.test.tsx` 11 个用例
>   (confirm / busy / 乐观回滚 / 异常列表)
> - `irBuilder.stopped` 共享字段 → 轮次 28 B1:停止集合改为显式传参
>   (`TestNestedParallelKeepsOuterStopSet` 先行暴露)
> - `WAIT_USER` 恢复时 Agent 被重跑 → 轮次 28 A1:复用已有响应语义
>   (`TestAgentWaitUserResumeReusesResponse` 等 4 个用例)
> - 备份 API 零 UI 消费 → 轮次 28 C5:Settings 页新增「备份」卡片

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

### #84 并行分支内 panic 杀掉整个进程(P0)

ADK 的 `parallelagent` 在**自己的** goroutine 里跑各分支(`errgroup.Go`),
而 `recover()` 在 adk 生产代码中零命中 —— 分支内的 panic 绕过
`engine.execute` 的 `defer recover()`(那只覆盖执行协程),直接杀进程,
违反 AGENTS.md 硬性约定 #12(桌面模式即整应用退出)。

**实测确认**:分支内 Agent 适配器 panic → `panic: boom from agent adapter`,
栈顶 `created by errgroup.(*Group).Go`,测试进程被杀。

修复:`RunEnv.runNodeGuarded` 在节点执行点兜住 panic —— 节点是 panic 的实际
发生地,无论跑在执行协程(顺序链)还是 ADK 分支协程(并行区)都覆盖。
panic 转成 FAILED outcome + `node.failed` 事件 + 完整栈日志,随后由既有
KeyAbort 流程把执行落库 FAILED,用户看到"哪个节点炸了"。
`TestPanicInsideParallelBranchDoesNotKillProcess` 断言进程存活、执行 FAILED、
错误信息含 panic、坏节点记 FAILED、事件已发出。

### #85 真崩溃后无法定位失败节点(P1)

`CurrentNodeID` 只存在内存,运行中从不落库。真崩溃(进程被强杀,**没有任何
收尾代码执行**)后库中仍是 Start 时的空值 → `RecoverPending` 标 FAILED →
`RetryNode` 恒返回 `NO_FAILED_NODE`("无法定位失败节点")—— 崩溃恢复后
无法从失败处重试,用户只能整条重跑。

**为什么现有崩溃模拟用例覆盖不到**:`executor_node_test.go` 的崩溃测试是
先把执行**跑完**再改库状态,`current_node_id` 早已被正常路径写过;而
panic 被兜住的路径上 `failExecution → save(exec)` 也会顺手把内存里的
`CurrentNodeID` 写进去 —— 两条路都掩盖了真崩溃场景。
**实测(无检查点时)**:节点执行期间 t=80/160/240ms 的 `current_node_id`
恒为空串,直到 t=320ms panic 被兜住后才变成 "work"。

修复:`RunEnv.maybeCheckpoint`(2s 节流,换节点时立即落盘)+
`DB.TouchExecutionNode`(单列 UPDATE,不触碰其余字段 —— 逐节点整行写会
明显拖慢执行)。检查点失败静默:它只是尽力而为的诊断信息,不能反过来
中断工作流。

`TestCrashMidExecutionStillLocatesFailedNode` 的断言窗口刻意短于 Agent
存活时间(250ms < 300ms),确保测的是"节点还在跑时"而非"panic 收尾后"。
**实测有效性**:回退检查点后该用例 FAIL 并报出
"节点执行期间 current_node_id 未落库"。

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

## 轮次 30:补齐剩余 ADK 契约 + pi 同形替身冒烟

> 目标:把轮次 29 登记的"仍然开放"清空。纯测试增量,不改生产代码。

### ADK 契约从 4 条补到 9 条

原有 4 条之外补 5 条,每条锁定一处本项目依赖、但 ADK 升级时会**静默**
失效的语义:

- **`loopagent` 恰好 N 轮**:原断言只有 `if n > 3`("至多"),语义从"恰好"
  退化为"至少"不会被发现。补 `n == max`(1/3/5 三档)后钉死。
  循环保护(`DefaultMaxIterations=5`)完全依赖这个上限。
- **`loopagent` Escalate 立即退出**:循环体内任一子 Agent 发 Escalate
  就退出整轮(不等跑完)。本项目把 WAIT_USER / 节点失败 / 循环上限全
  编码成 Escalate —— 若改成"跑完当前轮",人工闸门会在循环体内反复弹。
- **`parallelagent` 消费者提前停止仍 drain**:`yield` 返回 false 后,
  ADK 仍在 defer 里 cancel + drain,等待所有分支 goroutine 结束。
  这条是 `engine.execute` 在 ctx 取消时 `break` 后立刻回写 Execution 的
  前提;若这个保证消失,就是新的数据竞争入口。
- **分支错误回传消费者**:分支 Go error 会经迭代器回传。engine 的
  `lastErr` 完全依赖它 —— 若被吞掉,错误原因丢失。
- **`session.State` 的 Set(nil)/Get 缺失键/All 迭代器**:
  `Set(k,nil)` 后 `Get` 返回 `(nil,nil)` 不报错(本项目 Delete 约定的基础);
  缺失键返回 `ErrStateKeyNotExist`;`All()` 仍含被置 nil 的键
  —— 故派生视图必须显式跳过 nil(#83)。
- **`temp:` 前缀语义**:**实测确认**带前缀的键影响实时状态但被从事件里
  剥掉。本项目**不能**给需要 Resume 恢复的状态加 `temp:` 前缀,否则
  StateData 会静默丢失 —— 这条把"看似安全的优化"变成显式禁止。

### pi 同形替身冒烟(本机无 pi-agent CLI)

`agent/pi/stub_smoke_test.go`:`Bin` 可配置,用 shell 替身补上**子进程这一
层**(此前 A7/A8 只在直接调 `parseOutput` 的单元测试里覆盖,没经过进程)。
6 个用例:全部参数送达(参数名拼错同样会暴露)、含嵌套对象的尾部 JSON
正确提取、超时按 `PI_TIMEOUT` 返回、大输出被 8MB 上限截断**且带显式
标记**、stderr 不混入 Output、非零退出返回 `PI_PROCESS_ERROR` 且带
stderr 线索。

### 回归汇总(轮次 30)

- `gofmt`/`go vet` 干净;`go test -count=1 -race ./...` 全部通过。✅
- 新增回归:ADK 契约 5 条 + pi 替身 6 条。✅
- 顺带修正 TESTING.md 轮次 28「仍未覆盖」段的表述 —— 那是当时的快照,
  其四项均已在后续轮次解决,现标注去向以免被误读为现状。✅

## 轮次 31:复审最近三轮改动 —— 修掉自己引入的缺陷

> 做了一轮"改 A 坏 B"的复审(只读审计最近三轮的 diff),13 条结论逐条复核后
> 处理。其中**两条是本项目自己引入的**,三条是恒真/死代码。

### 本项目自己引入的缺陷

- **#86 检查点节流写成了死代码(P1)**:`maybeCheckpoint` 用
  `Exec.CurrentNodeID` 判断"是否换了节点",但调用点在它**之前**一行就调了
  `SetCurrentNode(nodeID)` —— 两者恒等,这条分支永远走不到,节流退化成纯
  2s 定时。后果:快速链里第 2..N 个节点都不落库,崩溃时
  `current_node_id` 仍指向链首,`RetryNode` 从错误的位置重试 —— 恰好是
  #85 想修的问题被打了折扣。改用独立的 `lastCheckpointNode` 字段。
  `TestCheckpointFollowsFastNodeChanges` 用 4 个 30ms 节点(全程远小于 2s)
  并在**执行过程中**轮询库,断言 n1..n4 全被观察到。
  **实测有效性**:回退后报出 n2/n3/n4 三个节点全丢(实际只观察到
  `{n1, tail}`)。注:断言必须在执行中做 —— 收尾的 `save(exec)` 会把内存值
  一并写入,执行结束后两种实现都是 `tail`,测不出差异。
- **#87 Approve 不重新映射决策(P1)**:Agent 返回 `WAIT_USER` 的同时可以带
  决策(`invokeAgent` 总会填 `Decision`,而 `status=WAIT_USER` 与
  `decision=REJECTED` 可同时成立 —— 一边要求人工确认、一边给倾向性结论)。
  `answeredOutcome` 用 `out := prev` 照搬该决策,用户点 Approve 之后
  `lower.go` 的 `case NodeSuccess` 仍把 `KeyDecision` 写成 REJECTED,路由走回
  拒绝分支 —— **用户的批准被无视**。改为按答复重新映射
  (approve→APPROVED / continue→清空 / reject→FAILED),与 routeAgent 的恢复
  消费路径一致。

  *测试打错位置,第一版没有牙齿*:最初把 WAIT_USER 放在**路由决策节点**上,
  而 `routeAgent` 的恢复消费路径会抢先写 `KeyDecision=APPROVED`,无论
  `answeredOutcome` 写什么都不影响结果。改为**非路由**的普通链 ——
  该链上 `KeyDecision` 的唯一来源就是 `answeredOutcome`。
  **实测有效性**:删掉整个 `switch` 后两个用例都 FAIL,分别报出
  `decision = "REJECTED"`(approve 被无视)与
  `Continue 后决策应被清空…实际 "REJECTED"`。

### 约定违反(人工裁决后修)

- **#88 并行不收敛落 COMPLETED,违反硬性约定 #14**:`warningAgent` 只发
  `runtime.warning` 就收尾,执行落 COMPLETED —— 各分支确实跑完了,但它们
  **之后的链一个都没跑**,用户看到"成功"却不知道缺了一截(事件流无订阅者
  时完全无感)。约定 #14 逐字点名"并行不收敛"必须显式失败。
  **经人工裁决,按约定改为落 FAILED**:与"无匹配出口"(#42)同款处理 ——
  置 `KeyAbort` + `_error` 写明修法("请把分支连到同一个 merge 节点")+ 
  `node.failed` 事件;各分支本身仍正常跑完,失败的只是"之后的链"。

### 恒真测试与死代码

- **#89 `TestNilStateDoesNotBecomeLiteralNil` 是恒真测试**:它把 nil 写库再
  `GetExecution`,断言 `node_states_json` —— 但 `GetExecution` **直接读那一
  列,不经过 `syncNodeStates`**,无论 #83 的修复在不在都通过。改为直接构造
  "StateData 含 nil / 空串"的状态调 `syncNodeStates`。
  **实测有效性**:回退后 4 处断言同时 FAIL(nil 渲染成 `"<nil>"`、空串进视图)。
- **#90 两个 ADK 契约测试只 `t.Log` 不 `t.Error`**:`err 为 nil 不取消兄弟`
  与 `重名子 Agent` 两条不断言任何东西 —— 意味着"ADK 改成 Escalate 即取消
  兄弟"这种回归**不会让 CI 变红**,而这恰是 `parallelCascadeAgent` 的契约
  前提。恒真的测试比没有测试更危险(虚假安全感),已改为真断言。
- **#91 清理死代码/半成品**:`Plan.Unconverged` 只写不读(告警实际由
  `KindWarning` 承载);`KindWarning` 携带的 `Branches` 无人读取却带
  "仅用于承载告警"的假注释;`logx` 有 `MaskMap` 幽灵注释(声称一个不存在的
  导出名,Go doc 会把它渲染到 `maskRecursive` 上);
  `runNodeGuarded` 把同一对象既当 receiver 又当参数(receiver 完全未用)。
- **#92 `mock.Agent.runs` 按 ExecutionID 无界增长**:桌面应用是长生命周期
  进程,每次执行永久留一个 `runState` —— 这是 ExecutionID 隔离引入的**新增**
  无界增长。加 256 空间上限,超限后丢弃最旧的一个(那个执行早已结束,
  复用它无害)。
- **#93 `agent/output.go` 手写 `itoa`**:同文件已 import 标准库,无理由自造
  轮子(且 `MinInt64` 取负会溢出)。改用 `strconv.FormatInt`。
- **#94 `lower()` 出口补结构体 Agent 的 panic 兜底(纵深防御,非已证缺陷)**:
  `runNodeGuarded` 只包住"节点执行"这一个点;`routeAgent` / `loopGuard` /
  `switchAgent` / `loopResetter` 里的表达式求值、状态操作同样可能 panic,
  而它们在并行分支内运行时不受 `engine.execute` 的兜底保护(ADK 的
  `errgroup.Go` 起了独立 goroutine,`recover()` 在 adk 生产代码中零命中)。
  **如实说明**:这一层**没有实测到 panic** —— 实测关掉它之后,Skill 在
  并行分支内 panic 的用例仍通过(该路径已被 runNodeGuarded 覆盖)。它是给
  "未来新增/修改的结构体 Agent"准备的兜底,放在 `lower()` 出口以保证新增
  Agent 类型时不会漏。

### 回归汇总(轮次 31)

- `gofmt`/`go vet` 干净;`go test -count=1 -race ./...` 全部通过。✅
- 新增/重写回归:检查点跟随快速节点切换 1 组、Approve 覆盖决策 1 组、
  nil 状态不进视图 1 组(3 条均为**实测有效性**验证)。✅

## 轮次 32:可视化/端到端验证

> 用真实浏览器与真实应用验证最近几轮的行为变更。**重要发现:一个测试环境
> 限制(见下),它一度被误判为应用缺陷。**

### 环境障碍与定位过程

内置 Browser(Electron)打开页面后,一旦应用建立 SSE 连接
(`EventSource('/api/events')`),**整个页面 JS 冻结** —— 连 `setTimeout`
都不再触发,React 的三个 API 请求永远停在 pending。

定位步骤(每一步都有证据):

1. 服务端直查:`/api/workflows` 13ms 返回 5 个工作流 —— **后端正常**;
2. 原生 `fetch` 探针页(与应用同源):`status=200 count=5`,
   `/api/stats`、`/api/executions` 全部 200 —— **传输层正常**;
3. 请求头探针:无头 / `application/json` / `text/plain` 三种都正常 ——
   **不是 CORS 或 Content-Type 问题**;
4. 增量探针:`api ok 200` 打出来了,`new EventSource(...)` 之后脚本
   **再也不推进** —— **定位到 EventSource 本身**。

结论:**这是内置 Browser 工具的环境限制,不是应用缺陷**(已与项目所有者
确认并按"继续用 API 冒烟 + 真实桌面 .app"推进)。

### 端到端验证结果(全部通过)

| 验证项 | 结果 |
| --- | --- |
| 首页 Dashboard 渲染 | 侧栏 8 项、5 个统计卡、最近执行区 —— 正常,**0 控制台错误**(唯一警告来自 Wails 框架的浏览器模式提示) |
| Workflows 页渲染 | 表头 8 列、空态文案正确 —— 正常 |
| **#88 并行不收敛 → FAILED** | `state=FAILED`,错误信息「节点 fanout 之后的链路不会执行:该处没有公共汇合点…请把分支连到同一个 merge 节点」;两个分支本身 `a/b/tail_a/tail_b` 均 SUCCESS —— **各分支跑完、失败的只是"之后的链",符合约定 #14** |
| **HITL 全链路** | 运行 → `WAITING_USER`(gate=WAITING,plan=SUCCESS)→ 提交 approve → `COMPLETED`,三个节点全 SUCCESS |
| **#46 节点明细按 attempt 独立成行** | coding-task 循环工作流:6 行明细,`review` 的 `attempts=[1,2]` —— 修复前会被覆盖成一行 |
| **#32 script 节点安全门禁** | 默认不放行,执行落 FAILED,错误写明「需在设置页开启「允许执行 script 节点」」 |
| **REST 列表契约(约定 #7)** | 6 个列表端点全部返回数组:workflows[6] / stats[6] / events/all[77] / agents[4] / skills[3] / executions[5] |
| **REST 错误体契约** | 4 个 404 端点全部含 `error` + `code:"NOT_FOUND"` |
| **#52 enable fail-closed** | 缺参 / `?value=0` / `?value=no` 全部 400;`true`/`false` 正常 200 |
| **桌面 .app 打包与启动** | `wails3 task package` 成功;启动后前端资源加载 11 个请求、**绑定 IPC 走真实 Wails 通道**;数据库 3 个种子工作流正常入库 |

### 桌面模式像素级验证(补做,已授权后完成)

首次尝试失败的真正原因不是"应用窗口没出来",而是**权限授给了错误的进程**:
查 `~/Library/Application Support/com.apple.TCC/TCC.db`,`kTCCServiceAccessibility`
与 `kTCCServiceScreenCapture` 两项均为空,而当时会话跑在 MiniMax Code Helper
里。给正确的进程授权后,桌面窗口正常置前并截图成功。

**Dashboard 桌面截图确认的内容**:
- 左下角 `mode: desktop` / `ADK Go runtime · Wails 3` —— **真实 Wails IPC
  模式**(不是浏览器 HTTP 回退),绑定走进程内通道;
- 侧栏 8 项齐全(Dashboard / Workflows / Executions / Events / Agents /
  Skills / Git / Settings);
- 统计卡 5 个:3 Workflows、0 Running、0 Waiting User、0 Completed、0 Failed;
- 统计表三行:Approval Demo / Coding Task / Parallel Review,含"总执行 /
  Completed / Failed / Waiting / 成功率"五列;
- Workflows 卡片三行,各带 `8 nodes · v1` / `5 nodes · v1` / `4 nodes · v1`
  与 enabled 徽章;
- macOS 原生标题栏 + 红黄绿交通灯 + 半透明背板(MacBackdropTranslucent
  配置生效)。

### 顺带修掉的真实缺陷:裸二进制启动即崩溃

为拿窗口 ID 给 `main.go` 加了一行日志,顺带发现:
**不带 bundle identifier 直接跑二进制会立刻退出** ——
`notifications require a valid bundle identifier`,
`wailsApp.Run()` 返回错误 → `os.Exit(1)`。`.app` 包因带 `Info.plist`
(identifier `com.agentworkflow.orchestrator`)不受影响,所以之前没暴露。

这影响开发体验与"桌面是否真的启动了"的判断:用裸二进制验证桌面模式会
**静默失败**(进程秒退,窗口从不出现)。已加窗口创建日志,失败时明确告警。

### 仍未覆盖

**点击交互未完成**:CGEvent 合成点击需要辅助功能 TCC,即便置前成功,
点击仍会穿透到前台应用(截到的是别的窗口)。因此"点击侧栏切换页面"
这类交互尚未在像素级验证 —— Dashboard 的**渲染**已验证,其余页面
(Workflows / Executions / Agents / Settings)的渲染在服务器模式下已逐一
验证过(见上表),桌面模式复用同一套 React 组件,渲染路径相同。
`osascript` 读窗口几何同样需要给 osascript 自身授权,当前未授。

## 轮次 33-40:两轮并行审计的回归(8 个子代理)

> 本节记录"两轮并行审计"(commit `3f5e68f` + `916a9c4`)的回归用例。
> 轮次号与 `docs/OPTIMIZATION.md` 第四、五节一一对应:
> 第一轮 33-36(前端 / DSL+模型 / 持久化+事件 / API+应用层),
> 第二轮 37-40(运行时 / 编译器+校验器 / 适配层 / 支撑包)。
>
> **测试规模变化**:
>
> | 维度 | 轮次 33 前 | 轮次 33-40 后 | 轮次 49-52 后 | **当前(轮次 57-60 后)** |
> | --- | --- | --- | --- | --- |
> | 有测试的 Go 包 | 19 个 | 21 个(新增 `workflow/model`、根包 `agentworkflow`) | 21 个 | **21 个** |
> | Go 测试文件 | 41 个 | 66 个 | 90 个 | **94 个** |
> | 前端测试文件 | 4 个 | 12 个 | 16 个 | **16 个** |
> | 前端用例 | 46 条 | 89 条 | 135 条 | **153 条** |
>
> 当前值的复算命令(轮次 57-60 之后实测):
>
> ```bash
> # Go 测试文件:已跟踪 + 新增未跟踪(提交后两者相等,此处给出并集口径)
> { git ls-files '*_test.go'; git ls-files --others --exclude-standard '*_test.go'; } \
>   | sort -u | wc -l                                          # 94 个 Go 测试文件
> { git ls-files '*_test.go'; git ls-files --others --exclude-standard '*_test.go'; } \
>   | xargs -n1 dirname | sort -u | wc -l                     # 21 个有测试的包
> cd frontend && find src -name '*.test.ts*' | wc -l          # 16 个前端测试文件
> cd frontend && npx vitest run                               # 153 条用例
> ```
>
> 前端 12 文件 / 89 用例是**第一轮结束时的**数;第二轮(轮次 37-40)另有
> 并发轮次继续加测,第三、四、五、六、七轮继续加测 —— 完整清单见本文
> "轮次 41-44 / 45-48 / 49-52 / 53-56 / 57-60"五节。

### 轮次 33:前端(约定 7 / 8 / 10)

| 文件 | 锁住的行为 |
| --- | --- |
| `frontend/src/test/list-guard.test.ts` | **探针式测试**(见下节):扫描 `src/**` 全部 `.ts/.tsx`,检查列表接口调用点是否过 `asArray` |
| `frontend/src/pages/Dashboard.test.tsx` | 事件风暴下只触发**一次**全量拉取(此前每条事件一次) |
| `frontend/src/pages/Executions.test.tsx` | 同上 + 列表渲染不因 `null` 崩 |
| `frontend/src/pages/WorkflowHistory.test.tsx` | 同上 |
| `frontend/src/pages/ExecutionMonitor.test.tsx` | 防抖窗口是 **200ms**(此前实测 120ms,违反约定 7) |
| `frontend/src/pages/EventsAudit.test.tsx` | 实时事件刷新走统一的 `debounce.ts` 单一来源 |
| `frontend/src/pages/Workflows.test.tsx` | 五处裸 `await` 失败时弹 `toast.error`,不再静默吞错 |
| `frontend/src/components/layout.test.tsx` | 侧栏列表同样过 `asArray` 防御 |
| `frontend/src/test/helpers.ts` | 共享测试桩(非用例文件) |

`frontend/src/lib/debounce.ts` 是本轮新增的**单一来源**:
`REFRESH_DEBOUNCE_MS = 200`,尾沿语义,组件卸载必须 `cancel()`。

### 轮次 34:DSL 与模型层

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/dsl/dsl_test.go`(12 例) | `TestParseJSONKeepsNodeConfig`(**JSON 格式导入后配置不丢**)、`TestJSONAndYAMLProduceSameModel`、`TestNumericConfigNormalizedToInt`(inline 整数不取成 0)、`TestToModelDoesNotMutateDocument`(不改写调用方文档)、`TestAllNodeTypesRoundTrip` |
| `workflow/model/model_test.go`(9 例) | 各模型 JSON 往返 + `TestNormalizeStateJSON`(含数组内 map 与 NaN/Inf 守卫) |
| `workflow/model/errors_test.go`(7 例) | `TestAsErrorWrapsPlainError` **断言错误链未被丢弃**(`errors.Is` 能挖到根因) |
| `workflow/model/intread_test.go`(1 例) | `state_json` 往返后的 int 读取路径(约定 2) |

### 轮次 35:持久化与事件总线

| 文件 | 锁住的行为 |
| --- | --- |
| `persistence/repo_cas_test.go`(12 例) | `TestSaveExecutionDoesNotRevertCastedNegativeTerminal`(CAS 落定的 FAILED/CANCELLED 不被过期整行写改回)、`TestCasExecutionStateSingleWinner`(双击只允许一路成功)、`TestMigrateColumnsSurfacesRealFailure`(迁移 ALTER 失败不再静默)、`TestWrappedErrorKeepsCause`、`TestVariablesKeepIntTypeAfterRoundTrip` |
| `event/bus_contract_test.go`(4 例) | `TestEmitSurvivesPanickingSubscriber` / `TestEmitKeepsDeliveringAfterPanic`(**坏订阅者不带走发布方**)、`TestBusKeepsNoHistoryByDefault`(约定 9) |

### 轮次 36:API 与应用服务层

| 文件 | 锁住的行为 |
| --- | --- |
| `api/server_test.go`(+14 例) | `TestErrorBodyAlwaysHasCodeAndKind`(**错误体形状唯一**)、`TestMethodNotAllowedAndUnknownRouteReturnJSONError`(405/未知路由不再是纯文本)、`TestJSONBodySizeLimit`(超限 → **413**)、`TestQueryIntOverflowIsClamped`(`?limit=99999999999999999999` 不再返回全表)、`TestSettingsRejectsInvalidLogLevel` |
| `api/sse_test.go`(4 例) | `TestSSEWriteFailureClosesStream`(**写失败即关流**,不再只记日志)、`TestSSEDisconnectReleasesSubscription`(订阅被释放)、`TestSSESlowClientDoesNotBlockProducer` |
| `api/assethandler_test.go` | 桌面模式 `/api` 前缀挂载的契约 |
| `app/application/application_test.go`(22 例) | `SetPermission` 并发不丢更新、Git 工作目录**先落库**、`log_level` 校验、备份恢复的未知 Agent 计入 invalid、节点 output 掩码 |
| `main_test.go`(9 例) | `TestParseArgsRejectsInvalidLogLevel` / `TestParseArgsRejectsInvalidAddr`(启动参数当场报错)、`TestRunExitCodes`(退出码由 `run()` 返回,defer 一定执行 → SQLite 一定关闭) |

### 轮次 37:运行时(状态写 / 事件落库 / 告警分级)

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/state_write_test.go`(7 例) | `TestSaveMetaNeverTouchesStateColumn`(**状态列只走 CAS**)、`TestStaleMetaWriteCannotRevertCompletedExecution`、`TestStaleTransitionCannotOverwriteNewerState`、`TestCancelledExecutionNotRevivedByStaleWrite` |
| `workflow/runtime/event_persist_test.go`(4 例) | `TestEventPersistFailureIsObservable`(落库失败 → 计数 + 日志 + `runtime.error`)、`TestEventPersistFailureDoesNotFailExecution`(不打断执行) |
| `workflow/runtime/escalate_test.go`(5 例) | `TestNormalLoopExitIsNotWarned`(正常循环退出不再记 WARN)、`TestLoopLimitEscalateIsUnexpected`、`TestNodeFailureEscalateIsUnexpected` |
| `workflow/runtime/masking_test.go`(3 例) | `TestNodeOutputMaskedBeforePersist` / `TestNodeErrorMaskedBeforePersist`(**落库前脱敏**)、`TestGitDiffArtifactKeepsRawContent`(制品保持原样) |
| `workflow/runtime/node_config_test.go`(6 例) | `TestRetryPolicyAcceptsUint64` / `TestNextAttemptAcceptsUint64`(int 读取点认全 uint64)、`TestNodeEnabledInvalidValueFailsNode`(非布尔 fail closed)、`TestNodeEnabledSharesContractWithValidator`(与校验期同源) |

### 轮次 38:编译器与校验器(约定 13 收口)

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/compiler/nodeconfig_contract_test.go`(7 例) | **探针式测试**(见下节):AST 扫描运行期源码,比对配置契约 |
| `workflow/compiler/conditionenv_test.go`(4 例) | `TestConditionEnvKeysAreStable` / `TestConditionEnvStaticAndRuntimeShapesMatch`(校验期与运行期的条件变量集合**形状一致**) |
| `workflow/validator/nodeconfig_test.go`(9 例) | `TestDisabledNodeSkipsRequiredConfig`(禁用节点不再要求必填)、`TestDisabledNodeSkipsAgentExistence`、`TestNonBoolEnabledRejected`、`TestConditionNodeExpressionNotRequired`(但 `TestConditionNodeExpressionStillSyntaxChecked` 保留语法检查)、`TestDisabledNodeStillReportsUnknownType` |

### 轮次 39:Agent / Skill 适配层

| 文件 | 锁住的行为 |
| --- | --- |
| `agent/claude/claude_audit_test.go`(7 例) | `TestParseOutputUsesTrailingJSON`(**取尾部而非首个**)、`TestParseOutputRejectsInvalidDecision`、`TestParseOutputIsErrorStringForm`(`is_error:"true"` 不再当 SUCCESS)、`TestExecuteTruncatedOutputFails`(8MB 截断 → 显式失败)、`TestExecuteProcessErrorRedactsSecrets` / `...RedactsInheritedSecret`(stderr 里的凭据被脱敏) |
| `agent/pi/pi_audit_test.go`(5 例) | 与 claude 同构的 5 条(尾部 JSON、非法决策、截断失败、stderr 脱敏) |
| `agent/output_depth_test.go`(4 例) | `TestJSONExtractionIsLinearOnPathologicalInput`(**O(n²) → 线性**)、深嵌套不 panic、8MB 写入不 panic |
| `agent/redact_test.go`(6 例) | 按**确实注入过的值**精确脱敏(段匹配、多个来源、非敏感值不动) |
| `agent/config_mask_test.go`(2 例) | `TestMaskedExtraArgs`(**ExtraArgs 里的凭据也被掩掉**) |
| `agent/decision_test.go` / `agent/mock/mock_evict_test.go` | 决策值形态校验;`TestEvictionSkipsInUseSpaces`(淘汰不删仍在运行的空间) |

### 轮次 40:支撑包

| 文件 | 锁住的行为 |
| --- | --- |
| `logx/mask_test.go`(9 例) | `TestMaskValueContainerCoverage` / `TestMaskValueNestedMapStringStringRegression`(**`map[string]string` 也要掩**)、`TestKeySensitivity`(`user_token` / `x-api-key` / `apiKey` 命中)、`TestMaskValueDoesNotMutateInputContainers` |
| `permission/permission_test.go`(9 例) | `TestMalformedOverrideFailsClosed`(**非法覆盖 fail closed,不再 fail open**)、`TestManagerConcurrentAccess` |
| `template/template_test.go`(14 例) | `TestRenderSurvivesPanickingStringer` / `TestRenderErrReportsPanic`(**渲染 panic 不逃逸**) |
| `git/git_test.go`(14 例) | `TestExecFailureMessageHasReason`(失败原因不丢)、`TestWorkingDirConcurrentAccess`、`TestExecTimeoutMapsToTimeoutCode` |
| `contextx/context_test.go`(7 例) | `TestTruncateKeepsRuneBoundary`(**按字节截断不再劈开中文**) |
| `skill/builtin/builtin_args_test.go` / `skill/status_test.go` | Skill 参数形态;状态归一 |

### 探针式测试:防"约定漂移"的机械化守卫

本轮新增的两类测试与普通单测**性质不同** —— 它们不测业务行为,而是
**扫描源码、防止约定 7 / 约定 13 被无声破坏**。这类测试值得单独说明:

1. **`workflow/compiler/nodeconfig_contract_test.go`(AST 扫描)**
   - 做法:用 `go/parser` 解析 `workflow/runtime/*.go`,抽出每个节点执行
     函数**实际读取的配置键**,与 `compiler/nodeconfig.go` 的契约表逐项比对。
   - 防的是:约定 13("校验期与运行期必须同源")此前**只有一句文字**,
     两份实现各自演化。漂移有三个方向,后果都严重:
     运行期读了表里没有的键 → **用户被自己的校验器挡住**;
     表里要求了运行期不读的键 → 用户被迫填一个**没有作用的键**;
     表里的键运行期已不再读 → 校验期按过期契约要求用户填。
   - 失败信息会指出该补哪一侧(`TestRuntimeReadsOnlyKnownConfigKeys` /
     `TestValidatorOnlyKeysNotReadByRuntime` / `TestRuntimeConfigReadsAllAttributed`)。
2. **`frontend/src/test/list-guard.test.ts`(源码扫描)**
   - 做法:遍历 `src/**` 全部 `.ts/.tsx`,定位
     `api.listWorkflows() / listExecutions() / listAgents() / …` 调用点,
     检查其结果是否先过 `asArray` 再 `.map`。
   - 防的是:约定 7(列表接口先过 `asArray`)同样只有一句文字。
   - **首次运行就抓到真实缺陷**:`pages/Designer.tsx` 直接对
     `api.listAgents()` 的结果 `.map`,异常返回时抛错又被 `.catch(() => {})`
     吞掉 —— 界面表现是"Agent 下拉框空的",没有任何提示(#98)。
   - **已知盲区**(登记在 `docs/OPTIMIZATION.md`"仍然开放"):先赋值给变量
     再 `.map` 的写法扫不到。

**方法论结论**:约定 7 / 约定 13 这类"跨文件的形状约定",靠人工 review
必然漂移;把它写成**探针**(扫描实现、比对契约表)之后,漂移会立刻让门禁变红。
本轮新增的两处探针是这个方向的开始,后续同源约定(条件变量集合)应照此办理。

## 轮次 41-44:第三轮(跨文件收口 / 前端契约同步 / 门禁重写,commit `8520115`)

> 4 个子代理并行:跨文件收口 / 前端契约同步 / 门禁脚本重写 / 文档同步。
> 缺陷号 #142-#162,见 `docs/OPTIMIZATION.md` 第六节。

### 轮次 41:跨文件收口(后端)—— 新增测试文件

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/dsl/no_config_structs_test.go`(1 例) | `TestDSLPackageDeclaresNoConfigStructs`:**dsl 包不得再声明节点配置结构体**(AST 扫描)。9 个零引用结构体删掉后,靠这条防止"第二份契约"重新长出来(约定 13) |
| `workflow/runtime/enabled_contract_test.go`(2 例) | `TestEnabledConclusionMatchesValidator`:**真实调用** `validator.Validate` 与运行期的 `nodeEnabled`,断言两边结论逐项相同(布尔 / 字符串 / 缺失 / 非法值);`TestEnabledInvalidFailsClosed` |
| `workflow/runtime/persist_contract_test.go`(2 例) | `TestExecMetaColumnsMatchPersistenceUpsert`(运行期直写 SQL 的列集合 == 持久层 upsert 列集合)、`TestExecMetaColumnsExistInTable`(列真的存在于表里) |
| `workflow/runtime/skill_status_test.go`(4 例) | `TestSkillStatusNormalized`(`"wait_user"` / `"Failed"` / `" wait_user "` 都归一)、`TestSkillStatusUnknownFailsNode`(契约外状态 → 失败)、`TestSkillStatusEmptyFailsNode`(空状态 → 失败)、`TestSkillStatusFailedKeepsReason`(失败带原因)。**注**:本文件由第三轮创建,其中"契约外状态"那条用例在**第五轮被改写**(见下节) |
| `workflow/validator/permission_test.go`(3 例) | `TestInvalidPermissionOverrideRejected`(非法 `permissions` 结构在校验期被拒)、`TestValidPermissionOverrideAccepted`、`TestEmptyPermissionOverrideAccepted` |
| `workflow/runtime/permission_test.go`(3 例) | `TestIllegalPermissionFailsExecutionWithReason`(运行期**明确失败**且原因写明"权限覆盖非法",而不是"无权执行 fs:write")、`TestStartRejectsIllegalPermissionWithReason`、`TestLegalPermissionOverrideStillApplies` |
| `logx/level_test.go`(3 例) | `TestValidateLevelMatchesSetLevelChecked`(校验入口与应用入口对同一批取值给**同一结论**)、`TestLevelsReturnsSupportedSet`(白名单内容与顺序固定)、`TestValidateLevelDoesNotChangeCurrentLevel`(校验不改变当前级别) |
| `app/application/loglevel_test.go`(3 例) | `TestLogLevelAcceptedSetMatchesLogx`(设置层接受集合 == logx)、`TestInvalidLogLevelNotPersisted`(非法级别不落库)、`TestValidLogLevelAppliedAndPersisted` |
| `main_loglevel_test.go`(1 例) | `TestIsValidLogLevelMatchesLogx`(启动参数校验与 logx 同一份白名单) |
| `app/application/masking_test.go`(+159 行) | API 读出口的 `state_data` / `task` 脱敏 |

### 轮次 42:前端契约同步 —— 新增测试文件

| 文件 | 锁住的行为 |
| --- | --- |
| `frontend/src/lib/api.test.ts`(+236 行) | 统一错误体解析:`Kind(CODE): ` 前缀被剥掉、只有 `code` 没有 `error` 时按码表给中文提示、未知 code 不落 Object 原型链 |
| `frontend/src/pages/Agents.test.tsx`(3 例) | 空键行:新增行未填名称时不再允许插同名空行;保存时过滤未命名行并**如实告知**;没有空键时原样提交(掩码值 `***` 不被丢掉) |
| `frontend/src/pages/Skills.test.tsx`(3 例) | 开关 in-flight 锁:请求未完成时连点只发一次请求;点击后立即乐观更新;失败时回滚并提示 |
| `frontend/src/pages/GitPanel.test.tsx`(3 例) | 三查询**独立成败**:status 失败时显示原因且不影响 Diff;Log 失败不影响状态面板;全部成功时不报错 |
| `frontend/src/pages/Settings.test.tsx`(6 例) | script 节点开关**二次确认**(拒绝则保持关闭且不提交);备份恢复的拒绝计数写明"结构非法**或**本机无此 Agent";日志级别非法在前端就拦下(不发给后端) |
| `frontend/src/pages/ExecutionMonitor.test.tsx`(+52 行) | 输出含 `***` 时给出脱敏说明;未脱敏的输出不显示该说明 |

### 轮次 43:门禁脚本重写 —— 门禁本身的回归

门禁不是"跑一次绿了就算数",本轮给它补了三类**自检**:

| 检查 | 防的是 |
| --- | --- |
| **用例存在性**检查 | 测试文件存在但用例被注释 / 全 skip → 门禁照样绿,覆盖率悄悄归零。**注**:本轮的范围是手写的"关键包"清单,**第六轮改为从 `go list` 自动推导的全项目 21 个包**(见 #193) |
| `go build` 步骤 | `vet` 发现不了的**链接期**错误(缺 main、符号冲突、平台专属代码) |
| 冒烟脚本的**端口预检** | 端口被上一次残留进程占用时,9 项断言全部打到旧进程上却照样打印 `SMOKE OK` |

`gofmt` 由"传目录"改为"传文件清单":gofmt 遇目录参数会递归整棵树,传模块根
目录会把 Wails 脚手架(`build/ios`)一起卷进来。

**实测**:重写后 `verify.sh` 8 步全通过,实测 65s。

> **后续变化**:第六轮把"关键包"清单改为自动推导(全项目 21 个包)、
> gofmt 清单改为 `git ls-files`;第七轮再扩为 **9 步**(补 Windows 交叉编译、
> 前端三个子检查各自独立),并补上"gofmt 解析不了 ≠ 已格式化"。
> 详见"轮次 53-56"与"轮次 57-60"两节。

## 轮次 45-48:第四轮(state_json 脱敏 / CAS 可观测 / 恒真测试清理 / DSL 严格化,commit `4e979eb`)

> 3 个子代理并行。缺陷号 #163-#174,见 `docs/OPTIMIZATION.md` 第七节。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/state_mask_test.go`(5 例) | `TestMaskStateDataKeepsStructuredKeys`(decision / status / `_abort` / `node:<id>:status` **原样保留** —— 脱敏它们等于改写执行语义)、`TestMaskStateDataMasksFreeTextKeys`(output / `_error` / `git_diff` 等自由文本**必须脱敏**)、`TestMaskStateDataKeepsOutcomeJSONValid`(脱敏后**仍是合法 JSON** —— 恢复路径要 Unmarshal)、`TestMaskStateDataDoesNotMutateInput`(不改运行期在用的 map)、`TestStateWritesAreClassified`(源码扫描:新增状态键未登记分类即红) |
| `workflow/runtime/cas_conflict_test.go`(4 例) | `TestTerminalCommitConflictDoesNotEmitCompletedEvent`(**状态没落库就不发终态事件**)、`TestCancelCommitConflictIsExpectedRace`(取消路径的冲突是预期竞态)、`TestFailExecutionConflictDoesNotEmitFailedEvent`、`TestNormalTerminalCommitStillEmitsEvent`(正常路径仍发事件 —— 防止"一律不发") |
| `workflow/runtime/masking_api_test.go`(1 例) | `TestExecutionAPIResponseMasksStateJSON`:**API 响应体**里没有明文(与落库侧是两条独立路径,各测各的) |
| `workflow/dsl/schema_test.go`(9 例) | `TestParseRejectsMisplacedPermissions` / `...JSON`(YAML 与 JSON **同源**)、`TestParseRejectsUnknownTopLevelKeyJSON`、`TestParseTopLevelStrictnessSameForYAMLAndJSON`、未知**嵌套**键只忽略但记入 `IgnoredKeys`(`TestParseIgnoresButReportsUnknownNestedKey` / `...SettingsKey`)、`TestParseAcceptsRepoExamples`(**仓库自己的示例必须仍能导入**)、`TestParseAcceptsPermissionsAtRoot`、`TestRoundTripExportImportHasNoIgnoredKeys`(导出→导入不得凭空多出一批 WARN) |
| `workflow/model/permission_alias_test.go`(4 例) | `TestWorkflowUnmarshalAcceptsDSLPermissionsSection`(读侧认 `permissions` 别名)、`...LegacyPermissionSection`、`...RejectsBothPermissionSections`(两个键同时出现报错,不是静默挑一个)、`TestWorkflowMarshalKeepsLegacyKey`(**输出侧键名不动** —— 既有 API 响应与历史快照不受影响) |
| `skill/builtin/builtin_args2_test.go`(5 例) | `message` 是标量 → 归一;是结构体 → **报错**(此前静默变空串);`LogSkill` 同构 |
| `skill/builtin/builtin_bool_test.go`(3 例) | `fail: "treu"`(拼错)→ 报错(此前一律当 false → "注入失败"静默失效) |
| `template/template_missing_test.go`(7 例) | 缺失变量的口径:**键在数据里 = 可解析**(值为 nil 渲染成空串,不算缺失);键不在数据里 = 缺失。另含自引用不递归、模板不得越界访问数据 |
| `agent/output_depth_test.go`(+168 行) | 改掉**逻辑上恒假的断言**,换成真实不变量(线性扫描的实测行为) |
| `workflow/compiler/adk_contract_test.go`(+104 行) | 三处只 `t.Log` 不 `t.Error` 的"契约测试"改成**真断言** |
| `agent/redact_test.go`(+102 行) | 脱敏补齐部分包含 / JSON / 多行 / 边界四类形态 |

## 轮次 49-52:第五轮(runtime 交接 / 前端与校验期 / API 严格化,commit `59ef7d7`)

> 3 个子代理 + 1 个独立 verifier。缺陷号 #175-#188,见
> `docs/OPTIMIZATION.md` 第八节。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/template_render_test.go`(3 例) | `TestTemplateRenderFailureFailsNode`(渲染失败 → **节点失败**,Agent 不被调用)、`TestTemplateRenderFailureInInstructionsFailsNode`、`TestTemplateRenderOKNoWarning` |
| `workflow/runtime/agent_status_test.go`(4 例) | `TestAgentStatusNormalized`(`"failed"` / `"wait_user"` 归一)、`TestAgentStatusUnknownFails`(契约外状态 → 失败)、`TestAgentStatusEmptyFails`、`TestAgentNotCalledAfterRenderFailure`(**Agent 不得被调用** —— 渲染失败时不能带着原文去干活) |
| `workflow/runtime/skill_args_test.go`(5 例) | `TestSkillArgsWrongTypeFails`(args 不是 map → 失败)、`TestSkillArgsMapReachesSkill`(正确的 map 确实送达)、`TestSkillArgsAbsentOK` / `TestSkillArgsNilOK`(缺省合法)、`TestGitArgsWrongTypeFails` |
| `workflow/runtime/node_config_guard_test.go`(4 例) | `TestAgentPromptObjectWithoutTemplateFails`(prompt 对象缺 template → 失败,不再发空提示词)、`...WithTemplateOK`、`TestSubworkflowInputWrongTypeFails`、`TestSubworkflowInputMapReachesChild` |
| `workflow/validator/unknown_config_test.go`(5 例) | `TestUnknownNodeConfigKeyIsReportedAsWarning`(未知配置键 → **警告**)、`TestUnknownNodeConfigKeyDoesNotBlockSave`(**不阻断保存** —— 存量工作流里有拼错的键)、`TestDeprecatedConfigKeyIsNotReportedAsUnknown`(已废弃的键只报废弃)、`TestDeclaredConfigKeysAreAllKnown`、`TestRepoExampleWorkflowsStayValid` |
| `api/workflow_payload_test.go`(7 例) | `TestFrontendSavePayloadsAreAccepted`(前端设计器的 payload **必须**被接受 —— 严格化不能打破保存路径)、`TestPostWorkflowAcceptsEveryKnownTopLevelKey`、`TestPostWorkflowAcceptsPermissionsAlias`、`TestPostWorkflowRejectsUnknownTopLevelKey`(**400 `UNKNOWN_FIELD`**)、`TestValidateWorkflowRejectsUnknownTopLevelKey`、`TestImportResponseCarriesIgnoredKeys`(导入响应带 `ignored_keys`)、`TestImportResponseWithoutIgnoredKeysHasNoField`(没有键被忽略时**不出现**该字段) |
| `frontend/src/pages/designer.test.ts`(+41 行) | `toModel` 抽成纯函数后:保存时**保留**从服务端读到的 `permission`;没有权限覆盖时 payload 不带这个键 |
| `frontend/src/pages/Workflows.test.tsx`(迭代 21 补) | 导入响应带 `ignored_keys` 时提示"N 个键被忽略"并列出键名;桌面模式没有该字段时 `?? []` 兜底不崩 |

## 轮次 53-56:第六轮(守卫扩面 / 假测试修正 / 门禁盲区,commit `60780a5`)

> 3 个子代理并行。缺陷号 #189-#196,见 `docs/OPTIMIZATION.md` 第九节。
> 本轮的重点不是"多测了什么",而是**把上一轮的弱守卫改强**。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/state_mask_test.go`(11 例,本轮 +819 行) | `TestMaskStateDataKeepsStructuredKeys`(结构化键原样)、`TestMaskStateDataMasksFreeTextKeys`(自由文本键脱敏 + 保留键名可读性)、`TestMaskStateDataKeepsOutcomeJSONValid`(脱敏后仍是**合法 JSON**)、`TestInsertExecutionStoresMaskedCopy`(**insert 路径也落脱敏副本** —— 与 saveMeta / casStateAndSave 三条路径口径一致)、`TestStateWriteEntryHandlesNilStateMap`、`TestRetryNodeWritesNodeStatusViaStateEntry`(统一写入口 `setExecState`)、`TestStateWritesAreClassified`(**探针扩面**:扫 compiler+runtime 全部非测试文件,按字段名识别直接 map 写与 `delete`,并有**防空转下限断言** —— 扫不到键 / 扫到的形态太少即失败) |
| `app/application/loglevel_test.go`(4 例,本轮 +60 行) | `TestLogLevelEffectiveLevelFollowsEveryChoice`:**logx 的实际生效级别**(四档逐一验证)—— 此前只断言 app 层字段与库里的值,改坏 `SetLevelChecked` 不会变红 |
| `app/application/validation_warning_test.go`(2 例,本轮新增) | `TestSavePathDoesNotDropValidationWarnings` / `TestRunPathDoesNotDropValidationWarnings`:**保存/运行路径不得把校验警告整个丢掉**(此前只回传 `res.Errors`,`res.Warnings` 被丢弃) |
| `persistence/exec_state_guard_test.go`(1 例,本轮新增) | `TestExecStateGuardAgreesWithStateMachine`:**守卫与状态机一致**(变异三次均变红)—— 终态守卫的语义与 `CanTransitionExecution` 不能各说各话 |
| `persistence/repo_cas_test.go`(本轮 +41 行) | `TestSaveExecutionRejectsIllegalStateJump` / `TestSaveExecutionAppliesLegalTransition` / `TestSaveExecutionAppliesWaitingUserTransition` / `TestSaveExecutionDoesNotRevertCompleted` / `TestSaveExecutionKeepsCompletedOnSameState` —— 把"终态守卫"的行为写死 |
| `persistence/zz_vt_test.go` | **本轮删除**:整个文件只有 `fmt.Println` 没有断言 → 恒真的调试探针(虚假安全感) |

**本轮的门禁改动**(`scripts/verify.sh`):

| 改动 | 防的是 |
| --- | --- |
| gofmt 文件清单从 `go list` 改 `git ls-files` + `git ls-files --others --exclude-standard` | 平台专属文件(`agent/process_windows.go`)不在检查范围 → macOS 上改坏它门禁全绿 |
| "关键包有用例"从**手写清单**改为**自动推导**(全项目 21 个包) | 手写清单拦不住"**新增**的包 0 用例" |
| `go build -o <临时目录>` | 只编译一个 main 包时产物写到项目根,污染工作区 |
| 删 `persistence/zz_vt_test.go` | 恒真的调试探针 |

## 轮次 57-60:第七轮(双模式一致性 / 探针补齐 / 终审复审,迭代 22-25)

> 4 个子代理并行(迭代 22 / 23 / 24 / 25)。缺陷号 #197-#211,见
> `docs/OPTIMIZATION.md` 第十节。

### 迭代 22:双模式一致性(顶层键契约下沉应用层)

| 文件 | 锁住的行为 |
| --- | --- |
| `api/dual_mode_test.go`(7 例,新增) | `TestSaveAndValidateAgreeOnUnknownTopLevelKey`(**保存与校验给出同一个判定** —— 否则"校验通过"却保存不了)、`TestServerAndDesktopAgreeOnUnknownTopLevelKey`(**两种模式对同一份非法载荷都报错,且给出同一个 code**)、`TestSaveResponseCarriesValidationWarnings` / `TestSaveResponseWithoutWarningsHasNoField`(**无警告时该字段不出现**)、`TestRunResponseCarriesValidationWarnings`、`TestImportResponseMatchesAppLayerIgnoredKeys`(**导入的 `ignored_keys` 与应用层一致**)、`TestParseWorkflowPayloadIsSharedByBothModes` |
| `app/application/dual_mode_test.go`(12 例,新增) | `TestSavePayloadRejectsUnknownTopLevelKey`(**桌面模式的载荷级入口也拒绝未知顶层键** —— 此前只有服务器模式拒绝)、`TestSavePayloadAcceptsFrontendPayload`(**前端设计器的 payload 必须能被接受** —— 严格化不能打破保存路径)、`TestSavePayloadAcceptsEveryKnownTopLevelKey`、`TestParseWorkflowPayloadRejectsInvalidJSON`、`TestSavePayloadReturnsValidationWarnings`、`TestImportYAMLReturnsIgnoredKeys` / `TestImportYAMLWithoutIgnoredKeys`、`TestRunReturnsValidationWarnings`、`TestWorkflowResultJSONKeepsFlatShape` / `TestWorkflowResultJSONRoundTrip` / `TestExecutionResultJSONRoundTrip`(**响应形状往返不变** —— 附加字段不能把既有 API 契约改掉)、`TestAllowedWorkflowTopLevelKeysDerivedFromModel`(**允许键集合由模型反射派生** —— 模型加字段自动生效) |
| `frontend/src/lib/api.test.ts`(本轮 +475 行) | 新增三组**双分支契约**用例:"保存(顶层键严格化)"、"校验警告(warnings)"、"导入的 ignored_keys" —— 同一份断言在**浏览器分支与桌面分支各跑一遍**,两种模式的行为必须一致(约定 10) |

### 迭代 24:探针补齐

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/compiler/conditionenv_contract_test.go`(4 例,新增) | `TestConditionEnvContractMatchesRuntimeBackings`(**契约表声明的取值来源运行期必须提供**,反之亦然)、`TestConditionEnvRuntimeBackingsAreReal`(运行期事实表里的每一项必须在源码里**真的被写入** —— 防事实表自己腐烂成恒真假保护)、`TestConditionEnvNoUndeclaredRuntimeInjection`(**运行期不得注入契约未声明的变量**)、`TestConditionEnvProbeScansEnough`(**防空转**:探针扫到的内容太少即失败) |
| `frontend/src/test/list-guard.test.ts`(本轮 +322 行,第 3 条用例) | "列表接口结果被赋值给变量后,使用前必须过 `asArray`" —— 补上"先赋值再 `.map`"的盲区;按**语义成员**(`map`/`forEach`/`filter`/`length`/…)判定"把值当列表用";含防空转断言(绑定数下限) |

## "改坏实现 → 测试变红":实测记录

> 回归史里最有价值的一类信息不是"测了什么",而是**"某条测试在什么改动下
> 会变红"**。恒真的测试比没有测试更危险(虚假安全感)—— 轮次 31 已清过一批,
> 轮次 47 又清出一批。
>
> **本表的读法**:每行给出的是**该测试所断言的不变量的反面** ——"把实现
> 改成什么样,这条断言就不成立"。这正是"改坏实现 → 变红"的等价表述:
> 断言不成立 = 测试变红。标注 **【实测】** 的条目是修复当时真的做过
> "回退修复 → 跑测试 → 确认变红 → 还原 → 确认变绿"的闭环验证
> (见各轮 commit message 与 `docs/OPTIMIZATION.md` 的"实测有效性"记述);
> 未标注的条目是**从断言反推出来的可验证方向**,可直接照做一次回退实验。
>
> **做法**:把修复回退(或按缺陷描述改坏实现)→ 跑该测试 → 确认变红 →
> 还原修复 → 确认变绿。全程不提交中间状态。

### 第三轮(实测过的几条)

| 测试 | 改坏什么会变红 |
| --- | --- |
| `TestDSLPackageDeclaresNoConfigStructs` | 在 `workflow/dsl` 里重新加一个节点配置结构体 → 立刻红,并指出"该去哪里改" |
| `TestEnabledConclusionMatchesValidator` | 让校验期放行字符串 `"false"`、或让运行期按字符串解析 → 两侧结论不一致 → 红 |
| `TestExecMetaColumnsMatchPersistenceUpsert` | 持久层加一列而运行期不写 → 列集合不等 → 红 |
| `TestSkillStatusNormalized` | 运行期恢复"精确匹配大小写"→ `"wait_user"` / `" wait_user "` 落空 → 红 |
| `TestIllegalPermissionFailsExecutionWithReason` | 把运行期改回"全拒 + 继续跑" → 失败原因不再是"权限覆盖非法" → 红 |
| `TestValidateLevelMatchesSetLevelChecked` | 让 `SetLevelChecked` 对某个非法值不报错(而 `ValidateLevel` 报错)→ 红 |
| `TestInvalidLogLevelNotPersisted` | 把落库挪到校验之前 → 非法级别落库 → 红 |
| Skills in-flight 锁用例 | 去掉锁 → 连点发出两次请求 → 红 |
| GitPanel 独立成败用例 | 把三查询改回 `Promise.all` → status 失败时 Diff 也没了 → 红 |
| Settings script 确认用例 | 去掉 `window.confirm` → 开关直接打开 → 红 |

### 第四轮(实测过的几条)

| 测试 | 改坏什么会变红 |
| --- | --- |
| `TestMaskStateDataKeepsStructuredKeys` | 把 `decision` / `node:<id>:status` 也纳入脱敏 → 结构化键被改写 → 红(这条正是"无差别脱敏"方案的拦路石) |
| `TestMaskStateDataMasksFreeTextKeys` | 让 `output` / `_error` 免脱敏 → 红 |
| `TestMaskStateDataKeepsOutcomeJSONValid` | 对 `node:<id>:result` 直接做正则替换 → 产出非法 JSON → 红 |
| `TestTerminalCommitConflictDoesNotEmitCompletedEvent` | 恢复"冲突也照发终态事件"→ 事件流说完成、库里还是运行中 → 红 |
| `TestCancelCommitConflictIsExpectedRace` | 把取消路径的冲突也记 ERROR → 噪音升级 → 红 |
| `TestParseRejectsMisplacedPermissions` | 去掉 `permissions` 错层级的报错 → 权限收紧静默失效 → 红 |
| `TestParseTopLevelStrictnessSameForYAMLAndJSON` | 只给 YAML 路径加顶层检查、JSON 路径不查 → 两条路径不一致 → 红 |
| `TestParseAcceptsRepoExamples` | 让键检查变严到阻断导入 → 仓库自己的示例导不进来 → 红 |
| `TestRoundTripExportImportHasNoIgnoredKeys` | 往导出文本里写进被忽略的键 → 往返后凭空多出一批 WARN → 红 |
| `TestWorkflowUnmarshalAcceptsDSLPermissionsSection` | 去掉 `permissions` 别名 → 权限段被静默丢弃 → 红 |
| `TestWorkflowMarshalKeepsLegacyKey` | 把输出侧也改成 `permissions` → 历史快照与既有 API 响应全变 → 红 |
| `TestRunTestSkillRejectsUnrecognizedBoolArg` | 让 truthy 对不认识的字符串返回 false → 拼错的 `fail: "treu"` 静默失效 → 红 |
| `TestRenderNilValueIsResolvableButNotMissing` | 把口径改回"值为 nil 即缺失" → 未产出槽位全刷告警 → 红 |
| `agent/output_depth_test.go` | 恢复原先**逻辑上恒假**的断言 → 改坏实现仍然绿(所以本轮直接换掉了) |
| `adk_contract_test.go` 三处 | 恢复成只 `t.Log` → ADK 契约回归不会让 CI 变红 → 绿(所以本轮改成真断言) |

> **一处"测试反过来钉住了错误口径"的实例**(值得单列):
> 第三轮把"契约外的 Skill 状态按成功处理(不猜)"当成**刻意口径**写进了
> 用例(`TestSkillStatusUnknownIsSuccess`),并在注释里写明"本用例把该口径
> 钉住,避免以后有人把未知状态改成 FAILED 造成行为漂移"。第五轮发现这个
> 口径本身违反约定 14("不猜某个具体状态值"被误写成"猜它是成功"),
> 遂**改写**该用例为 `TestSkillStatusUnknownFailsNode`。
>
> 教训:把当前行为写进测试**不等于**该行为正确 —— 钉住口径之前要先问
> "这个口径对吗"。否则测试会忠实地保护一个错误决定。

### 第五轮(实测过的几条)

| 测试 | 改坏什么会变红 |
| --- | --- |
| `TestTemplateRenderFailureFailsNode` | 让渲染层恢复"吞掉 error 并回退原始模板" → Agent 带着 `{{revwie}}` 原文去干活并成功 → 红 |
| `TestAgentNotCalledAfterRenderFailure` | 渲染失败后仍调用 Agent → 调用计数不为 0 → 红 |
| `TestSkillStatusUnknownFailsNode` | 让运行期把契约外状态当成功 → 红(**这条是改写第三轮那条用例得来的**,见上面的"钉住错误口径"实例) |
| `TestSkillStatusEmptyFailsNode` | 让空状态按成功处理 → 红 |
| `TestAgentStatusUnknownFails` / `...EmptyFails` | 让契约外状态 / 空状态按成功处理 → 红 |
| `TestSkillArgsWrongTypeFails` | 恢复 `args, _ := ...(map[string]any)` 的静默丢弃 → 红 |
| `TestAgentPromptObjectWithoutTemplateFails` | 恢复"prompt 变空串" → 红 |
| `TestSubworkflowInputWrongTypeFails` | 恢复"input 变 nil" → 红 |
| `TestUnknownNodeConfigKeyIsReportedAsWarning` | 去掉未知配置键的告警 → 拼错的键再次无声 → 红 |
| `TestUnknownNodeConfigKeyDoesNotBlockSave` | 把告警升级成错误 → 存量工作流打不开 → 红 |
| `TestDeprecatedConfigKeyIsNotReportedAsUnknown` | 让废弃键同时报"未知" → 用户看到两条互相矛盾的提示 → 红 |
| `TestPostWorkflowRejectsUnknownTopLevelKey` | 去掉 `UNKNOWN_FIELD` 检查 → 多余键再次静默丢弃 → 红 |
| `TestFrontendSavePayloadsAreAccepted` | 严格化时误伤前端 payload → 门禁里的**保存路径**直接断 → 红 |
| `TestImportResponseCarriesIgnoredKeys` | 去掉响应里的 `ignored_keys` → 用户再次看不到"N 个键被忽略" → 红 |
| `TestImportResponseWithoutIgnoredKeysHasNoField` | 无条件带上该字段 → 前端把"没有 ignored_keys"误当成"没解析到" → 红 |
| `TestValidLogLevelAppliedAndPersisted` | 让设置层不落库 / 不改内存级别 → 红(**注**:该用例只断言 app 层字段与库里的值,`logx` 的**实际级别**没有被验证 —— 见"门禁盲区") |

### 第六轮与第七轮(见下节)

第六轮之后的实测记录不在本表里,而是集中放在**"终审实测记录"**专节:
那里的 15 组变异实验是**在隔离副本上真的注入变异、跑测试、记录红/绿**得到的,
比本表"从断言反推可验证方向"更硬;另有 5 组门禁探针、5 组修复后复测,
以及端到端的正面证据("该失败却成功"5/5、脱敏链路、三层脱敏幂等)。

### 迭代 21 补做的两条实测

| 测试 | 改坏什么会变红 |
| --- | --- |
| `frontend/src/pages/Workflows.test.tsx` 的"被忽略的键必须可见" **【实测】** | 删掉 `toast.warning` 那一段 → 提示不再出现 → 红(实测报 `expected '' to contain '2 个键被忽略'`) |
| `frontend/src/lib/api.test.ts` 的 `CODE_HINTS` 参数化用例 **【实测】** | 从 `CODE_HINTS` 删掉任一新码 → 回落成"HTTP 400: 请求参数不合法" → 红(实测报 `Expected: "未知顶层键" / Received: "HTTP 400: 请求参数不合法"`) |

## 终审实测记录(第七轮 · 迭代 23/25)

> 这是回归史里**最有价值的一节**:不是"测了什么",而是**"把实现改坏以后,
> 哪些测试真的会红"**。前六轮的登记项里有相当一部分是**看代码看出来的**,
> 这一节把它们换成**可复现的实验**。
>
> **方法**:在仓库的一份**隔离副本**(`/tmp`,不碰工作区)上逐条注入变异 →
> 跑目标测试 → 记录"变红 / 没变红" → 还原 → 复跑确认变绿。
> 每条变异只改一处,还原后必须重新变绿(证明变异是唯一变量)。
>
> **结果:15 组变异,13 组变红、2 组没变红**。没变红的两组就是真实的
> 测试盲区(已登记为 #207 / #210,并在本轮修掉其中 #207)。

### 15 组"改坏实现 → 测试变红"

| # | 注入的变异 | 目标测试 | 结果 |
| --- | --- | --- | --- |
| 1 | `logx.SetLevelChecked` 不再真正设置级别(只解析不 store) | `TestLogLevelEffectiveLevelFollowsEveryChoice` | ✅ 红 |
| 2 | app 层 `SetPermission` 去掉 `permMu` 串行化 | `TestSetPermissionConcurrentNoLostUpdate` | ✅ 红 |
| 3 | `state_mask` 把 `output` 当结构化键(免脱敏) | `TestMaskStateDataMasksFreeTextKeys` | ✅ 红 |
| 4 | `state_mask` 不再把 `node:<id>:status` 当结构化键 | `TestMaskStateDataKeepsStructuredKeys` | ✅ 红 |
| 5 | 运行期 `enabled` 非布尔改为 fail open(照常执行副作用) | `TestNodeEnabledInvalidValueFailsNode` | ✅ 红 |
| 6 | 契约外 Skill 状态改为按成功处理 | `TestSkillStatusUnknownFailsNode` | ✅ 红 |
| 7 | 配置契约表多声明一个运行期不读的键 | `TestNodeConfigSpecMatchesRuntimeReads` | ✅ 红 |
| 8 | 运行期直接写 map 引入未登记的状态键 | `TestStateWritesAreClassified` | ✅ 红 |
| 9 | 条件变量契约表改错取值来源(与运行期分叉) | `TestConditionEnvContractMatchesRuntimeBackings` | ✅ 红 |
| 10 | 应用层不再拒绝未知顶层键(双模式同源失效) | `TestSavePayloadRejectsUnknownTopLevelKey` | ✅ 红 |
| 11 | 导入结果不再回报被忽略的键 | `TestImportYAMLReturnsIgnoredKeys` | ✅ 红 |
| 12 | 保存结果不再回报校验警告 | `TestSavePayloadReturnsValidationWarnings` | ✅ 红 |
| 13 | 前端:桌面分支 `saveWorkflow` 改走模型级 `Save` | `frontend/src/lib/api.test.ts` 的双分支契约用例 | ✅ 红(实测报"桌面分支与浏览器分支对同一份非法 payload 都报错,且给出同一个 code") |
| 14 | `template.Engine.RenderErr` 吞掉 panic 错误 / 整个删掉 `recover()` | `template` + `workflow/runtime` **全包** | ❌ **没变红** → 登记为 #207,本轮已修(加测试接缝 `lookupFn`) |
| 15 | 前端:列表接口调用点丢掉 `asArray` 防御 | `frontend/src/test/list-guard.test.ts` | ❌ **没变红** → 登记为 #210,仍然开放 |

**第 15 条的边界实测**(值得单列,因为它划出了守卫的真实能力):

| 变异 | 守卫结果 |
| --- | --- |
| `Dashboard.tsx` 的**两个列表调用点**去掉 `asArray`,但 `api.stats()` 上的 `asArray` 保留 | **三条用例全绿** —— 守卫第 2 条只要求"文件里**出现过** `asArray` 字样",非列表接口上的那次也算数 |
| 同一文件里**所有** `asArray` 都去掉 | ❌ 红(命中"文件里没出现 `asArray`") |

结论:守卫能挡住"整个文件都不防御",**挡不住"文件里少了一处防御"**。

### 5 组门禁探针(门禁自己可不可信)

| 探针 | 旧门禁(8 步) | 新门禁(9 步) |
| --- | --- | --- |
| **干净环境里 `npm run lint` 找不到 eslint** | ❌ `exit 127` → `&&` 链短路 → **前端构建与 153 条用例从未被执行**(登记为 #202) | ✅ eslint 已声明;三个子检查**各自独立**跑完再统一判定(lint 失败时 tsc/vitest 仍执行) |
| **含语法错误的 `//go:build windows` 文件** | ❌ 全绿(实测):`gofmt -l` 对它的输出是 **stdout 空 / stderr 解析错 / 退出码 2**,门禁 `2>/dev/null \|\| true` 把三者一起吞掉 → "解析不了"被当成"已格式化";而 `vet`/`build`/`test` 在 macOS 上**不编译** Windows 文件(登记为 #203) | ✅ 第 1 步分离 stdout/stderr 并检查退出码;新增第 4 步 `GOOS=windows go build` 交叉编译 |
| **一个只含 `t.Skip` 的包** | ❌ `go test` 输出 `ok skipcase 0.006s` → 门禁第 5/6 步的判据(`no tests to run`)**不命中** → 判为"有真实用例"放行(登记为 #209) | ❌ **仍然放行** —— 本轮未修 |
| **冒烟端口被占用** | ⚠️ 只报端口号,不报占用者(登记为 #211) | ✅ 打印占用者(`lsof` → `ss` → `netstat`,都拿不到时明说"未能定位") |
| **`asArray` 守卫的第 2 条判定粒度** | ⚠️ 文件级(登记为 #210) | ❌ 仍然放行 —— 本轮未修 |

### 修复后复测(本轮修掉的三条盲区)

> 上表第 14 行(以及"门禁探针"里的两条)在登记后当轮修复。为确认"新加的测试
> 真的能拦住对应的改坏",在**同一套隔离副本方法**下重跑了一遍:

| 登记项 | 注入的变异 | 目标测试 | 结果 |
| --- | --- | --- | --- |
| #204(顶层键名判定缺失) | `maskStateValue` 退回"只按值形态脱敏" | `TestMaskStateDataMasksSensitiveKeyNames` | ✅ 红 |
| #206(契约守卫恒真) | 归属检查改回"只记日志不报错" | `TestRuntimeConfigReadsAllAttributed` | ✅ 红 |
| #206(契约守卫恒真) | 运行期新增一个**未归属**的读配置函数 | `TestRuntimeConfigReadsAllAttributed` | ✅ 红 |
| #207(模板错误出口零覆盖) | `RenderErr` 吞掉 panic 错误 | `TestRenderErrConvertsRenderPanicToError` | ✅ 红 |
| #207(模板错误出口零覆盖) | `RenderErr` 整个删掉 `recover()` | `TestRenderErrConvertsRenderPanicToError` | ✅ 红 |

### 端到端实测结论(真实 server,正面证据)

> 这一组是**回归史里同样重要**的另一半:证明门禁与人工复审**确实有效**,
> 而不只是"没抓到东西"。

**"该失败却成功"专项 5/5 全部正确**(真实 server,HTTP 直连):

| 场景 | 期望 | 实测 |
| --- | --- | --- |
| 条件出口全不匹配 | 落 FAILED 并给出可读原因 | ✅ `state=FAILED`,`error=决策节点 review 没有匹配的出口分支(decision="REJECTED"),已尝试条件: decision == "NEVER_MATCHES_XYZ";请补一条无条件默认分支,或修正条件表达式` |
| 非布尔 `enabled` | 拒绝 | ✅ HTTP 400 `INVALID_NODE_ENABLED`:"节点 \"a\" 的 enabled 必须是布尔值" |
| agent 节点缺 `mode` | 拒绝 | ✅ HTTP 400 `MISSING_MODE`:"agent 节点 \"a\" 缺少 mode 配置" |
| 悬空边 | 拒绝 | ✅ HTTP 400 `INVALID_EDGE` + `NO_EXIT_PATH`:"第 1 条边的 to 节点 \"nope\" 不存在" |
| 顶层未知键 | 拒绝 | ✅ HTTP 400 `UNKNOWN_FIELD`:"未知的顶层字段 bogus_key(允许: …)" |

**脱敏链路端到端正确**(skill 节点输出 `ANTHROPIC_API_KEY=sk-e2etest123456`):

- SQLite 原始 `state_json` 里凭据是 `***`:`"output":"cli stderr: ANTHROPIC_API_KEY=***"`;
  节点明细表 `execution_nodes.output` 同样已脱敏;
- **结构化键原样**:`node:note:status` / `node:note:attempt` / `status` / `decision` 均未被改写;
- `node:<id>:result` **仍是合法 JSON**(可被 `json.Unmarshal` 解析)——
  恢复路径靠它还原节点结果,非法 JSON 会被当成"未答复"→ 静默重跑节点。

**三层脱敏幂等**(同一份状态连续脱敏 1 / 2 / 3 次,逐字节相同):

```
第 1 次: {"_error":"boom ANTHROPIC_API_KEY=***","decision":"APPROVED","node:work:result":"{\"Output\":\"ANTHROPIC_API_KEY=***\",\"State\":\"SUCCESS\"}","node:work:status":"success","output":"cli stderr: ANTHROPIC_API_KEY=***","status":"success","variables":{"api_key":"***"}}
第 2 次: (与第 1 次逐字节相同)
第 3 次: (与第 1 次逐字节相同)
```

## 门禁盲区(当前状态)

| 盲区 | 现状 | 后果 |
| --- | --- | --- |
| **门禁看不见"所有用例都被 `t.Skip()` 跳过"** | 门禁第 6 步按 `go test` 的结果行判"有没有真实用例",判据是 `no tests to run`;而**全部 skip** 时 `go test` 仍报 `ok <pkg> 0.006s` | **实测**:一个只含 `t.Skip` 的包,门禁判为"有真实用例"放行。`agent/claude/real_smoke_test.go` 在无 CLI 的环境里正是全 skip。**仍然开放**(见 `docs/OPTIMIZATION.md` 第十一节第 19 条 / #209) |
| **`asArray` 守卫是文件级判定,单点绕过** | 守卫第 2 条只要求"调用了列表接口的文件里**出现过** `asArray` 字样" | **实测**:同一文件里某一个列表调用点丢掉 `asArray`、别处(哪怕在非列表接口上)还有 `asArray` → 守卫全绿。**仍然开放**(见第十一节第 17 条 / #210) |
| **`asArray` 守卫是源码扫描,不是类型系统** | 即使判定粒度修到"每个调用点",它仍然是**文本扫描** | 换一种写法(`for (const x of list)`、`list.length`)仍可能绕过。**仍然开放** |
| **并发探针依赖 `time.Sleep` 建立读写重叠窗口** | 5 个并发测试靠 `time.Sleep(100ms)` 让读写重叠 | 调度不巧时重叠窗口为零,`-race` 什么也报不出。**实测**:受控复现里 `-race` 对 20 / 200 / 3000 次迭代的**报告数完全相同** → 问题不是"迭代次数太少",而是**重叠不确定**。**仍然开放**(见第十一节第 18 条 / #208) |
| **`output` 键在 resume 与实时路径的口径差** | 实时路径用原文求值,resume 路径用**已脱敏**的 `state_json` 求值 | **实测可达的分支改变**:人批准 A 分支,恢复后走 B 分支。**仍然开放**(见第十一节第 11 条 / #205) |
| **SSE 订阅释放无端到端断言** | 单测覆盖了写失败即关流 | 冒烟脚本没有"断开后订阅数归零"的断言,端到端层面仍可能回归 |
| **列表接口的 `limit` 上限 1000 无"已截断"标记** | 超过 1000 条被静默截断 | 响应里没有任何提示,真正需要大数据量时得引入分页契约 |

### 已关闭的盲区(留档)

| 曾经的盲区 | 关闭方式 |
| --- | --- |
| **gofmt 漏掉平台专属文件** | 第六轮:清单由 `go list` 改为 `git ls-files`(+ 新增未跟踪文件);第七轮:补"gofmt 解析不了 ≠ 已格式化"与 `GOOS=windows go build` 交叉编译(#192 / #203) |
| **`state_mask` 分类守卫覆盖面不足** | 第六轮:扫 compiler+runtime 全部非测试文件,按字段名识别直接 map 写与 `delete`,加防空转下限断言(#189) |
| **日志级别"即时生效"缺真正的断言** | 第六轮:补 `logx` 实际生效级别断言(`Handler.Enabled`)(#191) |
| **顶层键严格化只在服务器模式生效** | 第七轮:契约下沉到应用层,两模式走同一段代码(#197) |
| **契约探针只覆盖"配置键"维度** | 第七轮:补 `conditionenv_contract_test.go` AST 探针(#200) |
| **`asArray` 守卫漏"先赋值再 `.map`"** | 第七轮:补跨语句的赋值-使用形态(#201) |
| **门禁第 6 步在干净环境必红** | 第七轮:声明 eslint + 三个前端子检查各自独立(#202) |
| **`MaskStateData` 顶层字符串值不做键名判定** | 第七轮:脱敏改为"键名 + 值形态"两级判定(#204) |
| **`nodeconfig_contract_test.go` 的恒真路径** | 第七轮:归属检查改真断言 + 防空转断言(#206) |
| **`template.Engine.RenderErr` 的错误出口零覆盖** | 第七轮:加测试接缝 `lookupFn`,让 panic→error 出口可测(#207) |


## 汇总

| 轮次 | 类型 | 结果 | 修复的 Bug |
| --- | --- | --- | --- |
| 57-60 | **第七轮(4 个子代理)**:迭代 22 双模式一致性 / 迭代 24 探针补齐 / 迭代 23 终审复审 / 迭代 25 门禁与冒烟加固 | ✅ | #197-#211 共 **15 条**:当轮修复 11 条(#197 顶层键契约下沉 / #198 校验告警 / #199 `ignored_keys` / #200 条件变量 AST 探针 / #201 `asArray` 守卫补形态 / #202 eslint 未声明 / #203 gofmt 吞语法错误 + Windows 交叉编译 / #204 顶层键名判定 / #206 契约守卫恒真 / #207 模板错误出口零覆盖 / #211 冒烟打印端口占用者),**4 条仍然开放**(#205 `output` 键 resume/实时口径差、#208 并发探针重叠窗口、#209 全部 skip 看不见、#210 `asArray` 文件级判定);门禁由 **8 步扩为 9 步**;新增 `api/dual_mode_test.go` / `app/application/dual_mode_test.go` / `workflow/compiler/conditionenv_contract_test.go` 三个测试文件与 `frontend/src/lib/api.test.ts` 的三组双分支契约用例;新增专节"终审实测记录"(**15 组变异实验 + 5 组门禁探针 + 5 组修复后复测 + 端到端正面证据**);Go 测试文件 90→**94**、前端 135→**153** 用例 |
| 53-56 | **第六轮(3 个子代理)**:守卫扩面 / 假测试修正 / 门禁盲区 / 文档补第三至五轮 | ✅ | #189-#196 共 **8 条**(#189 `state_mask` 守卫扩面 / #190 统一状态写入口 `setExecState` / #191 logx 实际级别断言 / #192 gofmt 清单改 git / #193 用例存在性自动推导 / #194 build 产物落临时目录 / #195 insert 路径落脱敏副本 / #196 删零断言探针 `zz_vt_test.go` + `execStateGuardSQL` 注释事实化);`docs/OPTIMIZATION.md` 补齐轮次 41-52(此前断档)并重写开放清单;Go 测试文件 → **91** |
| 41-52 | **三轮并行复审(10 个子代理 + 1 个独立 verifier)**:跨文件收口 / 前端契约同步 / 门禁重写 / `state_json` 脱敏 / CAS 可观测 / 恒真测试清理 / DSL 严格化 / runtime 静默丢弃三处 / API 严格化 / 累积退化复审 | ✅ | #142-#188 共 **47 条**:当轮修复 43 条,转下一轮 4 条(#185 `state_mask` 守卫覆盖面、#186 日志级别无实际断言、#187 `execStateGuardSQL` 作用范围注释、#188 gofmt 平台文件盲区 —— **四条已在第六轮全部关闭**);新增**源码扫描守卫** 4 处(dsl 禁声明配置结构体 / 运行期与持久层列集合一致 / 状态键必须登记分类 / 前端未知顶层键);门禁重写为 **8 步**(补 `go build`、关键包用例存在性、gofmt 按文件清单、冒烟端口预检);Go 测试文件 66→**90**、有测试包 21 个、前端 12→**16** 文件 / 89→**135** 用例;新增专节"改坏实现 → 测试变红"(实测记录 40+ 条)与"门禁盲区" |
| 33-40 | **两轮并行审计(8 个子代理)**:前端 / DSL+模型 / 持久化+事件 / API+应用层 / 运行时 / 编译器+校验器 / 适配层 / 支撑包 | ✅ | #95-#122(第一轮 28 条)+ #123-#141(第二轮 19 条),共 47 条全部修复;新增**探针式测试** 2 处(AST 契约扫描 + 前端源码扫描);Go 有测试包 19→21、测试文件 41→66,前端 4→12 文件 / 46→89 用例;门禁自身修 3 处(排除 build/ 与 node_modules/、补 `go build`、前端构建输出不再被吞) |
| 32 | 可视化/端到端验证(Browser + 真实 .app) | ✅ | Dashboard 桌面模式像素级验证通过(mode: desktop / IPC 绑定 / 5 统计卡 / 3 工作流表);另修裸二进制启动即退出的真实缺陷(缺 bundle identifier → notifications 服务启动失败 → os.Exit);Browser 的 EventSource 冻结属工具限制,已用 API 冒烟覆盖 10 项端到端 |
| 31 | 复审最近三轮:修自己引入的缺陷 + 约定违反 + 恒真测试清理 | ✅ | #86 检查点节流是死代码 #87 Approve 不重映射决策 #88 并行不收敛违反约定 #14 #89 nil 测试恒真 #90 契约测试不断言 #91 死代码半成品 #92 mock runs 无界增长 #93 自造 itoa #94 结构体 Agent panic 兜底(纵深防御) |
| 30 | 补齐剩余 ADK 契约 + pi 同形替身冒烟 | ✅ | ADK 契约 4→9 条(恰好 N 轮/Escalate 即退/提前停止 drain/分支错误回传/Set(nil)/temp: 前缀)+ pi 子进程层冒烟 6 条 |
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
