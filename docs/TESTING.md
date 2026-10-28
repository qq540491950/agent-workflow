# 测试报告 — Agent Workflow Orchestrator

> 共 8 轮测试验证(超过最低 5 轮要求),其中轮次 2-8 为浏览器视觉测试(GUI 黑盒)。
> 每次功能完成与 Bug 修复均有独立 git 提交(见提交号)。
>
> **当前状态(轮次 93,对应 commit `6d76873` + `ce2e34f` + `a0549c4` +
> `f7d7f98` + 本轮工作区改动)**:
> Go 测试文件 **171** 个 / 有测试的包 **22** 个 / 顶层 `func Test*` **909** 个;
> 前端测试文件 **25** 个 / 用例 **302** 条;门禁 **9 步** + **41** 条自检场景
> (迭代 27 新增 3 条:零次循环 / 无匹配分支 / 未被调用的辅助函数 ——
> 见"轮次 87(续)"一节;**第二十八轮(迭代 32)新增 4 条**:恒假分支里的
> 调用点 / 从未被调用的函数字面量 / 无匹配 switch 分支里的调用点 / 链式
> 辅助函数 —— 见"轮次 91"一节;**第二十九轮(迭代 33)再新增 5 条**:
> 包级 var 取走 / 局部别名取走 / 结构体字段 / 方法值取走 / 链式外层被取走
> —— 见"轮次 92"一节;**第三十轮(迭代 34 收官)未动 `scripts/**`**,
> 自检条数不变)。
> **统计口径**(迭代 28 更正,见 #329):`git ls-files '*_test.go'` **加上**
> `git ls-files --others --exclude-standard '*_test.go'`(含**未跟踪**的新测试文件),
> 排除 `build/` 与 `node_modules/`;顶层 `func Test*` = 同口径文件里以
> `^func Test` 开头的顶层函数(`func Example*` 为 0)。**这是快照,不是常量** ——
> 同轮还有别的代理在加测试文件,数字会继续变;引用本数字时请写明基线
> (本轮基线:HEAD `a0549c4` + 迭代 32 在途改动)。
> (第十四轮**已落地** commit `91fe3e2`;第十五轮(迭代 21B)**已落地**
> commit `908ba0b` —— 取消终态语义 / ExtraArgs 值形态 / 门禁第 6 步判据;
> 第十六轮(迭代 17)**已落地** commit `e62f4ff` —— 单源回归锁去恒真 /
> 取消残余窗口的边界,见"轮次 80"一节;第十七轮(迭代 19)**已落地**
> commit `e62f4ff` —— 跨节点上下文链路修复 / env 第四态 / 权限三态 /
> argv 凭据告警,见"轮次 81"一节;第十八轮(迭代 20)**已落地**
> commit `3e4a5b3` —— 设置读失败可观测 / 内置 Agent 注册失败上抛 / Set 白名单化,
> 见"轮次 82"一节;第十九轮(迭代 20 收口)**已落地** —— 空上下文可见(#280)/
> 门禁第 6 步的断言维度(#289),见"轮次 83"一节;**第二十四轮(迭代 28 收口)**
> —— `submit` 产物进上下文链(#321)/ SSE 慢客户端用例改确定性同步点(#322)/
> 权限死入口清理(#323)/ 门禁前置条件与自检计数登记(#324 / #325)/
> 文档口径校正(#326-#329),见"轮次 87"一节;**第二十五轮(迭代 30 收口)**
> —— git 出口脱敏(#330)/ 状态码语义(#331)/ 前端错误文案统一(#332)/
> 错误文本去内部细节(#333)/ panic 兜底(#334)/ 路由级 404 文案(#335),
> 见"轮次 88-89"一节;**第二十六轮(迭代 31)**
> —— 真实适配器路径的"同形替身"场景矩阵(#336-#340),见"迭代 31"专节;
> **第二十八轮(迭代 32 收口)**
> —— 门禁断言扫描的 4 个新盲区(#341),见"轮次 91"一节。)完整清单与逐文件说明见
> "轮次 41-44 / 45-48 / 49-52 / 53-56 / 57-60 / 61-70 / 71-79 / 轮次 80 /
> 轮次 81 / 轮次 82 / 轮次 83"十一节;"某条测试在什么改动下
> 会变红"的实测记录见"改坏实现 → 测试变红"专节、"终审实测记录"专节与
> "第八~十轮实测记录"专节;门禁自身的盲区见"门禁盲区"一节,
> 门禁的**自检**机制见"门禁自检"专节;**形态 × 出口**的脱敏回归锁见
> "可执行覆盖矩阵"专节;**并发路径的唯一事实源**(失败台账)见"失败台账"专节;
> 前端测试的**确定性**保障见"前端测试稳定性"专节;
> **门禁第 6 步为什么不能用文本解析**见"门禁第 6 步:伪造输出绕过"专节;
> **真实 CLI 路径的测试手法**(同形替身)见"同形替身 CLI:本项目独有的
> 测试手法"专节。

### 第三十至三十二轮的新增测试文件清单(逐个说明锁住了什么)

> **基线**:HEAD `a0549c4` + 迭代 32 在途改动。**本清单只覆盖第三十至
> 三十二轮**(轮次 88-89 / 90 / 91)新增或修改的测试文件;此前各轮的
> 清单见各自小节。
>
> **规模变化**:Go 测试文件 163 → **171**(+8);顶层 `func Test*` 869 →
> **904**(+35);前端测试文件 24 → **25**(+1);前端用例 293 → **302**(+9);
> 门禁自检场景 32 → **36**(+4)。

| 轮次 | 文件 | 锁住的行为 |
| --- | --- | --- |
| 88-89 | `git/mask_test.go`(**新增**,6 例) | **#330** git 包五个出口不得返回未脱敏内容:`TestDiffMasksCredentialInFile`(凭据形态 + **低熵**两种形态;键名保留、值 → `***`);`TestDiffKeepsOrdinaryContentByteIdentical`(**可读性回归锁**:拿 `git diff` **原文**逐字节比对 —— 普通代码与路径**逐字不变**);`TestDiffMasksLowEntropyButKeepsStructure`(脱敏**不改变行数**);`TestLogMasksCredentialInCommitMessage`;`TestStatusMasksCredentialInPath`(**前置条件断言**:porcelain 原文必须真的含该路径,否则用例是假通过);`TestCommitOutputMasksCredential` |
| 88-89 | `api/git_mask_test.go`(**新增**,2 例) | **#330** 端到端:`GET /api/git/diff?staged=true` 与 `GET /api/git/log` 的**响应体**不得含凭据明文(走真实 `wrap` + 真实 SQLite + 真实 git 子进程;仓库建在 `t.TempDir()` 里,**不在真实仓库造带密钥的文件**) |
| 88-89 | `api/error_contract_test.go`(**新增**,8 例) | **#331** `TestErrorStatusMappingByKind`(**15 个 kind/code 组合**逐条断言期望状态码;既有 4xx 组合**一条没动**);`TestInternalErrorIsNotReportedAsBadRequest`;**#334** `TestWrapRecoversPanic`(panic → 500 + `{error,code,kind}` + 响应体不含 panic 原文);`TestWrapPanicAfterResponseCommitted`;**#333** `TestBadRequestDoesNotLeakGoInternals`(不得含 `Go struct field` / `Go value of type` / `[]string` / `AgentConfig` / `(实际 ` / `%T`);`TestReadBodyFailureIsHumanMessage`;`TestErrorBodyMasksCredentials`;**#335** `TestUnknownRouteErrorIsUserFacing` |
| 88-89 | `api/server_test.go`(**改**,1 处断言) | **#331** `TestErrorBodyAlwaysHasCodeAndKind` 的 `code < 400 \|\| code >= 500` → `code < 400 \|\| code >= 600`。**理由**:那条断言把"错误必须是 4xx"**钉成了契约** —— 而那正是缺陷本身;精确映射改由 `TestErrorStatusMappingByKind` 逐条断言 |
| 88-89 | `frontend/src/test/toast-guard.test.ts`(**新增**,4 例) | **#332** **静态守卫**:生产代码里的 `toast.error` **只许**出现在 `lib/errors.ts`(白名单为空表);另三条防空转用例(实现存在 / 白名单有效 / 扫描器看得见该模式) |
| 88-89 | `frontend/src/lib/errors.test.ts`(**改**,+2 例) | **#332** `NETWORK_HINTS` 补的两条:`AbortError` → "请求被中断(可能已取消或页面切换)";`timeout of 5000ms exceeded` → "请求超时" |
| 88-89 | `frontend/src/lib/api.test.ts`(**改**,+3 例 / 1 处断言变更) | **#332** 5xx 状态码有中文兜底提示(500/502/503/504 各一条);`httpText` 的非 JSON 错误体被**截断**;**桌面模式 panic 归一**(`code=INTERNAL`、不含 Go 包路径与 panic 原文)。**断言变更**:原"没有 Kind(CODE) 前缀的 IPC 错误"用例断言 `code` 为 `undefined` —— 那是"双模式**形状不同构**"的旧行为,现补 `code=BAD_REQUEST` / `kind=ValidationError`(约定 10) |
| 88-89 | `frontend/src/pages/*.test.tsx`(**改**,5 处断言 + 1 处 `beforeEach`) | **#332** 五条**精确匹配**断言的文案随 scope 变化而变;`Agents.test.tsx` 的 `beforeEach` 增加 `resetErrorDedup()` —— `reportError` 对同一个错误做 5 秒去重(防提示风暴),那是**生产行为**不是缺陷 |
| 90 | `agent/internal/stubcli/stubcli_main.go.txt`(**新增**,替身源码) | 替身是**真子进程**:真 stdout/stderr、真退出码、真信号、真进程组、真管道。**16 种行为形态**由环境变量切换(`STUBCLI_MODE` / `STUBCLI_SHAPE`) |
| 90 | `agent/internal/stubcli/stubcli.go`(**新增**,构建与驱动) | 测试期把替身编译成独立可执行文件(`go build` 到临时目录);`Build` / `Cleanup` / `Env` / `ReadPID` / `ProcessAlive`。**不联网**(`GOPROXY=off` + `GOTOOLCHAIN=local`),在没有 claude / pi 的机器上照样跑 |
| 90 | `agent/internal/stubcli/stubcli_test.go`(**新增**,3 例) | **替身是全部结论的"地面真值"** —— 替身坏了 → 全部结论不可信:替身自身的形态切换、pid 探测、清理都要有用例 |
| 90 | `agent/claude/stub_matrix_test.go`(**新增**,7 例) | **#336-#340** claude 侧 14 场景矩阵:空输出 / 空白 / 非 JSON / 前置垃圾 + 尾部 JSON / 截断 / 非零退出 / 退出码 0 但零输出 / fork 孤儿 / 超时 / 非法字节 / stderr 含凭据 —— 逐条断言**终态 + 决策值 + 错误消息的可读性与凭据脱敏** |
| 90 | `agent/pi/stub_matrix_test.go`(**新增**,6 例) | **#336-#340** pi 侧同族矩阵(自由文本形态合法 → **成功但留痕** `decision_missing`,不判失败) |
| 90 | `agent/claude/main_test.go` / `agent/pi/main_test.go`(**各新增**,1 例) | `TestMain` 里调 `stubcli.Cleanup()` 删除临时目录(每个测试进程只编译一次替身) |
| 90 | `agent/claude/claude_test.go` / `claude_audit_test.go` / `claude_argv_test.go`(**改**,3 处断言) | **#336 / #337** 旧断言("非 JSON 输出 → 成功")**必须跟着改** —— 它们钉的是**缺陷行为**;新断言钉的是"空输出 → FAILED"与"尾部信封被提取" |
| 91 | `scripts/verify.sh` 的 `assertscan`(**改**,+4 条自检场景) | **#341** 见"轮次 91"一节的逐条说明(恒假分支 / 函数字面量 / 无匹配 switch / 链式) |

### 门禁第 7 步的前置条件:只需 `cd frontend && npm install`(#324 —— **第三十轮实测更正**)

> **⚠️ 口径更正(第三十轮 / 迭代 34 收官,轮次 93)**:本节此前写的是
> "**仓库根也要 `npm install`**,否则第 7 步 lint `exit 127`" ——
> **这个前提不成立**。本轮实测:
>
> ① **eslint 的 5 个 devDep 就在 `frontend/package.json` 里**
> (`eslint` / `@eslint/js` / `eslint-plugin-react-hooks` / `globals` /
> `typescript-eslint`)。**追查历史**:这些声明由 commit `bb5cd63`
> (第八轮"门禁硬伤四件套",#215-#217)加入 —— 而 #324 登记时(`ce2e34f`)
> **清单里就已经有了**(`git show ce2e34f:frontend/package.json` 命中)
> → **前提从登记那一刻起就是错的**。
> ② **隔离复现**:把 `frontend/package.json` + `frontend/package-lock.json`
> 单独拷进一个**空**临时目录 → `npm ci` → 装出 **372** 个包,
> `node_modules/.bin/eslint` **存在**、`--version` = **v9.39.5**
> → **只装 `frontend/` 的干净 clone,第 7 步 lint 不会 `exit 127`**。
> ③ **本工作副本里 `frontend/node_modules/.bin/` 确实没有 eslint** ——
> 但那是因为**本机的 `frontend/node_modules` 是 9-18 装的旧版本**
> (目录 mtime `Sep 18 00:57`),而 eslint 是 `10-21 00:44` 才写进
> `frontend/package.json` 的 → `npm run lint` 靠**祖先目录**的
> `node_modules/.bin` 才跑通。**这是本机安装状态陈旧,不是清单缺口**。
>
> **更正后的结论**:从干净 clone 复现门禁,**只需要装前端依赖**:
>
> ```bash
> cd frontend && npm install   # eslint 与插件都在 frontend/package.json 里
> ```
>
> (仓库根的 `package.json` 也声明了一份 eslint —— 那是**重复声明**,
> 不影响正确性;根目录的 `npm install` 仍可执行,只是为了与前端对齐。)
>
> **残留的唯一一条**:`AGENTS.md`(仓库宪法)**没有记载 `npm install` 这条
> 前置条件** —— 该文件不归本代理,建议措辞见交付报告。
> 详见 `docs/OPTIMIZATION.md` 第十四节第 75 条(#324 的关闭口径)。

### 同形替身 CLI:本项目独有的测试手法

> **为什么要有这一节**:前三十轮的验证几乎都用 **mock agent** —— mock 直接
> 返回结构体,**不经过子进程**。真实路径(`claude` / `pi` 适配器调用真实
> CLI)上的子进程管理、超时、输出解析、退出码、stderr、管道与进程组,
> **从未被系统性地走过**。
>
> **"同形替身"是什么意思**:替身**不是 mock** —— 它是**真的子进程**:
> 真的 stdout/stderr、真的退出码、真的信号、真的进程组、真的管道。
> mock 只能覆盖"返回值是什么",替身能覆盖"**进程是怎么跑的**"。
>
> **它能覆盖什么**:CLI 的**输出形态**与**进程行为**。一个二进制按环境变量
> 切换 16 种行为形态,覆盖 14 个场景,claude / pi 两侧**共用同一份替身**
> (输出形态由 `STUBCLI_SHAPE` 切换:claude = JSON 信封 / pi = 正文 + 尾部
> JSON)。
>
> **它不能覆盖什么(必须知道)**:真实 CLI 的真实输出形态**永远无法完全
> 模拟** —— 替身覆盖"形态",覆盖不了"某个具体版本的 CLI 在某个具体环境
> 下才会吐出的东西"(某些版本把 `is_error` 写成字符串、某些版本在 stdout
> 混入 ANSI 转义、某些版本先打印 banner 再输出信封)。真机冒烟
> (`agent/claude/real_smoke_test.go`)仍然需要真机;**pi 适配器至今
> 无真机冒烟**(开放项,自轮次 29 起)。
>
> **一句话口径**:**"替身能过"≠"真实 CLI 能过"**,但**"替身过不了"
> 一定是适配器的真问题**。这个不对称是这套手法的价值所在 ——
> 它给了子进程层一个**确定性的**回归锁,而不需要真机。

#### 替身的组成与场景矩阵

见"迭代 31"一节(替身源码 / 构建驱动 / 替身自身的用例 / 14 场景矩阵 /
"先红后绿"的实测证据 / 本节没有覆盖的三条)。

## 环境

| 项 | 版本 |
| --- | --- |
| Go | 1.26.0 (darwin/amd64) |
| Wails | v3.0.0-beta.20 |
| ADK Go | google.golang.org/adk v1.7.0(实际最新版;需求假设的 2.x 不存在,按需求第 43 条采用真实 API) |
| SQLite | modernc.org/sqlite(纯 Go) |
| Node | v24.13.1 / Vite 8 / React 18 / TS 5 |
| 前端 | Tailwind CSS 4 + shadcn/ui + @xyflow/react (React Flow) |
| 依赖安装 | **仓库根 `npm install`** + **`frontend/` 下 `npm install`**(两处都要,见下) |

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
  (迭代 36 起重试循环抽为 `withNodeRetries` 供 agent/script 共用,事件名统一为
  `node.retry`;历史执行里的 `agent.retry` 行原样保留。)
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

> **编号说明**:本轮的编号从 `#24` **直接跳到 `#26`** —— `#25` 是
> **历史空号**(当年漏登记,原始记录里没有对应条目)。空号**保留、不追认**:
> 追认等于给一条不存在的缺陷编故事,比留个空号更糟。
> `docs/check-numbering.sh` 把这个空号列在 `KNOWN_GAPS` 里,并在输出里
> **显式报出来**(不算"缺号",但也不让它悄悄消失)。
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
> | 维度 | 轮次 33 前 | 轮次 33-40 后 | 轮次 49-52 后 | 轮次 57-60 后 | 轮次 71-77 后 | **当前(轮次 79)** |
> | --- | --- | --- | --- | --- | --- | --- |
> | 有测试的 Go 包 | 19 个 | 21 个(新增 `workflow/model`、根包 `agentworkflow`) | 21 个 | 21 个 | 21 个 | **21 个** |
> | Go 测试文件 | 41 个 | 66 个 | 90 个 | 94 个 | 114 个 | **123 个**(第十五轮再加 3 个 → **126**) |
> | 前端测试文件 | 4 个 | 12 个 | 16 个 | 16 个 | 21 个 | **22 个** |
> | 前端用例 | 46 条 | 89 条 | 135 条 | 153 条 | 204 条 | **246 条** |
>
> 当前值的复算命令(轮次 79 之后实测,commit `91fe3e2` + 第十五轮工作区):
>
> ```bash
> # Go 测试文件:已跟踪 + 新增未跟踪(提交后两者相等,此处给出并集口径)
> { git ls-files '*_test.go'; git ls-files --others --exclude-standard '*_test.go'; } \
>   | grep -v '^build/' | grep -v node_modules | sort -u | wc -l        # 126 个(含第十五轮 3 个新文件)
> git ls-files '*_test.go' | grep -v '^build/' | grep -v node_modules | wc -l  # 123 个(仅已跟踪)
> { git ls-files '*_test.go'; git ls-files --others --exclude-standard '*_test.go'; } \
>   | xargs -n1 dirname | sort -u | wc -l                     # 21 个有测试的包
> git ls-files '*_test.go' | xargs grep -hoE '^func (Test|Example)' | wc -l  # 692 个顶层用例
> cd frontend && find src -name '*.test.ts*' | wc -l          # 22 个前端测试文件
> cd frontend && npx vitest run                               # 246 条用例
> ```
>
> 前端 12 文件 / 89 用例是**第一轮结束时的**数;第二轮(轮次 37-40)另有
> 并发轮次继续加测,第三、四、五、六、七、八~十轮继续加测 —— 完整清单见本文
> "轮次 41-44 / 45-48 / 49-52 / 53-56 / 57-60 / 61-70 / 71-79"七节。

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

## 轮次 61-70:第六至十轮补登(门禁自检 / 凭据回写 / 制品脱敏 / 权限单一源)

> 缺陷号 #212-#244,见 `docs/OPTIMIZATION.md` 第十一节。
> 本轮的特点是**新增的测试大多在"改坏实现 → 变红"的方向上有实测记录**
> (见后面的"第八~十轮实测记录"专节)—— 前几轮登记的缺陷多数是**看代码看出来的**,
> 这一批大多带着**可复现的实验**。

### 轮次 61-63:第六轮补登

| 文件 | 锁住的行为 |
| --- | --- |
| `persistence/exec_state_guard_test.go`(1 例,本轮新增) | `TestExecStateGuardAgreesWithStateMachine`:**持久层守卫与状态机的判定必须逐条一致** —— 两者各写一份时,规则漂移就会出现"持久层拦住了状态机认为合法的跳变"(执行莫名失败)或反之。**实测**:改动其中任一侧的规则,交叉测试变红 |
| `workflow/runtime/state_mask_test.go`(本轮 +819 行) | `TestStateWritesAreClassified`:**防空转**双下限(扫不到任何写入点即失败;扫到的**状态键形态数** < `minScannedStateKeys`(15)即失败)—— 扫描范围或识别逻辑退化后测试不再空转恒过;**登记不许陈旧**(源码里已找不到写入点的登记项即失败) |
| `workflow/runtime/state_mask_test.go`(本轮改写的**行为**测试) | 结构化键的**脱敏行为**测试此前**近乎恒真**:用的值(`"APPROVED"` / `"success"`)不含敏感词 → `logx.Mask` 改不动 → 键在不在结构化集合里都 PASS。改用**会被 `logx.Mask` 改动**的凭据形态值(`key=value`),并加**前置自检**断言 `logx.Mask` 确实会改动它 —— 前提不成立就直接失败 |
| `scripts/verify.sh` 第 6 步(本轮调整) | "每个包都必须有真实用例"的判据细化为三种输出变体分别归类(`zero` / `nofiles` / `missing`),每种给**不同的原因文案** —— 此前"0 个用例"与"没有测试文件"共用一句话,排障时要多读一遍才知道是哪种 |

### 轮次 64-66:第七/八轮补登

| 文件 | 锁住的行为 |
| --- | --- |
| `frontend/src/lib/api.test.ts`(本轮 +475 行) | 三组**双分支契约**用例:"保存(顶层键严格化)"、"校验警告(warnings)"、"导入的 ignored_keys" —— 同一份断言在**浏览器分支与桌面分支各跑一遍**(约定 10) |
| `frontend/src/lib/api.test.ts`(本轮新增的"错误码全集守卫") | 扫描后端 Go 源码的**全部** code 与前端 `CODE_HINTS` 求**双向**差集:后端有前端没有 → 红;前端有后端没有 → 红(该码从未生效)。**修前实测**:缺 50 个、多 1 个(多出来的 `DENIED` 从未生效过) |
| `frontend/src/lib/api.test.ts`(本轮新增的桌面分支用例) | 桌面分支 IPC 错误必须转成 `ApiError`(剥 `Kind(CODE):` 前缀 + 解析 code/kind)—— 与服务器模式**同一形状**;`updateAgentConfig` 桌面分支更新后**回读**掩码后配置(此前返回 `null`,掩码展示失效) |
| `frontend/package.json` + `frontend/eslint.config.mjs`(本轮新增) | eslint **必须被声明** —— 此前 eslint 来自被 gitignore 的仓库根 `node_modules` → 干净 clone 里 `npm run lint` 找不到它(exit 127)→ `&&` 链短路 → **前端构建与全部前端用例从未被执行**。这条由"门禁自检"式的**干净副本实跑**验证(见"门禁自检"专节) |

### 轮次 67-68:第九轮

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/output_route_resume_test.go`(2 例,新增) | `TestResumeKeepsBranchChosenBeforePause`(**恢复时必须走暂停前已选定的分支** —— 人批准 A 分支、恢复后走 B 分支是可达的分支改变)、`TestRouteBranchDecisionIsStructuredState`(选定的分支记进**结构化**状态键 `route:<id>:branch`,值是分支下标 —— 枚举、不含自由文本、不落凭据) |
| `workflow/compiler/lower.go` + `workflow/runtime/engine.go` + `workflow/runtime/state_mask.go`(本轮修改) | **路由依据与展示文本分离**:路由一旦选定出口分支就是本次执行的**既成事实**,记进结构化键,恢复时**回放**而非拿脱敏副本重算。`state_mask.go` 写明**残余边界**(循环上限的首次求值、`switchAgent` 每轮回边选择)与规避建议(用 `decision` / `status`,别用 `output` 里的自由文本) |
| `workflow/runtime/state_mask_test.go`(本轮调整) | 脱敏副本仍要**保持结构化键原样** —— `route:<id>:branch` 被改写 = 恢复时选错分支 |
| `frontend/src/test/list-guard.test.ts`(本轮 +85 行) | 守卫**自身的**盲区回归(合成源码):换行写法(`api` 换行 `.listAgents()`)的调用点必须被识别、去掉防御必须被判违规;回调形参带类型标注(`.then((execs: Execution[] \| null) => …)`)时该绑定必须可见 |

### 轮次 69-70:第十轮

| 文件 | 锁住的行为 |
| --- | --- |
| `agent/config_mask_test.go`(11 例,新增) | `TestMaskedExtraArgs` / `TestMaskedExtraArgsDoesNotMutate`(掩码**不修改**入参)、`TestMergeExtraArgsKeepsStoredValueByName`(**按参数名**回写真值)、`TestMergeExtraArgsNeverCrossesOptionNames`(**A 的凭据绝不进 B 的参数位** —— 按下标对齐会写错参数名,导致凭据泄漏到第三方服务)、`TestMergeExtraArgsReorderKeepsCredential`(参数顺序变了,凭据仍跟着参数名走)、`TestMergeExtraArgsDropsMaskWithoutStoredValue`(无原值的 `***` 丢弃)、`TestMergeExtraArgsIgnoresStoredMask`(已存值本身是 `***` → 视为无真值)、`TestMergeExtraArgsKeepsUserSuppliedValue`(用户新填的值优先)、`TestSanitizeExtraArgsDropsLiteralMask`(下发 CLI 前剔除字面量 `***`)、`TestMaskExtraArgsRoundTrip` |
| `agent/credential_mask_test.go`(6 例,新增) | `TestMaskCredentialValuesMasksCredentialShapes`(凭据**形态**必须被替换)、`TestMaskCredentialValuesKeepsCodeIdentifiers`(**代码标识符必须保留** —— `const token = getToken()` 不能整行变 `***`,否则 diff 的可读性被毁)、`TestMaskCredentialValuesKeepsKeyName` / `TestMaskCredentialValuesKeepsDiffStructure`(**diff 结构不被破坏**)、`TestMaskCredentialValuesIdempotent`(脱敏幂等)、`TestMaskCredentialValuesEmpty` |
| `app/application/masking_test.go`(8 例,新增) | `TestArtifactsReadOutMasksCredentials`(**制品读出口必须脱敏** —— `git diff` 原文经 `/artifacts` 与 `/export` 返回)、`TestExecutionReadOutMasksSecrets` / `TestExecutionStateDataMaskedInDatabase`(**落库侧**也脱敏)、`TestExecutionReadOutDoesNotCorruptRuntimeState`(读出口脱敏**不得**改坏运行期状态)、`TestUpdateConfigExtraArgsMaskRoundTrip` / `...MaskWithoutStoredValue` / `...NoCrossSlotLeak` / `...DropsLegacyLiteralMask`(掩码往返不得毁掉真实凭据) |
| `app/application/permission_tighten_test.go`(4 例,新增) | `TestSaveWorkflowRejectsPermissionLoosening`(**permissions 段试图放宽全局禁用 → 校验期报出** `PERMISSION_OVERRIDE_LOOSENS`)、`TestSaveWorkflowAcceptsPermissionTightening`(收紧合法)、`TestRunningExecutionSeesTightenedPermission`(**全局收紧立即对在跑的执行生效** —— 此前是启动时快照)、`TestRunningExecutionAllowsBeforeTightening`(收紧前正常放行) |
| `permission/scoped_test.go`(8 例,新增) | 权限判定**单一来源**:`TestScopedViewFollowsParentLive`(全局策略一改,作用域视图立刻看到)、`TestScopedViewOverrideCannotLoosen`(覆盖段**永远无法放宽**全局禁用)、`TestScopedViewOverrideTightens`、`TestScopedViewDenyMessageExplainsLoosening`(拒绝原因要说明"试图放宽")、`TestValidateTightenOnlyDetectsLoosening` / `...AllowsTightening`、`TestGlobalApplyCheckedCannotLoosen`、`TestScopedViewSnapshotIsEffective` |
| `app/application/agent_workingdir_test.go`(4 例,新增) | `TestUpdateConfigRejectsNewDefaultWorkingDir`(`default_working_dir` 是**死设置** → 后端**拒绝新值**)、`TestUpdateConfigAcceptsLegacyDefaultWorkingDirRoundTrip`(**已存老值仍可原样回存** —— 不阻断存量工作流)、`TestUpdateConfigAcceptsClearingDefaultWorkingDir`(清空合法)、`TestAdaptersHaveNoWorkingDirLandingPoint`(适配器**没有**工作目录落地点) |
| `app/application/git_workingdir_clear_test.go`(2 例,新增) | `TestSettingsUpdateClearsGitWorkingDir`(**空串 = 清除**,不是"没传")、`TestSettingsUpdateKeepsGitWorkingDir` |
| `app/application/settings_update_test.go`(3 例,新增) | `TestSettingsUpdateInvalidLogLevelLeavesWorkingDirUntouched`(**任一项非法则一项都不改** —— 此前先落库后校验,返回 400 但目录已改)、`TestSettingsUpdateInvalidLogLevelKeepsAllowScriptNodesUnchanged`、`TestSettingsUpdateValidValuesAllApplied` |
| `workflow/validator/looplimit_test.go`(2 例,新增) | `TestInvalidOnLoopLimitRejected`(非法取值报 `INVALID_LOOP_LIMIT` —— 拼错的 `"fl"` 不再静默落回 `wait_user`)、`TestValidOnLoopLimitAccepted` |
| `workflow/compiler/looplimit_test.go`(2 例,新增) | `TestLoopLimitIllegalValueFailsClosed`(运行期也 fail closed)、`TestLoopLimitLegalValuePreserved` |
| `main_gitdir_test.go`(3 例,新增) | `TestApplyGitDirArgPersistsAndShowsInSettings`(`--git-dir` **持久化语义** + 设置页可见)、`TestApplyGitDirArgFallsBackWhenPersistFails`(**错误不再被丢弃**:持久化失败要明确回退并说明原因)、`TestApplyGitDirArgEmptyIsNoop`(空串是**无操作**,不是"清空") |
| `workflow/runtime/masking_test.go`(本轮 +42 行) | `TestGitDiffArtifactMasksCredentialValues`(git diff 制品必须脱敏)、`TestNodeOutputMaskedBeforePersist` / `TestNodeErrorMaskedBeforePersist` / `TestExecutionStateJSONMaskedBeforePersist` / `TestStateSnapshotMaskedBeforePersist`(**落库前**就脱敏)、`TestStateSnapshotKeepsDecisionRouting`(结构化键原样)、`TestMaskedStateDataStillResumesAfterHITL` / `TestMaskedStateDataStillAllowsRetry`(脱敏副本仍能恢复/重试) |
| `frontend/src/lib/errors.ts` + `frontend/src/lib/errors.test.ts`(13 例,新增) | 统一错误出口:`reportError` 把任意错误变成人话(普通 Error 取 message / 字符串原样 / 带码时补码 / 无文案时退回状态码 / 完全认不出来的也给提示 —— **提示路径本身不能崩**)、fetch 的英文网络错误翻成人话、**后端的具体错误不被泛化提示盖掉**、带 scope 时拼成「哪里出的错:什么错」、**同一个错误在去重窗口内只提示一次**(5 秒,防提示风暴)、超过去重窗口后可再次提示、不同错误各提示一次 |
| `frontend/src/test/error-guard.test.ts`(4 例,新增) | 静态守卫:生产代码里**没有静默吞掉失败的空 catch**;白名单里的例外**确实存在**(防空转:扫描器失效时白名单也一起失效);**扫描器本身看得见这些模式**(防空转);此前静默吞错的文件都**接上了可见的错误提示** |
| `frontend/src/lib/task-storage.ts` + `frontend/src/lib/task-storage.test.ts`(6 例,新增) | 任务文本**不往 localStorage 写**(明文任务不落在磁盘上)、**改用 sessionStorage**;超长文本被截断、条数超限淘汰最旧一条(**存储不会无限增长**)、没有记录时返回空串、**升级后清掉旧版本留在 localStorage 里的明文** |
| `frontend/src/lib/api-desktop-stream.test.ts`(1 例,新增) | 桌面分支 SSE:`@wailsio/runtime` 导入失败时被**报出来**(不再只有 unhandled rejection) |
| `frontend/src/pages/Dashboard.test.tsx` / `WorkflowHistory.test.tsx` / `Workflows.test.tsx` / `Settings.test.tsx` / `components/layout.test.tsx`(本轮新增用例) | 前端各页"接口失败必须提示"的正面断言(与静态守卫互补:守卫看**代码形态**,这些看**实际行为**) |

## 轮次 71-80:第十一至十六轮补登(孤儿包盲区 / 凭据形态统一 / 前端测试稳定性 / 门禁判据换框架事件 / 单源回归锁去恒真)

> 缺陷号 #245-#268,见 `docs/OPTIMIZATION.md` 第十二节。
> 这一段五轮的性质与前几轮不同:
> - **第十一轮(轮次 71-73,`2621a28`)** —— 孤儿包盲区 / `submit` 模式权限 /
>   设置省略字段 / 损坏列显式失败。四条均已进主干。
> - **第十二轮(轮次 74-76,`06276de`)** —— **P0 裸凭据形态全链路明文泄漏**
>   (第十一轮终审复审判定 FAIL 的主因)+ **可执行覆盖矩阵** + 前端测试稳定性。
>   本轮产出了本项目**最独特的一种回归锁**(形态 × 出口的矩阵),单列一节。
> - **第十三轮(轮次 77,commit `aa88aee`)** —— 并行分支错误传播竞态 /
>   门禁绕过 N1·N2 / P2·P3 四项。**已落地**:新增 **失败台账** 与 6 个测试文件,
>   逐文件说明见下节。
> - **第十四轮(轮次 78,commit `91fe3e2`)** —— **门禁自检的完备性加固**
>   (不改产品代码):门禁自检 **9 条 → 16 条**,并加**双向断言 + 场景计数守卫 +
>   断言助手防空转**三层防恒真机制;**同一 commit 还收口了 5 条开放项**
>   (#233 SSE marshal / #234 crypto-rand / #256 配置侧 / #208 并发探针 /
>   #210 asArray 守卫),新增 6 个 Go 测试文件。见"轮次 78"一节。
> - **第十五轮(轮次 79,迭代 21B,commit `908ba0b`)** —— 取消终态语义 / ExtraArgs 值形态 /
>   门禁第 6 步改判**框架事件**(伪造输出绕过)。**已进主干** ——
>   见"轮次 79"一节。
> - **第十六轮(轮次 80,迭代 17,commit `e62f4ff`)** —— 单源回归锁**去恒真**(#263)/
>   取消**残余窗口**的边界(#269)。**已进主干** —— 见"轮次 80"一节。

### 轮次 71-73:第十一轮

| 文件 | 锁住的行为 |
| --- | --- |
| `agent/mode_test.go`(1 例,新增) | `TestModeRequiresWrite`:`mode → 是否需要写权限`的**唯一来源**必须同时覆盖 `execute` / `fix` / `submit` —— 此前运行期查 `execute\|fix`、`pi` 适配器查 `execute\|fix\|submit`,两份集合不一致 → `mode: submit` 的节点不受工作流级 `permissions` 段约束 |
| `workflow/runtime/permission_submit_test.go`(2 例,新增) | `TestSubmitModeObeysWorkflowPermissionOverride`(`submit` 模式**受**工作流级 `permissions` 段约束 —— 这是第十一轮修掉的缺口)、`TestSubmitModeAllowedWhenWritePermitted`(允许写时仍放行,防止把权限收紧做成"永远拒绝") |
| `workflow/runtime/permission_loosen_test.go`(4 例,新增) | `TestEngineValidateRejectsLooseningOverride` / `TestStartRejectsLooseningOverride`(`permissions` 段试图**放宽**全局禁用 → 校验期**与**启动期都要报 `PERMISSION_OVERRIDE_LOOSENS`)、`TestEngineValidateAllowsTighteningOverride`(收紧合法)、`TestEngineValidateAllowsNoPermissionSection`(没有 `permissions` 段不误报) |
| `agent/claude/perm_field_test.go`(2 例,新增) | `TestConfigHasNoPermsField`(死字段 `claude.Config.Perms` **必须不存在** —— 挂着 `*permission.Manager` 却从不读它,调用方以为"权限已交给适配器兜底")、`TestWriteToolsAreDisallowedAtCLILevel`(Claude 适配器靠 CLI 层 `--disallowedTools` 硬禁写操作 —— 注释里"为什么不需要 Perms"的那句话要有依据) |
| `api/settings_contract_test.go`(2 例,新增) | `TestSettingsOmittedGitWorkingDirIsNotModified`(**没传该字段** ≠ **传了空串** —— 前者保持原值,后者清除)、`TestSettingsExplicitEmptyGitWorkingDirClears`(空串 = 清除)。这两个形态在 HTTP 层只有一种可见结果(都是"字段没出现"),所以必须在**契约层**分开测 |
| `app/application/agent_extraargs_strip_test.go`(1 例,新增) | `TestUpdateConfigExtraArgsStrippingCredentialLosesIt`:下发 CLI 前剔除字面量 `***` 后,**库里真值不得丢** —— 剔除的是掩码占位符,不是用户填的真值 |
| `persistence/corrupt_json_test.go`(7 例,新增) | 第十一轮的"列分类"口径:**定义列**损坏 → **显式失败**(`TestGetWorkflowFailsOnCorruptPermissionJSON` / `...CorruptNodesJSON` / `TestGetExecutionFailsOnCorruptStateJSON` —— 权限收紧静默失效、跑的是另一个工作流,都是 P0);**数据列**损坏 → **降级并带原因**(`TestListExecutionsWarnsOnCorruptRow` / `TestListRunningExecutionsSkipsCorruptRow`);`TestGetWorkflowToleratesEmptyColumns`(空列 ≠ 损坏)、`TestListWorkflowsFailsOnCorruptRow` |
| `scripts/verify.sh` 第 4 步(本轮调整) | 孤儿包检查:包集合改按 **git 跟踪的 .go 文件**推导,对 `go list` 看不见的目录逐个跑 `GOOS=windows/linux/darwin` 交叉 `vet`。**门禁自检**配套新增两条(类型正确的孤儿包必须通过 / 含类型错误的孤儿包必须被抓) |

### 轮次 74-76:第十二轮(凭据形态唯一来源 / 覆盖矩阵 / 前端测试稳定性)

| 文件 | 锁住的行为 |
| --- | --- |
| `logx/credential_test.go`(**9 例,新增** —— 本轮核心) | 值的形态规则的**唯一来源**(`logx/credential.go`)。`TestMaskCoversBareCredentialShapes` / `TestMaskValueCoversBareCredentialShapes`(裸值形态:键名规则**盖不住**,必须由形态规则兜住)、`TestMaskCredentialValuesKeepsCodeIdentifiers`(**代码标识符不误伤** —— `const token = getToken();` 逐字保留,否则 diff 的审计价值被毁)、`TestMaskKeepsEnglishSentences`(**英文句子不误伤** —— "The bearer of this document is unknown." 不是凭据)、`TestMaskDoesNotMaskPathsAndIdentifiers`(临时目录路径、`TestScriptNodeHonorsTimeout…1637892132` 这类"驼峰+数字后缀"的标识符不抹掉)、`TestMaskIsIdempotent`(脱敏**幂等** —— 1 次与 2 次逐字节相同)、`TestMaskCredentialValuesKeepsKeyName`(键名保留,值被替换)、`TestMaskValueShapeRuleIsSingleSource`(**两个入口逐字节一致** —— 规则的"唯一来源"必须有断言,否则两份实现各自演化就会分叉)、`TestCredentialShapeCoverageMatrix`(**覆盖矩阵**,见下节专节) |
| `agent/credential_mask_test.go`(9 例,本轮 +3) | `agent.MaskCredentialValues` 降为**薄封装**后的行为锁:`TestMaskCredentialValuesCoversBareShapes` / `TestMaskCredentialValuesIdempotentOnBareShapes`(裸值形态与幂等)、`TestMaskCredentialValuesKeepsEnglishSentences`(英文句子不误伤),以及第十轮已有的形态 / 标识符 / 键名 / diff 结构 / 幂等 / 空文本五条 |
| `workflow/runtime/credential_shape_leak_test.go`(2 例,新增) | **落库侧**:`TestBareCredentialValueMaskedBeforePersist`(裸值形态凭据在**进库之前**就被替换 —— 节点 `output` 与 `state_json` 都不含明文)、`TestBareCredentialValueMaskedInFailedNode`(**失败节点**的 `error` 同样要脱敏 —— 凭据常常出现在 stderr 里,失败路径最容易漏) |
| `app/application/credential_shape_outlet_test.go`(1 例,新增) | **API 读出口侧**:`TestBareCredentialMaskedAtAllReadOutlets` —— 同一个凭据经**执行详情 / 节点明细 / 导出**三个出口读出,任一出口含明文即红。这是"全链路"的端到端那一环:落库侧与读出口侧**各过一次** |
| `frontend/src/test/async-determinism-guard.test.ts`(新增) | **类级确定性守卫**(见下节"前端测试稳定性"专节):禁止 `new Promise` + `setTimeout` 当同步点;钉住 vitest 手动 mock 的缓存语义;守卫自身防空转 |
| `frontend/src/lib/api-desktop-stream.test.ts`(本轮改写) | 同步点从"**猜的事件循环节拍数**"换成"**效果本身**" —— 桌面分支 SSE 的导入失败必须被 `toast.error` 报出来 |

### 轮次 77:第十三轮(commit `aa88aee`)—— 失败台账 / 门禁绕过 N1·N2 / P2·P3

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/failure_ledger.go`(**新增,251 行** —— 本轮核心) | **失败台账**:引擎亲眼见到的节点 outcome 的**唯一事实源**。见下节专节"失败台账(并发路径的唯一事实源)" |
| `workflow/runtime/parallel_failure_test.go`(**新增,447 行**) | 并行区的失败传播:① 失败分支的**真因**必须出现在失败原因里,不得被兄弟分支的 `context canceled` 顶掉;② 真因必须**指向真正出问题的节点**;③ 兄弟分支被取消**不得**被记成失败;④ 同一工作流两次运行给出的失败原因**相同**(按节点 ID 排序,与调度顺序无关) |
| `workflow/runtime/panic_test.go`(收紧) | 原本只有 `t.Logf` 的 Skill panic 用例改为**真断言**:失败原因必须说明 panic、**不得含** `context canceled` —— 恒真的断言比没有断言更危险(虚假安全感) |
| `workflow/compiler/loopguard_history_test.go`(**新增,125 行**) | 循环守卫的历史记录:状态类型不对 → **显式失败**(此前 `_ = json.Unmarshal` 被吞 → 历史为空 → 停滞检测失效 → 人工介入的 `responses` 选项随之消失) |
| `agent/config_behavior_test.go`(**新增,143 行**)+ `app/application/behavior_mask_test.go`(**新增,64 行**) | `behavior` 字段(`map[string]any`)的**掩码**与**回写**:GET 必须掩码(自由 JSON 里的凭据形态值不得原样返回);前端原样回传时 `***` 哨兵按**同路径**换回已存值(约定 5),不得把凭据覆盖成哨兵 |
| `app/application/backup_apply_test.go`(**新增,90 行**) | 备份恢复的**逐项**结果:某个 Agent 的 `applyAgentConfig` 失败必须**可见**(此前 `_ =` 丢弃 → 报告"已恢复 N 个"但实际有配置没生效 = 假成功,约定 14) |
| `persistence/corrupt_json_test.go`(扩) | 事件 **data 列**损坏 → **降级 + WARN**(带 execution / event / error 上下文),不再静默变 `{}`;**定义列**损坏仍显式失败 |
| `frontend/src/lib/api.test.ts`(**+20 条**) | 双模式契约的**完备性**:桌面 IPC 分支与服务器 HTTP 分支**双向**比对,把此前没被断言的 `api` 方法纳入覆盖(约定 10) |
| `frontend/src/lib/debounce.test.ts`(**新增,9 例**) | 防抖的**窗口期(200ms)**与**尾沿语义**(假计时器)—— 它是约定 7 的**唯一来源**,零覆盖等于"没实现" |
| `scripts/verify.sh`(第 4 / 5 / 6 步) | 孤儿包枚举改用 `go tool dist list` **全集**(47 个目标)+ freebsd / js 两条新自检;第 5 步改 `-v`、第 6 步统计顶层 PASS/SKIP 并要求看到 `=== RUN` 行 → 全 `t.Skip()` 的包不再能伪装成"有真实用例" |

**本轮验证**:`go test -race -count=20 ./workflow/runtime/...` **0 失败**(此前 3 跑挂 2);
vitest **233/233**(0 → 233);门禁 9 步通过。

### 轮次 78:第十四轮(迭代 18)—— 门禁自检的完备性加固 + 剩余开放项收口

> **当前自检场景总数:32 条**(`scripts/verify.sh` 的 `SELFCHECK_EXPECTED`)。
> 第十四轮(轮次 78)加了 5 条(共 14 条);第十五轮(轮次 79)加 1 条(伪造输出,
> 17 条);第十六~十八轮(轮次 80-82)加了 6 条(路径排除规则 4 条 + 孤儿包候选
> 2 条,23 条);第十九轮(轮次 83)加 4 条(第 6 步"断言扫描"判定,27 条);
> **第二十二轮(轮次 86)加 2 条(第 6 步"断言**强度**"判定,29 条** ——
> 恒不触发的断言必须被抓 + 运行期才成立的正常断言不得误判);
> **第二十七轮(轮次 87 续)加 3 条(第 6 步"**死断言**"判定的三类新形态,32 条** ——
> 零次循环 / 无匹配分支 / 未被调用的辅助函数,后者同时断言绿方向)——
> 逐条见下表与各轮次小节。
>
> 这一轮的**主体**是**门禁自检本身的完备性加固**(门禁脚本的判定逻辑,
> 不改产品代码),
> 起因:第十三轮把门禁的**判据**修好了(#251 / #252),但"判据有没有被自检
> 证明过"是另一回事 —— 门禁里最复杂的几段判定此前**没有任何自检**。
> 登记于 `docs/OPTIMIZATION.md` #259-#262。
>
> **同一个 commit 还收口了 5 条开放项**(此前散落在开放清单里,本轮一并
> 关闭,证据逐条如下):

**改前的自检覆盖盘点**(9 条,全部集中在第 4 步):

| # | 自检场景 | 方向 | 防的是哪个绕过 |
| --- | --- | --- | --- |
| 1 | 交叉检查对照:类型正确的 `//go:build windows` **测试文件** | 绿 | 没有它,下面两条红断言是**恒真**的(vet 无论对错都失败也算"成功") |
| 2 | `//go:build windows` **测试文件**的类型错误 | 红 | `go build` 不编译 `_test.go` → 只有 vet 看得见(第十轮修的盲区) |
| 3 | `//go:build windows` **源文件**的类型错误 | 红 | 第八轮修的源文件盲区(回归确认) |
| 4-5 | 孤儿包(windows):类型正确 / 类型错误 | 绿 / 红 | 孤儿包对 `go list ./...` 完全隐形,只有 `orphan_check` 看得见 |
| 6-7 | 孤儿包(freebsd):类型正确 / 类型错误 | 绿 / 红 | **手写平台枚举**漏掉冷门平台(#251) |
| 8-9 | 孤儿包(js):类型正确 / 类型错误 | 绿 / 红 | 同上,换一个**完全不同**的平台再验一次 |

**改后新增的 5 条**:

| # | 自检场景 | 方向 | 防的是哪个绕过 | 造坏验证 |
| --- | --- | --- | --- | --- |
| 10 | 孤儿包**目录混包**:同一目录两个 package 名 | 红 | 混包错误长得像"该平台没有文件"(`found packages X and Y`)→ 被当成约束排除而跳过 | 隔离模块造 `mixedpkg/{a.go,b.go}` 两个包名 → `orphan_check` 必须红 |
| 11 | **平台枚举规模**:必须来自 `go tool dist list` 全集 | 绿 | freebsd/js 两条场景挡不住"换回一份含 freebsd/js 却漏冷门平台的**手写清单**" | 把枚举换成 3 个 GOOS → 断言红 |
| 12 | 第 6 步判定:**正常包**不得被误报 | 绿 | 判定逻辑整体失效(什么都报 / 什么都不报) | 隔离模块的正常包 → 不得出现在报告里 |
| 13 | 第 6 步判定:**0 用例包**必须被抓 | 红 | 新增一个只有测试文件、没有 `Test` 函数的包 | 隔离模块造 `zero` 包 → 报告里必须出现"0 个真实用例" |
| 14 | 第 6 步判定:**全 `t.Skip()` 包**必须被抓 | 红 | **#252 的绕过场景本身**:全 skip 的包伪装成"有真实用例" | 隔离模块造 `skip` 包 → 报告里必须出现"t.Skip" |

**自检的防恒真机制(#262)** —— 三层:

1. **双向断言**:同一道防线既断言"坏情况必须红"(`selfcheck_red`),也断言
   "好情况必须绿"(`selfcheck_ok`)。只有红方向是**恒真**的(vet 无论对错都失败
   也算"成功");只有绿方向又挡不住"检查被改成永远红"。两个方向合起来,防线被
   neuter(改成恒真或恒假)时**必然**有一侧变红。
2. **场景计数守卫**:每条自检场景都计数,结束时与 `SELFCHECK_EXPECTED` 比对 ——
   **少一条即红**(防"自检被删"),**多一条即红**(防"新增忘了同步期望值")。
3. **助手防空转**:断言助手自己被断言 —— 对必然成功的命令断言"必须失败"必须
   失败,对必然失败的命令断言"必须通过"必须失败。助手被改成恒真 → 门禁立刻红。

**同源原则**:第 6 步的判定抽成函数 `real_usage_report`,门禁与自检走**同一条
代码路径**(与第 4 步 `win_check` 抽函数的理由同源)—— 自检若自己另写一套判定,
将来有人把判据改坏,自检照样绿,盲区就又回来了。

**fail closed**:隔离模块的 `go test` 自身失败(环境不可用)→ 门禁**红**,
不静默跳过;`go tool dist list` 没有输出 → 门禁**红**;读不到 `go.mod` 的
go 指令 → 门禁**红**。

**造坏验证(5 组变异,实测)**:每组都在**隔离副本**里破坏一处"被自检守护的防线",
期望门禁**变红**:

| 变异 | 破坏什么 | 门禁的红字 |
| --- | --- | --- |
| 1 | 第 6 步判定里删掉 `--- SKIP:` 的计数 | `门禁自检失败:[第 6 步判定:全 t.Skip 包必须被抓(#252 的绕过场景本身)]` |
| 2 | 孤儿包检查把 `found packages`(混包)当成"该平台没有文件"跳过 | `门禁自检失败:[孤儿包检查:同一目录里两个 package 名(混包)必须被抓…]` |
| 3 | 目标平台枚举换回手写三平台清单 | `门禁自检失败:[孤儿包检查:只含 //go:build freebsd 文件的孤儿包里的类型错误必须被抓]` |
| 4 | 删掉一条自检场景(混包那条) | `门禁自检失败:实际执行了 15 条自检场景,期望 16 条 —— 有自检被删掉…`(**场景计数守卫**) |
| 5 | 断言助手 `selfcheck_red` 被改成恒真 | `门禁自检失败:断言助手失效 —— 对必然成功的命令断言'必须失败'却通过了(自检恒真)` |

**5/5 全部变红** —— 每一道防线都有"破坏它 → 门禁会红"的实测闭环。
其中变异 3 证明**枚举不能被换回手写清单**(哪怕清单里仍含 freebsd / js),
变异 4 证明**自检被删掉也会红**,变异 5 证明**自检自身恒真也会红**。

#### 轮次 78 收口的 5 条开放项:新增/改动的测试文件

| 文件 | 锁住的行为 |
| --- | --- |
| `api/sse_marshal_test.go`(**新增,126 行**) | **SSE 序列化失败必须可见**(#233):`json.Marshal` 出错的事件**不送达**,但必须留下**一条可追溯的告警**(事件类型 / 执行 ID / 节点 / 原因)。此前是静默 `continue` → 事件流里"少了一条"却没人知道是哪条。断言"失败事件不得被当作正常事件发出"**且**"日志里必须有告警" |
| `app/application/randid_test.go`(**新增,103 行**) | **熵源失败必须让调用方知道**(#234):`TestRandHexPropagatesEntropyFailure`(熵不足 → `randHex` 返回错误,**不**产出全零 ID)、`TestRandHexSucceedsNormally`、`TestSaveWorkflowFailsWhenEntropyUnavailable`(**保存工作流必须失败** —— 此前是拿全零 ID 把既有工作流**覆盖掉**) |
| `workflow/runtime/randid_test.go`(**新增,86 行**) | 同上,运行时侧:`TestNewIDPropagatesEntropyFailure` / `TestNewIDSucceedsNormally` / `TestRunFailsWhenEntropyUnavailable`(启动执行必须失败) |
| `workflow/runtime/hitl_responses_test.go`(**新增,83 行**) | **HITL `responses` 认全两种切片形态**(#256):`TestHumanNodeResponsesAcceptsBothSliceShapes` —— `[]any`(YAML/JSON 解析)与 `[]string`(程序化构造)都要被接受。此前只认 `[]any`,断言失败被 `_` 吞掉 → 用户配置的选项列表**静默丢弃**、回落默认四项 |
| `app/application/application_test.go`(**改**) | **并发探针的读写窗口必须确定**(#208):`TestSkillDisabledConcurrentAccess` / `TestLogLevelConcurrentAccess` 从"写者跑固定次数 + `time.Sleep(150ms)` 建窗口"改成**屏障 + 紧凑循环**(读者先就位 `ready.Wait()`、持续自旋,写者才开始写)→ 读写**必然重叠**,`-race` 报得出。实测:去掉锁后 `app` 从 **0/5 提升到 3-5/5** 命中 |
| `frontend/src/test/list-guard.test.ts`(**改,+358 行 / -28 行**) | **`asArray` 守卫的逐调用点判定**(#210):① "当列表用"加第三种形态 —— **作为实参传给函数**(`api.listX().then((x) => setRows(x))` 里 `x` 后跟 `)` → 此前放行);② 新增 `findUnguardedCallSites`(**逐调用点**判定:调用点要么被 `asArray` 包裹,要么流出一个受保护的绑定);③ 换行写法 `api\n  .listAgents()` 与带类型标注的回调形参 `(execs: Execution[] \| null)` 都必须被识别;④ 防空转断言(绑定数 ≥3、调用点数 ≥10)。**实测**:守卫作用域 **20 个列表调用点、零误报**;隔离副本改坏 `Dashboard.tsx` 两个调用点 → **2 条用例变红**(上一轮同一实验是 4 条全绿) |
| `frontend/src/lib/api.test.ts`(**改,+242 行**) | 双模式契约再补 **11 条写类分支**断言(桌面 IPC ↔ 服务器 HTTP 双向比对),把写操作也纳入覆盖(约定 10) |

**本轮验证**:`go vet` 干净、`go test -race` 21 个包全绿、vitest **246/246**、
门禁 9 步通过(89s)、`docs/check-numbering.sh` 通过。

### 轮次 79:第十五轮(迭代 21B)—— 取消终态语义 / ExtraArgs 值形态 / 门禁第 6 步改判框架事件

> **本轮已进主干**(commit `908ba0b`)。登记于 `docs/OPTIMIZATION.md` #264-#268。
>
> 本轮的三条修复有一个共同的形状:**判据的输入选错了** —— 用了一个
> "更方便但不是裁决源"的信号(进程内句柄 / 参数名 / 文本输出),
> 于是每类判据都有它看不见的一类输入。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/cancel_semantics_test.go`(**新增,7 例**) | **取消语义的三分**(#264):`TestCancelTerminalExecutionIsRejected`(终态执行 → `INVALID_TRANSITION`,**不是** 200 假成功)、`TestCancelTerminalExecutionNeverFakesSuccess`(**40/40** 次紧密轮询命中"终态已写库、句柄未摘除"的窗口,**0/40** 假成功)、`TestCancelRunningExecutionSucceeds`(运行中 → 正常取消)、`TestCancelWaitingUserExecutionActuallyCancels`(等待用户 → CAS 落 `CANCELLED`)、`TestCancelMissingExecutionIsNotFound`(不存在 → `NOT_FOUND`)、`TestCancelStuckRunningExecutionIsCancellable`(库里 `RUNNING` 但**句柄已摘除**的"卡死"执行也必须能被取消)、`TestExecuteAbortsWhenAlreadyCancelled`(**#268**:取消先到、协程后启动 → 协程**启动即核对**库里的状态,已是 `CANCELLED` 就立刻收尾,**不做任何节点执行**) |
| `api/cancel_semantics_test.go`(**新增,1 例**) | **端到端复刻那个窗口**(#264):`TestContract_CancelRightAfterCompletion` —— 紧密轮询执行状态,终态一出现就立刻 `POST /api/executions/{id}/cancel` → 必须 **400 + `INVALID_TRANSITION`**。与 `server_test.go` 的 `TestContract_ExecutionCancel` 的区别:那个用例按 50ms 间隔轮询,**几乎碰不到**那个 ≈210µs 的窗口 |
| `agent/config_mask_shape_test.go`(**新增,11 例**) | **ExtraArgs 的"参数名 ∪ 值形态"双判定**(#265):`TestMaskExtraArgsMasksCredentialShapedValues`(`--header "Authorization: Bearer ghp_xxx"`、`--foo sk-ant-xxx` 必须被掩)、`TestMaskExtraArgsKeepsPlainValues`(普通值**不误伤**)、`TestMaskExtraArgsIsIdempotent`(二次掩码逐字节不变)、`TestMaskExtraArgsKeepsBooleanFlags`(**`--api-key --verbose` 不得把布尔开关当凭据掩掉**)、`TestMergeExtraArgsRestoresShapeMaskedValue` / `TestMergeExtraArgsRestoresInlineShapeMaskedValue`(**按形态被掩的 `***` 必须能回填已存真值** —— 否则库里真值被抹掉,Agent 每次调用都不带鉴权参数)、`TestMergeExtraArgsDoesNotCrossRenamedShapeOption`(回填**按参数名**对齐,绝不跨参数名串值)、`TestMaskExtraArgsRoundTripShapeMasked`(往返)、`TestMaskExtraArgsRoundTripKeepsQuotes`(**往返不得丢引号** —— 引号是参数值的一部分,参数不经 shell)、`TestSanitizeExtraArgsDropsShapeMaskedValue`(下发 CLI 前剔除哨兵)、`TestMergeExtraArgsKeepsPlainValueAfterShapeRule` |
| `workflow/runtime/cas_conflict_test.go`(**改**) | 取消的**并发裁决**:库里状态已被另一个写者落成 `CANCELLED`(终态)时,`Cancel` 必须返回 `INVALID_TRANSITION` —— **不是** 200 假成功;用例要验的是**取消分支的 CAS 冲突处理**,ctx 仍被取消(引擎在终态分支里也会停掉还在跑的协程),冲突照样被计数 |
| `scripts/verify.sh`(第 6 步) | 判据换成 **`go test -json` 的框架事件**(见下节"门禁第 6 步:伪造输出绕过"):达标 ⇔ 存在 `Action=="pass"` 且 `Test` 非空、且 `Test` 不含 `/`(顶层用例)的事件;自检新增第 **17** 条场景("伪造 `=== RUN`/`--- PASS` 输出的包必须被抓"),`SELFCHECK_EXPECTED` **16 → 17** |

**本轮验证**:`go test -count=1 ./agent/... ./api/... ./workflow/runtime/...` 全绿
(`agent` / `api` / `workflow/runtime` 三包均 ok);前端 vitest **246/246**
(本轮未动前端代码)。

### 轮次 80:第十六轮(迭代 17)—— 单源回归锁去恒真 / 取消残余窗口的边界

> 登记于 `docs/OPTIMIZATION.md` #263(已关闭)与 #269(新登记)。
>
> 本轮的两条同属一类:**"看起来有断言,其实没有"**。
>
> - #263:回归锁比的是**同一个函数比较两次** → 恒真 → 永远绿;
> - #269:上一轮报"µs 级"的残余窗口,**实测 ≈0.5ms** —— 宽一个量级,
>   而且成因不是"句柄摘除",而是**两段 DB 往返**("请求到达 → ctx 真被取消"
>   + "最后一次取消检查 → 终态落库",分段测量后各 ≈250µs)。
>
> **方法论**:恒真断言要用**造坏**证明它曾经无效,而不是靠读代码下结论。

| 文件 | 锁住的行为 |
| --- | --- |
| `agent/credential_mask_test.go`(**改,+2 例**) | **跨入口逐字节比对**(#263):`TestMaskCredentialValuesMatchesLogxSingleSource` —— `agent.MaskCredentialValues(x)` 与 `logx.MaskCredentialValues(x)` 对 **23 条**输入(13 形态 + 代码标识符 + 英文句子 + 空串)必须**逐字节相同**。比对必须放在 **agent 侧**:`agent` 依赖 `logx`(单向),**logx 反过来 import agent 会成环**。`TestSingleSourceAssertionIsNotDegenerate` —— **防空转守卫**:两个入口必须是**不同的函数对象**(`reflect.Pointer`)+ 用 **AST** 读本文件确认单源用例**确实同时调用了两个入口**(只调一个的比对必然恒真) |
| `logx/credential_test.go`(**改**) | 同名用例改成比对**两个不同的入口**(`Mask` 键名 ∪ 值形态 vs `MaskCredentialValues` 只看值形态):键名规则**不触发**的输入上两者必须**逐字节相同**;覆盖矩阵再加一条"`Mask` 不得比 `MaskCredentialValues` **少遮**"(值形态规则不同源的信号) |
| `workflow/runtime/cancel_residual_test.go`(**新增,1 例**) | **取消残余窗口的当前语义**(#269):`TestCancelResidualWindowSemantics` —— 用**探针 + 阻塞事件总线**确定性地复刻"读库之后、执行恰好完成":取消返回 **nil(200)**、执行最终 **COMPLETED**、事件流里**只有一条** `workflow.completed`、**没有** `workflow.cancelled`、**没有** `runtime.error`、状态提交失败计数为 0。**造坏验证**:人为关上窗口(探针后再读一次库)→ 该用例**变红** → 证明它不是空转 |
| `workflow/runtime/engine.go`(**改**) | `Cancel` 的注释写清**残余窗口的边界**(什么情况会 200 但执行已完成、为什么可接受、用户看到什么)+ 一个**测试接缝** `Engine.cancelProbe`(默认 nil,生产路径零开销) |

**本轮验证**:`go vet` 干净、`go test -race -count=1` 相关包全绿、
`go test -race -count=20 -run TestCancelResidualWindowSemantics ./workflow/runtime/`
0 失败、造坏四组(隔离副本)全部变红、`docs/check-numbering.sh` 通过
(缺陷号区间 1..269 连续)。

### 轮次 81:第十七轮(迭代 19,commit `e62f4ff`)—— 跨节点上下文链路修复 / env 第四态 / 权限三态 / argv 凭据告警

> 登记于 `docs/OPTIMIZATION.md` 第十三节"#270-#278"(本轮全部已关闭)。
>
> 本轮的核心是**一条此前十六轮全部漏网的 P0**:`{{plan}}` 等**跨节点上下文引用
> 永远是空的** —— 而**仓库自带的示例工作流**(`workflows/examples/coding-task.yaml`)
> 的 review 节点 prompt 里就写着 `Plan: {{plan}}`,也就是说这条示例**从未生效过**,
> 执行却一直报告 SUCCESS(约定 14)。
>
> **本轮新增 Go 测试文件 11 个 / 顶层用例 +52 例**,另改 4 个既有文件
> (`e62f4ff` 口径:测试文件 126 → **138**,顶层 `func Test*` 711 → **766**;
> 其中 **+3** 属第十六轮 —— `agent/credential_mask_test.go` +2 与
> `workflow/runtime/cancel_residual_test.go` +1,见"轮次 80"一节)。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/context_chain_test.go`(**新增,10 例 —— 本轮核心**) | **跨节点上下文链路**(#270):`TestPlanReachesDownstreamNode`(上游产出的 plan **真的**到达下游节点 —— 此前断言的是"写进了一个随即被丢弃的副本",下游拿到的是空串)、`TestPlanFallsBackToSummaryWhenOutputEmpty`(**同链第二个断点**:`output` 键**总是存在**哪怕是空串 → 只产出 summary 的 Agent 写出的 plan 恒为空串;修法是 `pickText` 取第一个**非空**值)、`TestReviewAndIssuesReachDownstream`(`{{review}}` / `{{issues}}` 两槽位)、`TestTestResultReachesDownstream`(`{{test_result}}`)、`TestSkillOutputReachesDownstream`(skill 节点的产物进入 `{{last_output}}`)、`TestShippedExampleWorkflowPropagatesPlan`(**直接跑仓库自带的 `workflows/examples/coding-task.yaml`** → review 节点的提示词含方案正文 —— 这条是"示例从未生效过"的正面证据)、`TestContextChainStateJSONIsMasked`(链键落库**仍走脱敏**)、`TestContextChainDoesNotAffectConditions` / `TestContextChainKeysDoNotAffectConditionEvaluation` / `TestContextChainKeysDoNotCollideWithConditionKeys`(**链键不得影响条件表达式求值** —— 链键不在 `conditionenv` 的变量集内,加了链键不能顺手改掉条件语义) |
| `contextx/context_test.go`(**改,+5 例**) | **上下文链的写侧**(#270):`TestSaveNodeOutputWritesLiveState`(**写的是活的会话状态**,不是一次性副本 —— 这是 P0 的根因)、`TestSaveNodeOutputPropagatesWriteError`(**写失败必须传播** → 节点显式失败,而不是"成功但上下文空")、`TestSaveNodeOutputFallsBackToSummaryWhenOutputEmpty`(取第一个非空值)、`TestSaveNodeOutputSkillWritesLastOutput`(skill 节点 → `{{last_output}}`)、`TestBuildAndSaveShareKeyNames`(**键名同源**:`Build` 与 `SaveNodeOutput` 共用同一组键常量,拼写漂移不再可能 —— 约定 13 的同源精神) |
| `api/agent_config_env_test.go`(**新增,2 例**) | **env 的第四态**(#271,HTTP 层):`TestAgentConfigPutWithoutEnvKeepsStored`(**缺省 = 不修改** —— 只改模型的请求不得顺手删掉全部凭据)、`TestAgentConfigPutEmptyEnvClears`(**`{}` = 显式清空全部**)。这两个形态在 HTTP 载荷里**长得一样**(字段不存在 vs 空对象),必须在契约层分开测 |
| `app/application/agent_env_contract_test.go`(**新增,4 例**) | 同一条契约的**应用服务层**:`TestUpdateConfigEnvOmittedKeavesStored` / `TestUpdateConfigEnvEmptyObjectClears` / `TestUpdateConfigEnvReplacesTable`(`{k:v}` = **整表替换**,表里没有的键被删除)/ `TestResetConfigClearsEnv`(重置 = 清空) |
| `api/agent_permission_patch_test.go`(**新增,4 例**) | **权限端点三态**(#272):`TestAgentPermissionPartialUpdateKeepsOthers`(**只发一个能力位不得吊销其余的** —— 此前按"整表覆盖"解释,缺键 → false)、`TestAgentPermissionExplicitFalseIsHonored`(**显式 false 必须生效** —— "缺省"与"false"是两件事)、`TestAgentPermissionNonBoolRejected`(**非布尔值 400** —— 静默当作 false 等于吊销该能力)、`TestAgentPermissionFullMaskStillReplaces`(整表覆盖的旧用法**仍要能用**) |
| `app/application/settings_gitdir_test.go`(**新增,4 例**) | **Git 工作目录的存在性校验**(#277):`TestSettingsUpdateRejectsMissingGitWorkingDir`(**不存在的目录 → 拒绝** —— 此前"保存成功",之后每个 git / script / agent 节点都因 `cmd.Dir` 不存在而失败,报错与设置页毫无关联)、`TestSettingsUpdateRejectsFileAsGitWorkingDir`(**是个文件也不行**)、`TestSettingsUpdateAcceptsExistingDir`(合法目录**必须能存**)、`TestSettingsUpdateEmptyDirStillClears`(空串 = 清除,与"不传"严格区分) |
| `agent/claude/claude_argv_test.go`(**新增,2 例**) | **argv 凭据告警**(#273):`TestExtraArgsCredentialWarnsAndKeepsOriginalValue`(**告警必须发出** + **下发的仍是原值** —— 哨兵 `***` 不是真凭据,下发过去 CLI 鉴权必然失败,所以只告警不改写)、`TestExtraArgsNoCredentialNoWarning`(**没有凭据形态就不许告警** —— 否则告警会被训练成噪声而被忽略) |
| `agent/pi/pi_argv_test.go`(**新增,2 例**) | 同一条契约在 **pi 适配器**上的形态(pi 的默认参数走 `pi.Config.DefaultArgs`):`TestDefaultArgsCredentialWarnsAndKeepsOriginalValue` / `TestDefaultArgsNoCredentialNoWarning` |
| `app/application/mock_behavior_test.go`(**新增,6 例**) | **mock `behavior` 逐键合并**(#275):`TestMockBehaviorMergesPerKey`(只写 `decisions` 不得把默认脚本的 `summary_template` 丢掉 —— 此前是**整体替换**)、`TestMockBehaviorKeepsOtherModes`(改一个 mode 不得影响别的 mode)、`TestUpdateConfigRejectsUnknownBehaviorField` / `TestUpdateConfigRejectsWrongBehaviorTypes`(**未知键 / 错类型直接报错** —— 此前静默跳过)、`TestUpdateConfigAllowsKnownBehaviorFields` / `TestUpdateConfigAllowsCredentialField`(合法的**必须能存**,别把门关死) |
| `workflow/runtime/retry_policy_test.go`(**新增,5 例**) | **`retry.max_attempts` 非数值 → fail closed**(#274):`TestRetryPolicyRejectsUnparsableMaxAttempts`(读不出合法取值 → **节点失败**并说明原因;此前静默钳到 1,用户以为重试 3 次实际只跑 1 次)、`TestRetryPolicyRejectsNonObjectRetry`、`TestRetryPolicyAcceptsIntegerForms`(合法形态**必须能过** —— 别把门关死)、`TestRetryPolicyDefaults`、`TestAgentNodeFailsOnInvalidRetryConfig`(**端到端**:节点确实失败,不是只在解析层报错) |
| `workflow/validator/retry_config_test.go`(**新增,5 例**) | 同一条契约的**校验期**:`TestRetryMaxAttemptsMustBeNonNegativeInteger` / `TestRetryMaxAttemptsAcceptsIntegerForms` / `TestRetryMustBeObject` / `TestRetryBackoffMustBeString` / `TestRetryConfigValidFormsStayValid` —— **校验期与运行期必须同源**(约定 13):两边对同一份非法配置的结论必须一致 |
| `workflow/compiler/route_branch_test.go`(**新增,3 例**) | **出口分支下标写失败必须上抛**(#276):`TestSelectExitBranchReportsWriteFailure`(写失败 → **返回 error**,不再吞掉 —— `route:<id>:branch` 是恢复路径回放分支的**唯一依据**,静默失效比崩溃更危险)、`TestSelectExitBranchSucceedsNormally`(正常路径仍要选对分支)、`TestAbortRouteRecordFailed`(abort 路径上的同类写失败) |
| `workflow/runtime/state_mask_test.go`(**改**) | **链键也必须脱敏**(#270 的落库侧):`plan` / `review` / `issues` / `test_result` / `last_output` 五个新链键是**节点输出原文**(凭据的载体)→ 加进"自由文本键必须脱敏"的断言表里。**这五个键此前不在表内** —— 链键修好后,它们**第一次真的开始落库**,不登记就会明文进 `state_json` |
| `workflow/runtime/cas_conflict_test.go`(**改**) | 取消裁决竞态用例的**时序确定化**:原写法在 `Start` 之后**立刻**抢裁决 —— 抢在执行协程"启动即核对状态"之前 → 静默收尾(等满 10s 超时);抢在之后 → 终态提交冲突(用例的预期)。**这是竞态,不是缺陷**(实测 `GOMAXPROCS=1` 3/3 复现超时失败)。改成显式等 `node.started` 事件 → 竞态变成确定时序,**断言一个字都不改** |
| `workflow/runtime/node_config_test.go`(**改**) | `retryPolicyOf` 改返回 `(max, backoff, error)`(#274 的 fail closed 语义)→ 既有调用点跟着改:非数值 `max_attempts` 现在**返回 error**,用例必须断言它 —— 否则这条路径会被静默吞掉 |
| `agent/credential_mask_test.go`(**改,+2 例 —— 第十六轮已加**) | **跨入口逐字节比对**(#263)—— 本轮未动,此处只做交叉引用 |
| `logx/credential_test.go`(**改 —— 第十六轮已加**) | 同名用例改成比对**两个不同的入口**(#263)—— 本轮未动,此处只做交叉引用 |
| `agent/config_mask_shape_test.go`(**11 例 —— 第十五轮已加**) | **ExtraArgs 的"参数名 ∪ 值形态"双判定**(#265)—— 本轮未动,此处只做交叉引用(逐例说明见"轮次 79"一节) |
| `workflow/runtime/cancel_semantics_test.go`(**7 例 —— 第十五轮已加**) | **取消语义的三分**(#264 / #268)—— 本轮未动,此处只做交叉引用(逐例说明见"轮次 79"一节) |
| `api/cancel_semantics_test.go`(**1 例 —— 第十五轮已加**) | **端到端复刻取消窗口**(#264)—— 本轮未动,此处只做交叉引用(逐例说明见"轮次 79"一节) |
| `workflow/runtime/cancel_residual_test.go`(**1 例 —— 第十六轮已加**) | **取消残余窗口的当前语义**(#269)—— 本轮未动,此处只做交叉引用(逐例说明见"轮次 80"一节) |

**本轮验证**:`go vet` 干净、`go test -race` **21 个包全绿**、vitest **268/268**、
`./scripts/verify.sh` **9 步通过**(97s)、`docs/check-numbering.sh` 通过
(缺陷号区间 1..280 连续)。

### 轮次 82:第十八轮(迭代 20,commit `3e4a5b3`)—— 设置读失败可观测 / 内置 Agent 注册失败上抛 / Set 白名单化

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"#281-#283"(本轮全部已关闭)。
>
> 本轮收的是**第十八轮终验**列出的最后三条 P3,形状同属一类:**"失败被静默吞掉"**。
> 各自触发路径都极窄,但后果都是"用户看到的东西不是真的,而且没有一行日志"。
>
> **本轮新增 Go 测试文件 3 个 / 顶层用例 +16 例**(`3e4a5b3` 口径:测试文件
> 138 → **141**,顶层 `func Test*` 766 → **782**)。

| 文件 | 锁住的行为 |
| --- | --- |
| `app/application/settings_read_failure_test.go`(**新增,6 例 —— 本轮核心**) | **设置读失败必须可观测**(#281):`TestSettingsInfoLogsErrorWhenGitWorkingDirUnreadable`(读失败必须记 **ERROR**,不是 WARN/INFO —— 此前 `_ =` 吞得干干净净,"界面显示的不是库里那个"这件事**完全不可见**)、`TestSettingsInfoMarksGitWorkingDirReadFailure`(**必须标注** `git_working_dir_read_failed` —— 消费方据此知道上面的值是内存态、**不能当"库里那个"回存**(回存即覆盖,损坏事实消失))、`TestSettingsInfoReadFailureMarkerFalseWhenReadable`(行读得出来时**不得**误标 —— 误报同样是 bug)、`TestSettingsInfoNoRowIsNotAReadFailure`(**"从未设置过"不是失败** —— 全新安装的正常初始态,不该报错)、`TestSettingsInfoClearedRowIsNotAReadFailure`(空串 = 清除,是**有意义的库值**,读成功)、`TestNewAppLogsErrorWhenGitWorkingDirUnreadable`(**启动路径同样留痕** —— 此前 `if err == nil && wd != ""` 把读错误吞得干干净净,进程静默用默认目录,全程零日志) |
| `app/application/agent_registration_test.go`(**新增,2 例**) | **内置 Agent 注册失败必须上抛**(#282):`TestBuiltinAgentRegisterFailureIsLogged`(注入注册失败 → 必须**上抛** + 错误**指明是哪个 Agent** + 记 **ERROR** 日志 —— 此前 4 处 `_ = a.Agents.Register(...)` 把失败吞得干干净净,内置 Agent 缺失**一行日志都没有**)、`TestBuiltinAgentsRegisterSuccessfully`(正常路径 4 个内置 Agent 都**确实在注册表里** —— 防止"上抛"把正常注册也弄坏) |
| `app/application/settings_set_whitelist_test.go`(**新增,8 例 —— 本轮核心**) | **`Set` 不得成为绕过校验的后门**(#283):`TestSettingsSetRejectsNonWhitelistedKey`(白名单外的键必须被拒绝,尤其 **`agent_policies`** —— 它由 `AgentService` 在 `permMu` 下读改写,绕过去直写会丢并发更新;且**被拒绝的写入不得落库**)、`TestSettingsSetRejectsUnknownKey`(未知键必须被拒绝,含**大小写变体** `AGENT_POLICIES`)、`TestSettingsSetValidatesGitWorkingDir`(**复用 `Update` 的存在性校验**;校验失败时内存态与库都**不得**改动)、`TestSettingsSetValidatesLogLevel`(**复用级别校验**;非法级别不得落库)、`TestSettingsSetAcceptsWhitelistedKeys`(白名单里的键**必须都能写且真的生效** —— 落库 + 内存态都验;`log_level` 落库**归一为小写**)、`TestSettingsSetRejectsWrongValueType`(**值类型必须校验** —— 把字符串 `"true"` 写进 `allow_script_nodes` 会造出一行"读不出来"的值)、`TestSettingsSetErrorMentionsKey`(错误必须说清是哪个键)、`TestWritableSettingKeysAllWritable`(白名单是一份清单、处理逻辑是另一处 switch —— 两者漂移时"键在白名单里却写不进去",这条用例让漂移立刻显形) |

**本轮验证**:`go vet` 干净、`go test -race` **21 个包全绿**、vitest **268/268**、
`./scripts/verify.sh` **9 步通过**(实测 **118s**,分步 0s / 0s / 5s / 16s / 34s /
7s / 24s / 7s / 24s)、`docs/check-numbering.sh` 通过
(缺陷号区间 1..288 连续)。

### 轮次 83:第十九轮(迭代 20 收口)—— 空上下文可见(#280)/ 门禁第 6 步的断言维度(#289)

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 83"小节(#280 关闭 / #289 关闭 /
> #290 有意边界)。
>
> 本轮是**收口轮**:两份独立的终审复审交叉验证了同一份剩余清单,收掉最后两条。
> 两条同属一族:**"空洞不可见"** —— ① 运行时:上游没产出 → 下游空上下文
> **零告警**;② 门禁:零断言的测试包能骗过**整条 9 步门禁**。
>
> **本轮新增 Go 顶层用例 +7 例**(`contextx/context_test.go` 4 例 +
> `workflow/runtime/context_chain_test.go` 3 例),**未新增测试文件**
> (141 → **141**),顶层 `func Test*` 782 → **789**;门禁自检场景 23 → **27**。

| 文件 | 锁住的行为 |
| --- | --- |
| `contextx/context_test.go`(**改,+4 例**) | **空槽位必须能被看见**(#280):`TestBuildWithSlotsReportsEmptyChainSlots`(**键存在但值为空串**的 `plan` 与**键根本不存在**的 `review` 都要进"空槽位"清单;同时断言**数据表本身不变**(键仍在、值仍是空串 —— 渲染口径"键存在 = 可解析"**不动**))、`TestBuildWithSlotsIgnoresNonChainSlots`(**`task` / `iteration` 不是上游产出的链槽位** —— 非循环工作流里 `{{iteration}}` 恒为空,算进来就是纯噪音)、`TestBuildWithSlotsRespectsModeScope`(plan 模式拿不到 `git_diff` / `review` → **不得**报为空槽位)、`TestBuildWithSlotsKeepsNonEmptyValues`(**有内容的值不算空** —— 数字 0、空列表 `[]` 渲染出来是有内容的("0" / "[]"),不是"缺失") |
| `workflow/runtime/context_chain_test.go`(**改,+3 例 —— 本轮核心**) | **"上游没产出"必须可见**(#280):`TestEmptyUpstreamSlotIsVisible`(上游成功但 summary/output **均为空** → 下游 `{{plan}}` 渲染成空串 → **必须**有 `node.warning` 且 `reason="empty_context_slots"`、**点名 `plan`**、**挂在下游节点上**;执行**仍落 COMPLETED** —— 语义是"可见但不阻断")、`TestEmptySlotWarningAbsentWhenUpstreamProduced`(上游**有产出**时**不得**出现空槽位告警 —— 否则就是噪音,和"缺失必须可见"一样有害)、`TestEmptySlotAndMissingVarAreDistinguishable`(拼错的 `{{revwie}}` 与空槽位的 `{{plan}}` **必须报成两种 reason** —— 用户要能分清"我拼错了"与"上游没给我东西") |

**门禁侧(本轮改了 `scripts/verify.sh` 第 6 步)**:

| 项 | 内容 |
| --- | --- |
| 新判据 | 第 6 步除"有真实执行并通过的顶层用例"(框架事件)外,**再加一条**:该包的 `*_test.go` 里**必须真的有断言**(静态扫描)。**按包**判定:包里有一处断言即达标 |
| 为什么不用 `go vet` | 实测 `go vet -tests` 对一个**零断言**的包 **exit 0** —— vet 的 tests 分析器查的是**测试函数签名/示例命名**,**不是断言数**;断言数**不在 `go test -json` 的事件里**。所以只能静态扫源码 |
| 口径 | 断言 = 惯例接收者(t/tb/tt/t2…)上的 `Error/Errorf/Fatal/Fatalf/Fail/FailNow` 或第三方 `assert./require.` 的调用;扫描范围**只限该包的 `*_test.go`**(生产代码里 `err.Error()` / `fmt.Errorf()` 本仓库实测 73 + 71 处,扫生产代码等于没有判据) |
| **先剥注释** | `strip_go_comments`(字符串/字符常量状态感知):注释里的 `t.Errorf` **不是断言**。**实测**:一个只在注释里提到 `t.Fatal` 的零断言包,若不剥注释会被判成"有断言"而放行 —— 本项目的测试文件**大量**在注释里写着"这里应该 t.Errorf(...)",不剥注释等于判据当场失效。**注**:第二十二轮(轮次 86)已把整个断言扫描换成 **go/ast 静态分析** —— 注释天然不算断言(AST 里没有注释节点),`strip_go_comments` 随之删除;新增的判据是"**恒假分支里的断言不算数**"(见轮次 86 小节) |
| 自检(+4 条,23 → **27**) | ① 有断言的包**不得**被误报(绿方向);② 零断言的包**必须**被抓(红方向,#289 的绕过本身);③ **只在注释里**提到断言的包**必须**被抓(证明剥注释真的生效);④ 反向对照(对有断言的包断言"必须在报告里"**必须失败** —— 证明这个判定函数真的在区分,不是恒真) |
| 实测证据 | 隔离副本里放一个只含 `func TestNothingRuns(t *testing.T) { _ = 1 }` 的包 → **修前**:9 步门禁 **exit 0**(110s,全绿);**修后**:红在第 6 步("以下包的测试代码里没有任何断言");**真实仓库**:9 步 **全绿**(误报率实测 **0** —— 21 个包逐个扫,无一被报出) |

**本轮验证**:`go vet` 干净、`go test -race -count=1 ./...` **21 个包全绿**、
vitest **268/268**、`./scripts/verify.sh` **9 步通过**(实测 **113s**)、
`docs/check-numbering.sh` 通过(缺陷号区间 1..290 连续)。

### 轮次 85:第二十一轮(迭代 22 收口)—— 跨文件动作落地(8 项)与一处"守卫能力边界"的登记

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 85"小节(#297-#304 全部关闭,
> 另登记有意边界 #305)。
>
> 本轮是**施工轮**:上一轮收口时列了 8 条"需主代理接手"的跨文件动作
> (那些文件当时归别的代理所有,只能把精确改法写下来)。本轮把 8 条
> **全部落地**,并记下一条施工中撞见的**硬约束**(#305:运行期状态脱敏
> 守卫只能解析**字符串字面量**常量,挡住了"把 `contextx.StateKey*` 改成
> `model.ContextSlot*` 的别名"这条最直观的收敛改法 —— 改用"字面量 +
> 两道跨包守卫"达成同一目的)。
>
> 新增 Go 顶层用例 **+19 例**、**新增测试文件 5 个**(150 → **155**),
> 顶层 `func Test*` 814 → **833**;前端 **+13 例**(272 → **285**)。

| 文件 | 锁住的行为 |
| --- | --- |
| `contextx/mode_source_test.go`(**新,5 例**) | **contextx 不得自带 mode 清单**(#297):**源码级扫描**(AST 解析,注释不算代码)—— `contextx/context.go` 里不得出现任何 mode 名字面量(`plan`/`execute`/`review`/`fix`/`test`/`submit`/`skill`);**逐 mode 的槽位归属**由 `model.ModeSpecFor` 决定(声明"不进上下文链"的 mode 必须什么都不写);**`skill` 伪 mode** 的产出仍进 `last_output`;**槽位解析**完全由 model 决定(合法 mode → `ContextSlot`;伪 mode → `last_output`;不认识的 mode → 不写);**10 个键名与 `model.ContextSlot*` 逐项一致**(键名是写进 `state_json` 的持久化契约) |
| `app/application/version_dsl_test.go`(**新,3 例**) | **版本页读原文**(#299):返回的是**当初存下的原文**(`max_iter: 3` 而不是重新编码出来的 `3.0` —— 整数在 JSON 快照往返后变成 float64);**老库兼容**:`dsl_text` 为空 → 回退到从 `snapshot_json` 重编码(不报错、不返回空);**版本不存在** → `NOT_FOUND` |
| `app/application/backup_skill_test.go`(**新,7 例**) | **备份的 `version` 必须校验**(#301):缺失 → `BAD_REQUEST`(此前任何 JSON 都被当备份,用户看到"恢复成功"却什么也没恢复);不认识(如 99)→ `BAD_REQUEST`(fail closed);`version: 1` → 正常路径(**向后兼容**:现有备份恒为 1);**`skill_configs` 补齐**:导出含该段(禁用的技能 `enabled=false`);**恢复后禁用状态还原**且落库;**本机不存在的技能**跳过并如实计数(与未知 Agent 同一口径);**老备份(没有 `skill_configs` 段)仍能恢复**且不改动本机的技能状态 |
| `api/modes_test.go`(**新,3 例**) | **mode 列表的三个出口必须同源**(#298):`GET /api/modes` 逐项等于 `model.AllModes()`(端点里另写一份字面量清单 → 这里立刻变红);**服务器模式与桌面模式同一份**(REST vs `WorkflowService.Modes()`,约定 10);**前端兜底清单**(`Designer.tsx` 的 `FALLBACK_MODES`,跨语言扫描源文件)必须与 `model.AllModes()` **逐项一致** —— 兜底清单也出现在用户面前,漂移同样是缺陷 |
| `template/dead_const_test.go`(**新,1 例**) | **`template` 包的 6 个死常量不得复活**(#300):`VarTask`/`VarPlan`/`VarGitDiff`/`VarReview`/`VarTestResult`/`VarIteration` 零引用(它们只是上下文槽位键名的第三份抄写)—— 看着像清单会诱导后来者照着它加常量,多出一份会漂移的来源。删掉后编译器不会报错(导出常量没人引用是合法的),所以用源码扫描锁住 |
| `frontend/src/pages/designer.modes.test.tsx`(**新,8 例**) | **mode 下拉必须后端派生**(#298):`resolveModeList` —— 后端返回的清单优先,返回空/`null`/异常对象时用兜底(控件不留空);**下拉渲染**:属性面板的 mode 下拉里出现后端给的 `brand-new-mode`,且**不出现**前端兜底里的 `submit`(后端没给);**页面级**:Designer 挂载时真的调 `api.modes()`,点画布节点 → 属性面板 → 打开下拉 → 断言选项来自后端(jsdom 需补 `PointerEvent`/`ResizeObserver`/指针捕获三件套,否则 Radix 下拉静默打不开) |
| `frontend/src/lib/api.test.ts`(**改,+2 例**) | **`modes()` 的双分支**(约定 10):桌面走 `wf.Modes()` 绑定、浏览器走 `GET /api/modes`;**绑定导出清单守卫**同步更新(`workflowservice` 多 `Modes` —— 与真实生成物逐项一致) |
| `frontend/src/pages/Settings.test.tsx`(**改,+3 例**) | **备份文案与实际导出一致**(#301):写明备份含"技能启用状态"/`skill_configs`、写明"恢复时按版本号校验格式(不认识的会被拒)"、写明"老备份仍能恢复(只是没有技能状态)" |

**红 → 绿证据(修前实测)**:

| # | 旧代码上的实测输出 | 现在 |
| --- | --- | --- |
| #297 | `contextx/mode_source_test.go`:"contextx/context.go 里出现 mode 的字面量 \"plan\"…"(6 条) | 0 命中;槽位归属由 `model.ModeSpecFor` 决定 |
| #298 | `api/modes_test.go`:"GET /api/modes" 404;`Designer.tsx` 下拉写死六项 | 端点 200 且逐项一致;下拉渲染后端清单 |
| #299 | `VersionDSL` 返回 `max_iter: 3.0`(原文 `3`) | 返回 `max_iter: 3` |
| #300 | `template/dead_const_test.go`:6 个常量"仍在本包声明" | 0 命中 |
| #301 | "缺少 version 的备份必须报错"→ **不报错**;"导出缺少 skill_configs 段";恢复后 `log` 技能"又是启用状态" | 三条全绿 |
| #305 | 改成别名后:`workflow/runtime/state_mask_test.go` 报 **8 条"状态键写法无法解析" + 6 条"登记与实现漂移"** | 保持字面量 + 两道跨包守卫,守卫全绿 |

**本轮验证**:`gofmt -l` 干净(改过的文件)、`go vet` 干净、
`go test -race -count=1 ./...` **21 个包全绿**、前端 `npm run lint` +
`npx tsc --noEmit` + `npx vitest run` **285/285**、
`./scripts/verify.sh` **9 步通过**(实测 **113s**)。

### 轮次 86:第二十二轮(迭代 25 收口)—— 数值有限性三层防线 / 门禁断言强度 / 跨包常量别名 / 前端请求竞态 / timeout 上界

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 86"小节(**#306-#311 全部关闭**,
> 另登记 3 条开放项 **#312-#314**,精确改法见该节)。
>
> 本轮是**收口轮**:第二十四轮扫描与第二轮终验各抓到一条**独立**的新问题,
> 连同第三、四、五轮扫出的 P2 一起收掉。
>
> **本轮的一条方法论**:"**存在**断言"与"断言**可能触发**"是两件事 ——
> 第二轮终验实测:一个包里唯一的用例是 `if 1 != 1 { t.Fatal("不可能") }`
> 时,断言**永远不执行**,而第 6 步的判据只看**存在性** → 9 步门禁全绿
> = 绕过。本轮把判据升级为"**可能触发**",并给门禁自己补了红场景 + 绿对照。
>
> 新增 Go 顶层用例 **+20 例**、**新增测试文件 3 个**;前端 **+8 例**、
> **新增测试文件 1 个**。

**本轮新增/修改的测试**:

| 测试文件 | 覆盖的缺陷与断言 |
| --- | --- |
| `persistence/finite_json_test.go`(**新,8 例**) | **#306**:`SaveWorkflow` 遇 NaN → **返回错误**且**一行都不落库**(工作流行 0 + 版本快照行 0);节点配置含 Inf → 同样失败(否则整个 `nodes_json` 变空串);`SaveExecution` 遇 NaN → 失败且不落行;**老库兼容**:库里 `variables_json = ""` → `GetWorkflow` / `ListWorkflows` 读成**空 map**、**不报错**;事件载荷含 NaN → 落**占位载荷**(`__unserializable__`)而不是空串。**#307**:导入含 `.nan` 的 DSL → 校验期**必须失败**且错误码是 `INVALID_NODE` |
| `workflow/validator/finite_test.go`(**新,6 例**) | **#307**:变量表里的 NaN / +Inf / -Inf → 校验失败且错误码 `INVALID_NODE`、错误信息指出 `variables.env`;**嵌套**在 map / 数组里的非有限值(`variables.cfg.deep[2]`)同样被抓住;节点配置 / permissions 段里的非有限值同样被抓住;**正常值不得误报**(含 `int64(1)<<40`、嵌套 map);**字符串形态的 "NaN" / "Inf" / ".nan" 不得误报** |
| `workflow/validator/timeout_seconds_test.go`(**新,4 例**) | **#311**:`timeout_seconds` = 10000000000 / 4611686018427387904 / 1e20 / -1 / 86401 / "30" / 1.5 / true → **一律校验失败**(`INVALID_NODE`);0 / 1 / 30 / 3600 / 86400 → **一律通过**(0 = 不设节点超时,与运行期口径一致);script 节点同样适用 |
| `workflow/runtime/state_mask_test.go`(**改,+2 例**) | **#309**:跨包别名常量(`const KeyAlias = alpha.KeyBeta`)**能被解析**成目标常量的字面量值(否则该写入点会被当成"无法解析"跳过 → 守卫对它没有牙齿);**别名漂移**(目标包的常量改了)**仍能被发现**(解析结果跟着目标变,而不是冻结成旧值)。扫描范围同时加上 `workflow/model`(别名的指向目标必须也在范围内) |
| `frontend/src/lib/request-guard.test.ts`(**新,4 例**) | **#310**:序号单调递增;只有最新一次请求的响应算数;`invalidate` 作废所有在飞请求;`invalidate` 之后取到的新序号仍然算数 |
| `frontend/src/pages/EventsAudit.test.tsx`(**改,+2 例**) | **#310**:**旧请求的迟到响应不得覆盖新前缀的结果**(旧响应回来后,新前缀的事件仍在、旧的不得出现);**输入框每次按键不得发请求**(两次按键后仍只有 1 次请求,回车才提交第 2 次) |
| `frontend/src/pages/ExecutionMonitor.test.tsx`(**改,+2 例**) | **#310**:切换执行后**旧执行的迟到响应不得覆盖新执行**;**切换执行时先清空上一条执行的数据**(页面回到"加载中…",按钮无从作用在上一条执行上) |

**门禁自检的增量(第 6 步)**:

| 自检场景 | 断言 | 现在 |
| --- | --- | --- |
| 红:恒不触发的断言必须被抓 | `usage_expect "awo/usagecheck/deadassert" "$ASSERT_REPORT" "yes" "恒假分支"` | 断言扫描报"测试代码里的断言都在恒假分支里,永远不会被触发(等价于没有断言)" |
| 绿:运行期才成立的正常断言不得误判 | `usage_expect "awo/usagecheck/liveassert" "$ASSERT_REPORT" "no"` | 不被报出(新判据**不能过严**) |
| 绿:对照组夹具换成真断言 | `asserted` 夹具由 `if 1 != 1 { t.Fatal(...) }` 改成 `got := 1 + 1; if got != 2 { t.Fatalf(...) }` | **原夹具正是绕过形态本身**(断言永不触发)—— 判据升级后必须换掉,否则绿对照变成红场景 |

`SELFCHECK_EXPECTED` **27 → 29** 条(场景计数守卫同步);第 6 步的断言扫描由
**文本级**(`grep -E` + awk 去注释)改为 **go/ast 静态分析**(内嵌在
`scripts/verify.sh` 里的 `assertscan`):注释天然不算断言,恒假分支里的断言
**不算数**。**真实仓库误报 0**:全仓 158 个测试文件 / 3098 条断言,**全部可触发**。

**红 → 绿证据(修前实测)**:

| # | 旧代码上的实测输出 | 现在 |
| --- | --- | --- |
| #306 | `marshal err=json: unsupported value: NaN bytes="" len=0`;`DB variables_json="" len=0`;`DB snapshot_json="" len=0` | `SaveWorkflow` 返回 `PersistenceError(DB_ERROR): 序列化失败…`;一行都不落库 |
| #307 | `IMPORT code=200`(工作流里一个变量都没有) | `IMPORT code=400` + "variables.env 的值必须是有限数值";工作流**根本没被创建** |
| #308 | 恒不触发的断言包 → 判"有断言" → 9 步门禁**全绿** | 报"断言都在恒假分支里" → 门禁**红** |
| #309 | 别名化后:8 条"状态键写法无法解析" + 6 条"登记与实现漂移" | 别名解析出目标值;守卫**全绿**;漂移仍被报出 |
| #310 | 迟到响应把 "workflow.started" 覆盖成 "node.started"(断言非空 → 红) | 旧响应被丢弃;新结果保留 |
| #311 | `timeout_seconds: 10000000000` → 校验**通过** | 校验失败:`INVALID_NODE` + "必须在 0..86400 之间" |

**本轮验证**:`gofmt -l` 干净(改过的文件)、`go vet` 干净、
`go test -race -count=1 ./...` **全绿**、前端 `npm run lint` +
`npx tsc --noEmit` + `npx vitest run` **293/293**、
`./scripts/verify.sh` **9 步通过**、`docs/check-numbering.sh` 通过。

### 轮次 86(续):第二十三轮(迭代 26 收口)—— 运行期 timeout 钳制 / SaveEvent 收口 / 导入期预检判定

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 86(续)"小节(**#312-#314 全部关闭**,
> 另登记 3 条:**#315-#317**)。
>
> 本轮是**收口轮**:把上一轮写好的精确改法落地,并逐条核实"改法本身会不会引入
> 新问题"(递归 / 契约探针 / 向后兼容)。**没有**改动 `workflow/compiler/**`、
> `workflow/runtime/engine.go`、`app/**`、`agent/**`、`event/**`、`scripts/**`。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/timeout_seconds_test.go`(**新**) | **#312**:运行期交给适配器的 `req.Timeout` 必须是**合法范围**内的值(0..86400),15 个取值形态逐个锁死 —— 正常值(30 / `uint64` / `float64`)原样保留;0 → 0(= 不设节点超时);86400 → 86400;**超上界**(86401 / 10000000000 / `uint64` 4611686018427387904 / **1e20(float)**)→ **一律钳到 86400**(1e20 既不是负数也不是 0);**负值**(-1 / `int64(-1)<<40`)→ **0**;小数秒(30.5)→ **0**(不静默截断改写);字符串("30")→ 0;NaN → 0;+Inf → 86400。**#312(可观测后果)**:script 节点 `timeout_seconds: 10000000000` 必须**跑完**(SUCCESS),而不是刚起就被杀 |
| `workflow/runtime/event_persist_payload_test.go`(**新**) | **#316**:载荷含 NaN 的事件落库失败 → ① 失败被计数;② **有且仅有 1 条** `runtime.error`(**递归护栏,不级联**);③ 审计流里**只有那一条 `runtime.error`**(坏事件本身**不在**表里 —— 持久层失败即报错) |
| `persistence/finite_json_test.go`(**改**) | **#313**:`SaveEvent` 遇 NaN 载荷 → **返回错误**且**一行都不落库**(不得写占位载荷冒充正常事件) |
| `api/sse_marshal_test.go`(**改**) | **#315**:失败的事件**不得以正常事件的形态发出**(顶层 `type` 不得是 `node.started`);且**必须有显式的失败信号**(`"type":"runtime.error"` 必须出现)—— 失败不能只剩一条日志(约定 14) |
| `api/server_test.go`(**改,+1 例**) | **#314**:导入含 `.nan` 的 DSL → **400 `WORKFLOW_INVALID`** + 响应里看得见"有限数值" + 工作流**根本没被创建**(GET → 404) |

**红 → 绿证据(改前实测)**:

| # | 旧代码上的实测输出 | 现在 |
| --- | --- | --- |
| #312 | `运行期交给适配器的 timeout = 10000000000,want 86400`;`1e20(float) → timeout = -9223372036854775808,want 86400`;`负值 → timeout = -1,want 0`;`小数秒 → timeout = 30,want 0`;script:`超大 timeout_seconds 让 script 节点 FAILED(context deadline exceeded),want SUCCESS;耗时 547.637µs` | 15 个取值形态全部落到预期值(86400 / 0 / 原值);script 节点 **SUCCESS** |
| #313 | `载荷含 NaN 时 SaveEvent 必须返回错误(不得写占位载荷冒充正常事件)`(persistence);`事件落库失败必须留下显式的失败信号(runtime.error),实际输出: ": connected\n\ndata: {\"type\":\"node.completed\",...}"`(api) | `SaveEvent` 返回错误且**一行都不落库**;SSE 流里有 `{"type":"runtime.error",...}`、**没有** `{"type":"node.started",...}` |
| #315 | 旧断言 `!contains(out,"exec-bad")` 与正确行为**冲突**(失败信号本来就带执行 ID) | 判据改成**顶层 `type`** + 新增 `runtime.error` **正向**断言 |
| #316 | (覆盖缺口:只有"整库故障"的用例) | 新用例锁住"只有一条 `runtime.error`"+"审计流里只有失败信号" |

**契约探针仍然有牙齿(#312 的核心约束)**:

- 两个调用点都保持 `cfgFloat(node, "timeout_seconds")` 的**字面量**形态
  (探针只认 `cfg*(node, "字面量")`)→ `go test ./workflow/compiler/` **绿**,
  且本轮**没有**改动 `workflow/compiler/**` 的任何文件(契约表与探针原样);
- **造坏验证(隔离副本 `/tmp/aw26-probe`)**:把 script 节点的字面量读取换成
  `if s := 0; s > 0` → 探针立刻报
  `script 节点:契约表声明了配置键 "timeout_seconds",但运行期没有读取它` → 门禁红。

**门禁自检**:场景数仍为 **29 条**(`SELFCHECK_EXPECTED=29`,本轮**没有**新增 /
删除自检场景 —— `scripts/verify.sh` 未改动);`./scripts/verify.sh` **9 步通过**。

**本轮验证**:`gofmt -l` 干净(改过的文件)、`go vet` 干净、
`go test -race -count=1 ./...` **全绿**(21 个包)、`./scripts/verify.sh`
**9 步通过**、`docs/check-numbering.sh` 通过。

### 轮次 87:第二十四轮(迭代 28 收口)—— submit 产物进上下文链 / SSE 用例改确定性同步点 / 权限死入口清理 / 文档口径校正

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 87"小节(#321-#329)。
>
> 本轮是**收口轮**:不新增扫描面,只把第二十六/二十七轮两份复审各自留下的
> P2 / P3 收掉,并且**先核实**"复审说的是不是真的"。新增 Go 顶层用例
> **+6 例**、**新增测试文件 3 个**(160 → **163**),顶层 `func Test*`
> 853 → **869**(含第二轮在途改动带来的用例)。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/runtime/submit_slot_test.go`(**新,2 例**) | **`submit` 的产物必须进上下文链**(#321):端到端 —— `execute` → `submit` → 下游 `{{last_output}}`,下游必须拿到 **submit 自己的产物**(`提交摘要-NEW`),**不是**上一个 execute 节点的旧产物(`执行完成-OLD`);`submit` 之后再走一个 `execute` 时,下游拿到的是**最后一次**产出(槽位按执行顺序刷新,不是"submit 专属") |
| `workflow/model/mode_slot_test.go`(**新,3 例**) | **mode → 槽位的映射是全覆盖的**(#321):遍历**全部**合法 mode,每个都必须有**非空** `ContextSlot`(空槽位 = 该 mode 的产物不进上下文链 = 槽位里留**陈旧值**且无告警);`submit` 的槽位必须是 `last_output` 且 `RequiresWrite` 为真;`execute` / `fix` / `submit` 共用 `last_output` |
| `permission/dead_entry_test.go`(**新,1 例**) | **权限管理器只有一个应用覆盖的入口**(#323):用**反射**断言 `Manager` 的方法集里**不得**再有 `Apply`(静默版),且 `ApplyChecked` / `ApplyWorkflowOverrides` 必须都在 —— 防止"静默入口"被再加回来 |
| `api/sse_test.go`(改:1 例重写 + 1 例新增) | **慢客户端不得阻塞事件生产者**(#322):`TestSSESlowClientDoesNotBlockProducer` 去掉 **5s 硬阈值**(并行负载下误红),改成等"**Emit 已返回**"这个**信号**;`TestSSESlowClientQueueOverflowDoesNotBlockProducer`(新)用 `blockedWriter` 把"客户端卡住"变成**确定的事实**,两个同步点(① 写协程已卡在 `Write` 里 ② 生产者那一轮 `Emit` 已全部返回),并断言**队列丢弃分支被走到**(捕获 `logx` 的 WARN `SSE 客户端消费过慢`) |
| `workflow/runtime/node_config_test.go`(改) | **#317 关闭**:`TestCfgIntAcceptsAllIntShapes` → `TestCfgFloatAcceptsAllNumericShapes`(断言强度不变 —— 仍是"认全所有数值形态",字符串 `"30"` → 0),`cfgInt` / `toInt` 两个助手随之删除 |

#### 红 → 绿(逐条)

| 编号 | 红(改前实测) | 绿(改后) |
| --- | --- | --- |
| #321 | `submit_slot_test.go:83: 下游节点收到的提示词里没有 submit 节点的产物: "上次输出:执行完成-OLD"` / `submit_slot_test.go:86: 下游节点拿到的是**陈旧**的 last_output(上一个 execute 节点的产物): "上次输出:执行完成-OLD"` | 下游提示词 = `"上次输出:提交摘要-NEW"`,不含 `"执行完成-OLD"`,无残留占位符 |
| #322 | 旧判据是**耗时阈值**(复审实测 3 路并行 5.409s > 5s → 误红)。**本轮未能在本机复现该误报**:3 路并行 + 4 个满载进程 + 并发跑整包 `-race` 全量,旧用例耗时 **0.70-0.88s**(远低于阈值)—— 复审的 5.409s 是在更重的负载下测到的;但缺陷是**结构性的**(判据比墙钟时长,时长是负载的函数;该用例自己的注释就写着"单独跑约 0.55s,全量并发时 2.3s",5s 阈值只比已记录的最坏值高约 2.2 倍)。**造坏验证**(overlay 把订阅回调改回"同步写客户端"):`FAIL: TestSSESlowClientQueueOverflowDoesNotBlockProducer (10.02s)` / `sse_test.go:265: Emit 被慢客户端阻塞(触发事件的那次 Emit 没有返回)` | 两条用例都只等"Emit 已返回"的**信号**;新用例另断言队列丢弃分支被走到 |
| #323 | 造坏验证(overlay 把 `Apply` 加回去):`dead_entry_test.go:40: Manager 不得再有静默入口 Apply(方法集: Apply, ApplyChecked, ApplyWorkflowOverrides, Check, Clone, Get, Set, Snapshot, ValidateTightenOnly)` | 方法集里没有 `Apply`;`ApplyChecked` / `ApplyWorkflowOverrides` 都在 |
| #324-#329 | 纯文档项:判据是**核实**(文件与行号逐条读过 + 实测) | 文档口径与实现一致;`docs/check-numbering.sh` 全绿 |

#### 门禁第 7 步的前置条件(#324):**只需** `cd frontend && npm install`

> **这是"9/9 不可从干净 clone 复现"的那条前置条件**,此前**没有任何文档**记载。
>
> **⚠️ 第三十轮(迭代 34 收官)实测更正**:下表的第 2、4 行是**错的** ——
> 见上文"门禁第 7 步的前置条件"一节的完整更正过程(结论:
> **eslint 的 5 个 devDep 就在 `frontend/package.json` 里**,干净 clone
> 只装 `frontend/` 就有 eslint v9.39.5;本工作副本 `frontend/node_modules`
> 没有 eslint 是**安装陈旧**导致的,不是清单缺口)。

| 事实 | 依据 |
| --- | --- |
| eslint 与它的 4 个插件**也在** `frontend/package.json` 里声明 | 根 `package.json` 的 `devDependencies`:`@eslint/js` / `eslint` / `eslint-plugin-react-hooks` / `globals` / `typescript-eslint`;**`frontend/package.json` 的 `devDependencies` 里同样有这 5 个**(由 `bb5cd63` 加入,早于 #324 的登记) |
| `frontend/package.json` 的 lint 脚本是 `eslint .` | `frontend/package.json` → `"lint": "eslint ."`;**干净安装后** `frontend/node_modules/.bin/eslint` **存在**(隔离复现:`npm ci` 装 372 个包,`--version` = v9.39.5) |
| 本工作副本里它能跑通,是因为 npm 会把**祖先目录**的 `node_modules/.bin` 加进 PATH | 实测:`cd frontend && npm run lint` → exit 0;但**这是因为本机 `frontend/node_modules` 是 9-18 的旧安装**(eslint 10-21 才写进清单)—— **不是清单缺口** |
| ~~只装 `frontend/` 的干净 clone → 第 7 步 lint `exit 127` → 门禁**红**~~ | **已证伪**(第三十轮隔离复现:空目录里只放 `frontend/package.json` + lockfile → `npm ci` → eslint 就在 `frontend/node_modules/.bin/` 里) |

**更正后的结论**:从干净 clone 复现门禁,**只需要装前端依赖**:

```bash
cd frontend && npm install   # eslint 与插件都在 frontend/package.json 里
```

**`AGENTS.md`(仓库宪法)不归本代理** → 建议措辞见交付报告"需主代理执行"一节。

### 轮次 87(续):第二十七轮(迭代 27 收口)—— 凭据形态的"键值粘连"泄漏 / 门禁死断言的三类新形态

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 87"小节(**#318 / #319 / #320**)。
>
> 本轮只做**两项 P1 必修**:① 凭据在"**键值粘连**"形态下(`AK=AKIA…` /
> `+AKIA…`)从**六个出口**明文泄漏;② 门禁第 6 步的断言扫描可被**三种手法**
> 骗过(实测 9/9 全绿放行)。**纪律**:每条缺陷**先写会失败的测试** → 再修到绿,
> 逐条给出**红时断言输出**;**不写恒真测试**。新增 Go 顶层用例 **+5 例**
> (顶层 `func Test*` 864 → **869**;测试文件数 **163 不变** —— 本轮**未新增
> 测试文件**,只改既有的 `logx/credential_test.go`);前端**未改动**。

| 文件 | 锁住的行为 |
| --- | --- |
| `logx/credential_test.go`(改,+5 例) | **#318**"键值粘连"形态必须被脱敏:`TestMaskCoversKeyValueGluedCredentialShapes`(7 个形态 × 2 入口:`AK=AKIA…` / `+AKIA…` / `-AKIA…` / `X=sk-ant-…` / `X=ghp_…` / `X=eyJ…` / `+ghp_…`;**且键名 / diff 行标记必须保留** —— 脱敏不许毁掉可读性);`TestMaskCoversKeyValueGluedCredentialsAtOutlets`(三个出口的端到端:节点 output / state_json → `logx.Mask`、事件流 → `logx.MaskValue`、制品 → `logx.MaskCredentialValues`);`TestAWSAccessKeyPatternIsNotRedundant`(**给 `awsAccessKeyPattern` 补上"只有它能抓住"的用例** —— 终审指出"删掉它没有任何测试变红"是虚假信心:用一串**无数字**的 AWS 密钥 ID `AKIAABCDEFGHIJKL`,宽松前缀规则与高熵规则都要求含数字 → 只有它能命中);`TestBareBase64WithPaddingIsNotTruncated`(**base64 的 `=` 不得被截断** —— 用一串**恰好 24 字符**(含两个 `=` 填充)、驼峰起伏 < 4 次的 base64:去掉 `=` 就跌破裸值高熵门槛 → 整串明文泄漏);`TestBasicAuthBase64IsMasked`(`Basic <base64>` 四个形态 + **3 条英文短语反向对照** —— `Basic authentication` / `Basic Tokenization` / `Basic application` **不得**被误伤) |
| `logx/credential_test.go`(改:`TestCredentialShapeCoverageMatrix`) | 覆盖矩阵从 **13 形态**扩到 **15 形态 × 2 出口**:新增 `AK=AKIA…(键值粘连)` 与 `+AKIA…(diff 行前缀)` 两行,两个出口**都必须**脱敏 |
| `scripts/verify.sh`(改:第 6 步 `assertscan`) | **#320** 三类死断言判定(见下节);自检 **29 → 32** 条 |

#### 红 → 绿(逐条,红时断言输出逐字摘录)

| 编号 | 红(改前实测) | 绿(改后) |
| --- | --- | --- |
| #318 | `credential_test.go:247: Mask 出口仍含凭据明文: "AK=AKIAIOSFODNN7EXAMPLE" → "AK=AKIAIOSFODNN7EXAMPLE"`;`credential_test.go:284: 节点 output 出口仍含凭据明文: "AK=AKIAIOSFODNN7EXAMPLE"`;`credential_test.go:293: 事件流出口仍含凭据明文: {"output":"AK=AKIAIOSFODNN7EXAMPLE"}`;`credential_test.go:297: 制品出口仍含凭据明文: "AK=AKIAIOSFODNN7EXAMPLE"` | `AK=***`(键名保留)/ `+***`(行标记保留);三个出口均无明文;`AWS_ACCESS_KEY_ID=AKIA…`、`Basic <base64>`、PEM 块**不回归** |
| #319 | `in="AIDAIOSFODNN7EXAMPLE" old="AIDAIOSFODNN7EXAMPLE"`;`in="Basic dXNlcjpwYXNz" old="Basic dXNlcjpwYXNz"` | 四种 AWS 前缀 → `***`;`Basic dXNlcjpwYXNz` → `Basic ***`;**反向对照**`"Basic authentication is supported by the API."` **逐字节不变** |
| #320 | 第 6 步输出 `OK(25 个包均有真实执行并通过的用例,且测试代码里都有断言)`(隔离副本 + 三个绕过包 → **放行**);扫描器逐包:`bypassb1 total=1 live=1` / `bypassb2 total=1 live=1` / `bypassb3 total=1 live=1` | 第 6 步 `❌ VERIFY FAIL: 以下包的测试代码里没有可能触发的断言:` → `agentworkflow/bypassb1: …(零次循环)…` / `bypassb2: …(无匹配分支)…` / `bypassb3: …(未被调用的辅助函数)…`;本仓库 **21 个包**逐包 live 计数与改前**完全一致**(误报 0) |

#### 门禁第 6 步:三类"死断言"(#320)与新增的 3 条自检

**判据**(四类不可达,后两类之外的两类是本轮新增):**恒假分支**(既有)/
**零次循环**(新)/ **无匹配分支**(新)/ **未被调用的辅助函数**(新)。
报告里会写明**被哪条规则抓到**(原因列),自检据此断言**具体原因** ——
证明是被**对应那条**规则抓到的,而不是被别的规则碰巧抓到。

| # | 自检场景 | 方向 | 防的是哪个绕过 |
| --- | --- | --- | --- |
| 30 | 断言写在**永不执行**的 `for` 循环里(`for i := 0; i < 0; i++`)必须被抓 | 红 | 终审 P1-2 的 **B1**:把断言写进一个一次都不执行的循环 |
| 31 | 断言写在**不匹配**的 `switch` 分支里(`switch 1 { case 2: … }`)必须被抓 | 红 | 终审 P1-2 的 **B2**:把断言写进一个永不匹配的 case |
| 32 | 断言写在**从未被调用**的辅助函数里必须被抓;**同一条**自检同时断言"辅助函数**被调用**时不得被误报"(绿方向) | 红 + 绿 | 终审 P1-2 的 **B3**:把断言写进一个没人调用的辅助函数;绿方向防止新判据把正常代码全挡在门外 |

**跨文件口径**(误报风险最高的一处):"未被调用"按**名字在本包内出现过**判定,
且**按包收集**(包内**所有** `_test.go` 文件)—— 否则"定义在 `helpers_test.go`、
调用在别的测试文件里"的辅助函数会被误判成"从未被调用" → **门禁误红**。

**误报 0 的证据**:新扫描器与旧扫描器在本仓库 **21 个包 / 163 个测试文件**上
**逐包 live 计数完全一致**(0 处差异);真实仓库 `./scripts/verify.sh` **9/9
全绿**(第 6 步输出 `OK(21 个包均有真实执行并通过的用例,且测试代码里都有断言)`)。

**耗时对比**:扫描器自身(163 个测试文件)改前 **52ms/次** → 改后 **54ms/次**
(+2ms);门禁共调用 2 次(门禁 + 自检)→ **+4ms**;门禁总耗时 **123s**,
**+0.003%**(AST 分析的增量落在噪声里)。

### 轮次 88-89:第二十五轮(迭代 30 收口)—— git 出口脱敏 / 状态码语义 / 前端错误文案统一 / panic 兜底 / 错误文本去内部细节

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 88-89"小节(**#330-#335**)。
>
> 输入是第二十九轮的 **API 错误契约扫描**(6 条)。**纪律**:逐条**先核实**
> (复审给的是文件与行号 → 逐条读过、实测复现)→ **先写会失败的测试** → 再改
> 实现 → 逐条给出**红时断言输出**;**不写恒真测试**;**向后兼容是硬要求**
> (既有 4xx 映射一条不动,既有错误文案的变化逐条说明)。
>
> 本轮只动 `git/`、`api/`、`frontend/src/{lib,pages,test}/`、`docs/` ——
> `workflow/**`、`app/application/**`、`persistence/**`、`event/**`、`agent/**`、
> `skill/**`、`template/**`、`contextx/**`、`logx/**`、`permission/**`、
> `scripts/**`、根 `main.go` **均未触碰**。
>
> **本轮统计**(基线:HEAD `ce2e34f` + 本轮工作区改动):Go 测试文件 **171** 个 /
> 有测试的包 **22** 个 / 顶层 `func Test*` **899** 个;前端测试文件 **25** 个 /
> 用例 **302** 条;门禁 **9 步** + **32** 条自检场景(本轮未动 `scripts/**`,
> 自检条数不变)。

#### 新增 / 修改的测试(逐个文件)

| 文件 | 锁住的行为 |
| --- | --- |
| `git/mask_test.go`(**新增**,6 例) | **#330** git 包的五个出口不得返回未脱敏内容:`TestDiffMasksCredentialInFile`(凭据形态 + **低熵**两种形态;键名保留、值 → `***`);`TestDiffKeepsOrdinaryContentByteIdentical`(**可读性回归锁**:拿 `git diff` **原文**逐字节比对 —— 普通代码与路径**逐字不变**);`TestDiffMasksLowEntropyButKeepsStructure`(脱敏**不改变行数**);`TestLogMasksCredentialInCommitMessage`(commit message 里的凭据);`TestStatusMasksCredentialInPath`(文件路径里的凭据;**前置条件断言**:porcelain 原文必须真的含该路径,否则用例是假通过);`TestCommitOutputMasksCredential` |
| `api/git_mask_test.go`(**新增**,2 例) | **#330** 端到端:`GET /api/git/diff?staged=true` 与 `GET /api/git/log` 的**响应体**不得含凭据明文(走真实 `wrap` + 真实 SQLite + 真实 git 子进程;仓库建在 `t.TempDir()` 里,**不在真实仓库造带密钥的文件**) |
| `api/error_contract_test.go`(**新增**,8 例) | **#331** `TestErrorStatusMappingByKind`(**15 个 kind/code 组合**逐条断言期望状态码;既有 4xx 组合**一条没动**);`TestInternalErrorIsNotReportedAsBadRequest`(持久化失败必须 5xx);**#334** `TestWrapRecoversPanic`(panic → 500 + `{error,code,kind}` + 响应体不含 panic 原文);`TestWrapPanicAfterResponseCommitted`(响应已提交后**不得**再写错误体);**#333** `TestBadRequestDoesNotLeakGoInternals`(两个调用点:不得含 `Go struct field` / `Go value of type` / `[]string` / `AgentConfig` / `(实际 ` / `%T`);`TestReadBodyFailureIsHumanMessage`(读请求体失败 → 人话);`TestErrorBodyMasksCredentials`(错误文本里的凭据被替换为 `***`);**#335** `TestUnknownRouteErrorIsUserFacing`(404 文案不是"接口不存在",但仍说明"不存在") |
| `api/server_test.go`(**改**,1 处断言) | **#331** `TestErrorBodyAlwaysHasCodeAndKind` 的 `code < 400 \|\| code >= 500` → `code < 400 \|\| code >= 600`。**理由**:那条断言把"错误必须是 4xx"**钉成了契约** —— 而那正是缺陷本身(内部错误此前也报 400)。内部错误改 5xx 之后,继续要求 4xx 就会把**正确的 5xx** 判成失败。各 kind → 状态码的**精确**映射由 `TestErrorStatusMappingByKind` 逐条断言(那张表才是不放松的地方) |
| `frontend/src/test/toast-guard.test.ts`(**新增**,4 例) | **#332** **静态守卫**:生产代码里的 `toast.error` **只许**出现在 `lib/errors.ts`(白名单为空表);另有三条防空转用例(实现存在 / 白名单有效 / 扫描器看得见该模式) |
| `frontend/src/lib/errors.test.ts`(**改**,+2 例) | **#332** `NETWORK_HINTS` 补的两条:`AbortError` → "请求被中断(可能已取消或页面切换)";`timeout of 5000ms exceeded` → "请求超时" |
| `frontend/src/lib/api.test.ts`(**改**,+3 例 / 1 处断言变更) | **#332** 5xx 状态码有中文兜底提示(500/502/503/504 各一条);`httpText` 的非 JSON 错误体被**截断**(5000 字的 HTML → 提示 < 300 字且标注"已截断");**桌面模式 panic 归一**(`code=INTERNAL`、不含 Go 包路径与 panic 原文)。**断言变更**:原"没有 Kind(CODE) 前缀的 IPC 错误"用例断言 `code` 为 `undefined` —— 那是"桌面模式与服务器模式**形状不同构**"的旧行为,现补 `code=BAD_REQUEST` / `kind=ValidationError`(约定 10);另有 404 夹具同步为新的后端文案(#335) |
| `frontend/src/pages/*.test.tsx`(**改**,5 处断言 + 1 处 beforeEach) | **#332** 五条**精确匹配**断言的文案随 scope 变化而变(见下表);`Agents.test.tsx` 的 `beforeEach` 增加 `resetErrorDedup()` —— `reportError` 对同一个错误做 5 秒去重(防提示风暴),测试里多个用例报同一个错误时后一个会被去重掉,那是**生产行为**不是缺陷 |

#### 43 处 `toast.error` → `reportError` 的逐处清单(前端)

| 文件 | 处数 | scope(逐个) |
| --- | --- | --- |
| `pages/Agents.tsx` | 6 | 加载 Agent 列表 / 更新 Agent 权限 / 读取 Agent 配置 / 保存 Agent 配置 / 恢复默认配置 / 测试 Agent |
| `pages/Designer.tsx` | 7 | 加载工作流 / 校验工作流 / 保存工作流 / 运行工作流(+ 3 条**纯文案**提示:"校验失败: N 个问题" / "校验未通过,已阻止保存…" / "变量不是合法 JSON" —— 以字符串形式传给 `reportError`,文案**逐字保留**) |
| `pages/Workflows.tsx` | 11 | 加载工作流列表 / 创建工作流 / 运行工作流 / 切换工作流启用状态 / 复制工作流 / 导出工作流 YAML / 复制工作流 YAML / 删除工作流 / 导入工作流(+ "变量不是合法 JSON" / "剪贴板不可用" 两条纯文案) |
| `pages/ExecutionMonitor.tsx` | 5 | 加载执行详情 / 提交人工输入 / 取消执行 / 重试失败节点 / 跳过失败节点 |
| `pages/Settings.tsx` | 5 | 加载设置 / 保存设置 / 导出备份 / 恢复备份(+ "日志级别非法: …" 一条纯文案) |
| `pages/Skills.tsx` | 3 | 加载 Skill 列表 / 启用 Skill / 测试 Skill |
| `pages/EventsAudit.tsx` | 1 | 加载审计事件 |
| `pages/Executions.tsx` | 2 | 加载执行记录 / 删除执行记录 |
| `pages/GitPanel.tsx` | 2 | 提交 Git 提交 / "Git 状态/Diff/Log 查询失败"(scope 里带上失败的查询名;报**错误对象**而不是拼好的字符串 —— 这样 `reportError` 能补上错误码) |
| `pages/WorkflowHistory.tsx` | 1 | 查看工作流版本 |

#### 既有前端断言的逐条变化(全部因 scope / 错误码而变,断言**意图不变**)

| 文件:行 | 改前 | 改后 | 为什么变 |
| --- | --- | --- | --- |
| `pages/Skills.test.tsx:112` | `toHaveBeenCalledWith("启用失败")` | `toHaveBeenCalledWith("启用 Skill:启用失败")` | `reportError` 加上 scope(否则用户不知道是哪块失败) |
| `pages/Workflows.test.tsx:106` | `toHaveBeenCalledWith("权限不足")` | `toHaveBeenCalledWith("切换工作流启用状态:权限不足")` | 同上 |
| `pages/Workflows.test.tsx:128` | `toHaveBeenCalledWith("复制失败")` | `toHaveBeenCalledWith("复制工作流:复制失败")` | 同上 |
| `pages/Workflows.test.tsx:140` | `toHaveBeenCalledWith("删除失败")` | `toHaveBeenCalledWith("删除工作流:删除失败")` | 同上 |
| `pages/Workflows.test.tsx:194` | `toHaveBeenCalledWith("DSL 含未知顶层键")` | `toHaveBeenCalledWith("导入工作流:DSL 含未知顶层键")` | 同上 |
| `lib/api.test.ts:1611` | `expect(err.code).toBeUndefined()` | `expect(err.code).toBe("BAD_REQUEST")` + `expect(err.kind).toBe("ValidationError")` | 桌面模式补 code/kind 兜底(与服务器模式**同构**,约定 10)—— 旧断言钉的是"两种模式形状不同构" |
| `lib/api.test.ts`(404 夹具) | `error: "接口不存在"` | `error: "请求的资源不存在(链接可能已过期,或地址有误)"` | 夹具必须与真实后端一致(#335) |
| `pages/Agents.test.tsx` `beforeEach` | — | 增加 `resetErrorDedup()` | `reportError` 的 5 秒去重是**生产行为**;不清掉会让"断言这次确实提示了"变成假失败 |

#### 红 → 绿(逐条,红时断言输出逐字摘录;隔离副本 `git archive HEAD` + 本轮测试)

| 编号 | 红(改前实测) | 绿(改后) |
| --- | --- | --- |
| #330 | `mask_test.go:77: diff 不得含凭据明文("sk-ant-supersecret1234567890abcdef"):`;`mask_test.go:80: diff 不得含低熵凭据明文("hunter2-low-entropy"):`;`mask_test.go:154: 提交信息不得含凭据明文: "chore: rotate sk-ant-supersecret1234567890abcdef"`;`mask_test.go:183: status 原文不得含凭据明文: "## main\n?? API_KEY=sk-ant-supersecret1234567890abcdef.txt\n"` | 四个出口均无明文;键名保留、值 → `***`;**普通 diff 逐字节不变** |
| #331 | `error_contract_test.go:78: 状态码 = 400,期望 500 ({"code":"DB_ERROR","error":"PersistenceError(DB_ERROR): x","kind":"PersistenceError"}`;`…期望 502 ({"code":"AGENT_FAILED",…})`;`…期望 504 ({"code":"CLAUDE_TIMEOUT",…})`;`error_contract_test.go:102: 持久化失败应报 5xx,实际 400` | 15 个组合逐条符合映射表;既有 4xx 组合**逐字未动** |
| #332 | `error_contract_test.go:200: 错误文本泄漏内部细节 "Go struct field": json: cannot unmarshal string into Go struct field AgentConfig.extra_args of type []string`;`error_contract_test.go:200: 错误文本泄漏内部细节 "(实际 ": 权限字段 "filesystem_read" 的值不是布尔(实际 string)`;`error_contract_test.go:249: 错误文本含凭据明文: GitError(GIT_COMMAND_FAILED): git push 失败: ANTHROPIC_API_KEY=sk-ant-supersecret1234567890abcdef` | 三处调用点给人话;`wrap` 出口文本过 `logx.Mask`;前端 43 处走 `reportError`(守卫绿) |
| #333 | 同 #332 的前两条(泄漏 Go 内部细节) | 同上 |
| #334 | `panic: runtime error: index out of range [3] with length 2 [recovered, repanicked]`(栈穿 `api/server.go:423` 冒到测试进程 —— 整包测试**中止**) | 500 + `{"error":"服务内部错误","code":"INTERNAL","kind":"WorkflowError"}`;响应体不含 panic 原文 |
| #335 | `error_contract_test.go:267: 404 文案仍是开发者口径: 接口不存在` | "请求的资源不存在(链接可能已过期,或地址有误)";`code` 仍是 `NOT_FOUND` |

#### 端到端(真实 server 进程)

本轮用**真实 server 进程**复核 `GET /api/git/diff`:起 `go run .`(server 模式,
数据目录与 Git 工作目录都在 `mktemp -d` 造的**隔离目录**里),把工作目录指向
一个含 `ANTHROPIC_API_KEY=sk-ant-…` 的临时仓库,请求 `/api/git/diff?staged=true`
→ 响应体**不含**明文(值被替换为 `***`)。**不在真实仓库里造带密钥的文件,
不调用任何会写真实仓库的 git 接口**。

### 轮次 91:第二十八轮(迭代 32 收口)—— 门禁断言扫描的 4 个新盲区(#341)

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 91"小节(**#341**)。
>
> 本轮是**文档收口轮**:输入是**第三十二轮终审复审**的缺陷清单
> (1 条 P1 + 一份"可正式关闭的遗留项"清单)。**代码侧**的修法在
> `scripts/verify.sh`(门禁断言扫描),由**姊妹代理**落地(它拥有
> `scripts/**`);本轮由文档代理负责**核实 + 登记 + 文档**。
>
> **本轮统计**(基线:HEAD `a0549c4` + 迭代 32 在途改动):Go 测试文件
> **171** 个 / 有测试的包 **22** 个 / 顶层 `func Test*` **904** 个;前端
> 测试文件 **25** 个 / 用例 **302** 条;门禁 **9 步** + **36** 条自检场景
> (本轮 **+4** 条,见下)。

#### 本轮的新增 / 修改(逐个文件)

| 文件 | 锁住的行为 |
| --- | --- |
| `scripts/verify.sh` 的 `assertscan`(**改**,+4 条自检场景) | **#341** 门禁第 6 步的断言扫描**不再被骗过**:① **恒假分支里的调用点**不算可达引用(`if 1 != 1 { helper(t) }` → helper 判为不可达);② **函数字面量**里的断言要求该字面量**确有调用点**(立即调用 / 赋值后被调用 → live;赋值后从未被调用 → **不** live);③ **无匹配 switch 分支**里的调用点不算可达引用;④ **链式**辅助函数按**不动点迭代**传递可达性(outer 不可达 → outer 的调用点不计 → inner 也不可达)。**自检 32 → 36**:B4 / B5 / B6 / B7 各一条**红**场景(断言**具体原因**,证明是被**对应那条**规则抓到),每条同时断言**绿方向**(正常写法不得被误报)。**判据从"存在性"升级为"可达性"** —— 这正是上一轮(#320)留下的残余 |

#### 隔离复现的手法(本轮自建,可复跑)

上一轮的复现手法是"改坏实现 → 跑真门禁",本轮**更轻**:把 `assertscan`
的 **Go 源码段**从 `scripts/verify.sh` 里抽出来(`awk` 取
`ASSERTSCAN_GOEOF` 之间的部分)→ 单独编译成一个二进制 → 造绕过包 →
用同一份 manifest 喂给**新旧两个**扫描器,逐包比对
`total` / `live` / 不可达原因。

**为什么这样做**:门禁第 6 步的判据就是这一个二进制的行为;把判据单独
拎出来,可以在**秒级**内跑几十种绕过形态,而不必每次都起整条 9 步门禁
(≈120s)。**代价**:抽出来的源码与脚本里的那份必须**逐字一致** —— 否则
测的是"另一个程序"。本轮的核对方式是:抽出的段数与脚本里的段数一致,
且**同一份 manifest**下新旧两个二进制给出**可解释的差异**。

#### 本轮的**红 → 绿**证据(逐条)

| 编号 | 红(旧扫描器) | 绿(新扫描器) |
| --- | --- | --- |
| #341 | `bypass1 1 1`(辅助函数**只在恒假分支**里被调用)/ `bypass2 1 1`(断言在**从未被调用的函数字面量**里)/ `bypass3 1 1`(辅助函数**只在无匹配 switch 分支**里被调用)/ `bypass4 1 1`(**链式**:恒假分支调 outer → outer 调 inner)—— **live 全等于 total,四道判据同时被骗过**;4 个包 `go test` 全部 `ok` | `bypass1 1 0 未被调用的辅助函数` / `bypass2 1 0 从未被调用的函数字面量` / `bypass3 1 0 未被调用的辅助函数` / `bypass4 1 0 未被调用的辅助函数` —— **四个包全被抓,原因与形态一一对应** |

**误报 0 的证据**(本轮实测):新扫描器与旧扫描器在本仓库 **22 个包 /
171 个测试文件**上**逐包 live 计数完全一致**(0 处差异;总计 **3304** 条
断言,全部 live)。

#### 本轮**仍然开着**的两处(有意保留的边界)

这两处**不是**本轮的 4 个盲区(它们在改前就已是 live),本轮**没有**
扩大判据去堵它们 —— 扩大判据会带来**误报**(门禁红),而门禁误报的代价
高于漏报(#290 的口径):

1. **函数字面量作为参数传给别的函数**(典型:`t.Run("x", func(t *testing.T){…})`)
   → 静态判不出调用与否 → **保守记为 live**。
2. **辅助函数以"函数值 / 方法值"形态被传递**(`var f func(*testing.T) = helper`)
   → 这类引用**不是调用点** → 保守记为"出现过" → 偏保守。

**这与 #320 登记的"残余"是同一族**(见 `docs/OPTIMIZATION.md` 第十四节
第 76 条),口径一致。

#### 本轮**关闭**的遗留项(终审复审实测确认)

终审复审给出一份"可正式关闭的遗留项"清单,本轮**逐条核实**后划掉。
**核实方式**:读代码 + 跑对应回归用例(逐条结果见
`docs/OPTIMIZATION.md` 第十三节末"轮次 91"小节的同名表)。

| 原遗留项 | 编号 | 核实方式(本轮实测) | 结论 |
| --- | --- | --- | --- |
| 第 19 轮 P0「跨节点上下文链是死的」 | #270 | `workflow/runtime/context_chain_test.go`(**13 例**)全绿;五个槽位各自到达下游 + `{{last_output}}` + 条件表达式不受影响 + **仓库自带示例工作流 `workflows/examples/coding-task.yaml` 的方案正文确实传到了 review 节点** | **已关闭** |
| 第 28 轮 P2-1「`submit` 产物静默丢弃、`last_output` 留陈旧值」 | #321 | `TestSubmitOutputReachesDownstreamLastOutput` / `TestEveryValidModeHasContextSlot` → PASS | **已关闭** |
| 第 29/30 轮 P1「`AKIA` 键值粘连形态从 6 个出口泄漏」 | #318 | `TestMaskCoversKeyValueGluedCredentialShapes`(7 形态 × 2 入口)→ PASS;git 出口单列归 #330 | **已关闭** |
| 第 28 轮 P3「`permission.Manager.Apply` 静默死入口」 | #323 | `TestManagerHasNoSilentApplyEntry`(反射守卫)→ PASS | **已关闭** |
| 第 27 轮 P1-1「`NaN` 连坐清空变量表」 | #306 | `TestSaveWorkflowRejectsNonFiniteVariables` / `TestSaveWorkflowKeepsValidVariablesWhenOneValueIsBad` → PASS(坏值**不连坐**旁边合法值) | **已关闭** |
| 第 26 轮 P1「SSE 慢客户端用例 5s 墙钟阈值脆弱」 | #322 | `TestSSESlowClientDoesNotBlockProducer`(实测 0.44s)/ `TestSSESlowClientQueueOverflowDoesNotBlockProducer`(0.23s)→ PASS,**远离任何阈值** | **已关闭** |
| 第 30 轮 P1「门禁第 6 步断言强度不足(3 种手法)」 | #320 | 3 种已堵;**残余的 4 个新盲区**由本轮 **#341** 堵住 | **部分关闭 → 已全部关闭** |

### 轮次 84:第二十轮(迭代 21 收口)—— mode 取值域单一来源 / 取消收尾 / PAUSED 移除 / 统计分列 / 死列裁决

> 登记于 `docs/OPTIMIZATION.md` 第十三节末"轮次 84"小节(#291-#296 全部关闭)。
>
> 本轮是**清单收敛轮**:五个缺陷形状不同,根子同一条 —— **同一份事实散落
> 多处、各自演化**。新增 Go 顶层用例 **+24 例**、**新增测试文件 9 个**
> (141 → **150**),顶层 `func Test*` 790 → **814**;前端 **+4 例**
> (268 → **272**)。

| 文件 | 锁住的行为 |
| --- | --- |
| `workflow/validator/mode_domain_test.go`(**新,3 例**) | **非法 mode 必须被校验期拒绝**(#291):`exectue` / `banana` / `PLAN` / `"execute "` / `skill` 全部报 `INVALID_NODE`;**空 mode 报 `MISSING_MODE` 且不再报 `INVALID_NODE`**("没填"与"填错了"是两个问题);**取值域内的 mode 一律放行**(校验期不得把用户挡在合法工作流外面) |
| `workflow/runtime/mode_domain_test.go`(**新,3 例**) | **运行期对非法 mode fail closed**(#291):`exectue` 的节点 **FAILED**、错误点名该 mode、**Agent 一次都没被调用**;**未知 mode 的写权限检查不得被跳过**(写权限被收紧时失败原因必须是 `fs:write` —— 锁住"写权限检查在前、非法 mode 判定在后"的顺序);**合法 mode 照常执行**(fail closed 不能变成"一律拒绝") |
| `workflow/runtime/mode_contract_test.go`(**新,3 例**) | **model ↔ contextx 的漂移守卫**(#291):每个 mode 的产出必须写进 `model.ModeSpec` 声明的槽位(声明"不进上下文链"的 mode 必须什么都不写);**10 个槽位键名两边必须一致**(键名是写进 `state_json` 的持久化契约,漂移会让存量数据读不出来);**`skill` 是伪 mode** —— 不在 agent 的取值域里,但上下文层仍按伪 mode 处理(不许被"收敛取值域"顺手踢出上下文链) |
| `workflow/runtime/cancel_skip_test.go`(**新,2 例**) | **取消后节点必须收尾**(#294):取消一个等待人工的执行后,`approve`/`execute`/`submit` 全部落 **SKIPPED**,而已完成的 `plan` 保持 **SUCCESS**(取消不改写历史);**`execution_nodes` 明细行同步**(仍是"运行中/等待中"的行必须变 SKIPPED —— 同一份事实的两个出口) |
| `workflow/model/execution_state_test.go`(**新,1 例**) | **`PAUSED` 不可达**(#295):既不能迁入也不能迁出(它是**曾经存在**的状态,现在状态机里没有) |
| `workflow/compiler/taskkey_contract_test.go`(**新,2 例**) | **任务键只有一份来源**(#292):`compiler.KeyTask` 必须等于 `contextx.StateKeyTask`,且键名本身必须是 `"task"`(`state_json` 存量数据用的是它) |
| `persistence/stats_test.go`(**新,2 例**) | **统计分列 + 各列之和 == 总数**(#293):`failed` 只算 FAILED、`cancelled` 单列、running 含 CREATED;**遗留 `PAUSED` 行归入 cancelled**(否则它既不在任何一列里又计入总数,表格里的数字加不起来) |
| `persistence/legacy_state_test.go`(**新,3 例**) | **遗留数据的读法**(#295):`state='PAUSED'` 的行读出来是 **CANCELLED**、**不出现在"运行中列表"**;`NormalizeExecutionState` 的逐值契约 |
| `persistence/dead_columns_test.go`(**新,5 例**) | **死列裁决 + 老库兼容**(#296):新库**不得**再建 `agent_configs.{name,enabled,updated_at}` / `skill_configs.updated_at` / `workflow_versions.seq`(保留下来的列必须还在);**老库(本轮之前的 schema)必须照常能写**(老库的 `name`/`updated_at` 是 NOT NULL 且**无默认值** → 不写就违反约束);**新库也要能写**;两个新读点(`GetWorkflowVersionDSL` / `ListWorkflowVersionMeta`)返回原文与落库时间;版本不存在 → `NOT_FOUND` |
| `agent/mode_test.go`(**改,+1 例**) | **fail closed 语义**(#291):未知/空/`exectue` **一律按"会写"处理**;`agent.ModeRequiresWrite` 与 `model.ModeRequiresWrite` **逐个 mode 一致**(单源回归锁) |
| `workflow/model/model_test.go`(**改**) | 状态机用例里**删掉** PAUSED 的 4 条转换,并把"不能迁入 PAUSED"补进 `denied` 列表(断言方向反转:此前是"允许") |
| `persistence/exec_state_guard_test.go`(**改**) | 守卫与状态机的关系用例里**删掉** `ExecutionPaused`(它不再是状态机里的状态) |
| `frontend/src/pages/Dashboard.test.tsx`(**改,+3 例**) | **取消与失败分列**(#293):Cancelled 单独成卡成列;**成功率的分母排除被取消的执行**(6÷8=75%,而不是 6÷10=60%);**旧后端(没有 `cancelled` 字段)不出现 NaN%** |
| `frontend/src/components/state-badge.test.tsx`(**改,+1 例**) | **`PAUSED` 回退为原始文本**(#295):该状态已从状态机移除,前端不再有它的专属徽章 |

**红 → 绿证据(隔离副本实测旧代码)**:

| # | 旧代码上的实测输出 | 现在 |
| --- | --- | --- |
| #291 校验期 | `mode="exectue" 应当判非法,实际 valid=true(无任何错误)`(5 个取值全部放行) | 报 `INVALID_NODE` |
| #291 运行期 | `非法 mode 的节点必须失败,实际状态: SUCCESS(错误: "")` | FAILED 且点名 mode |
| #291 写权限 | `未知 mode 必须先过写权限检查,实际状态: SUCCESS(错误: "")` —— 写权限检查**被跳过** | 先撞 `fs:write` |
| #291 fail closed | `ModeRequiresWrite("") = false, want true` / `("unknown") = false` / `("exectue") = false` | 一律 true |
| #293 统计 | `failed 应只算 FAILED(=1),实际 2(CANCELLED 被混进了 failed 列)` | 分列,各列之和 == 总数 |
| #294 取消收尾 | `取消后节点 "approve" 的状态 = "WAITING", want SKIPPED` / `节点明细 "approve" 仍是 WAITING` | 全部 SKIPPED |
| #295 PAUSED | `不应允许迁移到 PAUSED:该状态不可达` ×4(状态机仍认它) | 状态机里没有它 |
| #296 死列 | `agent_configs.name 是只写不读的列,新库不该再建它` ×5 | 新库无死列,老库仍可写 |
| #293 前端 | `Unable to find an element with the text: 75%`(旧 Dashboard 算的是 60%) | 75%,分母排除 cancelled |

**本轮验证**:`gofmt -l` 干净、`go vet` 干净、`go test -race -count=1 ./...`
**21 个包全绿**(exit 0)、vitest **272/272**、`./scripts/verify.sh`
**9 步通过**(实测 **112s**)、`docs/check-numbering.sh` 通过(缺陷号区间
1..296 连续)。

## 门禁第 6 步:伪造输出绕过 —— 为什么不能用文本解析

> 这是本项目**最独特的一条工程教训**,单独成节。
> 登记于 `docs/OPTIMIZATION.md` #266;修法在第十五轮(已进主干,commit `908ba0b`)。

### 发生了什么

门禁第 6 步要回答一个问题:**"这个包有没有**真正执行过**的用例?"**
—— 因为一个只含 `t.Skip()` 的包,或一个只有测试文件、没有 `Test` 函数的包,
会让"覆盖率悄悄归零"这件事**对门禁完全隐形**(#252)。

第十三轮(#252)把判据做成了:统计**顶层** `--- PASS:` / `--- SKIP:` 行,
并要求同时看到该包自己的 `=== RUN` 行。

**但 `=== RUN` 与 `--- PASS:` 都只是"一行文本"**,而**文本是用例自己可控的**:

```go
func TestForged(t *testing.T) {
	fmt.Println("=== RUN   Forged")
	fmt.Println("--- PASS: Forged (0.00s)")
	t.Skip("环境不具备")   // 真实结果:什么都没验证
}
```

这个包的**真实结果**是"一条用例被跳过",但在文本口径下它看起来像是
"跑过一条并通过"的包。上一轮复审**实测**:伪造包 RUN=2 / PASS=1 / SKIP=1
→ **门禁放行**。第十三轮补的"必须看到该包自己的 `=== RUN` 行"被
**同一手法**击穿 —— 因为那条 `=== RUN` 正是伪造出来的。

### 根因:判据用了一个"被检查方可控"的信号

这一类缺陷在本项目里出现过**三次**,形状完全一样:

| 判据 | 被检查方能控制什么 | 后果 |
| --- | --- | --- |
| 取消看**进程内句柄**是否存在 | 句柄由执行协程的 `defer` 摘除,与**库里的状态**在几百微秒内不一致 | 200 假成功(#264) |
| 掩码看**参数名**是否指向凭据 | 参数名是用户写的,凭据可以藏在名字不敏感的选项里 | 明文外泄(#265) |
| 门禁看**输出文本**里有没有 `--- PASS:` | 文本是用例自己 `Println` 出来的 | 全门禁放行(#266) |

**共同的修法**:换一个**检查方持有、对方无法伪造**的权威事实 ——
库里的状态(`executions.state`)、值的**形态**规则(`logx` 唯一来源)、
**测试框架的事件**(`go test -json` 的 `Action`/`Test` 字段)。

### 修法:判框架事件,不看输出文本

`go test -json` 每行是一个扁平 JSON 对象,框架给每个用例发一条事件:

```json
{"Action":"run","Package":"p","Test":"TestX"}      // 用例开始
{"Action":"pass","Package":"p","Test":"TestX"}     // 顶层用例通过
{"Action":"skip","Package":"p","Test":"TestX"}     // 顶层用例被跳过
{"Action":"pass","Package":"p","Elapsed":0.007}    // 包整体通过(Test 为空)
{"Action":"output","Package":"p","Test":"TestX","Output":"...\n"}  // 只是输出
```

**达标 ⇔ 存在 `Action=="pass"` 且 `Test` 非空、且 `Test` 不含 `/`(顶层用例)的事件。**

伪造为什么失效:用例自己 `fmt.Println` 出来的
`=== RUN   Fake` / `--- PASS: Fake (0.00s)` 在 JSON 里只会落进
`Action=="output"` 事件的 `Output` 字段 —— **框架不会为它生成 `pass` 事件**。
判据只数框架事件、不看输出文本,伪造就失效了。

**只看顶层用例**:子用例的 pass 事件(`Test="TestX/sub"`)**不代表**这个包
有真用例 —— 一个"只跑了个子用例就自己 `t.Skip`"的包照样能拿到 pass 事件。
第十三轮的文本口径只认行首(不缩进)的 `--- PASS:` 行,也就是只认顶层用例;
这里保持同一口径,不顺手放宽。

**JSON 字段取值为什么可以"暴力"地找**:每行是**扁平** JSON 对象,字段用 `,`
分隔,而**值里的引号一定被转义成 `\"`**。于是值内部不可能出现未转义的 `"`
→ 序列 `,"键":"` 只可能出现在真正的键位置;用例自己打印的 `"Action":"pass"`
在 JSON 里是 `\"Action\":\"pass\"`(中间夹着反斜杠)→ **不含** `"Action":"`
这个子串 → 骗不过解析。**取不到字段 → 该行被忽略 → 统计偏保守 → fail closed。**

**两段式执行(为了省时间)**:① **窄化运行** —— 用文本日志里每个包的第一条
`=== RUN` 名做 `-run` 模式,每个包只跑 1 个用例(框架事件照样权威:
出现顶层 pass 事件就证明这个包真的有通过的用例);② **兜底运行** ——
窄化没能证明的包(候选用例被跳过 / 名字对不上 / 包没跑起来)整包再跑一次
JSON,拿完整事件。判据始终是"框架顶层 pass 事件";两段结果合并后统一判定。
**定原因只能用兜底运行的完整事件**(窄化运行的事件是残缺的 —— 例如候选用例
被单独跑时失败、整包跑却是过的)。

**fail closed**:工具链缺失 / 输出为空 / 解析不出事件 → **一律判红**,不静默跳过。

### 防回归:自检第 17 条场景

判据换了,但"判据本身有没有被证明过"是另一回事(第十四轮 #259 的教训)。
第十五轮在第 6 步自检里加了**第 17 条场景**:

```
selfcheck_ok "第 6 步判定:伪造 '=== RUN'/'--- PASS' 输出的包必须被抓(用例自己打印的行不是框架事件)" \
	usage_expect "awo/usagecheck/fake" "$USAGE_REPORT" "yes"
```

隔离模块里造一个 `fake` 包(用例被 `t.Skip` 跳过,却自己打印框架格式的行)
→ **必须**出现在报告里。`SELFCHECK_EXPECTED` 同步 **16 → 17**
(场景计数守卫:少一条即红,防"自检被删")。

## 可执行覆盖矩阵(形态 × 出口)—— 本项目最独特的一种回归锁

> 位置:`logx/credential_test.go` 的 `TestCredentialShapeCoverageMatrix`。
> 登记于 `docs/OPTIMIZATION.md` #247。

**为什么需要它**:第十一轮终审实测到的 P0 泄漏,形态是"**裸值形态 × 节点
输出**"这一个**组合**。此前**没有任何断言**能发现它:

- 规则本身是对的 —— 但改规则(调门槛)不会让任何测试变红;
- 少一条规则也不会让任何测试变红 —— 因为每个形态只在**它当时被测的那个
  出口**上测过;
- 于是"规则覆盖了哪些形态 × 哪些出口"这件事,一直是**没人断言的事实**。

**形态怎么来的**:出口在代码里收敛成**两个入口**,矩阵就是这两个入口的笛卡尔积:

| 入口 | 覆盖的出口 |
| --- | --- |
| `logx.Mask` | 日志 / 节点 `output` / `state_json` / 事件流 / 执行详情 / 导出 |
| `logx.MaskCredentialValues` | 制品(`git diff`)—— 只看**值形态**,保住代码可读性 |

**矩阵(13 形态 × 2 出口 = 26 格,逐格登记期望)**:

| # | 形态 | `Mask` | `MaskCredentialValues` | 备注 |
| --- | --- | --- | --- | --- |
| 1 | `API_KEY=sk-xxx`(key=value) | 脱敏 | 脱敏 | 键名 + 值形态双命中 |
| 2 | `sk-ant-xxx`(裸值) | 脱敏 | 脱敏 | 已知前缀 |
| 3 | `ghp_xxx`(裸值) | 脱敏 | 脱敏 | 已知前缀 |
| 4 | `AKIA…`(裸值) | 脱敏 | 脱敏 | AWS 访问键形态 |
| 5 | `eyJ…`(裸 JWT) | 脱敏 | 脱敏 | JWT 三段式 |
| 6 | `Bearer eyJ…`(裸方案词) | 脱敏 | 脱敏 | **无键名**的方案词 |
| 7 | `Authorization: Bearer eyJ…`(头形态) | 脱敏 | 脱敏 | |
| 8 | `Authorization: Bearer abc123xyz`(短 token) | 脱敏 | 脱敏 | 头形态下不套用高熵门槛 |
| 9 | `Basic <base64>`(头形态) | 脱敏 | 脱敏 | |
| 10 | PEM 私钥块 | 脱敏 | 脱敏 | 多行块 |
| 11 | `password: hunter2`(低熵) | 脱敏 | **不脱敏** | 见下方取舍 |
| 12 | `token=abc123 secret=xyz789`(低熵) | 脱敏 | **不脱敏** | 见下方取舍 |
| 13 | 高熵长串(无前缀无键名) | 脱敏 | 脱敏 | 裸值门槛 ≥24 字符 |

**"未覆盖的格子必须写清理由"** —— 这是矩阵的**核心纪律**:第 11、12 两格
的 `why` 字段写明"制品出口只看值形态:低熵值不呈凭据形态 → 不脱敏;这是
第十轮的取舍(diff 里 `password = "xxx"` 多半是代码/占位符,抹掉会毁掉审计
证据);DB / API / 日志出口走键名规则,照常脱敏"。

**这条纪律的意义**:将来谁改了高熵门槛或删了某条规则,矩阵**立刻变红**,
而**每一条"未覆盖"都必须被重新论证一遍** —— 取舍不能靠记忆维持。

> **第十三轮补上的第三个入口**:`logx.MaskValue`(带键名线索的递归脱敏)
> 是 `AgentConfig.Masked()` 处理 `behavior` 字段时用的入口(#255)。它**不在**
> 本矩阵的两列里 —— 矩阵的两列是"日志 / 状态"与"制品"两条口径,
> `MaskValue` 走的是**第三条**(结构化键名 + 值形态)。
> **待办**:把 `MaskValue` 作为矩阵的**第三列**补进来,或写清"为什么它不需要
> 单独一列"(**第十六轮仍未做** —— `MaskValue` 的键名规则与 `Mask` 共用同一份
> `keySensitivity`,而值形态规则共用 `maskCredentialShapes`;矩阵的两列已经
> 分别覆盖了这两条规则,但"结构化出口"这一列仍然缺一个可执行的矩阵)。
>
> **另一条与矩阵同源的发现**(#263,**第十六轮已关闭**):本节与
> `docs/OPTIMIZATION.md` #246 曾把 `TestMaskValueShapeRuleIsSingleSource`
> 记成"断言两个入口逐字节一致" —— **它其实恒真**:该断言比对的是
> `MaskCredentialValues(in)` 与 `MaskCredentialValues(in)`
> (**同一个函数比较两次**),永远不会失败。也就是说"值的形态规则只有一份"
> 这件事**没有被断言保证**(今天 `agent.MaskCredentialValues` 确实只是薄封装,
> 但那是**读代码读出来的**,不是**测试锁住的**)。
> **第十六轮(迭代 17)已关闭**:① 跨包逐字节比对搬到
> `agent/credential_mask_test.go`(`agent` 依赖 `logx`,**logx 反向 import
> agent 会成环** → 只能写在能同时 import 两边的包里):
> `TestMaskCredentialValuesMatchesLogxSingleSource`;② `logx` 侧同名用例
> 改成比对**两个不同的入口**(`Mask` vs `MaskCredentialValues`);③ 防空转守卫
> `TestSingleSourceAssertionIsNotDegenerate`(函数对象 + AST)。
> **造坏验证(隔离副本)**:把 `logx.MaskCredentialValues` 改成原样返回 →
> **旧断言照样绿**(恒真的直接证据)、新断言红。
>
> **这与约定 13(同源)的关系**:约定 13 要求"同一件事只有一份定义"。
> 本轮把它推广到**脱敏规则**这条轴:此前 `logx.Mask`(按键名)与
> `agent.MaskCredentialValues`(按值形态)是两套实现,各覆盖一半,"出口用
> 哪套就漏另一半"。现在值的形态规则只有 `logx/credential.go` 一份,
> `agent` 侧是薄封装 —— 而"薄封装"这件事从第十六轮起**由测试锁住**
> (`agent.MaskCredentialValues(x)` 与 `logx.MaskCredentialValues(x)`
> 对 23 条输入逐字节相同)。

## 失败台账(并发路径的唯一事实源)

> 位置:`workflow/runtime/failure_ledger.go`(第十三轮,commit `aa88aee`)。
> 登记于 `docs/OPTIMIZATION.md` #250。
> **这一节记录的是一类"通道会丢信息"的缺陷** —— 不是并发写坏了数据,
> 而是**失败原因经过的那条通道本身是有损的**,于是真因被"失败之后发生的
> 另一件事"顶掉。

### 症状

`TestPanicInsideParallelBranchDoesNotKillProcess` 在 `-race` + 负载下
**3 次跑挂 2 次**。失败原因不是断言写错,而是**失败原因本身不对**:
用户看到的是 `context canceled`(兄弟分支被级联取消),而真正 panic 的那个
节点的原文**一个字都没留下**。约定 14 的味道:**真因被掩盖**,用户与维护者
都看不到真正的失败原因。

### 根因:失败原因住在一条有损通道上

失败信息此前只存在于 **ADK 事件的 `StateDelta`** 里,而事件投递是**有损**的:

- `parallelagent` 的分支协程用 `select` 在 `<-ctx.Done()` 与 `results <- …`
  之间选;两者**同时就绪**时 Go 的 select **随机**选一个。
- 并行区的级联取消(compiler/lower.go 的 `parallelCascadeAgent`)在任一分支
  失败后取消共享 ctx → 选到 `ctx.Done()` 那一支 → 该分支的**终态事件
  永久不到 runner**。
- 节点状态的唯一来源就是**事件里的 `StateDelta`**(ADK 的 `session.State`
  只是 runner 手里的**副本**;引擎收尾时读的是 session service 里那份,
  只有 `AppendEvent` 的 `StateDelta` 才会写进去)→ **事件一丢,失败信息整段消失**:
  - 没有 `_abort` → 引擎退到"ADK 报的最后一个错误"= 兄弟分支的
    `context canceled`,**真因被彻底掩盖**;
  - 没有 `_error` → 用户看到一句空错误;
  - 没有 `node:<id>:status` → 节点状态表里根本没有那个节点,用户看到的是
    "失败,但没有节点失败"。

**一句话**:失败原因是**副作用**,而副作用走的通道会丢。

### 修法:三层(记 / 纠 / 滤)

| 层 | 做什么 | 为什么必须是这一层 |
| --- | --- | --- |
| **记** | 引擎在 `ExecuteNode` 的 `defer` 与失败分支上**自己留底**:节点失败即 `record(execID, nodeID, reason, cancelled)`。**引擎是唯一见过节点 outcome 的地方**(compiler 只把 outcome 变成事件,事件可能丢)→ 台账是**唯一事实源** | 不记 → 没有可对照的"真相";靠事件补不回来 |
| **纠** | `repairFailureState` 用台账**纠正会话状态**的三种坏形态:① **缺**(失败分支的终态事件被丢掉,`_abort` / `_error` / `node:<id>:status` 整段没有);② **串位**(被取消的兄弟分支的 escalate 事件先落地,把 `_abort` 写成 `FAILED:<被取消的兄弟>`、`_error` 写成 `context canceled`);③ **被别的中断盖住**(同一并行区里另一个分支在等人工,`_abort` 变成 `WAIT_USER`,真正失败的那个分支的事件丢了 → 执行停在"等用户输入"上,**明明已经失败却没人被告知**) | 只记不纠 → 台账对了,但用户看到的还是错的 |
| **滤** | `secondaryFailure` 用 **`errors.Is` 穿透 `%w`** 剔除**派生错误**(`context.Canceled` / `context.DeadlineExceeded`):派生错误是"失败**之后**又发生了什么",不是"为什么失败" | 用字符串匹配会被 ADK 的 `fmt.Errorf("failed to run sub-agent %q: %w", …)` 骗过 |

### 三条设计口径

1. **取消 ≠ 失败**:台账只记**确凿失败**(`cancelled == false`)。ctx 已取消 =
   用户取消 / 超时 / 兄弟分支失败后的级联取消 —— 不是"这个节点坏了"。
   把取消也算成失败,真因照样会被 `context canceled` 顶掉。
2. **确定性**:`list()` **按节点 ID 排序**返回。并行分支可能同时失败,
   收集到哪一个**不该取决于调度顺序** —— 否则同一个工作流两次运行给出的
   失败原因不同,没法比对、没法复现(这条是回归锁能写死的前提)。
3. **失败必须给出可读原因**:原因不允许为空 —— 空原因会让用户只看到
   "失败"两个字,无从下手。

### 为什么这是"并发路径的唯一事实源"的做法

一般的并发修法是"加锁 / 加原子 / 加 channel",把竞态**消掉**。但并行区的
级联取消是**设计如此**(一个分支失败就要停掉其余分支),**消不掉** ——
被取消的兄弟分支**一定会**产生 `context canceled`。所以这条路径的正确修法
不是"让竞态不发生",而是:

> **承认通道会丢,把"谁失败了、为什么"记在引擎自己手里(唯一见过 outcome
> 的地方),再用它去纠正那条有损通道的投影。**

这条思路可以推广到任何"结果要穿过一条会丢 / 会乱序 / 会被别人覆盖的通道"
的场景:**先在唯一见过真相的地方留底,再把真相投影回去**。本项目里同样的
做法还出现在 `state_mask.go`(落库侧与读出口各过一次,幂等)与
`logx/credential.go`(脱敏规则的唯一来源)。

### 验证

- `workflow/runtime/parallel_failure_test.go`(**447 行**,新增):真因必须在
  失败原因里、必须指向真正出问题的节点、兄弟分支被取消不得记成失败、
  两次运行原因相同。
- `workflow/runtime/panic_test.go`(收紧):原本只有 `t.Logf` 的用例改成
  **真断言**(失败原因必须说明 panic、**不得含** `context canceled`)。
- 稳定性:`go test -race -count=20 ./workflow/runtime/...` **0 失败**
  (修复前 3 跑挂 2)。

## 前端测试稳定性(同步点错误与类级守卫)

> 登记于 `docs/OPTIMIZATION.md` #248 / #249。
> **这一节记录的是一类"测试自己会骗人"的缺陷** —— 不是被测代码有 bug,
> 而是**测试的同步点选错了**,于是测试在大多数时候通过、偶尔失败。

**症状**:`frontend/src/lib/api-desktop-stream.test.ts` 实测**全量连跑 20 次
挂 1 次**(`expected "vi.fn()" to be called at least once`),并行时更容易撞上。

**根因**:该用例用

```ts
await new Promise((r) => setTimeout(r, 0));   // ×2
```

当"异步链已经跑完"的**同步点** —— 也就是**猜事件循环要跑几个节拍**。而
`ensureEventStream()` 的异步链要跨几个节拍**不是常数**(vite 的 `resolveId`
命中缓存时 1 拍、需要 fs 往返时 2 拍)→ **余量为 0**。根子还在
`ensureEventStream` 是 **fire-and-forget**,**没有可 await 的句柄** —— 于是
测试只能去猜。

**修法(同步点换成"效果本身")**:不再等节拍,而是等**效果被观察到** ——
`toast.error` 被调用(由效果 resolve)。节拍数不再进入断言。同批还做了一件事:
`vi.hoisted` + 每个用例**重装 mock**,消除用例间的顺序依赖;`mockReset` 取代
`mockClear`(后者只清调用记录、**不清实现**)。

**类级守卫**(`frontend/src/test/async-determinism-guard.test.ts`,3 组):

| 守卫 | 锁住的行为 |
| --- | --- |
| 真实计时器不得当同步点 | **扫描全部前端测试文件**,禁止 `new Promise` 里含 `setTimeout`;白名单里的例外必须**真的存在**且**写清理由**(防空转:扫描器失效时白名单也一起失效) |
| 扫描器自身防空转 | 扫描器必须**看得见**这种写法(合成源码命中计数必须正确),且**不误伤**合法写法(`new Promise(resolve => { onDone(resolve) })`、`vi.advanceTimersByTimeAsync`) |
| 手动 mock 的缓存语义 | `vi.resetModules()` **不会**让手动 mock 的工厂重新求值,真正会的是 `vi.doMock()` —— 不钉住这条,`api-desktop-stream.test.ts` 会**静默退化成恒真测试**(断言照样过,但它已不再验证"导入失败会被报出来") |

**稳定性实测**:连跑 20 + 压测 24 + shuffle 8 + 抢资源 3 = **56 次 0 失败**。
**判断**:保留并行(串行慢 2.5 倍且会**掩盖**问题 —— 串行下竞态更难复现)。

> **这一节的通用教训**:当一个动作**没有可 await 的句柄**时,用"等 N 个
> 事件循环节拍"当同步点 = 用**运气**换通过率。要么给动作一个句柄
> (`ensureEventStream` 返回一个 Promise),要么**等效果**(`toast.error`
> 被调用)。前者要改生产代码,后者只改测试 —— 但后者要求"效果"可观测。

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

## 第八~十轮实测记录(轮次 61-70)

> 这一节记的是**第八、九、十轮**真正做过的"改坏实现 → 跑门禁 → 记录红/绿"
> 实验。与前面的"终审实测记录"同一套方法(隔离副本、逐条注入变异、还原后
> 复跑确认变绿),但对象是后三轮新增的防线。

### 门禁红/绿对照实验(第十轮)

| 注入的变异 | 旧门禁(9 步,不含 `go vet`) | 新门禁(含 `GOOS=windows go vet`) |
| --- | --- | --- |
| 新增 `agent/zz_windows_test.go`(`//go:build windows`,内含 `var s string = 42`) | ❌ **exit 0 全绿(79s)** —— `go build` 手册明文 "build ignores files that end in `_test.go`" → 平台专属**测试文件**里的类型错误**永久不可见** | ❌→✅ **exit 1,第 4 步红** —— `go vet` 连同 `_test.go` 一起做类型检查 |

**结论**:这不是"多跑了一条命令",而是补上了一整类**从未被任何步骤覆盖**的文件。

### 门禁自检(迭代 11 加的机制,第十轮落地)

> **问题**:门禁第 4 步写着 `GOOS=windows go vet`,但**没有任何机制证明它真的
> 能抓到错误**。将来有人把 `vet` 从这一步删掉 —— 门禁照样绿,盲区静默回来,
> 而且**没有任何用例会发现**。
>
> **做法**:在 `/tmp` 里造一个**隔离的最小模块**(不碰工作区),用**与真正的
> 门禁步骤同一条代码路径**(`win_check` 函数)依次验证三件事:

| # | 造什么坏文件 | 期望 | 为什么必须有这一条 |
| --- | --- | --- | --- |
| 1 | **对照组**:类型**正确**的 `//go:build windows` 测试文件 | **必须通过** | 没有这一条,下面两条就是恒真断言 —— "vet 无论对错都失败"也会被算成"成功" |
| 2 | 含类型错误的 `//go:build windows` **测试**文件 | **必须失败** | 这正是 `go build` 看不见、只有 `vet` 看得见的那一类(第十轮修的盲区) |
| 3 | 含类型错误的 `//go:build windows` **源**文件 | **必须失败** | 第八轮修好的源文件盲区,这里做**回归确认** |

**关键设计**:自检**复用**门禁步骤 4 的 `win_check` 函数,而不是自己另写一套
命令 —— 自检若自己另写一套,将来有人把 `vet` 从门禁里删掉,自检照样绿,
盲区就又回来了。

**隔离模块为什么放 `/tmp` 而不是仓库里**:仓库里造坏文件会污染 `go list` 的
包集合与第 1 步的 gofmt 清单,也会让"工作区是否干净"的自证失去意义。
`go.mod` 的 go 指令**取自仓库自己的 `go.mod`**(`awk '/^go /{print $2; exit}'`),
不硬编码版本号 —— 将来 `go.mod` 升版本时这里不会失配。

**第十一轮补的第 4 条**(孤儿包检查的自检,commit `2621a28`,**已进主干**):类型正确的
`//go:build windows` **孤儿包**(只含该平台文件的全新目录)必须通过;
含类型错误的孤儿包必须被抓 —— 因为 `go list ./...` **看不见**这类包
(`win_check` 走 `./...`,看不见它),只有 `orphan_check` 能看见。
**该残余边界已在第十三轮关闭**:枚举改用 `go tool dist list` 的全集。

**第十三轮补的两条**(commit `aa88aee`):同一套孤儿包自检扩到 **freebsd** 与
**js** 两个平台(各一条绿 + 一条红)—— 证明目标平台枚举**不是手写清单**。

**第十四轮补的五条 + 三层守卫**(迭代 18,见"轮次 78"一节的完整表格):
第 6 步"真实用例"判定的三条(正常包不误报 / 0 用例包被抓 / 全 skip 包被抓)、
目录**混包**必须被抓、目标平台**枚举规模**断言;并给全部 **14 条**自检套上
**双向断言 + 场景计数守卫 + 断言助手防空转**三层守卫 —— 门禁自检本身也是
代码,被删掉或被改成恒真同样会让门禁"永远绿"。

### 第九轮:终审复审的正面证据(真实 server,HTTP 直连)

| 场景 | 期望 | 实测 |
| --- | --- | --- |
| **"该失败却成功"专项 10/10 场景** | 全部**正确失败**,错误体可读可定位 | ✅ 10/10 通过(条件出口全不匹配 / 并行不收敛 / 回边 / 节点失败 / 契约外状态 / 非布尔 `enabled` / 缺必填 / 悬空边 / 未知键 / 决策缺失) |
| **凭据六个出口** | 零明文 | ✅ 落库 / 读出口 / 备份 / 导出 / 事件流 / 实时 SSE —— 全部零明文 |
| **门禁九步的防线** | 8/9 条实测有效 | ✅ 8/9 条防线实测能抓到对应的坏输入(第 9 条 = "全部 skip 看不见",仍开放) |
| **Go 测试文件恒真模式** | 零恒真 | ✅ 94 个 Go 测试文件**零恒真模式** |
| **变异实验** | 11 组全部变红 | ✅ 11/11 组变红(覆盖前几轮登记的每一条可测不变量) |

**干净副本实跑**:门禁九步在干净副本上 **93s 全绿**。

### 第十轮:新增测试的"改坏即红"方向

> 以下是第十轮新增测试所断言的不变量的反面 —— 按"改坏实现 → 测试变红"的
> 等价表述给出。标注 **【实测】** 的是修复当时真的做过闭环验证的。

| 测试 | 改坏什么会变红 |
| --- | --- |
| `TestMergeExtraArgsNeverCrossesOptionNames` **【实测】** | 把回写改回**按下标对齐** → A 的凭据被写到 B 的参数位 → 红 |
| `TestMergeExtraArgsReorderKeepsCredential` | 按下标对齐且**参数顺序变化** → 凭据跟着位置走而不是跟着名字走 → 红 |
| `TestMergeExtraArgsDropsMaskWithoutStoredValue` | 无原值的 `***` 被原样保留 → 用户下次保存时把 `***` 当成真值写进去 → 红 |
| `TestSanitizeExtraArgsDropsLiteralMask` | 下发 CLI 前不剔除字面量 `***` → 每次调用都带 `--api-key ***` → 红 |
| `TestMaskCredentialValuesKeepsCodeIdentifiers` | 按"键名敏感就整值替换"脱敏 → `const token = getToken()` 整行变 `***` → diff 可读性被毁 → 红 |
| `TestArtifactsReadOutMasksCredentials` | 制品出口不脱敏 → `git diff` 原文里的凭据直接进响应体 → 红 |
| `TestExecutionStateDataMaskedInDatabase` | 落库路径不落脱敏副本 → SQLite 里出现明文 → 红 |
| `TestExecutionReadOutDoesNotCorruptRuntimeState` | 读出口**就地**改运行期状态 → 脱敏把结构化键也改了 → 红 |
| `TestRunningExecutionSeesTightenedPermission` | 全局策略回到"执行启动时的快照" → 运行中收紧对在跑的执行无效 → 红 |
| `TestScopedViewOverrideCannotLoosen` | 覆盖段回到"替换"语义 → `permissions` 段能放宽全局禁用 → 红 |
| `TestSaveWorkflowRejectsPermissionLoosening` | 校验期不报 `PERMISSION_OVERRIDE_LOOSENS` → 放宽尝试静默通过 → 红 |
| `TestUpdateConfigRejectsNewDefaultWorkingDir` | 后端接受 `default_working_dir` 新值 → 死设置继续骗人 → 红 |
| `TestUpdateConfigAcceptsLegacyDefaultWorkingDirRoundTrip` | 一并拒绝**已存老值** → 存量工作流打不开自己的配置 → 红 |
| `TestSettingsUpdateClearsGitWorkingDir` | 空串被当成"没传" → 旧值保留 → 红 |
| `TestSettingsUpdateKeepsGitWorkingDir` | 一律清空 → 没传该字段也被清 → 红 |
| `TestSettingsUpdateInvalidLogLevelLeavesWorkingDirUntouched` | 先落库后校验 → 返回 400 但目录已改 → 红 |
| `TestInvalidOnLoopLimitRejected` | 不校验 `on_loop_limit` → 拼错的 `"fl"` 静默落回 `wait_user` → 红 |
| `TestApplyGitDirArgPersistsAndShowsInSettings` | `--git-dir` 不持久化 → 设置页看不到 → 红 |
| `TestApplyGitDirArgFallsBackWhenPersistFails` | 持久化失败时**静默吞掉错误** → 用户以为工作目录设对了 → 红 |
| `TestApplyGitDirArgEmptyIsNoop` | 空串被当成"清空" → 启动参数清掉了用户的设置 → 红 |
| `TestResumeKeepsBranchChosenBeforePause` **【实测】** | 恢复时拿脱敏副本**重新求值**分支 → 人批准 A 分支、恢复后走 B 分支 → 红 |
| `TestRouteBranchDecisionIsStructuredState` | 把选定的分支记进**自由文本**键 → 落库时被脱敏 → 恢复时读不到 → 红 |
| `TestMaskedStateDataStillResumesAfterHITL` | 脱敏破坏了 `node:<id>:result` 的 JSON 合法性 → 恢复路径把它当成"未答复"→ 节点**静默重跑** → 红 |
| `frontend/src/test/error-guard.test.ts` 的"生产代码里没有静默吞掉失败的空 catch" **【实测】** | 任何一处 `.catch(() => {})` → 红(实测报"pages/Dashboard.tsx: 存在静默吞错的空 catch") |
| `frontend/src/lib/errors.test.ts` 的"同一个错误在去重窗口内只提示一次" | 去掉 5 秒去重 → 事件刷新风暴一次弹几十条 → 红 |
| `frontend/src/lib/errors.test.ts` 的"提示路径本身不能崩" | `reportError` 处理完全认不出来的错误时抛异常 → 红 |
| `frontend/src/lib/task-storage.test.ts` 的"不往 localStorage 写" | 改回 `localStorage` → 明文任务永久留在磁盘上 → 红 |
| `frontend/src/lib/task-storage.test.ts` 的"升级后清掉旧版本留在 localStorage 里的明文" | 不清扫旧数据 → 老版本留下的明文一直留着 → 红 |

## 门禁盲区(当前状态)

| 盲区 | 现状 | 后果 |
| --- | --- | --- |
| **`asArray` 守卫是源码扫描,不是类型系统** | 即使判定粒度已到"每个调用点",它仍然是**文本扫描** | 换一种写法(`for (const x of list)`、`list.length`)仍可能绕过。**仍然开放**(守卫已覆盖 `map/forEach/filter/length/…` + 数字下标 + **作为实参传给函数**,但语义判定终究不是类型系统) |
| **SSE 订阅释放无端到端断言** | 单测覆盖了写失败即关流 | 冒烟脚本没有"断开后订阅数归零"的断言,端到端层面仍可能回归。**仍然开放** |
| **列表接口的 `limit` 上限 1000 无"已截断"标记** | 超过 1000 条被静默截断 | 响应里没有任何提示,真正需要大数据量时得引入分页契约。**仍然开放** |
| **前端测试的同步点靠"猜节拍数"** | 曾用 `await new Promise(r => setTimeout(r, 0))` × 2 当同步点 | 异步链的节拍数**不是常数** → 余量为 0 → 实测全量连跑 20 次挂 1 次。**已关闭**:同步点换成"效果本身" + 类级守卫 `async-determinism-guard.test.ts`(见"前端测试稳定性"专节)(#248 / #249) |
| **脱敏规则的"形态 × 出口"组合无人断言** | 规则本身正确,但"每个形态在每个出口都被覆盖"从未被断言 | 第十一轮终审的 P0 正是"**裸值形态 × 节点输出**"一个组合漏了,而当时**没有任何断言**能发现它 —— 改规则不会红,少一条规则也不会红。**已关闭**:可执行覆盖矩阵 `TestCredentialShapeCoverageMatrix`(13 形态 × 2 出口,**未覆盖的格子必须写清理由**)(#247) |
| **门禁第 6 步的断言扫描:引用点的可达性** | 上一轮(#320)堵住了"零次循环 / 无匹配分支 / 未被调用的辅助函数"三种形态,但**引用点**的判据仍是"名字在本包里**出现过**" —— 恒假分支里的调用点也算"出现过" | 4 种新手法(恒假分支调用 / 函数字面量 / 无匹配 case / 链式)能骗过**整条 9 步门禁**。**已关闭**(第三十二轮 / 迭代 32,#341):判据从"存在性"升级为"**可达性**"—— 不可达分支里的引用点不计;`FuncLit` 要求确有调用点;链式按**不动点迭代**传递。自检 32 → **36** 条;真实仓库**误报 0**(22 包 / 171 个测试文件逐包 live 计数**完全一致**) |
| **门禁第 6 步的断言扫描:非调用形态的引用** | 辅助函数以**函数值 / 方法值**形态传递(`var f func(*testing.T) = helper`)、或**只被另一个死函数**引用 —— 这类引用**不是调用点**,静态判不出 | 判为"出现过"→ 偏保守(宁可漏判,不可误报)。**仍然开放(有意边界)**:扩大判据 → 误报 → 门禁红,代价高于漏报(#290 的口径)。见 `docs/OPTIMIZATION.md` 第十四节第 76 条 |
| **门禁第 6 步的断言扫描:函数字面量作为参数** | `t.Run("x", func(t *testing.T){…})` —— 静态判不出该字面量会不会被调用 | 保守记为 live。**仍然开放(有意边界)**,同上 |

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
| **门禁第 6 步在干净环境必红(eslint 未声明)** | 第八轮:声明 eslint + `frontend/eslint.config.mjs`;前端三子检查**各自独立**跑完再统一判定(#215 / #216) |
| **干净 clone 在门禁第 0 步崩溃**(`//go:embed all:frontend/dist`) | 第八轮:门禁在 `go list` 之前加 embed 兜底(目录缺失/为空时放占位文件)(#217) |
| **`CODE_HINTS` 码表只覆盖子集** | 第八轮:补到 64 码 + **双向守卫**(后端有前端没有 → 红;前端有后端没有 → 红)(#218 / #221) |
| **桌面分支错误不转 `ApiError` / `updateAgentConfig` 不回读** | 第八轮:`toApiError` 统一转换;桌面分支更新后回读掩码后配置(#219 / #220) |
| **平台专属 `_test.go` 的类型错误不可见** | 第十轮:第 4 步补 `GOOS=windows go vet`(`go build` 手册明文忽略 `_test.go`)(#237) |
| **门禁的防线没有自检** | 第十轮:隔离临时模块跑 3 个用例(对照组必须过 / 坏测试文件必须红 / 坏源文件必须红),**复用**门禁步骤 4 的同一函数(#238) |
| **前端 11 处静默吞错** | 第十轮:统一错误出口 `lib/errors.ts`(5 秒去重)+ 静态守卫 `error-guard.test.ts`(含防空转断言)(#239) |
| **`output` 键在 resume 与实时路径的口径差** | 第九轮:**路由依据与展示文本分离** —— 选定的分支记进结构化键 `route:<id>:branch`,恢复时**回放**而非拿脱敏副本重算。**残余边界**:循环上限的首次求值、`switchAgent` 每轮回边选择(#205 / #222) |
| **权限判定双源** | 第十轮:有效权限 = **全局实时** ∧ `permissions` 段(**只收紧**);放宽在校验期报出,全局收紧立即对在跑的执行生效(#229 / #240) |
| **门禁看不见"孤儿包"** | 第十一轮(commit `2621a28`,**已进主干**):包集合改按 **git 跟踪的 .go 文件**推导,对 `go list` 看不见的目录逐个跑三平台交叉 `vet` + 自检。**残余边界**:平台枚举只有三个 GOOS(见上面的"孤儿包检查的平台枚举不全") |
| **裸凭据形态全链路明文泄漏** | 第十二轮(commit `06276de`):值的形态规则收敛到 `logx/credential.go` **唯一来源**(`agent` 侧降为薄封装);`logx.Mask` 升级为"键名 ∪ 值形态";落库侧与读出口**各过一次** → 幂等。回归锁 = 可执行覆盖矩阵(#245 / #246 / #247) |
| **门禁看不见"所有用例都被 `t.Skip()` 跳过"** | 第十三轮(commit `aa88aee`):第 5 步改 `-v`,第 6 步统计**顶层** `--- PASS:` / `--- SKIP:` 行并要求看到该包自己的 `=== RUN` 行 → 区分 `zero` / `skipall` / `nofiles`。**第十四轮再补上这条判定本身的自检**(隔离小模块跑真的 `go test -v`)(#209 / #252 / #259) |
| **孤儿包检查的平台枚举不全** | 第十三轮(commit `aa88aee`):枚举改用 `go tool dist list` **全集**(实测 47 个 GOOS/GOARCH)+ 批量探测 + 按文件集去重 vet;新增 freebsd / js 两条自检。**第十四轮再加"枚举规模"断言**(#251 / #261) |
| **前端双模式契约的断言面极窄** | 第十三轮(commit `aa88aee`):`frontend/src/lib/api.test.ts` 补 **20 条**桌面分支契约断言(双向比对),vitest 233/233(#257) |
| **前端的防抖窗口与尾沿语义零覆盖** | 第十三轮(commit `aa88aee`):新增 `frontend/src/lib/debounce.test.ts`(**9 例**,假计时器)(#258) |
| **门禁的复杂判定没有自检**(第 6 步"真实用例" / 目录混包 / 枚举规模 / 自检自身恒真) | 第十四轮(迭代 18):新增 5 条自检场景(共 **14 条**),并给全部自检套上**双向断言 + 场景计数守卫 + 助手防空转**三层守卫(#259-#262) |
| **`asArray` 守卫是文件级判定,单点绕过** | 第十四轮(迭代 18,commit `91fe3e2`):守卫补上**逐调用点判定**(`findUnguardedCallSites`),"当列表用"加第三种形态(**作为实参传给函数**)→ `api.listX().then((x) => setRows(x))` 不再放行。**实测**:隔离副本改坏 `Dashboard.tsx` 两个调用点 → **2 条用例变红**(上一轮同一实验是 4 条全绿);守卫作用域 **20 个调用点、零误报**(#210) |
| **并发探针依赖 `time.Sleep` 建立读写重叠窗口** | 第十四轮(迭代 18,commit `91fe3e2`):改成**屏障 + 紧凑循环**(读者先就位 `ready.Wait()`、持续自旋,写者才开始写)→ 读写**必然重叠**。实测:去掉锁后 `app` 从 **0/5 提升到 3-5/5** 命中,`git`/`logx`/`permission`/`contextx` 均命中(#208) |
| **SSE 的 `json.Marshal` 失败被丢弃** | 第十四轮(迭代 18,commit `91fe3e2`):序列化失败落一条 `logx.Warn`(事件类型/执行 ID/节点/原因)→ 事件仍不送达但**可追溯**;验证 `api/sse_marshal_test.go`(#233) |
| **`crypto/rand.Read` 失败被吞** | 第十四轮(迭代 18,commit `91fe3e2`):`randRead` 测试接缝 + **失败即返回错误** → `randHex`/`newID` 不再产出全零 ID,`SaveWorkflow`/`Run` 显式失败;验证 `app/application/randid_test.go`、`workflow/runtime/randid_test.go`(#234) |
| **HITL `responses` 的单态类型断言** | 第十四轮(迭代 18,commit `91fe3e2`):`node.Config["responses"].([]any)` 换成 `cfgStringList`,认全 `[]any` 与 `[]string` 两种切片形态;验证 `workflow/runtime/hitl_responses_test.go`(#256) |
| **门禁第 6 步的文本解析可被伪造输出绕过** | 第十五轮(迭代 21B,commit `908ba0b`,**已进主干**):判据换成 `go test -json` 的**框架事件**(`Action=="pass"` 且 `Test` 非空且不含 `/`)→ 用例自己 `Println` 的 `=== RUN`/`--- PASS:` 只落进 `Action=="output"`,框架**不为它生成 pass 事件** → 伪造失效;自检新增第 **17** 条场景(`SELFCHECK_EXPECTED` 16 → 17)。专节见"门禁第 6 步:伪造输出绕过"(#266) |
| **值的形态规则"只有一份来源"没有被断言保证** | 第十六轮(迭代 17,#263 **已关闭**):此前的回归锁比的是**同一个函数比较两次** → 恒真 → 永远绿(造坏验证:把唯一来源改成原样返回,旧断言照样 PASS)。现在跨包逐字节比对放在 `agent/credential_mask_test.go`(`agent` 依赖 `logx`,logx 反向 import agent 会成环),并加**防空转守卫**(函数对象 + AST) |
| **取消的残余窗口:读库之后、执行恰好完成 → 200 而最终 COMPLETED** | 第十六轮(迭代 17,#269 **登记为可接受的边界**):窗口实测 ≈**0.5ms**(220 次随机采样:混合带 [-300µs,+250µs],约一半落到假成功,9/220 ≈ 4%;宽度由**两段 DB 往返**决定 —— "请求到达 → ctx 真被取消" + "最后一次取消检查 → 终态落库",分段测量后各 ≈250µs)。该窗口里状态与事件流**仍然正确**(执行真的跑完了 → `COMPLETED` + 只有一条 `workflow.completed` + 没有 `workflow.cancelled`),用户的取消意图已满足 → **不改行为**,改**语义登记 + 确定性用例钉死**(`workflow/runtime/cancel_residual_test.go`)。彻底关闭要改"谁写 `CANCELLED`",连带改四条对外契约(落库时刻 / 事件发出者与顺序 / 取消分支 CAS 冲突计数语义 / 双击取消的响应)→ 留给后续迭代 |




### 迭代 31:真实适配器路径的"同形替身"场景矩阵(迭代 31 新增)

> **为什么有这一节**:前三十轮的验证几乎都用 mock agent —— mock 直接返回
> 结构体,**不经过子进程**。真实路径(`claude` / `pi` 适配器调用真实 CLI)
> 上的子进程管理、超时、输出解析、退出码、stderr、管道与进程组,
> **从未被系统性地走过**。本节是这一层的回归锁。

#### 替身:一个可复用、跨包共用的二进制

`agent/internal/stubcli/`(本轮新增):把仓库既有的"同形替身"先例
(`agent/pi/stub_smoke_test.go` 的 shell 替身、`agent/claude/real_smoke_test.go`
的真机冒烟)升级成**一个** Go 替身二进制 ——

| 组成 | 位置 | 作用 |
| --- | --- | --- |
| 替身源码(内嵌) | `agent/internal/stubcli/stubcli_main.go.txt` | 真子进程:真 stdout/stderr、真退出码、真信号、真进程组、真管道 |
| 构建与驱动 | `agent/internal/stubcli/stubcli.go` | 测试期编译成独立可执行文件(`go build` 到临时目录);`Build` / `Cleanup` / `Env` / `ReadPID` / `ProcessAlive` |
| 替身自身的用例 | `agent/internal/stubcli/stubcli_test.go` | 替身是全部结论的"地面真值" —— 替身坏了 → 全部结论不可信 |

- **一个二进制覆盖全部场景**:行为由环境变量切换(`STUBCLI_MODE`),
  输出形态由 `STUBCLI_SHAPE` 切换(`claude` = JSON 信封 / `pi` = 正文 + 尾部
  JSON),两侧适配器**共用同一份替身**。
- **不需要真实 CLI**:测试在没有 claude / pi 的机器上照样跑(替身只用
  标准库,独立模块编译,`GOPROXY=off` + `GOTOOLCHAIN=local`,不联网)。
- **每个测试进程只编译一次**(`sync.Once`),退出时由 `TestMain` 调
  `stubcli.Cleanup()` 删除临时目录。

#### 场景矩阵(14 个场景 × 两侧)

`agent/claude/stub_matrix_test.go` / `agent/pi/stub_matrix_test.go`:
表驱动,每个场景断言**终态、决策值、错误消息的可读性与凭据脱敏**,
不止"没崩"。

| # | 替身形态 | 期望 | claude | pi |
| --- | --- | --- | --- | --- |
| 1 | `ok` 尾部 JSON | 解析成功、决策正确 | ✅ REJECTED | ✅ REJECTED |
| 2 | `huge`(9MB) | 截断 → **显式 FAILED** | ✅ | ✅ |
| 3 | `empty` stdout 0 字节 | 明确失败 | ✅ FAILED | ✅ FAILED |
| 4 | `blank` 只有空白 | 明确失败 | ✅ FAILED | ✅ FAILED |
| 5 | `nonjson` 纯文本 | 明确失败 | ✅ FAILED | ⚠️ 成功**但留痕**(见下) |
| 6 | `garbage_prefix` 前置垃圾 + 尾部 JSON | 提取尾部 JSON 成功 | ✅ REJECTED | ✅ REJECTED |
| 7 | `multi_json` 多个 JSON | 取**最后一个** | ✅ REJECTED | ✅ REJECTED |
| 8 | `exit_nonzero` 非零退出 + stderr | 失败、错误含 stderr 线索、凭据脱敏 | ✅ `CLAUDE_PROCESS_ERROR` | ✅ `PI_PROCESS_ERROR` |
| 9 | `exit_zero_silent` 退出码 0 且零输出 | 明确失败(**不得** SUCCESS + 空决策) | ✅ FAILED | ✅ FAILED |
| 10 | `fork_hold_pipe` fork 出持有管道的子进程 | 孤儿被收掉(进程组击杀 + WaitDelay) | ✅ | ✅ |
| 11 | `sleep` 无限睡眠 | 超时触发、进程组被杀、无残留 | ✅ `CLAUDE_TIMEOUT` | ✅ `PI_TIMEOUT` |
| 12 | `binary` NUL / 非 UTF-8 | 不 panic、错误可读 | ✅ FAILED | ✅ 不产出决策、留痕 |
| 13 | `stderr_credential` stderr 含凭据 | 错误消息里凭据已脱敏 | ✅ | ✅ |
| 14 | `exit_code` 137 / 143 | 明确失败、原因可读 | ✅ | ✅ |

补充用例(两侧各一份):

- `TestStubForkChildIsReaped` —— 替身 fork 出 `sleep 60` 的子进程
  (继承 stdout)后立刻退出:适配器返回后**孤儿必须已死**
  (`ProcessAlive` 探测信号 0)。
- `TestStubSignalKillReapsOrphan` —— 替身 fork 出持有管道的子进程后
  **自己被信号杀死**:`exec.ErrWaitDelay` **不得**把"进程已死"掩盖成
  成功(断言返回 `*_PROCESS_ERROR`),且孤儿被收掉。
- `TestStubTimeoutLeavesNoResidue` —— 超时之后替身进程不得残留。
- `TestStubConcurrentCallsShareNothing` —— 同一个适配器实例并发调用
  8 次:不得有共享可变状态(`-race` 下会直接报竞争)。
- `TestStubEmptyOutputCarriesStderrHint` —— "退出码 0 却零输出"时,错误
  消息必须**带上 stderr 线索**(CLI 退出码 0 时 stderr 是唯一诊断),
  且 stderr 里的**凭据不得随行**(脱敏后仍留 `***` 标记)。

#### 场景 5 的有意偏离(pi 侧)

pi 的输出形态本就是自由文本,仓库自带示例工作流
(`workflows/examples/coding-task.yaml`)的 plan / execute 节点靠的正是它 ——
把"纯文本输出"判成失败会打断既有工作流形态。pi 侧因此改成
"**成功但留痕**":`Data["decision_missing"] = true` + 摘要追加
"(未解析出结构化决策)"。claude 侧可以判失败,因为**适配器自己**在提示词
里要求了 JSON 决策(`buildPrompt` 末尾那句
`Respond with JSON at the end of your reply`)。

#### "先红后绿"的实测证据

隔离副本(`/tmp/aw31-scratch`,`git archive HEAD` + 本轮测试)把
`agent/claude/claude.go`、`agent/pi/pi.go`、`agent/process_unix.go`、
`agent/process_windows.go` **还原为 HEAD**、只保留本轮测试 → 红的用例
即"本轮测试确实能抓住这些缺陷":

```
--- FAIL: TestStubScenarioMatrix (3.40s)
    --- FAIL: TestStubScenarioMatrix/03_空输出            status = "SUCCESS", want "FAILED"
    --- FAIL: TestStubScenarioMatrix/04_只有空白          status = "SUCCESS", want "FAILED"
    --- FAIL: TestStubScenarioMatrix/05_非_JSON_输出       status = "SUCCESS", want "FAILED"
    --- FAIL: TestStubScenarioMatrix/06_前置垃圾_+_尾部_JSON decision = "", want "REJECTED"
    --- FAIL: TestStubScenarioMatrix/09_退出码_0_但无任何输出 status = "SUCCESS", want "FAILED"
    --- FAIL: TestStubScenarioMatrix/12_非法字节落在_JSON_内 status = "SUCCESS", want "FAILED"
    --- FAIL: TestStubScenarioMatrix/12b_非法字节落在_JSON_外 decision = "", want "REJECTED"
--- FAIL: TestStubForkChildIsReaped     孤儿进程 25062 仍然存活 —— 进程组未被清理
--- FAIL: TestStubSignalKillReapsOrphan 孤儿进程 … 仍然存活 —— 进程组未被清理
(pi 侧同族:03/04/05/09/12 + 两条孤儿用例)
```

还原实现 → 红;本轮实现 → 全绿。逐条缺陷与修法见
`docs/OPTIMIZATION.md` 的"轮次 90"小节(#336-#340)。

#### 本节**没有**覆盖的(必须知道)

1. **真实 CLI 的真实输出形态永远无法完全模拟** —— 替身覆盖"形态",
   覆盖不了"某个具体版本的 CLI 在某个具体环境下才会吐出的东西"。真机
   冒烟(`agent/claude/real_smoke_test.go`)仍然需要真机。
2. **替身不是并发/竞态的压力测试** —— 它按形态切换,不模拟真实 CLI 的
   内部并发(本轮只验适配器侧的共享状态无竞争)。
3. **`pi` 适配器仍无真机冒烟** —— 开放项(自轮次 29 起)保持开放。

## 第二十九轮(迭代 33 收口):凭据脱敏的最后关口 + 门禁可达性

> 输入是**收官验收复审**的结论(**0 P0 / 2 P1** + 2 条 P2/P3)。
> 缺陷登记见 `docs/OPTIMIZATION.md` 第十三节末"轮次 92"小节(#342-#346)。

### 本轮新增/修改的测试用例清单

| 文件 | 用例 | 覆盖的缺陷 | 断言意图 |
| --- | --- | --- | --- |
| `logx/credential_test.go` | `TestRandomBase64CredentialsAreMasked` | **#342** | 200 个**固定 seed** 的随机 base64 样本(std 100 + raw 100)→ `Mask` 与 `MaskCredentialValues` **两个出口**都必须遮住(不能只遮一个);且必须出现占位符 |
| `logx/credential_test.go` | `TestCamelCodeIdentifiersArePreserved` | **#342**(绿方向) | 22 个代码标识符(含 `getToken` / `apiKey` / `mySecretKey` / `userAccessToken` … 与仓库里真实存在的 `TestScriptNodeHonorsTimeoutAndWorkingDir1637892132` / `TestJSONBodyTooLargeReturns413` / `TestSha256ChecksumAlgorithmName`)**原样保留** —— 收窄判据不能把正常代码全当凭据 |
| `logx/credential_test.go` | `TestMaskIsIdempotentForRandomBase64` | **#342** | 200 个随机样本"掩一次 vs 掩两次"**逐字节相同**(落库侧与读出口各过一次) |
| `logx/credential_test.go` | `TestMaskIsIdempotentForPEMInAssignment` | **#345** | 8 种"PEM 嵌在赋值形态"的输入(赋值 / 冒号 / 引号 / 双 diff 前缀 / 裸块)→ **幂等** + **明文不残留** |
| `logx/credential_test.go` | `TestDoubleDiffPrefixCredentialIsMasked` | **#346** | `++AKIA…` / `--AKIA…` / `+++AKIA…` / `diff --git …` 四种形态 → 遮住,且**行首标记保留**(diff 的行标记是审计证据) |
| `logx/credential_test.go` | `TestBareBase64WithPaddingIsNotTruncated`(改) | **#342** | 夹具前提自检改用**生产实现**的 `camelHumps`(口径不再两处各写一份);断言**未改** |
| `workflow/runtime/cas_conflict_test.go` | `TestCancelCommitConflictIsExpectedRace`(改) | **#344** | 前置条件从"等 `RUNNING`"改成"等 **`node.started`**";**断言意图与强度一个字没改** —— 只是把竞态变成确定时序 |
| `scripts/verify.sh`(门禁自检) | 5 条新场景(`SELFCHECK_EXPECTED` **36 → 41**) | **#343** | 五种"以值引用"绕过各一条**红**场景(断言**具体原因** `未被调用的辅助函数`),每条同时断言**绿方向**(`var f = helper; f(t)` 真调用 / `h.assertRuns(t)` 方法真调用 / `t.Run(…, func(){…})` 回调) |

### 本轮的**红 → 绿**实测

| 编号 | 红(改前) | 绿(改后) |
| --- | --- | --- |
| #342 | 旧 `credential.go` + 新测试:200 个随机 base64 样本中 **180 个逃逸** → **360 条断言失败**(例:`样本 0 未被 Mask 脱敏(驼峰护栏放行了随机 base64): "S0H0NtfHXZIK8I50kOM7scZAisuWnEn52qh3SwQAex4R2EXITPLsXHpz1+2CsUpT5lU="`) | 200/200 全遮;22 个标识符全保留;200 个样本幂等 |
| #343 | 旧扫描器(文本计数):5 个绕过包 `deadvaluevar 1 1` / `deadvaluealias 1 1` / `deadvaluefield 1 1` / `deadvaluemethod 1 1` / `deadvaluechain 1 1` —— **live 全等于 total** | 新扫描器(真实调用点):5 个包**全部** `live = 0` + `未被调用的辅助函数`;3 个绿对照全部 `live = 1`;**真实仓库 22 包 / 171 文件逐包 live 计数 0 差异** |
| #344 | 隔离副本(HEAD 版用例)+ CPU 负载 + `GOMAXPROCS=1`:**10/10 失败** —— `取消分支的 CAS 冲突未被计数(事件: [workflow.started])` | 同样条件下 **0/10 失败** |
| #345 | 一次 `…-----***` / 两次 `…-----******` | 一次 == 两次(逐字节) |
| #346 | `"++AKIAIOSFODNN7EXAMPLE"` 原样输出 | 遮住,`++` 保留 |

### 本轮的门禁**负向验证**(证明自检非恒真)

把 `scripts/verify.sh` 内嵌的 `assertscan` **换回旧版**(文本计数)后重跑门禁
第 6 步:新增的 5 条自检场景**全部变红**(旧扫描器把 5 个绕过包判成
`live = 1` → `usage_expect … "yes"` 失败)→ **门禁红**。
换回新版后同一条自检**绿**。

## 第三十二轮(迭代 36):对照主流工作流引擎的能力补齐(2026-10-28 凌晨)

> 本轮不是审计轮,是**功能轮**:调研了 Temporal(RetryPolicy)/ n8n
> (retry-on-fail、continue-on-fail、per-workflow 并发)/ Argo
> (retryStrategy)/ Windmill(typed args)的公开设计,把收敛度最高的
> 四项能力按本项目的契约风格落地,每项独立提交、回归同 commit。

### 1. script 节点支持节点级 retry(commit `fa6a74a`)

retry 此前只在 agent 节点的契约表里声明 —— 而子进程(script)恰是最常见的
瞬时失败源。重试循环从 runAgentNode 抽为 `withNodeRetries`(agent/script
共用),事件名 `agent.retry` → `node.retry`(带 node_type;历史执行里的
`agent.retry` 行原样保留,前端按原文展示)。门禁拒绝与配置检查在重试循环
**外**(策略拒绝不是可重试失败);节点级超时额度每次尝试重新计时。
契约探针 `runtimeNodeFuncs` 改为**多类型归属**(retryPolicyOf 归
agent+script)。测试:`script_retry_test.go` 4 例(重试后成功/次次失败/
非法配置 fail closed/门禁拒绝不重试)+ validator `TestScriptRetrySubKeysSameAsAgent`
(7 子例,钉住契约表)。

### 2. 声明式运行入参 inputs(类型/必填/默认值)

DSL 根级 `inputs:` 段(name/type/required/default/description),对标
Temporal 活动入参 / n8n 子工作流输入 schema / Windmill typed args:

- **解析层**(dsl):条目键集合封闭,未知键记 `IgnoredKeys` + WARN(与
  edges 同口径;`required` 拼成 `reqired` 必须可见);默认值数值形态在
  ToModel 钉死(YAML uint64 → int)。
- **校验层**(validator):名称空/重复、type 非法、required+default 并存、
  default 类型不符、与 `variables` 键同名、与条件顶层变量(decision/
  status/output/iteration/variables)同名 —— 全部 `INVALID_NODE`。
  同名冲突报错的理由:条件里读到的 `variables.xxx` 是**运行时变量表**,
  但 `status`/`decision` 这类顶层名读到的是**节点状态** —— 用户以为条件
  在读自己声明的入参,实际是节点状态(静默走错分支,约定 14 同族)。
- **运行期**(Engine.Start):必填缺失 → `INPUT_REQUIRED`、类型不符 →
  `INPUT_TYPE_MISMATCH`(在 Agent 重置**之前**,启动失败零副作用);
  缺省项兜 default 落入 exec.Variables —— 模板 `{{variables.xxx}}` 与
  条件变量宇宙**不变**(约定 13:可用变量集合仍只有 conditionenv 一份)。
  合并顺序:`wf.Variables` 为底 → input default → 请求 variables 覆盖。
- **持久化**:workflows 表新增 `inputs_json`(建表 + ALTER 迁移),读回走
  与 variables 同一套数值归一。
- **API/前端**:run 端点请求体形状不变;Designer 保存路径**原样带回**
  inputs(与 permission 同口径 —— 保存会重建整个对象,漏掉即静默抹掉);
  `CODE_HINTS` 补码表。
- 类型判定的同源点:`model.InputValueMatchesType`(校验期查 default 与
  运行期查传入值走同一个函数)。

测试:dsl 4 例 / validator 20+ 子例 / runtime 5 例 / persistence 往返 /
api 契约(400 两种 + 200 兜底 + 详情带声明)/ designer.test.ts 保存透传 2 例。

### 3. settings.max_concurrent 每工作流并发上限(commit 独立)

对照 n8n/Windmill 的 per-workflow 并发限制:`settings.max_concurrent`
(0 = 不限,范围 0..100,负数报 `INVALID_MAX_CONCURRENT` —— 想限 3 写成
-3 会静默变成"不限")。达到上限 → **`WORKFLOW_BUSY`(409,不排队)**:
排队需要新执行状态(QUEUED)并波及状态机/CAS/前端徽标,fail fast 让调用方
立刻看到原因。计数口径 = 非终态执行(CREATED/RUNNING/WAITING_USER)。
**检查与首次插入在同一临界区**(`Engine.startMu`):check-then-insert 是
两条语句,SQLite 单写连接不构成原子性。检查在 Agent 重置之前(失败零副作用)。
409 的依据:请求本身合法,只是"现在不能跑"(与既有 `INVALID_TRANSITION`
=400 的历史契约无关,不动)。测试:runtime 3 例 / validator 5 子例 /
api 409 契约。

### 4. retry 退避参数可配(commit 独立)

Temporal 对齐的两颗旋钮(agent/script 共用):
`retry.initial_backoff_seconds`(首次重试前等待,缺省 2s,0=立即)、
`retry.max_backoff_seconds`(增长上限,缺省 exponential 30s / fixed 不封顶
—— fixed 无增长,封顶会违背用户显式设置的 initial)。**缺省行为逐位不变**
(延迟序列有逐位断言)。解析 fail closed:字符串/布尔/负数/NaN 一律节点失败
(与 max_attempts 同口径);上界 `compiler.RetryBackoffSecondsMax`=86400
(与 timeout_seconds 同界)。校验期同口径,max < initial 只在两者都显式
声明时报(缺省不参与)。测试:runtime 5 例(含端到端 initial=0 重试不等
2s)/ validator 9 子例。

### 5. 运行对话框渲染声明式入参(commit 独立)

inputs 的 UI 半边:运行对话框新增「运行入参」区 —— 字符串/数字输入框、
布尔开关,default 预填、required 打星号,全部控件带 aria-label(走查 #8
的教训)。**字段是声明入参的主控件**(覆盖 JSON 文本框同名键;文本框只承载
未声明变量)。对话框抽为导出的 `RunDialog` 组件(单测无需画布与路由)。
数字字段在 `<input type=number>` 下浏览器会把非法文本清洗成空串(实测
jsdom 同行为)→ 空值不上送、交给默认值/必填校验,用例按真实行为断言。
测试 6 例。

### 6. 浏览器实测走查(真跑产品)

起真实 server + 浏览器逐页操作:运行对话框类型化字段渲染 / 必填缺失的人话
toast / 填写+开关+启动 → 监控页执行变量逐项正确 / 设计器保存不丢声明 /
8 页面健康检查(无 ErrorBoundary)。**抓到 1 个真缺陷**(RunDialog 抽组件
时丢失 try/catch → 启动失败静默无提示;mock 测试抓不到,因为假 onLaunch
永远成功),修复 + 回归用例(designer.rundialog.test.tsx 第 7 例)。

### 本轮的门禁联动

- `api.test.ts` 的**错误码全集守卫**(后端有码而前端无提示 → 红)当场
  抓到 `INVALID_MAX_CONCURRENT` 缺码表 —— 守卫有效。
- 契约探针(nodeconfig contract)当场抓到"契约表声明了 retry 而探针按
  单类型归属看不到 script 的读取点" —— 归属表升级为多类型。

## 汇总

| 轮次 | 类型 | 结果 | 修复的 Bug |
| --- | --- | --- | --- |
| 94 | **第三十一轮(迭代 35)**:**真缺陷清零**(limit 截断标记 / permissions 节点级错放同码 / #287 库值标记 / #325 自检注册表 / #286 重分类 / SSE 断连释放冒烟)+ **首次浏览器实测走查**抓出 **15 个体验缺陷** | ✅ 已落地(`7047e14`..`cbc9d57`,38 个独立提交) | 六条真缺陷的逐条处置见 `docs/OPTIMIZATION.md` 第十六节(5 修 + #286 按口径重分类为有意取舍:**残余只剩"断言故意藏进全 skip 闭包"的对抗构造**,修它需要断言→顶层测试体的归属判据,误报面正是历轮修死断言的雷区)。走查 6 项(逐条见第十六节):**节点输出结构化**(agent 的 Summary/Decision 此前完全不可见,"代码评审 ×2"看不出判了什么)/ **执行终态落 finished_at**(此前从未赋值,监控页耗时无限走表)/ **审批面板以执行状态为准**(Resume 后历史 WAITING 行把面板挂在已完成执行上)/ **零执行成功率显示 — 而非 0%** / **画布暗色模式**(MiniMap/缩放控件此前是白色默认样式)/ **WORKFLOW_INVALID 拍平**(校验错误数组直接弹原始 JSON)/ 过滤无匹配空态提示 / 四处图标按钮 a11y / 统计表窄视口横向滚动 + 成功率分母只含已落定 / 两处回车提交(新建工作流、运行对话框)/ 实时日志跟随滚动(上翻不拽人)/ 设计器打开即静默校验(已知警告立现)/ **顶层 ErrorBoundary**(渲染异常此前整窗白屏)。**新增测试**:Go `finished_at_test.go` 4 + validator/app 层 permissions 8 + settings 读失败 3 + sse stats 1 + api 截断头 2;前端 ExecutionMonitor +12(结构化结果 5 / 审批面板一致 3 / 日志跟随滚动 2 / 加载失败态 2)/ Dashboard +4(无数据 2 / 溢出与分母改断言 2)/ errors +5 / Settings +3 / api 契约 +1 / Workflows +2(过滤空态 / 回车创建)/ ErrorBoundary 3 / unsaved-guard 3。SSE 冒烟 4 → **6 项**(归零断言 + ≥1 反向对照防恒真);门禁自检改注册表派生(`scripts/selfcheck_registry.txt`,41 条,红方向已证伪)。**实测**:9 步门禁全绿 / 前端 **27 文件 · 334 用例**全绿 / 桌面打包 `wails3 task package` 通过 / 浏览器实跑:运行 · 审批(Approve/Continue)· 并行 · 导入错误提示 · 暗色画布 · 深链失败态 · 未保存守卫 · 拖拽添加 · 逐类型属性面板 · 掩码三态(API+React 层)逐项目视 |
| 95 | **第三十二轮(迭代 36)**:**功能轮**(对照 Temporal/n8n/Argo/Windmill 的公开设计):script 节点 retry(`node.retry` 事件)/ 声明式运行入参 inputs(类型·必填·默认值,DSL→校验→运行期→持久化→API→UI 全链路)/ `settings.max_concurrent` 并发上限(WORKFLOW_BUSY 409,fail fast 不排队)/ retry 退避参数可配(initial/max_backoff_seconds,缺省行为逐位不变)/ 运行对话框入参类型化字段(RunDialog 抽组件) | ✅ 已落地(5 个独立提交,回归同 commit) | 新增测试:Go dsl 4 / validator 36+ / runtime 17 / persistence 1 / api 2;前端 designer.test +2、designer.rundialog.test 6。门禁联动:错误码全集守卫当场抓到 INVALID_MAX_CONCURRENT 缺码表;契约探针促成 runtimeNodeFuncs 多类型归属。**实测**:9 步门禁全绿(verify 163s→中途复跑全绿)/ 前端 **28 文件 · 342 用例**全绿 |
| 93 | **第三十轮(迭代 34 收官)**:**开放清单最终收敛** + **三十三轮总体小结** + **回归史总结** + **8 条对外契约复核** | ✅ 已落地(工作区,待提交) | **不是修复轮,是归档轮** —— 逐条核实第十四节的开放项:关闭 **#279**(门禁对根级 `build/` 的盲区,修法已随 `f7d7f98` 进主干)、**#324 的前提实测不成立**(干净 clone 只装 `frontend/` 就有 eslint v9.39.5 —— 原登记写的"`frontend/package.json` 里没有 eslint"从登记那刻起就是错的);其余**按性质重新归类**(真缺陷 6 / 有意取舍 11 / 环境受限 3 / 仍然暂缓 1),新增"开放项分类总表";补 `docs/OPTIMIZATION.md` 第十五节"三十三轮总体小结"(五个缺陷家族 / 度量 / **六条方法论** / 仍然开放)与本文件末尾"回归史总结"(规模演进 / **六种关键测试手法** / 门禁九步的**实测拦截点**);`docs/api.md` 的 **8 条对外契约逐条对照代码**,**全部成立**,契约正文未改。**实测**:9 步门禁全绿(**111s**)/ 前端 **25 文件 · 302 用例**全绿 / REST+SSE+HITL 冒烟 **9 项** / SSE 冒烟 **4 项**;编号自检 `docs/check-numbering.sh` 全通过(号段 **1..346**,已知历史空号 #25 单列) |
| 92 | **第二十九轮(迭代 33 收口)**:随机 base64 凭据不被脱敏 / 门禁可被"以值引用"骗过 / CAS 冲突用例去 flake / `logx.Mask` 非幂等 / 双 diff 前缀漏 | ✅ 已落地(commit `f7d7f98`) | **#342-#346 共 5 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 92 小节):**#342**(P1)随机 base64 凭据**不被脱敏** —— 裸值高熵规则的"驼峰标识符"护栏("大写后紧跟小写"≥4 次就豁免)对随机字符**几乎必然**满足(实测 16.5%/位)→ 护栏等于"高熵串豁免"→ 随机 base64 **194/200 原样输出**(终审实测 198/200)→ node output / state_json / 事件流 / SSE / git diff 六出口全明文 → **驼峰护栏收窄为五条**(起伏≥4 / 驼峰词序列 / 词长≥2 / 每词含小写 / **数字段≤1 处**),并补"整段就是路径"的路径护栏;**#343**(P1)门禁第 6 步的断言扫描可被 **5 种"以值引用"手法**骗过(整条 9 步门禁全绿放行)→ 可达性改为**真实调用点**(`ast.CallExpr` 的 callee 解析到该 `FuncDecl`;函数值别名按作用域解析,别名链用不动点求解),自检 **36 → 41**;**#344**(P2)`TestCancelCommitConflictIsExpectedRace` 在 CPU 负载下**假红**(实测 10/10 失败)→ 前置从"等 RUNNING"改成"等 **node.started**",断言未改;**#345**(P3)`logx.Mask` **非幂等**(赋值内 PEM:一次 `…-----***`、两次 `…-----******`)→ PEM 正则认得已掩形态;**#346**(P3)`++AKIA…`(**双 diff 前缀**)漏 → 行首 `+`/`-` **逐个**摘除。**红→绿证据**:旧实现 + 新测试 → 360 条断言失败 / 旧扫描器 5 个绕过包 `live == total` / 负载下 10/10 失败 / 一次两次不等 / `++AKIA…` 原样输出;改后全部转绿。**误报 0**:新旧扫描器在真实仓库 22 包 / 171 文件上**逐包 live 计数完全一致**。**新增测试**:`logx/credential_test.go` 5 个用例 + 门禁自检 5 条场景;**修改**:`logx/credential_test.go` 的夹具前提自检改用生产实现的 `camelHumps`、`workflow/runtime/cas_conflict_test.go` 的前置条件(断言未改)。Go 测试文件 **171** 个(不变)/ 顶层 `func Test*` **904 → 909**;门禁 **9 步全绿**(总耗时 **138s**) |
| 91 | **第二十八轮(迭代 32 收口)**:门禁断言扫描的 4 个新盲区 + 终审复审确认关闭 6 条遗留项 | ✅ 已落地(工作区,待提交) | **#341 共 1 条,已关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 91 小节):**#341**(P1)门禁第 6 步的断言扫描可被 **4 种手法**骗过(辅助函数**只在恒假分支**里被调用 / 断言在**从未被调用的函数字面量**里 / 辅助函数**只在无匹配 switch 分支**里被调用 / **链式**辅助函数只在恒假分支被调用)→ **整条 9 步门禁全绿放行**;根因:`funcUnreachableReason` 用 `refs[name] > 0` 判"是否被调用",而 `packageRefs` 把**恒假分支里的调用点**也计入,且 `FuncLit` 完全不追踪调用关系 → 修法:**判据从"存在性"升级为"可达性"**(引用点收集时跳过不可达分支里的调用点 + `FuncLit` 内断言要求该字面量确有调用点 + 链式辅助函数用**不动点迭代**传递可达性);自检 **32 → 36** 条(B4/B5/B6/B7 各一条红场景 + 绿方向)。**红→绿证据**(本轮自建"抽出 assertscan 单独编译"的手法):旧扫描器 4 个绕过包全部 `live == total` → 新扫描器全部 `live == 0` 且**原因与形态一一对应**;**误报 0**:新旧扫描器在本仓库 22 个包 / 171 个测试文件上**逐包 live 计数完全一致**(3304 条断言全 live)。**另关闭 6 条**终审复审确认的遗留项(**#270** 跨节点上下文链 / **#321** `submit` 产物 / **#318** AKIA 粘连 / **#323** 权限死入口 / **#306** NaN 连坐 / **#322** SSE 慢客户端阈值),逐条核实方式见"轮次 91"一节。**仍然开着 2 处**(有意边界):函数字面量**作为参数**传给别的函数 / 辅助函数以**函数值**形态传递 —— 静态判不出 → 保守记为 live(#320 残余同族,见 OPTIMIZATION 第 76 条) |
| 90 | **第二十六轮(迭代 31 收口)**:真实适配器路径的"同形替身"场景矩阵(14 场景 × claude/pi 两侧) | ✅ 已落地(工作区,待提交) | **#336-#340 共 5 条**(逐条见 `docs/OPTIMIZATION.md` 轮次 90 小节):**#336**(P0)CLI **退出码 0 但 stdout 为空** → 适配器一律 `StatusSuccess` + 空决策→ 节点绿灯、路由**静默走默认分支**(约定 14"该失败却成功")→ 两侧 `parseOutput` **先判空**→ 显式 FAILED + 可读原因(第二十九轮的 P3 由此**升级为 P0**:同一函数里"截断"早已是显式失败,信息量更少的空输出却是成功 —— 两套口径自相矛盾);**#337**(P1)claude:stdout **不是合法 JSON 信封**(前置提示行 / 纯文本 / 非法字节)→ 整段 Unmarshal 失败 → **决策整个丢失** → 改成**先找信封再解析**(整段 → 尾部对象 → 第一个对象,取第一个**像信封**的:含 `result`/`is_error` 字段);**#338**(P1)进程组击杀**只在取消/超时路径兑现** —— CLI 正常退出但孤儿持有管道时(`ErrWaitDelay` 路径)孤儿**继续在后台跑**(进程泄漏)→ 新增 `agent.KillProcessGroup` 在 Wait 返回后收掉孤儿(失败路径同样收);**#339**(P3)pi:自由文本输出是合法形态但**静默成功** → 改成"成功但留痕"(`Data["decision_missing"]` + 摘要标注),**不判失败**(判失败会打断仓库示例工作流的 plan/execute 节点);**#340**(P2)"信号杀死 + 孤儿持有管道"不会被 `ErrWaitDelay` 掩盖成成功 —— 实现本来就对,本轮把它**钉住**(新增 `fork_and_signal` 替身形态 + 两条孤儿用例)。**红→绿证据**:隔离副本把适配器实现**还原为 HEAD**、只保留本轮测试 → claude 侧 7 条矩阵用例 + 2 条孤儿用例红、pi 侧 5 条矩阵用例 + 2 条孤儿用例红;本轮实现全绿。新增测试文件 **5 个 Go**(`agent/internal/stubcli/stubcli_test.go` 3 / `agent/claude/stub_matrix_test.go` 5 / `agent/pi/stub_matrix_test.go` 5 / 两侧 `main_test.go` 各 1)+ 改 4 个既有 Go 文件(`agent/claude/claude.go` / `agent/pi/pi.go` / `agent/process_unix.go` / `agent/process_windows.go`)+ 3 个既有测试文件的断言(`claude_test.go` 的 `TestParseOutputNonJSON`、`claude_audit_test.go` 的 `TestParseOutputRejectsInvalidDecision`、`claude_argv_test.go` 的假 CLI 形态);前端**未改动** |
| 88-89 | **第二十五轮(迭代 30 收口)**:git 出口脱敏 / 状态码语义 / 前端错误文案统一 / 错误文本去内部细节 / panic 兜底 / 路由级 404 文案 | ✅ 已落地(工作区,待提交) | **#330-#335 共 6 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 88-89 小节):**#330**(P0,越界)`GET /api/git/diff` 返回**未脱敏**文件内容(用户代码库里的 `.env` / 含密钥文件凭据明文进响应体,再由 GitPanel 渲染到屏幕)→ git 包的**五个出口**(`Status`/`Diff`/`Log`/`Commit`/`CurrentBranch`)一律过 `logx.Mask`(键名 ∪ 值形态,**与节点 output 出口同源**);为什么不用更松的"只按值形态":`.env` 里的密码多半是**低熵**的(`DB_PASSWORD=hunter2`)—— 值形态规则判它"不是凭据",只靠它秘密照样明文出站;**可读性回归锁**:普通 diff 拿 `git diff` 原文**逐字节**比对;**#331**(P1)内部错误(DB 锁死 / Agent 崩溃 / 超时)**一律报 400** → 用户与监控无法区分"填错了"与"后端炸了",且 `api/server_test.go` 的"错误必须是 4xx"断言把错误行为**钉成了契约** → 新增 `statusForError`(**先 code 再 kind**):内部类 → 500/502/504,**既有 4xx 映射一条没动**;**#332**(P1)同一后端错误前端**两种文案**(43 处 `toast.error` vs 16 处 `reportError`;丢掉 scope、丢掉错误码、绕过 `NETWORK_HINTS` 的中文翻译 —— 网络失败时弹的是浏览器的原始英文 `Failed to fetch`)→ 43 处逐个换成 `reportError(e, scope)` + **静态守卫**(生产代码里 `toast.error` 只许出现在 `lib/errors.ts`);`NETWORK_HINTS` 补 `AbortError` 与 `timeout of Nms exceeded`;`STATUS_HINTS` 补 5xx;`httpText` 非 JSON 分支截断 200 字;桌面 `toApiError` 补 code/kind 兜底 + panic 归一;**#333**(P2)错误文本夹带 Go/SQL/git 内部细节(`json: cannot unmarshal string into Go struct field AgentConfig.extra_args of type []string`)→ `badRequest` 改结构化错误 + 三个调用点给人话 + `wrap` 出口文本过 `logx.Mask`(原始错误只进日志);**#334**(P2)`wrap` 无 `recover()` → panic 一路冒到 net/http(服务器模式 EOF / 桌面模式泄漏 Go 包路径)→ 兜住后回 500 `INTERNAL`(`http.ErrAbortHandler` 放行;响应已提交时不再写错误体);**#335**(P3)路由级 404 文案"接口不存在"面向开发者 → 面向用户但可定位的措辞。**红→绿证据**:① `diff 不得含凭据明文("sk-ant-supersecret1234567890abcdef")` → 无明文 ② `状态码 = 400,期望 500` → 500 ③ `panic: runtime error: index out of range [3] with length 2 [recovered, repanicked]` → 500 `INTERNAL` ④ `404 文案仍是开发者口径: 接口不存在` → 新文案。新增测试文件 **3 个 Go**(`git/mask_test.go` 6 / `api/git_mask_test.go` 2 / `api/error_contract_test.go` 8)+ 改 2 个既有 Go 文件(`api/server_test.go` 1 处断言 / `git/` 无);Go 测试文件 163 → **171**、顶层 `func Test*` 869 → **899**;前端 24 → **25** 文件 / 293 → **302** 用例;**既有断言变更逐条**:① `api/server_test.go` 的"必须 4xx"→"4xx 或 5xx"(**理由**:它钉住的正是缺陷本身;精确映射改由 `TestErrorStatusMappingByKind` 逐条断言)② 前端 5 条精确匹配的 toast 断言(加了 scope)③ `api.test.ts` 的 `expect(err.code).toBeUndefined()` → `toBe("BAD_REQUEST")`(桌面模式补 code/kind 兜底,旧断言钉的是"两种模式形状不同构")④ 404 夹具同步新文案 ⑤ `Agents.test.tsx` 的 `beforeEach` 加 `resetErrorDedup()`(5 秒去重是生产行为,不是缺陷) |
| 85 | **第二十一轮(迭代 22 收口)**:跨文件动作落地(8 项)/ 守卫能力边界登记 | ✅ 已落地(工作区,待提交) | **#297-#304 共 8 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 85):**#297**(P1)`contextx` 自带 mode → 槽位映射表(与 `workflow/model.ModeSpec` 各演各的 → model 认了而 contextx 没认的 mode **产物静默丢弃**)→ 分派改由 `model.ModeSpecFor(mode).ContextSlot` 决定,`skill` 伪 mode 单列,`submit` 什么都不写,**新增 fail-closed 默认分支**(槽位不认识 → 显式报错,不再静默丢弃);**#298**(P1)mode 取值域没有对外出口(前端硬编码一份 → 后端加了 mode 用户选不到)→ `GET /api/modes` + `WorkflowService.Modes()` + 前端 `modes()` 双分支 + Designer 下拉改后端派生(加载中用 `FALLBACK_MODES` 兜底,控件不留空)+ **跨语言守卫**(前端兜底清单必须与 `model.AllModes()` 逐项一致);**#299**(P2)版本页显示的是重新编码的 YAML(整数 `3` → `3.0`,**内容差异不是排版差异**)→ 改读 `Repo.GetWorkflowVersionDSL` 原文,老库 `dsl_text` 为空 → 回退重编码;**#300**(P3)`template` 的 6 个零引用死常量(看着像清单 → 诱导后来者多抄一份)→ 删除 + 源码级守卫;**#301**(P2)备份 `version` **只写不读**(任何 JSON 都被当备份,"恢复成功"却什么也没恢复)→ 缺失/不识别 → `BAD_REQUEST`;备份**缺 `skill_configs`**(禁用的技能换机器后又可用)→ 导出补齐 + 恢复写回(按本机已有 Skill 过滤)+ 前端文案;**#302**(P3)前端 `types.ts` 的 `"PAUSED"` → 删;**#303**(P3)Wails 绑定过期 → 重新生成(先隔离副本实测:结构性差异只有 `cancelled: number` 一行);**#304**(P3)`api.ts` 的 `stats()` 类型缺 `cancelled` → 补。**另登记有意边界 #305**:运行期状态脱敏守卫的常量表只收**字符串字面量**常量 → 把 `contextx.StateKey*` 改成 `model.ContextSlot*` 的**别名会打掉那道守卫**(实测 8 条"无法解析" + 6 条"登记与实现漂移")→ 改用"字面量 + 两道跨包守卫"达成同一目的。**红→绿证据**:① contextx 源码扫描 6 条 mode 字面量 → 0 ② `/api/modes` 404 → 200 且逐项一致 ③ `VersionDSL` 返回 `max_iter: 3.0` → `3` ④ template 6 个常量 → 0 ⑤ 缺 `version` 的备份**不报错** → `BAD_REQUEST` ⑥ 导出无 `skill_configs` → 有且恢复后禁用状态还原。新增测试文件 **5 个 Go**(`contextx/mode_source_test.go` 5 / `app/application/version_dsl_test.go` 3 / `app/application/backup_skill_test.go` 7 / `api/modes_test.go` 3 / `template/dead_const_test.go` 1)+ 改 2 个既有前端文件(`api.test.ts` +2 / `Settings.test.tsx` +3)+ 新增前端测试文件 1 个(`designer.modes.test.tsx` 8);Go 测试文件 150 → **155**、顶层 `func Test*` 814 → **833**;前端 22 → **23** 文件 / 272 → **285** 用例;门禁 9 步通过(实测 **113s**) |
| 86 | **第二十二轮(迭代 25 收口)**:数值有限性三层防线 / 门禁断言强度 / 跨包常量别名 / 前端请求竞态 / timeout 上界 | ✅ 已落地(工作区,待提交) | **#306-#311 共 6 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 86):**#306**(P1)持久层把 `json.Marshal` 的错误吞掉 → 列被写成**空串**(一个 `.nan` **连坐清空整张 map** + 版本快照变空)→ 新增 `persistence.marshalJSON`(唯一出口),7 处调用点全部**失败即报错**;**读出口不放宽**(老库的空串 `variables_json` 读成空 map);事件列走**可见降级**(占位载荷 + WARN,理由见 #313);**#307**(P1)校验期不查"数值必须有限" → 含 `.nan` 的工作流能存能跑,**条件恒假**、静默走错分支 → 新增 `validator.findNonFinite`(变量表 / 节点配置 / permissions 段递归查)→ 导入返回 **400** + 明确文案,工作流根本没被创建;**#308**(P2)门禁第 6 步的断言判据只看**存在性** → `if 1 != 1 { t.Fatal }` 恒不触发却能骗过整条门禁 → 判据升级为"**可能触发**"(go/ast 静态分析恒假分支;注释天然不算断言),自检 27 → **29** 条,真实仓库**误报 0**(3098 条断言全部可触发);**#309**(P2)状态脱敏守卫的常量解析不支持跨包别名(轮次 85 的 #305)→ 新增 `constAliases` + `resolveAliases`(一层跨包别名,别名链迭代到不动点),扫描范围加 `workflow/model`;**别名漂移仍能被发现**;**#310**(P2)前端请求竞态(旧前缀 / 旧执行的迟到响应覆盖新结果)→ 新增 `frontend/src/lib/request-guard.ts`,`EventsAudit`(输入不再每次按键发请求)+ `ExecutionMonitor`(守卫 + 切换时先清空上一条执行)接入;**#311**(P2)`timeout_seconds` 无上界 → `time.Duration` 溢出(立即超时 / 静默改用适配器默认 600s)→ 范围常量收敛到 `workflow/compiler`(`TimeoutSecondsMin/Max` = 0..86400,约定 13),校验期加上下界(**下界取 0** = 不设节点超时,与运行期口径一致)。另登记 3 条开放项:**#312**(运行期钳制 `timeout_seconds`,`executor.go` 不归本轮所有)、**#313**(`SaveEvent` 改回"失败即报错" + `api/sse_marshal_test.go` 的粗判据精化,`api/**` 不归本轮所有)、**#314**(导入期有限性预检,`workflow/dsl` 不归本轮所有,校验期已拦住故可省)。**红→绿证据**:① `variables_json="" len=0` → `SaveWorkflow` 报错且一行不落 ② 导入 200 → **400 + 明确文案** ③ 恒不触发的断言 → 门禁**红** ④ 别名化后 8+6 条报错 → 守卫**全绿** ⑤ 迟到响应覆盖 → 被丢弃 ⑥ `timeout_seconds: 10000000000` 校验通过 → 校验失败。新增测试文件 **3 个 Go**(`persistence/finite_json_test.go` 7 / `workflow/validator/finite_test.go` 6 / `workflow/validator/timeout_seconds_test.go` 4)+ 改 1 个既有 Go 文件(`workflow/runtime/state_mask_test.go` +2)+ 新增前端测试文件 **1 个**(`frontend/src/lib/request-guard.test.ts` 4)+ 改 2 个既有前端文件(`EventsAudit.test.tsx` +2 / `ExecutionMonitor.test.tsx` +2);Go 顶层 `func Test*` 833 → **853**;前端 23 → **24** 文件 / 285 → **293** 用例;门禁 9 步通过 |
| 87 | **第二十四轮(迭代 28 收口)**:submit 产物进上下文链 / SSE 用例改确定性同步点 / 权限死入口清理 / 文档口径校正 | ✅ 已落地(工作区,待提交) | **#321-#329 共 9 条**(**#321**/**#322**/**#323** 落地,#317 关闭;#324-#329 为文档口径项,其中 #325 登记为开放项 —— 逐条见 `docs/OPTIMIZATION.md` 轮次 87):**#321**(P2)`submit` 模式产物**静默丢弃**,`last_output` 留**陈旧值** → 下游 `{{last_output}}` 拿到上一个节点的旧值且**无任何告警**("空槽位告警"只报空、不报陈旧)→ `model.ModeSpec` 给 `ModeSubmit` 设 `ContextSlot: last_output`,并把 mode → 槽位改成**全覆盖**(每个合法 mode 都必须有非空槽位 —— 结构性修法,"陈旧"从此不可达);**#322**(P2)SSE 慢客户端用例的 **5s 硬阈值** → 并行负载下误红 → 判据改成**确定性同步点**(等"Emit 已返回"的信号,不比秒数;超时只作死锁兜底),并新增一条用 `blockedWriter` 把"客户端卡住"变成确定事实的用例(两个同步点 + 断言队列丢弃分支被走到);**#323**(P3)`permission.Manager.Apply`(静默版)零调用点 → **删除**,并用反射断言锁住"不得再有静默入口";**#324**(P3)门禁第 7 步依赖**仓库根** `node_modules/.bin/eslint` → `docs/TESTING.md` 记录前置条件(`AGENTS.md` 不归本代理,措辞建议见交付报告);**#325**(P3)门禁自检计数是"人肉同步的常量" → **登记为开放项**(`scripts/**` 不归本代理,建议改法:自检注册表);**#326**(P3)`#297` 的"fail-closed 默认分支"表述与实现不符 → 写清 `contextx/context.go:261`(未知 mode → 静默不写)与 `:295`(未知**槽位** → 显式报错)是两类输入;**#327**(P3)`#290` 的断言判据描述已过时 → 改成 **go/ast 静态分析 + `isTestReceiver` 门控**(不再"任何 `X.Error(` 都算断言");**#328**(P3)第十四节口径过期(自称已按 `3e4a5b3` 逐条复核,实际其后还有 5 个 commit)→ 写明基线;**#329**(P3)顶层 `func Test*` 文档写 851 ≠ 实测 → 更新并写明统计口径与基线。**红→绿证据**:① 下游提示词 `"上次输出:执行完成-OLD"` → `"上次输出:提交摘要-NEW"` ② overlay 造坏(同步写客户端)→ `Emit 被慢客户端阻塞(触发事件的那次 Emit 没有返回)` ③ overlay 造坏(加回 `Apply`)→ `Manager 不得再有静默入口 Apply`。新增测试文件 **3 个 Go**(`workflow/runtime/submit_slot_test.go` 2 / `workflow/model/mode_slot_test.go` 3 / `permission/dead_entry_test.go` 1)+ 改 2 个既有 Go 文件(`api/sse_test.go` / `workflow/runtime/node_config_test.go`);Go 顶层 `func Test*` 853 → **869**(含第二轮在途改动带来的用例);门禁 9 步通过 |
| 87(续) | **第二十七轮(迭代 27 收口)**:凭据形态的"键值粘连"泄漏 / 门禁死断言的三类新形态 | ✅ 已落地(工作区,待提交) | **#318-#320 共 3 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 87 小节的前三行):**#318**(P1)凭据在"**键值粘连**"形态下(`AK=AKIA…` / `+AKIA…`)从**六个出口**明文泄漏 —— 根因是凭据 token 化把 `=` / `+` 并进同一个 token,导致 AWS 密钥 ID 的 `^…$` 规则、已知前缀的 `HasPrefix`、高熵兜底**四路全不命中** → token 化改为**按片段判定**(摘掉行首 diff 前缀 / 按首个 `=` 切开且**只有右侧非空才切** —— base64 的填充在末尾 → **不截断 base64**),片段判不出时退回整段判定(既有行为逐字节不变);**#319**(P1)同源补漏:预筛提示词漏了 `aida` / `aroa` / `anpa` / `acca` 四个 AWS 前缀(连预筛都过不去)+ `Basic <base64>` 短一档(`dXNlcjpwYXNz`)漏判 → 预筛与 `awsAccessKeyPattern` 前缀集**对齐**,`basicAuthToken` 增加"**能解出含 ':' 的字节**"这第四档(英文短语解不出 ':' → 不误伤);**#320**(P1)门禁第 6 步的断言扫描可被**三种手法**骗过(实测 9/9 全绿放行)→ 增加三类死断言判定(**零次循环** / **无匹配分支** / **未被调用的辅助函数**),报告里写明被哪条规则抓到;**跨文件口径**:调用点按**包**收集(包内所有 `_test.go` 文件)→ 跨文件调用的辅助函数不会误判;**自检 29 → 32 条**(B1/B2/B3 三条红场景,B3 同时断言绿方向)。**红→绿证据**:① `Mask 出口仍含凭据明文: "AK=AKIAIOSFODNN7EXAMPLE"` → `AK=***` ② 隔离副本第 6 步`OK(25 个包均有真实执行并通过的用例…)` → `❌ VERIFY FAIL: …(零次循环)/(无匹配分支)/(未被调用的辅助函数)`。新增顶层 `func Test*` **+5 例**(顶层 864 → **869**;测试文件 163 **不变** —— 只改既有的 `logx/credential_test.go`);前端未改动;**误报 0**:新扫描器与旧扫描器在 21 个包 / 163 个测试文件上逐包 live 计数**完全一致**;门禁 **9/9 通过**(123s,+0.003% 耗时) |
| 86(续) | **第二十三轮(迭代 26 收口)**:运行期 timeout 钳制 / SaveEvent 收口 / 导入期预检判定 | ✅ 已落地(工作区,待提交) | **#312-#314 共 3 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 86 续):**#312**(P1)运行期不钳制 `timeout_seconds` → **存量**工作流里的超大值照样溢出(`time.Duration` 溢出成负 deadline → **立即超时**;`1e20` → `int` 溢出成负 → **静默改用适配器默认 600s**)→ 运行期按 `workflow/compiler` 的 0..86400 钳制(超上界钳上界 / 负值归 0 / **先按浮点取值再钳制**),**保持 `cfg*(node, "字面量")` 形态**(契约探针仍然有牙齿:隔离副本造坏 → 探针立刻报"运行期没有读取它");**#313**(P2)`SaveEvent` 走"可见降级"(占位载荷)而不是"失败即返回错误" → 改回失败即报错,**递归风险已核实**(`persistFailGuard` 最多一级嵌套、不级联;`runtime.error` 载荷可 JSON 化 → 链路自然终止;嵌套 Emit 不死锁:`Bus.Emit` 先释放锁再分发);**#314**(P3)导入期有限性预检 —— **判定:不必做**(导入路径确实走校验器:`import → ImportYAML → saveModel → validateWorkflow → validator`),改为把该事实**锁成测试**(导入含 `.nan` → 400 + "有限数值" + 工作流根本没被创建)。另登记 3 条:**#315**(P2)SSE 的"失败事件"契约断言过粗 —— 拿"执行 ID 不得出现在流里"当判据,反而把正确的修法挡住 → 改成**顶层 `type`** 判据 + 新增 `runtime.error` **正向**断言;**#316**(P3)事件落库失败缺"载荷级"故障的回归锁 → 新增用例(只有一条 `runtime.error`、审计流里只有失败信号);**#317**(P3)`cfgInt`/`toInt` 失去生产调用方 —— 清理项,**仍开放**。**红→绿证据**:① `timeout = 10000000000 / -9223372036854775808 / -1 / 30`(小数秒)→ 全部落到 86400 / 0 / 0 / 0;script 节点 `FAILED(context deadline exceeded);耗时 547.637µs` → **SUCCESS** ② `SaveEvent 不得失败` → **返回错误且一行都不落库**;SSE 流里出现 `runtime.error`、**没有** `node.started` ③ 导入含 `.nan` → **400 + 明确文案**。新增测试文件 **2 个 Go**(`workflow/runtime/timeout_seconds_test.go` 2 / `workflow/runtime/event_persist_payload_test.go` 1)+ 改 3 个既有 Go 文件(`persistence/finite_json_test.go` / `api/sse_marshal_test.go` / `api/server_test.go` +1 例);Go 测试文件 158 → **160**、顶层 `func Test*` 854 → **857**;前端**未改动**(24 文件 / 293 用例不变);门禁 9 步通过 |
| 84 | **第二十轮(迭代 21 收口)**:mode 取值域单一来源 / 取消收尾 / PAUSED 移除 / 统计分列 / 死列裁决 | ✅ 已落地(工作区,待提交) | **#291-#296 共 6 条,全部关闭**(逐条见 `docs/OPTIMIZATION.md` 轮次 84):**#291**(P1)`mode` 取值域有**三份**清单(校验期只查必填不查取值 / 运行期不校验 / `ModeRequiresWrite` 对未知 mode 返回 false → **写权限检查被跳过**)→ 取值域收敛到 `workflow/model`(`AllModes`/`ModeSpecFor`/`IsValidMode`),校验期报 `INVALID_NODE`、运行期 fail closed(排在写权限检查**之后**,两道门都不绕),`ModeRequiresWrite(未知)=true`(fail closed,**有意**改掉旧断言);**#292** `compiler.KeyTask` 与 `contextx.StateKeyTask` 两份字面量 → compiler 消费 contextx 的定义 + 守卫测试;**#293** 统计把 FAILED 与 CANCELLED 混在一列 → 分列 + 成功率分母排除 CANCELLED + "各列之和 == 总数";**#294** 取消 WAITING_USER 后节点永远停在"等待中" → 非终态节点落 SKIPPED + `execution_nodes` 同步;**#295** `PAUSED` **从来不可达** → 状态机/运行期/持久层/前端徽章/事件常量整条移除,老库的遗留行**读出口归一为 CANCELLED**(不写库改数据);**#296** 8 个"只写不读"的列 → 逐列裁决(5 删 + 2 加读点 + 1 本就有读点),老库兼容靠**按表实际拥有的列拼 INSERT**(`persistence.configUpsert`,老库的 NOT NULL 无默认值列照常补齐)。**红→绿证据**:隔离副本实测旧代码下 ① 非法 mode `valid=true` ② 未知 mode 的写权限检查被跳过(节点跑成 SUCCESS)③ `ModeRequiresWrite("unknown")=false` ④ `failed` 列把 CANCELLED 也算进去 ⑤ 取消后节点停在 WAITING ⑥ 遗留 PAUSED 行原样读出 ⑦ 新库仍建着 5 个死列 ⑧ Dashboard 成功率 60%(应为 75%)。新增测试文件 **9 个 Go**(`workflow/validator/mode_domain_test.go` 3 / `workflow/runtime/mode_domain_test.go` 3 / `workflow/runtime/mode_contract_test.go` 3 / `workflow/runtime/cancel_skip_test.go` 2 / `workflow/model/execution_state_test.go` 1 / `workflow/compiler/taskkey_contract_test.go` 2 / `persistence/stats_test.go` 2 / `persistence/legacy_state_test.go` 3 / `persistence/dead_columns_test.go` 5)+ 改 4 个既有文件(`agent/mode_test.go` / `workflow/model/model_test.go` / `persistence/exec_state_guard_test.go` / `persistence/repo_cas_test.go`);前端 `Dashboard.test.tsx` +3 例 / `state-badge.test.tsx` +1 例;Go 测试文件 141 → **150**、顶层 `func Test*` 790 → **814**;前端 22 文件 / 268 → **272** 用例;门禁 9 步通过(实测 **112s**) |
| 83 | **第十九轮(迭代 20 收口)**:空上下文可见 / 门禁第 6 步的断言维度 | ✅ 已落地(工作区,待提交) | **#280 关闭**:"上游什么都没产出"曾是**静默空上下文**(`contextx.Build` 总是把链键写进数据表 → 模板引擎按"键存在 = 可解析"判缺失 → `{{plan}}` 永远不被报成缺失 → 下游拿到空串、节点照常 SUCCESS、**零告警**)→ 改**上下文层报"空槽位"**(`contextx.BuildWithSlots` 产出"键存在但值为空"的链槽位清单;运行期把**被提示词引用到的**空槽位报成 `node.warning` / `reason="empty_context_slots"`),**可见但不阻断**;`template` 包的"键存在 = 可解析"口径**不动**(渲染层口径 ≠ 上下文层事实)。**#289 关闭**:门禁第 6 步只看"**有代码跑过**"→ 一个**零断言**的用例(`{ _ = 1 }`)真的执行、真的 pass → **9 步门禁全绿**(隔离副本实测 exit 0)→ 第 6 步加**第二个判据**:包里必须**真的有断言**(静态扫描;实测 `go vet -tests` **检测不出**零断言测试,断言数不在框架事件里);扫描**只限 `*_test.go`**且**先剥注释**(注释里的 `t.Errorf` 不是断言 —— 实测不剥注释会让只提注释的零断言包溜过去);自检 +4 条(23 → **27**)。**#290 新登记**:断言判据"按包 + 文本级"的粒度与实现 —— **有意边界,不修**(细到"每个用例"必然误报)。新增顶层用例 **+7 例**(`contextx/context_test.go` 4 / `workflow/runtime/context_chain_test.go` 3),**未新增测试文件**(141 → **141**),顶层 `func Test*` 782 → **789**;前端 22 文件 / **268** 用例(未动前端代码);门禁 9 步通过(实测 **113s**),新判据在真实仓库上**误报 0** |
| 82 | **第十八轮(迭代 20)**:设置读失败可观测 / 内置 Agent 注册失败上抛 / Set 白名单化 | ✅ 已进主干(`3e4a5b3`) | #281-#283 共 **3 条**,**全部关闭**:**#281** `Settings.Info()` 吞掉 `git_working_dir` 的**读错误** → 静默回落到内存态目录,界面显示的不是库里那个,**用户一保存就把损坏行覆盖成内存值**(损坏事实消失)→ 改**三态分开**(读到 / `NOT_FOUND` 正常初始态 / 其它读失败)+ 记 ERROR + 响应新增 `git_working_dir_read_failed` 标记(`omitempty`,正常响应形状不变);启动路径同样留痕;**#282** `reloadAgents` 里 4 处 `_ = a.Agents.Register(...)` → 内置 Agent 注册失败**一行日志都没有**(此前不可达,但"不可达"不等于"永远不可达")→ 改 **ERROR 日志 + 上抛**(`NewApp` 启动即失败,零误伤风险);**#283** `SettingsService.Set(key, value)` **任意键写入**经 Wails IPC 暴露(`main.go:310` 的 `wails.NewService(app.Settings)` + 生成绑定 `settingsservice.ts:48`)→ 可绕过 `permMu` 写 `agent_policies`(并发保护失效)、绕过 `git_working_dir` 存在性与 `log_level` 校验 → 改**白名单三键** + **复用 `Update` 的同一套校验** + **值类型校验**(此前 `value any` 可把字符串 `"true"` 写进 `allow_script_nodes`),`agent_policies` **刻意排除**;`Update` 自己也改为复用 `Set`(设置写入的**唯一实现**)。新增测试文件 **3 个 Go**(`app/application/settings_read_failure_test.go` 6 例 / `app/application/agent_registration_test.go` 2 例 / `app/application/settings_set_whitelist_test.go` 8 例);新增专节"轮次 82"一节;Go 测试文件 138 → **141**,顶层 `func Test*` 766 → **782**;前端 22 文件 / **268** 用例(未动前端代码);门禁 9 步通过(实测 **118s**) |
| 81 | **第十七轮(迭代 19)**:跨节点上下文链路修复 / env 第四态 / 权限三态 / argv 凭据告警 | ✅ 已进主干(`e62f4ff`) | #270-#278 共 **9 条**,**全部关闭**:**#270**(**P0**)跨节点上下文整条链是死的(`{{plan}}` / `{{review}}` / `{{issues}}` / `{{test_result}}` **永远渲染成空串** —— 而仓库自带的 `workflows/examples/coding-task.yaml` 就在用 `{{plan}}`,这条示例**从未生效过**却一直报 SUCCESS)→ 改写**活的会话状态**并让写失败显式失败 + 键名统一到 10 个常量 + `pickText` 取第一个**非空**值(同链第二个断点);**#271** env 表的**第四态**缺失(只改模型的请求会顺手删掉全部凭据)→ 缺省 = 不修改 / `{}` = 显式清空 / `{k:v}` = 整表替换;**#272** 权限端点无法区分"缺省"与"false"(缺键 → false → 其余能力**静默吊销**)→ 改局部更新 + 非布尔值 400;**#273** **argv 是凭据的"本机可见"出口**(`extra_args` 原样进子进程 argv)→ 启动 CLI 前按**值形态**告警(只报下标**不回显值**)+ **绝不改写 argv**;**#274** `retry.max_attempts` 非数值 → 重试被静默关闭 → 运行期 **fail closed** + 校验期子键类型校验(两边同源);**#275** mock `behavior` 整体替换 → 改**逐键合并** + 未知键/错类型报错;**#276** 出口分支下标写失败被吞(恢复路径回放分支的**唯一依据**)→ 写失败返回 error;**#277** Git 工作目录不校验存在性("保存成功"但之后每个节点都失败)→ 落库前校验;**#278** 备份恢复的"运行时应用失败"信号前端整个丢掉 → 前端补类型与警告。新增测试文件 **11 个 Go**(`workflow/runtime/context_chain_test.go` 10 例 / `contextx/context_test.go` +5 / `api/agent_config_env_test.go` 2 / `api/agent_permission_patch_test.go` 4 / `app/application/agent_env_contract_test.go` 4 / `app/application/settings_gitdir_test.go` 4 / `app/application/mock_behavior_test.go` 6 / `agent/claude/claude_argv_test.go` 2 / `agent/pi/pi_argv_test.go` 2 / `workflow/runtime/retry_policy_test.go` 5 / `workflow/validator/retry_config_test.go` 5 / `workflow/compiler/route_branch_test.go` 3)+ 改 4 个既有文件(`contextx` / `state_mask_test.go` / `cas_conflict_test.go` / `node_config_test.go`);新增专节"轮次 81"一节;Go 测试文件 126 → **138**,顶层 `func Test*` 711 → **766**;前端 22 文件 / **268** 用例;门禁 9 步通过(97s) |
| 80 | **第十六轮(迭代 17)**:单源回归锁去恒真 / 取消残余窗口的边界 | ✅ 已进主干(`e62f4ff`) | **#263 已关闭**(恒真的"单源"回归锁):跨包逐字节比对搬到 `agent/credential_mask_test.go`(`agent` 依赖 `logx`,logx 反向 import agent 会成环 → 只能写在能同时 import 两边的包里),23 条输入 `agent.MaskCredentialValues(x)` 与 `logx.MaskCredentialValues(x)` 必须逐字节相同;`logx` 侧同名用例改成比对**两个不同的入口**;覆盖矩阵加"Mask 不得比 MaskCredentialValues 少遮";加**防空转守卫**(函数对象 + AST)。**造坏四组全红**(实现改坏 / agent 侧自写一份 / 改成别名 / 比对改回同一函数)。**#269 新登记**:取消的残余窗口(读库之后、执行恰好完成 → 200 而最终 COMPLETED)**实测 ≈0.5ms**(上一轮估的"µs 级"宽一个量级;成因是**两段 DB 往返**("请求到达 → ctx 真被取消" + "最后一次取消检查 → 终态落库"),分段测量后各 ≈250µs),不是句柄摘除;220 次随机采样:混合带 [-300µs,+250µs],约一半落到 200 假成功,9/220 ≈ 4%)→ **判定为可接受的边界**(状态与事件流仍正确:执行真的跑完了 → COMPLETED + 只有一条 workflow.completed + 没有 workflow.cancelled;用户的取消意图已满足),不修代码,改**语义登记 + 确定性用例钉死**(`workflow/runtime/cancel_residual_test.go`,探针 + 阻塞事件总线;造坏:人为关上窗口 → 用例变红)。新增测试文件 **1 个 Go**(`workflow/runtime/cancel_residual_test.go` 1 例)+ 改 `agent/credential_mask_test.go`(+2 例)/ `logx/credential_test.go` / `workflow/runtime/engine.go`(注释 + 测试接缝 `cancelProbe`);新增专节"轮次 80"一节;Go 测试文件 126 → **127**,顶层 `func Test*` 711 → **714**;前端未动 |(恒真的"单源"回归锁):跨包逐字节比对搬到 `agent/credential_mask_test.go`(`agent` 依赖 `logx`,logx 反向 import agent 会成环 → 只能写在能同时 import 两边的包里),23 条输入 `agent.MaskCredentialValues(x)` 与 `logx.MaskCredentialValues(x)` 必须逐字节相同;`logx` 侧同名用例改成比对**两个不同的入口**;覆盖矩阵加"Mask 不得比 MaskCredentialValues 少遮";加**防空转守卫**(函数对象 + AST)。**造坏四组全红**(实现改坏 / agent 侧自写一份 / 改成别名 / 比对改回同一函数)。**#269 新登记**:取消的残余窗口(读库之后、执行恰好完成 → 200 而最终 COMPLETED)**实测 ≈0.5ms**(上一轮估的"µs 级"宽一个量级;成因是**两段 DB 往返**("请求到达 → ctx 真被取消" + "最后一次取消检查 → 终态落库",分段测量后各 ≈250µs),不是句柄摘除;220 次随机采样:混合带 [-300µs,+250µs],约一半落到假成功,9/220 ≈ 4%)→ **判定为可接受的边界**(状态与事件流仍正确:执行真的跑完了 → COMPLETED + 只有一条 workflow.completed + 没有 workflow.cancelled;用户的取消意图已满足),不修代码,改**语义登记 + 确定性用例钉死**(`workflow/runtime/cancel_residual_test.go`,探针 + 阻塞事件总线;造坏:人为关上窗口 → 用例变红)。新增测试文件 **1 个 Go**(`workflow/runtime/cancel_residual_test.go` 1 例)+ 改 `agent/credential_mask_test.go`(+2 例)/ `logx/credential_test.go` / `workflow/runtime/engine.go`(注释 + 测试接缝 `cancelProbe`);新增专节"轮次 80"一节;Go 测试文件 126 → **127**,顶层 `func Test*` 711 → **714**;前端未动 |
| 79 | **第十五轮(迭代 21B)**:取消终态语义 / ExtraArgs 值形态 / 门禁第 6 步改判框架事件 | ✅ 已进主干(`908ba0b`) | #264-#268 共 **5 条**:**#264** `Engine.Cancel` 的终态快路径只看进程内句柄 → 取消一个刚写完终态的执行返回 200 假成功(实测窗口 ≈210µs,紧密轮询 40/40 命中)→ 改为**先读库里的状态**(约定 4 的唯一裁决来源):终态 → `INVALID_TRANSITION`(400)/ 运行中 → 照旧 / 不存在 → 404,修后 40/40 命中窗口但 **0/40** 假成功;**#265** `MaskExtraArgs` 只认参数名 → 值形态凭据(`--header "Authorization: Bearer ghp_xxx"`、`--foo sk-ant-xxx`)明文外泄 → 判据改为**参数名 ∪ 值形态**(形态规则仍取 `logx` 唯一来源),掩码/合并/剔除三处共用同一口径,顺带修掉"布尔开关被当凭据掩掉"与"往返丢引号"两个旧 bug;**#266** 门禁第 6 步用**文本解析**判"真实用例" → 用例自己 `fmt.Println` 伪造的 `=== RUN`/`--- PASS` 能骗过 → 改判 `go test -json` 的**框架事件**,自检 16 → **17** 条;**#267** 备份导出的**明文范围**比文档声明的更广(`env` / `behavior` / `extra_args` + 工作流变量)→ **文档口径更正**(`docs/api.md`),导出明文本身是约定 8 允许的有意取舍;**#268** 取消**先到**、协程**后启动** → 协程仍把工作流跑完(库里"已取消"而副作用已发生)→ `execute` 启动即核对库里的状态。新增测试文件 **3 个 Go**(`api/cancel_semantics_test.go` 1 例 / `workflow/runtime/cancel_semantics_test.go` 7 例 / `agent/config_mask_shape_test.go` 11 例)+ 改 `workflow/runtime/cas_conflict_test.go`;新增专节"**门禁第 6 步:伪造输出绕过**"(本项目最独特的一条工程教训)与"轮次 79"一节;Go 测试文件 123 → **126**(含未跟踪),顶层 `func Test*` **692**;前端 22 文件 / **246** 用例(本轮未动前端代码) |
| 71-78 | **第十一至十四轮补登(4 个 commit)**:孤儿包盲区 / `submit` 模式权限 / 设置省略字段 / 损坏列显式失败 / **P0 裸凭据形态全链路明文泄漏** / 前端测试稳定性 / **并行分支错误传播竞态** / **门禁绕过 N1·N2** | ✅ | #245-#263 共 **19 条**:**全部关闭**(其中 **#263 由第十六轮关闭** —— #246 的"单源"回归锁恒真)。第十二轮**已进主干**(#245 P0 裸凭据形态全链路明文泄漏 / #246 掩码规则两套实现并存 / #247 形态 × 出口覆盖矩阵 / #248 前端测试同步点错误 / #249 前端确定性类级守卫),第十一轮**已进主干**(孤儿包盲区 / `submit` 模式权限 / 设置省略字段 / 损坏列显式失败),第十三轮**已进主干**(#250 并行分支错误传播竞态 → **失败台账** / #251 孤儿包平台枚举全集 / #252 门禁看不见全 skip / #253 备份恢复吞错 / #254 事件 data 列损坏 / #255 `behavior` 未掩码 / #256 HITL `responses` 单态断言 / #257 双模式断言面 / #258 `debounce.ts` 零覆盖);第十四轮(迭代 18)**已关闭 4 条门禁自检完备性缺口**(#259 第 6 步判定无自检 / #260 目录混包无自检 / #261 枚举可换回手写清单 / #262 自检自身无防空转守卫),**并收口 5 条开放项**(#233 SSE marshal 丢弃 / #234 `crypto/rand` 吞错 / #256 配置侧 / #208 并发探针重叠窗口 / #210 asArray 守卫单点绕过);新增测试文件 **7 个 Go**(`workflow/runtime/parallel_failure_test.go` / `workflow/compiler/loopguard_history_test.go` / `agent/config_behavior_test.go` / `app/application/{behavior_mask,backup_apply}_test.go` / `persistence/corrupt_json_test.go` / `workflow/runtime/failure_ledger.go` 源码)+ **2 个前端**(`lib/api.test.ts` +20 条 / `lib/debounce.test.ts` 9 例),第十四轮再加 **6 个 Go**(`api/sse_marshal_test.go` / `app/application/randid_test.go` / `workflow/runtime/randid_test.go` / `workflow/runtime/hitl_responses_test.go` + 改 `app/application/application_test.go` / `frontend/src/test/list-guard.test.ts`);新增**四个专节**:"可执行覆盖矩阵(形态 × 出口)"、"**失败台账(并发路径的唯一事实源)**"、"轮次 78:门禁自检的完备性加固"、"**门禁第 6 步:伪造输出绕过**";Go 测试文件 114→**123**、前端 21→**22** 文件 / 204→**246** 用例;门禁自检 **9 条 → 16 条**(第十五轮 16 → **17**),并加**双向断言 + 场景计数守卫 + 助手防空转**三层防恒真机制 |
| 61-70 | **第六至十轮补登(4 个 commit)**:门禁硬伤 / 凭据回写 / 制品脱敏 / 门禁自检 / 权限单一源 | ✅ | #212-#244 共 **33 条**:当轮修复 30 条(#212 守卫防空转双下限 / #213 状态机一致性交叉测试 / #214 用例存在性判据细化 / #215 eslint 未声明 / #216 前端 `&&` 链短路 / #217 embed 兜底 / #218+#221 `CODE_HINTS` 64 码 + 双向守卫 / #219 桌面 IPC 转 `ApiError` / #220 `updateAgentConfig` 回读 / #222 `output` 键 resume 口径差根治 / #223 `asArray` 守卫调用点识别盲区 / #224+#235 ExtraArgs 按名合并 / #225+#236 制品出口脱敏 / #237 `GOOS=windows go vet` / #238 门禁自检 / #239 前端吞错可见化 / #240 权限单一源 / #241 `default_working_dir` 拒绝新值 / #242 `Settings.Update` 先校验后落库 / #243 `on_loop_limit` 白名单 / #244 `--git-dir` 持久化),**3 条仍然开放**(#233 SSE marshal 失败丢弃 / #234 `crypto/rand` 失败吞掉 / #226~#232 中转入第十轮修复的 7 条);新增测试文件 **13 个 Go**(`agent/config_mask_test.go` / `agent/credential_mask_test.go` / `app/application/{agent_workingdir,git_workingdir_clear,masking,permission_tighten,settings_update}_test.go` / `permission/scoped_test.go` / `workflow/{compiler,validator}/looplimit_test.go` / `main_gitdir_test.go` / `workflow/runtime/output_route_resume_test.go`)+ **3 个前端**(`lib/errors.test.ts` / `lib/task-storage.test.ts` / `test/error-guard.test.ts`);新增专节"第八~十轮实测记录"(门禁红/绿对照实验 + **门禁自检** 3+1 用例 + 第九轮 10/10 场景正面证据 + 第十轮 26 条"改坏即红"方向);Go 测试文件 94→**104**、前端 16→**20** 文件 / 153→**190** 用例 |
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

---

## 回归史总结(第三十轮 / 迭代 34 收官)

> **本节是什么**:三十三轮测试演进的**总账** —— 规模怎么长起来的、
> **靠哪几手**长起来的、门禁九步各自**真的拦下过什么**。
> 它**不替代**上面逐轮的记录(那才是逐条证据),只回答"现在这套东西
> 到底值多少、够不够用"。
>
> **本节的数字全部是本轮(2026-10-21)自己统计 / 实跑出来的**:
> `./scripts/verify.sh` → **9 步全绿(111s)**;`npx vitest run` →
> **25 文件 / 302 用例全绿**;`scripts/smoke.sh` → **9 项全通过**;
> `scripts/smoke_sse.sh` → **4 项全通过**。

### 一、规模演进

| 维度 | 起点(轮次 33 前) | 当前(轮次 93) | 变化 |
| --- | --- | --- | --- |
| Go 测试文件 | **41** 个 | **171** 个 | ×4.2 |
| 有测试的 Go 包 | **19** 个 | **22** 个 | +3(`workflow/model`、根包 `agentworkflow`、`agent/internal/stubcli`) |
| 顶层 `func Test*` | 692 个(轮次 79 口径) | **909** 个 | +217 |
| 前端测试文件 | **4** 个 | **25** 个 | ×6.3 |
| 前端用例 | **46** 条 | **302** 条 | ×6.6 |
| 门禁步数 | 1 段 `go test` | **9** 步 | +8 步 |
| 门禁**自检场景** | **0** 条 | **41** 条 | 从 0 到 41 |
| 冒烟 | 无 | REST+SSE+HITL **9 项** + SSE 实时流 **4 项** | 13 项 |
| 覆盖矩阵 | 无 | 凭据形态 × 出口 **13 × 2**(可执行) | 新增 |

> **最重要的变化不是数字,是门禁的性质**:起点时门禁是"**跑一遍,绿了就算**";
> 现在是"**跑一遍 + 证明它自己没被绕过**"(九步 + 41 条自检)。
> 前者只能保证"当前代码能编译能过测",后者才开始保证"**当前代码真的被检查了**"。

### 二、六种关键测试手法 —— 各解决了什么问题

#### 手法 1:造坏验证(改坏实现 → 断言必须变红)

**做法**:新写一条断言后,**把实现改坏**(或在隔离副本里改坏),跑测试,
**确认它真的会红**。红不起来 = 这条断言是**恒真的**,不算断言。

**解决的问题**:
- **#263**:"单源"回归锁断言的是同一个函数比较两次 → **永远绿**。
  造坏(脱敏实现改成原样返回)→ 旧断言**照样 PASS** → 这才暴露它是恒真的。
- **#206 / #256 / #289 / #308 / #320 / #341 / #343**:门禁第 6 步的
  **断言扫描**判据被 **14 种手法**骗过(只在注释里提到断言 / 恒假分支里的断言 /
  零次循环 / 无匹配 switch 分支 / 从未被调用的辅助函数 / 辅助函数只在恒假
  分支里被调用 / 从未被调用的函数字面量 / 辅助函数只在无匹配 switch 分支里
  被调用 / 链式辅助函数 / **以值引用的 5 种**:包级 var / 局部别名 /
  结构体字段 / 方法值 / 链式外层);同族的"**有真实用例**"判定另被 **3 种**
  骗过(0 用例包 / 全 skip 包 / 伪造输出)。
- **#330**:git 出口脱敏 —— 造坏(去掉 `logx.Mask`)→ "diff 不得含凭据明文"
  的断言变红。
- **#344**:CAS 冲突用例在 CPU 负载下**假红**(实测 10/10 失败)→
  前置条件从"等 RUNNING"改成"等 **node.started**",**断言一个字没改**。

**沉淀**:见 `docs/OPTIMIZATION.md` 第十五节 3.2。

#### 手法 2:AST 探针(配置契约 / 状态键分类 / 断言强度)

**做法**:不靠运行时断言,而是**静态读源码**并断言源码的**结构**。

**解决的问题**:
| 探针 | 断言的东西 | 挡住了什么 |
| --- | --- | --- |
| `workflow/compiler/nodeconfig_contract_test.go` | 节点配置的**必填结构**在**校验期与运行期同源** | "运行期支持但校验期判非法"(约定 13) |
| `workflow/compiler/conditionenv_contract_test.go` | 条件表达式的**可用变量集合**只有一份来源 | 同一族 |
| `workflow/runtime/state_mask_test.go` | **状态键分类**(`decision` / `status` 等结构化键原样保留,其余一律脱敏) | 新的状态键漏登记 → 明文落库 |
| `template/dead_const_test.go` | 不存在**零引用的死常量** | 死常量被后人当"清单"再抄一份(#300) |
| `permission/dead_entry_test.go` | 不存在**静默版**的权限入口 | 两套权限实现并存(#323) |
| `scripts/verify.sh` 的 `assertscan` | 断言**可达**(真实调用点)且**可能触发**(非恒假分支) | 恒真测试骗过门禁 |
| `api/modes_test.go` | mode 取值域只有一份来源 | 端点里另写一份字面量清单(#298) |

**为什么需要 AST**:运行时断言**看不见"没有的东西"** ——
"这个键根本没人读""这个常量没人引用""这个权限入口没人调"——
只有**静态读源码**才能断言"它不存在"。

#### 手法 3:隔离副本端到端(门禁防线 / 真实 server 场景)

**做法**:把仓库**整体复制**到临时目录(或 `git archive` 出快照),
在副本里**造坏**、跑**真的**门禁 / **真的** server,验证它红。

**解决的问题**:
- **门禁的 8 次绕过**:每一次都是在**隔离副本**里造出一个"骗过门禁"的包,
  确认门禁**确实被骗过**(红),再修门禁,再确认**变绿**。
- **前端列表守卫**(#210):隔离副本里去掉两个调用点的 `asArray`
  → `list-guard.test.ts` **2 条变红**(上一轮同一实验是 **4 条全绿**)。
- **单源回归锁**(#263):隔离副本里把 `logx.MaskCredentialValues` 改成原样返回
  → 旧断言绿、新断言红。
- **真实 server 场景**:冒烟脚本自己起进程、预检端口、可靠清理 ——
  避免"打到上一次遗留的进程"造成的**假通过**(脚本注释里明写了这个坑)。

**沉淀**:**"门禁绿"必须在副本里被证伪过一次**,否则它只是一个说法。

#### 手法 4:同形替身 CLI(真实适配器路径)

**做法**:写一个**行为与真 CLI 同形**的替身二进制(能吐各种异常形态),
让适配器**真的 exec 它** —— 走完整的进程、管道、退出码、信号路径。

**解决的问题**(轮次 90,#336-#340):
- **#336**(P0)退出码 0 但 stdout 为空 → 适配器判 SUCCESS → 路由静默走默认分支;
- **#337**(P1)stdout 不是合法 JSON 信封 → **决策整个丢失**;
- **#338**(P1)进程组击杀只在取消/超时路径兑现 → **孤儿进程泄漏**;
- **#339**(P3)pi 的自由文本输出是合法形态但**静默成功**;
- **#340**(P2)"信号杀死 + 孤儿持有管道"的组合此前**没有任何用例钉着**。

**矩阵规模**:**14 个场景 × claude / pi 两侧**(空输出 / 只有空白 /
非 JSON / 前置垃圾行 + 尾部 JSON / 非法字节 / 退出码 0 无输出 /
信号杀死 / fork 持有管道 …)。

**沉淀**:**替身不是 mock** —— mock 绕过被测代码,替身**穿过**被测代码。
"空输出"这种缺陷,只有真的跑一个进程才看得见。

#### 手法 5:可执行覆盖矩阵(凭据形态 × 出口)

**做法**:一张**表**,行是凭据形态(13 种:AWS key / `sk-ant-` / `ghp_` /
`Bearer` / `Basic <base64>` / PEM 私钥块 / 高熵裸串 / 键值粘连 /
diff 行前缀 …),列是**出口**;矩阵本身**可执行** ——
每个单元格是一条断言,改脱敏规则就跑一遍矩阵。

**解决的问题**:
- **#245–#247**:裸凭据形态在**六个出口**明文 → 矩阵把"每个形态 × 每个出口"
  钉死,任何一次改脱敏规则漏掉一个格子都会红。
- **#318**(`AK=AKIA…` 粘连)、**#319**(AWS 前缀预筛漏 4 个)、
  **#342**(随机 base64 被"驼峰护栏"放过)、**#346**(双 diff 前缀)。
- **矩阵的"未覆盖"格必须写理由** —— 第十二轮就有一条:制品出口
  **刻意**不脱敏低熵值(保住 diff 的审计证据)。**矩阵逼你把取舍写下来**。

**沉淀**:见 `docs/OPTIMIZATION.md` 第十五节 3.3 —— 矩阵同时是
**脱敏规则的回归锁**与**取舍的登记表**。

#### 手法 6:门禁自检(41 条,验证门禁自身)

**做法**:在门禁里**跑门禁** —— 造一个"骗过我"的包,确认**我抓得住**。

**41 条自检的分布**:

| 位置 | 条数 | 守住什么 |
| --- | --- | --- |
| **第 4 步**(交叉编译 / 孤儿包) | **1-29** | 交叉检查对照(3)/ 孤儿包在 windows·freebsd·js 三个平台上绿+红(6)/ 目录混包(1)/ 目标平台枚举必须是 `go tool dist list` 全集(1)/ 路径排除规则 `build/` 下的项目代码必须在检查面上、Wails 平台脚手架必须继续被排除(4)/ 孤儿包候选必须包含 `build/` 下的包、排除 `testdata/` 下的包(2)/ 其余为各条红方向的对照(绿方向对照) |
| **第 6 步**(每包真实用例 + 断言强度) | **30-41** | 第 6 步判定:正常包不误报 / 0 用例包被抓 / 伪造输出被抓(3);断言扫描:有断言的包不误报 / 只在注释里提到断言被抓 / 恒假分支被抓 / 运行期才成立的正常断言不误判(4);零次循环 / 无匹配分支 / 未被调用的辅助函数(3);恒假分支里被调用 / 从未被调用的函数字面量 / 无匹配 switch 分支里被调用 / 链式辅助函数(4);**以值引用 5 种**:包级 var 取走 / 局部别名取走 / 结构体字段 / 方法值取走 / 链式外层被取走(5) |

**每一条红场景都同时断言**对应的**绿方向**("真调用不得被误报")——
否则判据一收紧就会**误伤真实测试**,门禁红了就得加豁免,最后就没人信它了。

**计数守卫**:`SELFCHECK_EXPECTED=41` 与实际执行条数必须相等 ——
**删掉一条自检并同时把计数改小 → 门禁绿**(这是 #325 登记的、
**方向不对称**的缺口:新增漏改计数 → 门禁红(安全);删除并改小 → 门禁绿(不安全))。

### 三、门禁九步与每一道防线的**实测拦截点**

> 下表左列是门禁的实际步骤(`scripts/verify.sh` 的 `step` 横幅),
> 右列是**这一步真的拦下过什么** —— 全部有实测记录,不是"理论上能防"。

| 步骤 | 做什么 | **实测拦下过的缺陷** |
| --- | --- | --- |
| **1/9** | `gofmt` | **#188 / #192 / #203**:gofmt 的文件清单原先不含**平台专属文件** → 语法错误被吞掉;修法:清单改用 `git ls-files`,且**"gofmt 解析不了 ≠ 已格式化"**(退出码与 stderr 必须被检查) |
| **2/9** | `go vet` | **#251**:孤儿包(只含 `//go:build windows` 的全新包)原先**对整条门禁完全隐形** —— `go list ./...` 不列它;修法:包集合改按 **git 跟踪的 .go 文件**推导,对看不见的目录逐个交叉 vet |
| **3/9** | `go build` | 编译错误(手误级);它是**最便宜**的一道防线 |
| **4/9** | Windows 交叉编译(`GOOS=windows go build` + `go vet`)+ 孤儿包检查 | **#237**:`go build` 的手册明文**忽略 `_test.go`** → 平台专属测试文件的类型错误看不见;**#251**:孤儿包枚举用 `go tool dist list` 全集(实测 47 个 GOOS/GOARCH)+ 批量探测;**#261**:防止枚举被换回手写清单;**#279**(本轮关闭):根级 `build/` 对整条门禁隐形 → 三处路径判定收敛为**一条** `PATH_EXCLUDE_RE` |
| **5/9** | `go test -count=1 -race -v` | **#208**:并发探针原先靠 `time.Sleep(150ms)` 建读写重叠窗口 → 改成**屏障 + 紧凑循环**;"去掉锁"后命中从 0/5 提升到 3-5/5;**#344**:CAS 冲突用例在 CPU 负载下假红(10/10 失败)→ 断言未改,只把"竞态"变成"确定时序";**#15 / #47**:裸 map 的并发读写(runtime fatal,`recover` 救不了) |
| **6/9** | **每个包都必须有真实执行的用例** + 断言强度扫描 | **#209 / #252**(所有用例被 `t.Skip()` 跳过)、**#266**(伪造 `=== RUN` / `--- PASS` 输出 → 改判 `go test -json` 的**框架事件**)、**#289**(零断言的包)、**#308**(恒不触发的断言)、**#320 / #341 / #343**(八种"把断言藏起来"的手法)。判据从**存在性** → **可达性** → **真实调用点** 三级演进 |
| **7/9** | 前端 `lint` / `build`(tsc + vite)/ `vitest` | **#215-#217**(eslint 声明 + 三子检查各自独立 + `//go:embed` 兜底 —— 干净 clone 不再在第 0 步崩)、**#310**(迟到响应覆盖新结果 → 请求竞态守卫)、**#248 / #249**(前端测试用"猜的事件循环节拍数"当同步点 → 换成"**效果本身**";实测连跑 20 + 压测 24 + shuffle 8 + 抢资源 3 = **56 次 0 失败**) |
| **8/9** | 冒烟:服务器模式 REST + SSE + HITL(真实进程) | **#66**(WAIT_USER 答复被丢弃且 Agent 重跑)、**#41**(script 配置失效)、**#32**(script 节点无权限门)、**#72**(pi 嵌套 JSON 解析失败)。**门禁自身**:端口预检 + 进程存活检查 —— 否则"打到上次遗留的进程"会**假通过** |
| **9/9** | 冒烟:SSE 实时流 | **#17**(SSE 未接通)、**#35**(SSE 背压反压引擎);断言改为**确定性同步点**(等"Emit 已返回"的信号,不比秒数) |

**门禁自身的盲区(至今仍开着)**:第 4 条(门禁对根级 `build/` 的盲区)已在
本轮关闭;**#325**(自检计数是"人肉同步的常量")仍开着 ——
修法方向是把自检改成**注册表**(一条自检 = "标签 → 期望行为 → 场景 id"一行),
门禁**从注册表派生**要跑哪些场景,于是"删掉一条自检"必然同时删掉
注册表里的一行 → 计数与注册表对不上 → 门禁**红**。
**要守的不变式**:自检的**集合**只有一个来源,计数是它的**派生物**。
