#!/bin/bash
# 一键完整验证门禁:
#   gofmt → go vet → go build → Windows 交叉编译 → 全量 race 测试
#   → 每个包都必须有真实用例 → 前端 lint/构建/vitest(各自独立)
#   → 冒烟(REST+SSE+HITL)→ 冒烟(SSE 实时流)
#
# 用法: ./scripts/verify.sh   (任一步失败即非零退出)
#
# ---- 路径排除规则:三处判断的**单一事实来源** ---------------------------
# 门禁有三处需要判断"哪些目录不参与检查",三处**必须**用同一套规则:
#   ① 包列表(`go list ./...` 的结果)—— 决定 vet / build / race 测试 /
#      "每个包必须有真实用例"四道防线的检查范围;
#   ② gofmt 文件清单(git 跟踪 + 新增未忽略的 .go 文件);
#   ③ 孤儿包候选目录(由 ② 推导)—— 决定跨平台交叉 vet 的范围。
#
# 三处口径不一致 = 某个目录只在其中一处被剔掉 → 它从另外两处的视野里
# 消失 → 没有任何一道防线看得见它。**第十八轮终验 #279 就是这么发生的**:
# 根级 `build/` 同时被 ① 与 ② 剔掉 → 连带从 ③ 消失 → 放在那里的包对
# 整条门禁**完全隐形**(实测:类型错误 + 零用例 + 伪造 PASS 输出的包,
# 9 步门禁全绿,而 `go build ./build/evilpkg/` 明确报错)。
#
# 规则分三类,**每类都写清理由与边界**:
#
#   A. **Go 工具链约定**(不可见是**正确行为**,不是绕过):
#      任意一级以 "_" 或 "." 开头的目录、任意一级叫 "testdata" 的目录。
#      go 工具**整体忽略**它们(`go help packages`:"Directory and file
#      names that begin with "." or "_" are ignored by the go tool, as are
#      directories named "testdata")→ 它们不属于任何包,本来就不会被编译。
#      这类目录里的代码"隐形"是**约定使然**,门禁不查它们是**对的**。
#   B. **第三方产物**(不是本项目代码):任意一级叫 "node_modules" 的目录。
#      根目录误装的 npm 依赖里夹带了 Go 代码
#      (node_modules/flatted/golang/pkg/flatted),`go list ./...` 会捎带进去。
#   C. **Wails 平台脚手架**(框架产物,**不能**当项目代码编译):
#      模块根下 `build/<平台模板根>/**`,平台模板根 = ios android darwin
#      windows linux(Wails v3 的 build/ 平台模板布局,见 build/Taskfile.yml)。
#      必须排除的原因:`build/ios` 是 `package main`,只有 `//go:build ios`
#      下才有 func main(其余平台只剩 app_options_default.go)→ 在 macOS 上
#      编译必失败("function main is undeclared in the main package");
#      `build/android` 需要 NDK。**已登记项 #120**
#      (docs/OPTIMIZATION.md 的"仍然开放"一节)。
#      边界:**只**排除这 5 个平台模板根,**不再排除整棵 `build/` 树** ——
#      整棵排除正是 #279 的根因;`build/` 下其它位置、以及任何**嵌套**的
#      `<dir>/build/` 都是项目代码,必须进入检查范围。
# ------------------------------------------------------------------------
set -e
set -o pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

TOTAL_START=$(date +%s)

# step <序号> <标题> —— 打印步骤头,结束时回报该步耗时。
step() {
	STEP_NO="$1"
	STEP_NAME="$2"
	STEP_START=$(date +%s)
	echo ""
	echo "== $STEP_NO $STEP_NAME =="
}

# end_step —— 回报本步耗时。
end_step() {
	echo "   (耗时 $(( $(date +%s) - STEP_START ))s)"
}

# fail <消息> —— 打印失败原因并退出。
fail() {
	echo "❌ VERIFY FAIL: $1" >&2
	exit 1
}

# ---- 临时文件清理 ----------------------------------------------------
# go build / go test 的日志与 go build 的产物目录都在这里登记,
# 退出时统一清掉 —— 门禁不该在仓库里留下东西。
BUILD_LOG=""
TEST_LOG=""
BUILD_OUT=""
WIN_OUT=""
WIN_LOG=""
WIN_VET_LOG=""
WIN_CHECK_DIR=""
USAGE_CHECK_DIR=""
ASSERT_SCAN_DIR=""
GOFMT_OUT=""
GOFMT_ERR=""
EMBED_PLACEHOLDER=""
cleanup() {
	if [ -n "$BUILD_LOG" ]; then rm -f "$BUILD_LOG"; fi
	if [ -n "$TEST_LOG" ]; then rm -f "$TEST_LOG"; fi
	if [ -n "$BUILD_OUT" ]; then rm -rf "$BUILD_OUT"; fi
	if [ -n "$WIN_LOG" ]; then rm -f "$WIN_LOG"; fi
	if [ -n "$WIN_VET_LOG" ]; then rm -f "$WIN_VET_LOG"; fi
	if [ -n "$WIN_OUT" ]; then rm -rf "$WIN_OUT"; fi
	if [ -n "$WIN_CHECK_DIR" ]; then rm -rf "$WIN_CHECK_DIR"; fi
	if [ -n "$USAGE_CHECK_DIR" ]; then rm -rf "$USAGE_CHECK_DIR"; fi
	if [ -n "$ASSERT_SCAN_DIR" ]; then rm -rf "$ASSERT_SCAN_DIR"; fi
	if [ -n "$GOFMT_OUT" ]; then rm -f "$GOFMT_OUT"; fi
	if [ -n "$GOFMT_ERR" ]; then rm -f "$GOFMT_ERR"; fi
	# 占位文件:只在它**仍是**我们放的那一个时才清掉(按内容比对)。
	# 前端真实构建已经把它换成真产物的话,一个字都不动。
	if [ -n "$EMBED_PLACEHOLDER" ] && [ -f "$EMBED_PLACEHOLDER" ]; then
		if grep -q 'build placeholder' "$EMBED_PLACEHOLDER" 2>/dev/null; then
			rm -f "$EMBED_PLACEHOLDER"
			rmdir frontend/dist 2>/dev/null || true
		fi
	fi
	return 0
}
trap cleanup EXIT

# ---- embed 目标目录兜底(必须在 go list 之前)--------------------------
# main.go 有 `//go:embed all:frontend/dist`,而 frontend/dist 被
# .gitignore 忽略、从未入库 —— 干净 clone 里 `go list ./...` 直接失败:
#   "pattern all:frontend/dist: no matching files found"
# 门禁在**第 0 步**就退出,后面 9 步一步都跑不到:前端构建与全部前端
# 用例在干净环境里仍然是死代码(只是死在了更前面)。
#
# 兜底方式:目录已存在且**非空** → 一个字节都不动(不碰真实构建产物);
# 缺失或为空 → 放一个占位文件(embed 不接受空目录:"contains no
# embeddable files")。前端真实构建(vite build,emptyOutDir 默认 true)
# 会清掉占位文件换成真产物。
if [ ! -d frontend/dist ] || [ -z "$(ls -A frontend/dist 2>/dev/null)" ]; then
	mkdir -p frontend/dist
	EMBED_PLACEHOLDER="frontend/dist/index.html"
	printf '<!doctype html><title>build placeholder</title>\n' >"$EMBED_PLACEHOLDER"
	echo "提示: frontend/dist 缺失或为空,已放占位文件让 //go:embed 能解析(前端构建后会被真实产物替换)"
fi

# ---- 路径排除规则(单一事实来源)--------------------------------------
# 正则,作用于**模块内相对路径**:包列表先用相对路径判定、再还原成导入路径;
# gofmt 文件清单本来就是相对路径;孤儿包候选由文件清单推导 → 同一口径。
#
#   A. Go 工具链约定("_"/"." 前缀、testdata)不在这里 —— 那些目录
#      **不属于任何包**,`go list` 本来就不会列出来;孤儿包候选按同一约定
#      另行过滤(见 orphan_candidates)。
#   B. node_modules —— 第三方产物,不是本项目代码。
#   C. Wails 平台脚手架 —— 理由与边界见文件头注释。
PATH_EXCLUDE_RE='(^|/)node_modules/|^build/(ios|android|darwin|windows|linux)(/|$)'

# pkg_rel_paths <模块目录> → 该模块下**所有**包的模块内相对路径
# ("." 表示模块根)。失败(不是模块 / go list 失败)返回非 0。
pkg_rel_paths() {
	pr_mod="$1"
	pr_mp=$(cd "$pr_mod" && go list -m 2>/dev/null) || return 1
	pr_list=$(cd "$pr_mod" && go list ./...) || return 1
	printf '%s\n' "$pr_list" | awk -v mp="$pr_mp" '
		{
			if ($0 == "") next
			if (mp != "" && $0 == mp) { print "."; next }
			if (mp != "" && index($0, mp "/") == 1) { print substr($0, length(mp) + 2); next }
			print $0
		}'
}

# gate_pkg_rels <模块目录> → 门禁**纳入检查**的包(模块内相对路径)
gate_pkg_rels() {
	gp_mod="$1"
	gp_rel=$(pkg_rel_paths "$gp_mod") || return 1
	printf '%s\n' "$gp_rel" | grep -vE "$PATH_EXCLUDE_RE" || true
}

# gate_pkgs <模块目录> → 门禁纳入检查的包(**导入路径**,供 go 命令使用)
gate_pkgs() {
	gp_mod="$1"
	gp_mp=$(cd "$gp_mod" && go list -m 2>/dev/null) || return 1
	gp_rel=$(gate_pkg_rels "$gp_mod") || return 1
	printf '%s\n' "$gp_rel" | awk -v mp="$gp_mp" '
		{ if ($0 == ".") print mp; else print mp "/" $0 }'
}

# ---- 本项目的包列表(按上面的排除规则)--------------------------------
if ! PKGS=$(gate_pkgs "$ROOT"); then
	echo "❌ VERIFY FAIL: go list ./... 失败(代码树当前不可编译?),先修代码再跑门禁" >&2
	exit 1
fi
PKG_COUNT=$(printf '%s\n' "$PKGS" | grep -c . || true)
[ "$PKG_COUNT" -gt 0 ] || fail "包列表为空(go list ./... 过滤后一个包都不剩?)"
echo "包列表: $PKG_COUNT 个(已排除 node_modules/ 与 Wails 平台脚手架 build/<平台模板根>/)"

# ---- 每个包都必须有真实用例(自动推导,不手写清单)--------------------
# 此前这里是一份手写的 KEY_PKGS 清单。它防不住自己要防的场景:
# 新增包时忘了把它加进清单 → 新包即使一条用例都没有,门禁照样绿灯。
# (手写清单只能拦住"清单里已有的包变成 0 用例",拦不住"新包 0 用例"。)
# 现在改成:本项目**所有**包(排除 build/、node_modules/)都必须有真实用例,
# 清单从包列表自动推导 —— 新包一落地就被覆盖。
#
# 豁免清单 EXEMPT_PKGS:确实**不该**有测试的包(空格分隔的包路径)。
# 当前为空 —— 本项目 21 个包全部有真实用例。
# 往里加之前先确认这个包真的"没有可测行为"(纯类型定义、或只在某个
# 平台编译、在门禁平台上根本跑不了用例)。把一个该写测试的包豁免掉,
# 等于把"新增包忘写测试"重新放进门禁的盲区 —— 那正是这里要消灭的东西。
EXEMPT_PKGS=""

CHECK_PKGS=""
for p in $PKGS; do
	case " $EXEMPT_PKGS " in
	*" $p "*) continue ;;
	esac
	CHECK_PKGS="$CHECK_PKGS $p"
done
CHECK_COUNT=$(printf '%s\n' $CHECK_PKGS | grep -c . || true)
echo "用例存在性检查范围: $CHECK_COUNT 个包(全项目包 $PKG_COUNT 个)"
if [ -n "$EXEMPT_PKGS" ]; then
	echo "豁免(确实不该有测试的包): $EXEMPT_PKGS"
fi

step "1/9" "gofmt"
# 文件清单来自 git,**不再**从 `go list` 推导 —— 后者只给出**当前平台**
# 参与编译的文件(GoFiles/TestGoFiles/XTestGoFiles),被 build constraint
# 排除的文件(如 agent/process_windows.go)根本不在清单里:在 macOS 上跑
# 门禁,Windows 专属文件可以烂掉没人管(实测:未格式化的
# `//go:build windows` 文件不在旧清单里,门禁放行)。
#
# 清单 = 已跟踪文件 + 新增但未被 .gitignore 忽略的文件:
#   git ls-files                              → 已跟踪
#   git ls-files --others --exclude-standard  → 新增(未跟踪且未忽略)
# 两者相加 = 工作区里所有该做格式检查的 .go 文件。
#
# 前提说明(避免误解):
#   - 被 .gitignore 忽略的目录(bin/、frontend/node_modules/)不在检查
#     范围 —— 里面不是源码,这是有意的。
#   - 路径排除用**同一条规则**(PATH_EXCLUDE_RE):只排除 node_modules/ 与
#     Wails 平台脚手架 build/<平台模板根>/;**build/ 下的其它位置不再被
#     整棵排除**(第十八轮终验 #279 的根因)。
#   - 因此"文件已 git add"不再是前提:未跟踪的新增 .go 文件同样会被检查。
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	fail "不在 git 工作树里,无法枚举 Go 文件(gofmt 清单依赖 git ls-files)"
fi
GOFMT_FILES=$(
	{
		git ls-files '*.go'
		git ls-files --others --exclude-standard '*.go'
	} | grep -vE "$PATH_EXCLUDE_RE" | while IFS= read -r f; do
		# git ls-files 列的是**索引**里的条目:工作区已删、尚未提交删除的
		# 文件仍会出现在清单里 —— 按存在性过滤掉,免得 gofmt 报
		# "no such file"(其余文件仍会被检查,但报错被吞掉很难看)。
		if [ -f "$f" ]; then printf '%s\n' "$f"; fi
	done | sort -u || true
)
GOFMT_COUNT=$(printf '%s\n' "$GOFMT_FILES" | grep -c . || true)
[ "$GOFMT_COUNT" -gt 0 ] || fail "Go 文件清单为空(git ls-files 没给出任何文件?)"
# gofmt 对**语法错误**文件的输出是:stdout 空、stderr 报解析错、退出码 2。
# 此前这里是 `gofmt -l $GOFMT_FILES 2>/dev/null || true` —— stderr 与
# 退出码一起被吞掉,于是"gofmt 解析不了"被当成"已格式化",门禁放行。
# (实测:一个含语法错误的 `//go:build windows` 文件能让整条门禁全绿。)
# 现在 stderr 与退出码都必须被检查:非 0 → 门禁失败并回显解析错误。
GOFMT_OUT=$(mktemp /tmp/awo-verify-gofmt-out.XXXXXX)
GOFMT_ERR=$(mktemp /tmp/awo-verify-gofmt-err.XXXXXX)
GOFMT_RC=0
gofmt -l $GOFMT_FILES >"$GOFMT_OUT" 2>"$GOFMT_ERR" || GOFMT_RC=$?
if [ "$GOFMT_RC" -ne 0 ]; then
	echo "---- gofmt 解析错误(stderr)----" >&2
	cat "$GOFMT_ERR" >&2
	fail "gofmt 解析失败(exit $GOFMT_RC):上述 .go 文件有语法错误 —— gofmt 解析不了时 stdout 为空,不能当成'已格式化'"
fi
if [ -s "$GOFMT_ERR" ]; then
	echo "---- gofmt 警告(stderr,退出码 0)----"
	cat "$GOFMT_ERR"
fi
UNFORMATTED=$(cat "$GOFMT_OUT")
[ -z "$UNFORMATTED" ] || fail "未格式化: $UNFORMATTED"
echo "OK($GOFMT_COUNT 个文件)"
end_step

step "2/9" "go vet"
go vet $PKGS
echo "OK"
end_step

step "3/9" "go build"
# 此前门禁只有 vet 没有 build:vet 能发现类型/语法错,但链接期错误
# (缺 main、符号冲突、平台专属代码)只有 build 才暴露。
#
# `-o` 指向临时目录:go build 在**只编译一个 main 包**时会把可执行文件
# 写到当前目录(项目根),污染工作区(多出一个未跟踪的二进制,还会被
# .gitignore 之外的规则漏掉)。指向目录后产物一律落在临时目录,退出时删掉。
# 现在包数 ≥2,go build 本来就只做检查不落产物 —— 但"靠包数 ≥2"是个
# 脆弱前提(哪天项目只剩一个包,根目录就会多出二进制),用 -o 目录钉死。
#
# 日志落盘再回显:Wails/objc 的链接产物带 macOS SDK 版本告警,每次
# 都会刷十几行;失败时全量打印,成功时只回显非噪声告警。
BUILD_OUT=$(mktemp -d /tmp/awo-verify-buildout.XXXXXX)
BUILD_LOG=$(mktemp /tmp/awo-verify-build.XXXXXX)
if ! go build -o "$BUILD_OUT/" $PKGS >"$BUILD_LOG" 2>&1; then
	cat "$BUILD_LOG" >&2
	fail "go build 失败"
fi
grep -v "was built for newer 'macOS' version" "$BUILD_LOG" || true
echo "OK"
end_step

step "4/9" "Windows 交叉编译(GOOS=windows go build + go vet)"
# 此前 Windows 专属文件(`//go:build windows`)在 macOS/Linux 上**从不
# 参与**任何编译:go list / vet / build / test 都只看当前平台的文件。
# 结果:一个含语法错误或类型错误的 Windows 文件可以让整条门禁全绿 ——
# AGENTS.md 里的 `GOOS=windows go build` 只是**手工**命令,不在门禁里。
# 现在把交叉编译纳入门禁。
#
# 包集合 = $PKGS(与第 3 步同范围,排除 build/ 与 node_modules/):
# 用全量而不是 AGENTS.md 里的 ./agent/... ./workflow/... 子集,是为了
# 任何**新增**的 //go:build windows 文件(不论在哪个包)都被覆盖 ——
# 今天的 Windows 文件只有 agent/process_windows.go,手写子集就会有
# 下一次同样的漏检。
#
# **为什么还要 go vet(本轮新增)**:
# `go build` **不编译 _test.go**(go 命令手册原文:"When compiling
# packages, build ignores files that end in _test.go")。所以第八轮修好的
# 是**源文件**盲区,**测试文件**盲区一直在:
# 实测(本轮):新增 agent/zz_windows_test.go(`//go:build windows`,
# 内含 `var s string = 42`)→ 完整 9 步门禁 exit 0 全绿。
# 任何平台专属的**测试**文件里的类型错误,在 macOS/Linux 上永久不可见。
# `go vet` 会连同 _test.go 一起做类型检查(实测能报出该错误),所以
# 这里把 vet 加进步骤 4。
#
# 两个子检查各司其职,都保留:
#   go build —— 链接期错误(缺 main、符号冲突、平台专属代码);
#   go vet   —— 类型检查,**含 _test.go**。
#
# 不污染工作区:-o 指向临时目录,退出时删掉(多包调用时 go build 本来
# 就不落产物,但根 main 包会落一个 .exe —— 用 -o 目录钉死)。
# GOOS=windows 会自动把 CGO_ENABLED 置 0;本项目无 cgo 文件
# (grep 'import "C"' 无命中),交叉编译不需要 C 工具链。
WIN_OUT=$(mktemp -d /tmp/awo-verify-winbuild.XXXXXX)
WIN_LOG=$(mktemp /tmp/awo-verify-winbuild-log.XXXXXX)
WIN_VET_LOG=$(mktemp /tmp/awo-verify-winvet-log.XXXXXX)

# win_check <工作目录> <包参数>...
#
# 在 <工作目录> 下做 GOOS=windows 交叉检查(build + vet)。
# 抽成函数是为了让**门禁自检**与真正的门禁步骤走**同一条代码路径** ——
# 自检若自己另写一套命令,将来有人把 vet 从这里删掉,自检照样绿,
# 盲区就又回来了(而且没有任何用例会发现)。
#
# 两个子检查的日志分别落盘,失败时回显(否则只看到"失败"两个字,
# 排障时看不到真正的类型错误)。任一失败 → 返回非 0。
win_check() {
	win_dir="$1"
	shift
	win_rc=0
	( cd "$win_dir" && GOOS=windows go build -o "$WIN_OUT/" "$@" ) >"$WIN_LOG" 2>&1 || win_rc=1
	if [ "$win_rc" -ne 0 ]; then
		echo "---- GOOS=windows go build 输出 ----" >&2
		cat "$WIN_LOG" >&2
		return 1
	fi
	( cd "$win_dir" && GOOS=windows go vet "$@" ) >"$WIN_VET_LOG" 2>&1 || win_rc=1
	if [ "$win_rc" -ne 0 ]; then
		echo "---- GOOS=windows go vet 输出 ----" >&2
		cat "$WIN_VET_LOG" >&2
		return 1
	fi
	return 0
}

if ! win_check "$ROOT" $PKGS; then
	fail "GOOS=windows 交叉检查失败:Windows 专属文件(含 _test.go)里的类型错误在当前平台不会被其它任何步骤发现"
fi
echo "OK(build + vet 均通过)"

# ---- 孤儿包检查(迭代 13)-------------------------------------------------
# 上面那道防线只覆盖"**当前平台存在的包**里的平台专属文件"。
#
# 还有一个整类盲区(迭代 11 发现、迭代 13 修):
#   - `go list ./...` 只列出**当前平台**有可编译文件的包 —— 一个只含
#     `//go:build windows` 文件的**全新包**根本不在列表里(实测:0 命中);
#   - `go build -o <dir>/` 只编译**从 main 可达**的包。
# 于是一个全新 windows-only 包对整条 9 步门禁**完全隐形** ——
# 连类型错误都不报(实测:造一个这样的包,9 步门禁全绿)。
#
# 修法:包集合不再只由 `go list` 决定,而是按 **git 跟踪的 .go 文件**推导
# (与第 1 步的 gofmt 清单同源:已跟踪 + 新增未忽略)。对其中**当前平台
# 看不见**的目录(孤儿包)逐个跑交叉 vet:
#
#   - 通过 → 该平台能编译这个包(盲区被覆盖);
#   - "build constraints exclude all Go files" → 该平台**没有**这个包的
#     文件(正常,不是错误),换下一个平台;
#   - 其它任何错误 → 类型/语法错误 → 门禁失败。
#
# 目标平台枚举:**全集**,单一事实来源 = `go tool dist list`
# (go 工具自带的"我支持哪些目标平台"清单,输出 "<goos>/<goarch>" 组合)。
#
# 此前这里写死的是 "windows linux darwin" —— 只覆盖 3 个平台。于是只含
# `//go:build freebsd`(或 openbsd / netbsd / plan9 / js-wasm / ios /
# android / aix / solaris / dragonfly / illumos …)的孤儿包对门禁
# **完全隐形**:类型错误也不报(实测:9 步全绿 = 绕过)。
#
# 手写清单必然漏:这次把漏的补进去,下一次 go 支持新平台时还是漏。
# 唯一可靠的做法是问 go 工具要全集 —— 实测 47 个目标组合。
#
# 为什么是"逐包"而不是"整包集合一次 vet":`GOOS=windows go vet ./...`
# 遇到"该平台没有文件的包"会直接报错退出,没法区分"这个包在 Windows 下
# 不存在"(正常)与"这个包有类型错误"(要报)。逐包跑才能把两者分开。
ALL_TARGETS=$(go tool dist list | sort -u)
ALL_TARGET_COUNT=$(printf '%s\n' "$ALL_TARGETS" | grep -c . || true)
[ "$ALL_TARGET_COUNT" -gt 0 ] || fail "go tool dist list 没有输出(拿不到目标平台全集,孤儿包检查无从下手)"
echo "目标平台全集: $ALL_TARGET_COUNT 个(go tool dist list,含 GOOS/GOARCH 组合)"

# orphan_check <模块目录> <go list 参数> <候选包目录...>
#
# 对每个候选包目录判断:它是不是被某个平台编译到了。
# 返回 0 = 全部候选包都被覆盖(或所有平台都没有它的文件,正常跳过);
# 返回 1 = 有候选包在它**声称支持**的平台上编译不过(真实错误)。
#
# ---- "所有目标平台都没有它的文件"= 什么?边界登记 ----------------------
# 候选包在 `go tool dist list` 的**全部**目标上都"没有可编译文件"时,
# 门禁**打印一行"跳过"**(不是静默忽略)。这有两种成因,**都不是绕过**:
#
#   ① **自定义 build tag**(例:`//go:build awo_custom_tag`):
#      Go 工具链的约定 —— 约束不满足的文件**本来就不参与构建**
#      (`go build` 对它直接报 "build constraints exclude all Go files")。
#      门禁枚举的是**目标平台**,不是任意自定义 tag → 这类包的**类型错误
#      不会被本门禁抓到**。但:① 它的**语法错误仍会被第 1 步抓到**
#      (gofmt 不看 build tag);② 它在门禁日志里是**可见的**("跳过: …")。
#      若将来要收紧,可行的做法是"对被跳过的候选,用该文件自己的
#      `//go:build` 表达式再 vet 一次" —— 需要解析布尔表达式,容易误红,
#      属于独立的工程决策,不在本轮范围内。
#   ② **嵌套 go.mod**(子模块):子模块**不属于**父模块
#      (`go list ./...` 不含它 —— Go 工具链的约定)。
#      实测(隔离副本):仓库里出现嵌套 go.mod 时,门禁会**红**在孤儿包检查
#      (把"父模块不包含该包"当成解析失败)—— fail closed,不是绕过,
#      但错误信息有误导性。**本仓库不得引入嵌套 go.mod**。
#
# 性能(目标平台从 3 个涨到 47 个,实测耗时见报告):
#   - **常态零开销**:当前平台全部可见(没有孤儿包)→ 直接 return,
#     连一次额外探测都不做;
#   - 探测**按目标平台批量**:一次 `go list` 覆盖**所有**候选包,
#     不是每个候选包跑一次(47 目标 × N 候选 = 47N 次进程调用);
#   - vet **按文件集去重**:同一组文件在几个 GOARCH 下都一样 → 只 vet
#     一次(实测:一个 `//go:build freebsd` 的包在 freebsd/{386,amd64,arm,
#     arm64} 下文件集相同 → 1 次 vet 而不是 4 次)。
orphan_check() {
	oc_mod="$1"
	shift
	oc_listargs="$1"
	shift
	oc_rc=0
	oc_covered=$(cd "$oc_mod" && go list -f '{{.Dir}}' $oc_listargs 2>/dev/null | sort -u)
	# 只有**当前平台看不见**的候选才需要交叉检查(其余包前面的步骤已经编译过)
	oc_dirs=""
	oc_cands=""
	for oc_rel in "$@"; do
		oc_abs=$(cd "$oc_mod/$oc_rel" 2>/dev/null && pwd) || continue
		# 当前平台已经看得见的包 → 前面的步骤已经编译过它,跳过。
		if printf '%s\n' "$oc_covered" | grep -qxF "$oc_abs"; then
			continue
		fi
		oc_cands="$oc_cands $oc_rel"
		if [ "$oc_rel" = "." ]; then
			oc_dirs="$oc_dirs ."
		else
			oc_dirs="$oc_dirs ./$oc_rel"
		fi
	done
	# 没有孤儿包 → 没有任何交叉检查要跑(常态路径:零开销)
	[ -n "$oc_dirs" ] || return 0

	oc_modpath=$(cd "$oc_mod" && go list -m 2>/dev/null || true)
	oc_vetted="" # 已 vet 过的 "<包>|<文件集>"(去重键)
	oc_ok=""     # 已确认可编译的包
	oc_err=""    # 检出真实错误的包(错误详情已单独回显)
	for oc_target in $ALL_TARGETS; do
		oc_goos=${oc_target%/*}
		oc_goarch=${oc_target#*/}
		# 一次 go list 覆盖**所有**候选包,输出(制表符分隔):
		#   导入路径 | 源文件数 | 内部测试文件数 | 外部测试文件数 |
		#   源文件集 | 内部测试文件集 | 外部测试文件集 | 错误
		oc_probe=$(cd "$oc_mod" && GOOS="$oc_goos" GOARCH="$oc_goarch" CGO_ENABLED=0 \
			go list -e -f '{{.ImportPath}}{{"\t"}}{{len .GoFiles}}{{"\t"}}{{len .TestGoFiles}}{{"\t"}}{{len .XTestGoFiles}}{{"\t"}}{{.GoFiles}}{{"\t"}}{{.TestGoFiles}}{{"\t"}}{{.XTestGoFiles}}{{"\t"}}{{.Error}}' \
			$oc_dirs 2>/dev/null) || oc_probe=""
		# 归一化:只留下"这个平台下有文件"的包;以及"不是约束排除"的真实错误
		# (例如一个目录里有两个不同的 package 名 —— 那是实打实的错误,
		#  不能因为"没有可编译文件"就被当成约束排除而放过)。
		oc_norm=$(printf '%s\n' "$oc_probe" | awk -F'\t' -v mp="$oc_modpath" '
			{
				if (NF < 8) next
				rel = $1
				if (rel == "") next
				if (mp != "" && substr(rel, 1, length(mp)) == mp) rel = substr(rel, length(mp) + 1)
				sub(/^\//, "", rel)
				if (rel == "") rel = "."
				n = $2 + $3 + $4
				err = $8
				if (n == 0) {
					if (err != "<nil>" && err !~ /build constraints exclude all Go files/) {
						printf "ERR\t%s\t%s\n", rel, err
					}
					next
				}
				files = $5 " " $6 " " $7
				gsub(/[ \t]+/, " ", files)
				printf "FILES\t%s\t%s\n", rel, files
			}')
		[ -n "$oc_norm" ] || continue
		while IFS=$'\t' read -r oc_kind oc_rel oc_rest; do
			case "$oc_kind" in
			ERR)
				oc_rc=1
				oc_err="$oc_err $oc_rel"
				echo "---- GOOS=$oc_goos GOARCH=$oc_goarch 包 ./$oc_rel 无法解析(孤儿包检查)----" >&2
				printf '%s\n' "$oc_rest" >&2
				;;
			FILES)
				oc_key="$oc_rel|$oc_rest"
				case "$oc_vetted" in
				*"|$oc_key|"*) continue ;;
				esac
				oc_vetted="$oc_vetted|$oc_key|"
				if [ "$oc_rel" = "." ]; then
					oc_arg="."
				else
					oc_arg="./$oc_rel"
				fi
				oc_vet_rc=0
				oc_out=$(cd "$oc_mod" && GOOS="$oc_goos" GOARCH="$oc_goarch" CGO_ENABLED=0 go vet "$oc_arg" 2>&1) || oc_vet_rc=$?
				if [ "$oc_vet_rc" -eq 0 ]; then
					oc_ok="$oc_ok $oc_rel"
				elif printf '%s' "$oc_out" | grep -q "build constraints exclude all Go files"; then
					# 该平台没有这个包的文件(正常)—— 探测阶段已按文件数过滤,
					# 这里只是兜底(例如 CGO_ENABLED=0 把 cgo 文件全排除了)。
					:
				else
					oc_rc=1
					oc_err="$oc_err $oc_rel"
					echo "---- GOOS=$oc_goos GOARCH=$oc_goarch go vet $oc_arg 输出(孤儿包检查)----" >&2
					printf '%s\n' "$oc_out" >&2
				fi
				;;
			esac
		done <<<"$oc_norm"
	done
	# 汇总:覆盖到 / 检出错误(详情已回显)/ 完全没被任何目标平台编译到
	for oc_rel in $oc_cands; do
		case " $oc_ok " in
		*" $oc_rel "*)
			echo "   覆盖: ./$oc_rel"
			;;
		*)
			case " $oc_err " in
			*" $oc_rel "*) : ;; # 上面已经回显过错误详情,不再重复
			*) echo "   跳过: ./$oc_rel(全部已知目标平台下都没有可编译文件)" ;;
			esac
			;;
		esac
	done
	return "$oc_rc"
}

# 候选包目录 = git 推导出的、含 .go 文件的目录(相对模块根,不带 "./" 前缀)。
#
# 排除 go 工具本身就忽略的目录(与 `go list ./...` 的口径一致):
#   - 任意一级以 "_" 或 "." 开头的目录(go 工具整体忽略);
#   - 任意一级叫 "testdata" 的目录(go 工具约定:测试夹具,允许故意写坏)。
# 否则这些目录会被当成"孤儿包"而误报。
#
# **口径说明**:这里**不再**单独排除 build/ —— 排除规则已经统一到
# PATH_EXCLUDE_RE(第 1 步的 gofmt 文件清单用的是**同一条**规则)→ build/
# 下的**项目代码**会同时出现在"gofmt 清单"与"候选目录"里,被跨平台交叉
# vet 覆盖;Wails 平台脚手架 build/<平台模板根>/ 已在 PATH_EXCLUDE_RE 里
# 剔掉,不会在这里复活。
#
# 注意:这里**不能**在 $( ) 里写 case —— macOS 自带的 bash 3.2 解析不了
# (实测:bash: syntax error near unexpected token `;;')。用 awk + grep 代替。
#
# 抽成函数是为了让**门禁自检**与真正的门禁走**同一条代码路径** ——
# 自检若自己另写一套目录推导,将来有人把排除规则改回"整棵 build/",
# 自检照样绿,盲区就又回来了。
orphan_candidates() {
	printf '%s\n' "$1" |
		awk -F/ 'NF==1 {print "."; next} {d=$1; for (i=2;i<NF;i++) d=d"/"$i; print d}' |
		sort -u |
		grep -vE '(^|/)[_.]' |
		grep -vE '(^|/)testdata(/|$)' || true
}
ORPHAN_CANDIDATES=$(orphan_candidates "$GOFMT_FILES")
ORPHAN_COUNT=$(printf '%s\n' "$ORPHAN_CANDIDATES" | grep -c . || true)
echo "孤儿包候选目录: $ORPHAN_COUNT 个(按 git 跟踪的 .go 文件推导)"

# ---- 门禁自检的公共设施(断言助手 + 场景计数)---------------------------
# 为什么要有这一层:**门禁自检本身也是代码**,同样会被改坏、被删掉、被改成
# 恒真。本项目历史上最贵的一次教训就是"门禁看上去在跑,其实某道防线
# 早就没了"。因此自检必须自己站得住:
#
#   ① **双向断言**:同一道防线既要断言"坏情况必须红",也要断言"好情况
#      必须绿"。只有红方向是**恒真**的(检查无论对错都失败也算"成功");
#      只有绿方向又挡不住"检查被改成永远红"。两个方向合起来,防线被
#      neuter(改成恒真或恒假)时**必然**有一侧变红。
#   ② **场景计数**:每条自检场景都计数,结束时与期望值比对 ——
#      删掉一条自检调用(手滑/重构)→ 计数对不上 → 门禁红。
#   ③ **助手防空转**:断言助手自己也被断言(见下方 meta 守卫)——
#      助手被改成恒真 → meta 守卫立刻红。
#
# 计数变量的赋值发生在**函数体内**(不是子 shell),因此计数能真正累加。
selfcheck_count=0
# 期望的自检场景条数。**新增或删除任何一条自检场景,都必须同步改这里** ——
# 改漏了 → 门禁红(这正是这个守卫的作用)。当前 32 条:
#   1-3  交叉检查:windows 测试文件对照 / 测试文件类型错 / 源文件类型错
#   4-9  孤儿包检查:windows·freebsd·js 各一条绿 + 一条红
#   10   孤儿包检查:目录混包(两个 package 名)必须被抓
#   11   目标平台枚举:必须是 go 工具全集(不是手写清单)
#   12   目标平台枚举断言的反向对照:三平台手写清单必须通不过
#   13-17 第 6 步"真实用例"判定:正常包不误报 / 0 用例包被抓 / 全 skip 包被抓
#          / 伪造输出包被抓 / 反向对照(对正常包断言"必须在报告里"必须失败)
#   18-23 第 6 步"断言扫描"判定(#289 + 第二轮终验 P2-1):有断言的包不误报 /
#          零断言的包被抓 / 只在注释里提到断言的包被抓 / 恒不触发的断言被抓 /
#          运行期才成立的正常断言不误报 / 反向对照(有断言的包断言"必须在
#          报告里"必须失败)
#   24-27 路径排除规则(第十八轮终验 #279):build/ 下的项目代码包必须在门禁
#          包集合里 / 类型正确时必须能过编译 / 类型错误时必须被抓 /
#          Wails 平台脚手架(build/ios)必须继续被排除
#   28-29 孤儿包候选:build/ 下的包必须在候选里 / testdata/ 下的包必须不在
#          候选里(Go 工具链约定 —— 夹具目录不可见是正确行为)
#   30-32 迭代 27(终审 P1-2)的三类"死断言"绕过:零次循环 / 无匹配分支 /
#          未被调用的辅助函数(最后一条同时断言绿方向)
SELFCHECK_EXPECTED=32

# selfcheck_ok <标签> <命令...> —— 断言命令必须**成功**(绿方向)。
selfcheck_ok() {
	sc_label="$1"
	shift
	selfcheck_count=$((selfcheck_count + 1))
	if ! "$@"; then
		fail "门禁自检失败:[$sc_label] 本该通过的命令失败了 —— 自检或它守护的防线已失效"
	fi
}

# selfcheck_red <标签> <命令...> —— 断言命令必须**失败**(红方向)。
selfcheck_red() {
	sc_label="$1"
	shift
	selfcheck_count=$((selfcheck_count + 1))
	if "$@"; then
		fail "门禁自检失败:[$sc_label] 本该被抓住的坏情况却通过了 —— 这道防线没有生效"
	fi
}

# meta 守卫:断言助手自己必须真的能判对错。
#
# 上面两个助手若被改成恒真(例如 selfcheck_red 永远判定"失败"),所有自检
# 都会失去意义 —— 而这正是"自检恒真"这类失效最难被发现的原因:它不会
# 让门禁变红,只会让门禁**永远绿**。
#
# 用**子 shell** 跑:助手判定失败时会 fail → exit → 只退出子 shell,
# 主流程不受影响;子 shell 里的计数也随之丢弃(不会污染场景计数)。
if ( selfcheck_red "meta-guard-red" true ) >/dev/null 2>&1; then
	fail "门禁自检失败:断言助手失效 —— 对必然成功的命令断言'必须失败'却通过了(自检恒真)"
fi
if ( selfcheck_ok "meta-guard-ok" false ) >/dev/null 2>&1; then
	fail "门禁自检失败:断言助手失效 —— 对必然失败的命令断言'必须通过'却通过了(自检恒真)"
fi

# ---- 门禁自检:本步骤的防线必须真的能抓到平台专属文件的类型错误 ------
# 目的:证明上面那道防线**有效**,而不是"看上去在跑"。
# 做法:在 /tmp 里造一个**隔离的最小模块**(不碰工作区),用**同一个**
# win_check 函数依次验证三件事:
#   1. 对照组 —— 一个**类型正确**的 //go:build windows 测试文件必须**通过**。
#      没有这一条,下面两条就是恒真断言(vet 无论对错都失败也算"成功")。
#   2. 一个含类型错误的 //go:build windows **测试**文件必须**失败**。
#      这正是 go build 看不见、只有 vet 看得见的那一类(本轮修的盲区)。
#   3. 一个含类型错误的 //go:build windows **源**文件必须**失败**。
#      第八轮修好的源文件盲区,这里做回归确认。
#
# 隔离模块放在 /tmp 而不是仓库里:仓库里造坏文件会污染 go list 的包集合
# 与第 1 步的 gofmt 清单,也会让"工作区是否干净"的自证失去意义。
# go.mod 的 go 指令取自仓库自己的 go.mod —— 与本仓库的模块要求保持一致,
# 不用硬编码版本号(将来 go.mod 升版本时这里不会失配)。
WIN_CHECK_DIR=$(mktemp -d /tmp/awo-verify-wincheck.XXXXXX)
mkdir -p "$WIN_CHECK_DIR/pkg" "$WIN_CHECK_DIR/cmd/selftest"
SELF_GO_VER=$(awk '/^go /{print $2; exit}' go.mod)
[ -n "$SELF_GO_VER" ] || fail "读不到 go.mod 的 go 指令(门禁自检需要它)"
printf 'module awo/selfcheck\n\ngo %s\n' "$SELF_GO_VER" >"$WIN_CHECK_DIR/go.mod"
printf 'package pkg\n\nfunc Hello() string { return "hi" }\n' >"$WIN_CHECK_DIR/pkg/pkg.go"
printf 'package main\n\nfunc main() {}\n' >"$WIN_CHECK_DIR/cmd/selftest/main.go"

# 1. 对照组:类型正确的 windows 测试文件 → 必须通过
printf '//go:build windows\n\npackage pkg\n\nvar good string = "ok"\n' >"$WIN_CHECK_DIR/pkg/zz_windows_test.go"
selfcheck_ok "交叉检查对照:类型正确的 //go:build windows **测试文件**必须通过(否则下面两条红断言是恒真的)" win_check "$WIN_CHECK_DIR" ./...

# 2. 含类型错误的 windows **测试**文件 → 必须失败(go build 看不见这一类)
printf '//go:build windows\n\npackage pkg\n\nvar bad string = 42\n' >"$WIN_CHECK_DIR/pkg/zz_windows_test.go"
selfcheck_red "交叉检查://go:build windows 的**测试文件**里的类型错误必须被抓(go build 不编译 _test.go,只有 vet 能抓)" win_check "$WIN_CHECK_DIR" ./...

# 3. 含类型错误的 windows **源**文件 → 必须失败(第八轮的盲区,回归确认)
rm -f "$WIN_CHECK_DIR/pkg/zz_windows_test.go"
printf '//go:build windows\n\npackage pkg\n\nvar Bad string = 42\n' >"$WIN_CHECK_DIR/pkg/zz_windows.go"
selfcheck_red "交叉检查://go:build windows 的**源文件**里的类型错误必须被抓" win_check "$WIN_CHECK_DIR" ./...
rm -f "$WIN_CHECK_DIR/pkg/zz_windows.go"

# ---- 门禁自检:孤儿包检查本身必须真的能抓到类型错误 ----------------------
# 目的:证明 orphan_check **有效**,而不是"看上去在跑"。
# 做法:在同一个隔离模块里造一个只含 //go:build <平台> 文件的包 ——
# 它对 `go list ./...` **完全隐形**(这正是要修的盲区:上面三条自检用的
# win_check 走 ./...,看不见它),只有 orphan_check 能看见它。
#
# 每个平台依次验证两件事:
#   1. 对照组 —— 类型**正确**的 <平台>-only 包必须**通过**。
#      没有这一条,下面那条就是恒真断言(vet 无论对错都失败也算"成功")。
#   2. 含类型错误的 <平台>-only 包必须**失败**。
#
# 覆盖的平台:windows(迭代 11/13 的老自检,必须继续有效)+
# **freebsd / js**(迭代 14B 新增:这正是"手写 windows linux darwin"
# 枚举漏掉的那一类 —— 终审实测:只含 //go:build freebsd 的孤儿包 +
# 类型错误 → 9 步门禁全绿 = 绕过)。
#
# 自检**必须走真实的枚举路径**:orphan_check 内部用的是
# `go tool dist list` 的全集,这里只声明"这个平台应该被覆盖到",
# 具体由 orphan_check 自己去找 —— 自检若自己另写一套平台列表,
# 就复现了原来那个 bug(枚举与检查不同源)。
orphan_selfcheck() {
	# orphan_selfcheck <平台标签> <build 标签> <目录名> <包名>
	osc_tag="$1"
	osc_build="$2"
	osc_dir="$3"
	osc_pkg="$4"
	mkdir -p "$WIN_CHECK_DIR/$osc_dir"
	printf '//go:build %s\n\npackage %s\n\nvar Good string = "ok"\n' "$osc_build" "$osc_pkg" >"$WIN_CHECK_DIR/$osc_dir/only_$osc_dir.go"
	selfcheck_ok "孤儿包检查对照:类型正确的 //go:build $osc_build 孤儿包必须通过(否则下面的红断言是恒真的)" orphan_check "$WIN_CHECK_DIR" "./..." "$osc_dir"
	printf '//go:build %s\n\npackage %s\n\nvar Bad string = 42\n' "$osc_build" "$osc_pkg" >"$WIN_CHECK_DIR/$osc_dir/only_$osc_dir.go"
	selfcheck_red "孤儿包检查:只含 //go:build $osc_build 文件的孤儿包里的类型错误必须被抓" orphan_check "$WIN_CHECK_DIR" "./..." "$osc_dir"
	rm -rf "$WIN_CHECK_DIR/$osc_dir"
	echo "   自检通过: //go:build $osc_build 的孤儿包(类型正确 → 通过 / 类型错误 → 被抓)"
}

orphan_selfcheck "windows" "windows" "winonly" "winonly"
orphan_selfcheck "freebsd" "freebsd" "fbonly" "fbonly"
orphan_selfcheck "js" "js" "jsonly" "jsonly"

# ---- 门禁自检:目录"混包"必须被抓 ---------------------------------------
# 一个目录里出现**两个不同的 package 名**(例如重构时把两个文件并进同一
# 目录却忘了改包名)—— 这是实打实的编译错误,不是"平台问题"。
#
# 为什么它危险:它长得**像**"这个平台没有可编译文件"。go list 会把
# .Error 填成 "found packages X (...) and Y (...) in ...";如果检查把这类
# 错误当成"build constraints exclude all Go files"而跳过,这个包就永远
# 没有任何人编译、永远不会被发现 —— 与 #251 的绕过同族。
#
# 绿方向由上面的 freebsd 孤儿包对照场景提供(**同一条代码路径**:orphan_check
# 对一个 freebsd-only 目录),这里只补红方向(成本:一次枚举 ≈1s)。
orphan_mixed_selfcheck() {
	om_dir="mixedpkg"
	mkdir -p "$WIN_CHECK_DIR/$om_dir"
	printf '//go:build freebsd\n\npackage mixedone\n\nvar Good string = "ok"\n' >"$WIN_CHECK_DIR/$om_dir/a.go"
	printf '//go:build freebsd\n\npackage mixedtwo\n\nvar Also string = "ok"\n' >"$WIN_CHECK_DIR/$om_dir/b.go"
	selfcheck_red "孤儿包检查:同一目录里两个 package 名(混包)必须被抓,不能被当成'该平台没有文件'跳过" orphan_check "$WIN_CHECK_DIR" "./..." "$om_dir"
	rm -rf "$WIN_CHECK_DIR/$om_dir"
}
orphan_mixed_selfcheck

# ---- 门禁自检:目标平台枚举必须是 go 工具的全集 -------------------------
# 防的是"有人把手写清单换回去"。三条孤儿包场景(freebsd / js)能挡住
# "枚举只剩 windows",但**挡不住**"枚举被换成一份含 freebsd / js、
# 却漏掉其余冷门平台的手写清单"—— 那种情况下三条自检照样绿,而
# //go:build plan9 / openbsd / solaris 的孤儿包仍然隐形。
# 因此把**集合规模**钉死:目标数与不同 GOOS 数都要有下限。
enum_assert_ok() {
	ea_list="$1"
	ea_targets=$(printf '%s\n' "$ea_list" | grep -c . || true)
	ea_goos=$(printf '%s\n' "$ea_list" | cut -d/ -f1 | sort -u | grep -c . || true)
	[ "$ea_targets" -ge 40 ] || return 1
	[ "$ea_goos" -ge 13 ] || return 1
	for ea_t in windows/amd64 freebsd/amd64 js/wasm; do
		printf '%s\n' "$ea_list" | grep -qxF "$ea_t" || return 1
	done
	return 0
}
selfcheck_ok "目标平台枚举必须是 go 工具的全集(go tool dist list),不是手写清单(>=40 个目标 / >=13 个 GOOS,且含 windows·freebsd·js)" enum_assert_ok "$ALL_TARGETS"
# 反向对照:一份"只有三个常见平台"的手写清单必须**通不过**这条断言 ——
# 它通不过,才说明断言真的在数集合规模(而不是恒真)。
selfcheck_red "枚举断言必须能判负:三平台手写清单必须通不过" enum_assert_ok "windows/amd64
linux/amd64
darwin/amd64"

echo "OK(自检:类型正确的孤儿包通过 / 含类型错误的孤儿包被抓 / 混包被抓 / 平台枚举是全集)"

# ---- 门禁自检:build/ 下的项目代码不得对门禁隐形(第十八轮终验 #279)------
# 绕过场景:在**根级** build/ 下放一个包(类型错误 + 零用例 + 伪造
# "=== RUN" / "--- PASS" 输出)→ 旧门禁的包列表与 gofmt 清单**同时**把它
# 剔掉 → 连带从孤儿包候选里消失 → vet / build / race 测试 / "每个包必须
# 有真实用例"**四道防线全部不查它** → 9 步门禁全绿。
#
# 本自检用**同一个**路径排除规则与**同一个**包集合推导函数
# (gate_pkg_rels / gate_pkgs / orphan_candidates),在隔离模块里验证五件事:
#   1. build/ 下的**类型正确**的包**必须**在门禁包集合里(绿方向:
#      没有这一条,下面第 3 条就是恒真断言);
#   2. build/ 下的**类型正确**的包必须能通过门禁**自己**的编译检查
#      (绿方向:证明新规则没有把正常代码也挡在门外);
#   3. build/ 下的**类型错误**的包 → 用门禁自己的包集合跑 vet **必须失败**
#      (红方向:证明漏洞被堵住了);
#   4. Wails 平台脚手架(build/ios)**必须不在**门禁包集合里(红方向:
#      证明新规则没有把脚手架放进来 —— 放进来门禁会因 #120 必红);
#   5. 孤儿包候选:build/ 下的包**必须**在候选里(绿方向:平台专属的
#      build/ 包靠这条交叉 vet 覆盖),而 testdata/ 下的包**必须不在**
#      候选里(红方向:Go 工具链约定 —— 夹具目录允许故意写坏,不可见是
#      **正确行为**,不是绕过)。
#
# 全部在隔离模块里造(/tmp),不碰工作区。

# pkg_in_gate_set <模块目录> <相对包路径> → 该包**在**门禁包集合里则返回 0
pkg_in_gate_set() {
	pi_dir="$1"
	pi_rel="$2"
	gate_pkg_rels "$pi_dir" | grep -qxF "$pi_rel"
}

# selftest_vet_gate_pkgs <模块目录> → 用**门禁自己的**包集合跑 go vet
# (返回 vet 的退出码;失败=非 0)
selftest_vet_gate_pkgs() {
	sv_dir="$1"
	sv_pkgs=$(gate_pkgs "$sv_dir") || return 1
	( cd "$sv_dir" && go vet $sv_pkgs ) >/dev/null 2>&1
}

# orphan_candidate_in_set <文件清单> <候选目录> → 该目录**在**候选里则返回 0
orphan_candidate_in_set() {
	orphan_candidates "$1" | grep -qxF "$2"
}

# 1) 类型正确的 build/ 包 → 必须在门禁包集合里(绿方向)
mkdir -p "$WIN_CHECK_DIR/build/evilpkg"
printf 'package evilpkg\n\nvar Good string = "ok"\n' >"$WIN_CHECK_DIR/build/evilpkg/evilpkg.go"
selfcheck_ok "路径排除规则:build/ 下的项目代码包必须进入门禁的包集合(否则它对四道防线全都隐形 —— 第十八轮终验 #279 的绕过本身)" \
	pkg_in_gate_set "$WIN_CHECK_DIR" "build/evilpkg"

# 2) 类型正确的 build/ 包 → 必须能通过门禁自己的编译检查(绿方向)
selfcheck_ok "路径排除规则:build/ 下类型正确的包必须能通过门禁自己的编译检查(否则新规则把正常代码也挡在门外)" \
	selftest_vet_gate_pkgs "$WIN_CHECK_DIR"

# 3) 类型错误的 build/ 包 → 门禁自己的 vet 必须失败(红方向)
printf 'package evilpkg\n\nvar Bad string = 42\n' >"$WIN_CHECK_DIR/build/evilpkg/evilpkg.go"
selfcheck_red "路径排除规则:build/ 下类型错误的包必须被门禁抓到(用门禁自己的包集合跑 vet 必须失败)" \
	selftest_vet_gate_pkgs "$WIN_CHECK_DIR"
rm -rf "$WIN_CHECK_DIR/build"

# 4) Wails 平台脚手架 → 必须继续被排除(红方向;放进来门禁会因 #120 必红)
mkdir -p "$WIN_CHECK_DIR/build/ios"
printf 'package main\n\nfunc main() {}\n' >"$WIN_CHECK_DIR/build/ios/main_ios.go"
selfcheck_red "路径排除规则:Wails 平台脚手架(build/ios)必须继续被排除(否则门禁会因脚手架在宿主平台编译失败而必红 —— #120)" \
	pkg_in_gate_set "$WIN_CHECK_DIR" "build/ios"
rm -rf "$WIN_CHECK_DIR/build"

# 5) 孤儿包候选:build/ 下的包必须在候选里;testdata/ 下的包必须不在
selfcheck_ok "孤儿包候选:build/ 下的包必须在候选目录里(平台专属的 build/ 包靠这条交叉 vet 覆盖)" \
	orphan_candidate_in_set "build/evilpkg/only.go" "build/evilpkg"
selfcheck_red "孤儿包候选:testdata/ 下的包必须不在候选目录里(Go 工具链约定:夹具目录允许故意写坏,不可见是正确行为不是绕过)" \
	orphan_candidate_in_set "pkg/testdata/fixture.go" "pkg/testdata"

echo "   自检通过:build/ 下的项目代码进入门禁包集合 / 类型错误被抓 / Wails 脚手架仍被排除 / testdata 仍不可见"

# ---- 真正的孤儿包检查(工作区) -------------------------------------------
if ! orphan_check "$ROOT" "$PKGS" $ORPHAN_CANDIDATES; then
	fail "孤儿包检查失败:当前平台看不见的包里有编译不过的(类型/语法错误)—— 这类包对门禁的其它所有步骤都是隐形的"
fi

rm -rf "$WIN_CHECK_DIR"
WIN_CHECK_DIR=""
echo "OK(自检:对照组通过 / 测试文件类型错误被抓 / 源文件类型错误被抓)"
end_step

step "5/9" "go test -count=1 -race -v"
TEST_LOG=$(mktemp /tmp/awo-verify-test.XXXXXX)
# 退出码直接取自 go test(不经管道):管道会把"grep 没匹配到行"的退出码
# 混进来,让一次成功的测试被误判为失败。完整日志落盘(第 6 步要用),
# 控制台只回显结果行 —— Wails/objc 的链接产物每次刷十几行,与被测代码
# 无关。
#
# **为什么加 -v**:第 6 步要区分"真跑过的用例"与"被 t.Skip 跳过的用例",
# 而这两种情况在**不带 -v** 的输出里长得一模一样(都是 `ok <pkg> 0.00s`)——
# 终审实测:一个只含 t.Skip() 的新包 → 门禁判"有真实用例" → 全绿 = 绕过。
# 只有 -v 会打印 `--- PASS:` / `--- SKIP:` 两种行,才分得开。
#
# 性能实测(全量 21 个包):带 -v 18.3s vs 不带 -v 19.1s —— **没有可测的
# 额外开销**(噪声范围内),只是日志从 43 行涨到 ~2300 行。日志落盘、
# 成功时只回显结果行,控制台不会被淹没;失败时回显全文。
if ! go test -count=1 -race -v $PKGS >"$TEST_LOG" 2>&1; then
	cat "$TEST_LOG"
	fail "go test 失败"
fi
grep -E '^(ok|FAIL|\?)[[:space:]]' "$TEST_LOG" | grep -v "was built for newer 'macOS' version" || true
echo "OK"
end_step

step "6/9" "每个包都必须有真实执行的用例"
# 逐包核对两件事:
#   ① 至少有一条**真正执行并通过**的顶层用例(框架事件判据);
#   ② 测试代码里**至少有一处断言**(静态扫描判据,#289)——
#      "跑过了"不等于"验证过什么"。
#
# ---- 判据:框架事件,不是文本输出 ----------------------------------------
# `go test -json` 每行是一个扁平 JSON 对象,框架给每个用例发一条事件:
#   {"Action":"run","Package":"p","Test":"TestX"}        用例开始
#   {"Action":"pass","Package":"p","Test":"TestX"}       顶层用例**通过**
#   {"Action":"skip","Package":"p","Test":"TestX"}       顶层用例被跳过
#   {"Action":"pass","Package":"p","Elapsed":0.007}      **包**整体通过(Test 为空)
#   {"Action":"fail","Package":"p","Elapsed":0.001}      **包**整体失败
#   {"Action":"skip","Package":"p","Elapsed":0}           **包**没有测试文件
#   {"Action":"output","Package":"p","Test":"TestX","Output":"...\n"} 只是输出
#
# 达标 ⇔ 存在至少一个 Action=="pass" 且 Test **非空**、且 Test **不含 "/"**
#        (顶层用例名里没有 "/",只有子用例名才有)的事件。
#
# **只看顶层用例**:子用例的 pass 事件(T="TestX/sub")**不代表**这个包有真
# 用例 —— 一个"只跑了个子用例就自己 t.Skip"的包照样能拿到 pass 事件。
# 迭代 14B 的文本口径只认行首(不缩进)的 `--- PASS:` 行,也就是只认顶层
# 用例;这里保持同一口径,不顺手放宽。
#
# **为什么伪造骗不过**:用例自己 fmt.Println 出来的
# `=== RUN   Fake` / `--- PASS: Fake (0.00s)` 在 JSON 里只会落进
# Action=="output" 事件的 Output 字段 —— 框架**不会**为它生成 pass 事件。
# 判据只数框架事件、不看输出文本,伪造就失效了。
#
# 迭代 14B 用的是**解析文本**(`--- PASS:` 行),那个判据能被用例自己的
# Println 骗过(终审实测:伪造包 RUN=2/PASS=1/SKIP=1 → 放行;第 13 轮
# 加的"必须看到 === RUN"被同一手法击穿)。本步因此换成事件判定。

# ---- JSON 字段取值:唯一实现(下面几个 awk 共用同一份)--------------------
#
# 为什么可以这么"暴力"地找字段:go test -json 每行是**扁平** JSON 对象,
# 字段用 `,` 分隔,而**值里的引号一定被转义成 \"**。于是:
#   - 值内部不可能出现未转义的 `"` → 序列 `,"键":"` 只可能出现在真正的键位置;
#   - 用例自己打印的 `"Action":"pass"` 在 JSON 里是 `\"Action\":\"pass\"`,
#     中间夹着反斜杠 → 不含 `"Action":"` 这个子串 → 骗不过这里的解析。
# 取不到字段 → 该行被忽略 → 事件统计偏保守 → fail closed。
JSON_GET_FN='
function jget(line, key,   pat, p, i, c, out) {
	pat = "\"" key "\":\""
	p = index(line, pat)
	if (p == 0) return ""
	p += length(pat)
	out = ""
	for (i = p; i <= length(line); i++) {
		c = substr(line, i, 1)
		if (c == "\\") { out = out c substr(line, i + 1, 1); i++; continue }
		if (c == "\"") break
		out = out c
	}
	return out
}
'

# json_good_pkgs <json 日志> → 每行一个"有顶层 pass 事件"的包
json_good_pkgs() {
	awk "$JSON_GET_FN"'
	{
		if (jget($0, "Action") != "pass") next
		tst = jget($0, "Test")
		if (tst == "") next
		if (tst ~ /\//) next
		pkg = jget($0, "Package")
		if (pkg != "" && !seen[pkg]++) print pkg
	}
	' "$1"
}

# usage_candidates <文本日志> → 每行一个候选用例名(只作 -run 模式用)
#
# **只作提示**:取错不会让判定出错(兜底运行会补齐),取多只是多跑几个用例。
# 名字只保留 [A-Za-z0-9_](Go 的顶层用例名就是标识符)—— 用例自己伪造的
# RUN 行可能带奇怪字符,一律丢弃,免得拼出非法正则让整次运行失败。
usage_candidates() {
	awk '
	/^=== RUN   / { if (!got) { cand = $3; got = 1 }; next }
	/^(ok|FAIL|\?)[[:space:]]/ {
		p = $2
		if (p in seen) next
		seen[p] = 1
		if (got) print cand
		got = 0; cand = ""
		next
	}
	' "$1" | grep -E '^[A-Za-z0-9_]+$' | sort -u || true
}

# real_usage_verdict <工作目录> <包列表> <文本日志>
#   → 打印"缺少真实执行用例"的清单(每行 "  <pkg>: <原因>");空输出 = 全部达标。
#
# 门禁与**自检**走同一个函数 —— 自检若自己另写一套判定,将来有人把这里
# 改坏(例如把判据改回解析文本),自检照样绿,盲区就又回来了(与 win_check
# 抽函数的理由同源)。
#
# 两段式执行(性能):
#   ① **窄化运行**:用文本日志里每个包的第一条 "=== RUN" 名做 -run 模式,
#      每个包只跑 1 个用例。框架事件照样权威(出现顶层 pass 事件就证明
#      这个包真的有通过的用例),但执行时间从"整包"降到"一个用例"。
#   ② **兜底运行**:窄化运行没能证明的包(候选用例被跳过 / 名字对不上 /
#      包没跑起来),整包再跑一次 JSON,拿完整事件。
# 判据始终是"框架顶层 pass 事件";两段的结果合并后统一判定。
# 工具链缺失 / 输出为空 / 解析不出事件 → 一律判红(fail closed),不跳过。
real_usage_verdict() {
	rv_dir="$1"
	rv_pkgs="$2"
	rv_text="$3"
	rv_log1=$(mktemp /tmp/awo-verify-json1.XXXXXX)
	rv_log2=$(mktemp /tmp/awo-verify-json2.XXXXXX)
	rv_good=""

	# ① 窄化运行:每个包只跑它的第一个候选用例
	rv_cands=$(usage_candidates "$rv_text" | awk 'NR > 1 { printf "|" } { printf "%s", $0 }' || true)
	if [ -n "$rv_cands" ] && [ -n "$rv_pkgs" ]; then
		( cd "$rv_dir" && go test -count=1 -json -timeout 300s -run "^($rv_cands)\$" $rv_pkgs ) >"$rv_log1" 2>&1 || true
		rv_good=$(json_good_pkgs "$rv_log1" | tr '\n' ' ' || true)
	fi

	# ② 兜底运行:窄化没证明的包,整包再跑一次(拿完整事件)
	rv_rest=""
	for rv_p in $rv_pkgs; do
		case " $rv_good " in
		*" $rv_p "*) continue ;;
		esac
		rv_rest="$rv_rest $rv_p"
	done
	if [ -n "$rv_rest" ]; then
		( cd "$rv_dir" && go test -count=1 -json $rv_rest ) >"$rv_log2" 2>&1 || true
		rv_good="$rv_good $(json_good_pkgs "$rv_log2" | tr '\n' ' ' || true)"
	fi

	# ③ 报告:达标的包不出现;其余从**兜底运行的完整事件**里取原因
	#    (窄化运行的事件是残缺的,不能用来定原因 —— 例如候选用例被单独跑时
	#     失败,整包跑却是过的。)
	awk -v goodlist=" $rv_good " -v pkgs="$rv_pkgs" "$JSON_GET_FN"'
	BEGIN {
		n = split(pkgs, a, " ")
		for (i = 1; i <= n; i++) if (a[i] != "") want[a[i]] = 1
		reason["zero"]    = "0 个真实用例(测试文件里没有 Test 函数,或被 build 标签跳过)"
		reason["skipall"] = "全部用例都被 t.Skip 跳过(没有一条真正执行过 —— 看着有测试,实际什么都没验证)"
		reason["nofiles"] = "没有测试文件([no test files])"
		reason["fail"]    = "测试失败"
		reason["missing"] = "测试结果行缺失(包没进测试列表?)"
	}
	{
		act = jget($0, "Action"); if (act == "") next
		pkg = jget($0, "Package"); if (pkg == "") next
		tst = jget($0, "Test")
		if (act == "pass") {
			if (tst != "" && tst !~ /\//) goodpkg[pkg] = 1
			else if (tst == "") ppass[pkg] = 1
		} else if (act == "fail") {
			pfail[pkg] = 1
		} else if (act == "skip") {
			if (tst != "") tskip[pkg] = 1; else pskip[pkg] = 1
		}
	}
	END {
		for (p in want) {
			if (index(goodlist, " " p " ") > 0) continue
			if (p in goodpkg) continue
			if (p in pfail) s = "fail"
			else if (p in tskip) s = "skipall"
			else if (p in pskip) s = "nofiles"
			else if (p in ppass) s = "zero"
			else s = "missing"
			printf "  %s: %s\n", p, reason[s]
		}
	}
	' "$rv_log2" | sort
	rm -f "$rv_log1" "$rv_log2"
}
MISSING=$(real_usage_verdict "$ROOT" "$CHECK_PKGS" "$TEST_LOG") ||
	fail "第 6 步判定执行失败(拿不到 go test -json 事件 —— 必须红,不能静默跳过)"
[ -z "$MISSING" ] || fail "以下包缺少真实执行的用例:$MISSING"

# ---- 第 6 步的第二个判据:包里必须真的有"**可能触发**的断言" -------------
#
# 上面的判据证明的是"**有代码跑过**";它证明不了"**验证过什么**"。
# 一个**零断言**的用例(只调用函数、不断言)照样会拿到框架的顶层 pass
# 事件 → 判据判"有真实用例" → 整个门禁放行。
#
# 实测(隔离副本,轮次 83):造一个只含
#     func TestNothing(t *testing.T) { _ = 1 }
# 的新包 → 第 6 步判"有真实用例" → 9 步门禁全绿 = 绕过(#289)。
# 这个包实际上**什么都没验证** —— 它和"没有测试文件"的包一样,是穿着
# 测试外衣的空壳。
#
# **为什么不用 `go vet -tests`**:实测它**检测不出**"没有断言的测试" ——
# vet 的 tests 分析器查的是测试函数签名/示例命名,**不是断言数**;对一个
# 零断言的包 `go vet -tests ./...` exit 0(实测)。断言数**不在框架事件里**
# (`go test -json` 不报告),只能静态扫源码。
#
# 口径(**必须写清,否则会误报**):
#   - **按包**判定:包里只要有**一处**可能触发的断言调用就算达标 ——
#     不是"每个用例都要有断言"(那必然误报:表格驱动测试把断言放在
#     子函数/辅助函数里是常态)。
#   - 断言调用 = ① 惯例接收者(t / tb / tt / t2 …)上的
#     Error/Errorf/Fatal/Fatalf/Fail/FailNow 方法调用,或
#     ② 第三方断言包 assert./require. 的调用。
#   - 扫描范围 = 该包的 `*_test.go`(**只扫测试文件**):生产代码里
#     `err.Error()` / `fmt.Errorf()` 满地都是(本仓库实测 73 + 71 处),
#     扫生产代码等于没有判据。
#
# **"可能触发"这条(第二轮终验 P2-1)**:只判"**存在**断言"是不够的 ——
# 一个包里唯一的用例是
#     func TestX(t *testing.T) { if 1 != 1 { t.Fatal("不可能") } }
# 时,断言**永远不会被执行**,但"存在性"判据照样放行 → 9 步门禁全绿
# = 绕过(终审实测)。所以判据升级为"断言必须**可能触发**":
# 处在**恒假分支**里的断言不算数(恒假 = 条件能被静态求值成 false:
# `false` / `1 != 1` / `!true` / `true && false` / 包级布尔常量 false)。
#
# 实现:用 go/ast 做静态分析(见下面的 assertscan)。**注释天然不算断言**
# (AST 里没有注释节点)—— 此前靠 strip_go_comments 去注释,现在不再需要。
#
# **四类"不可达"(迭代 27 终审 P1-2 补齐)**:终审实测,此前只认第一类,
# 另外三类都能让门禁 **9/9 全绿放行**:
#
#   1. **恒假分支** —— 条件能被静态求值成 false(`false` / `1 != 1` /
#      `!true` / `true && false` / 包级布尔常量 false);
#   2. **零次循环** —— for 的条件恒假(`for i := 0; i < 0; i++`)或
#      range 一个**空**字面量(`for range []int{} {}`);
#   3. **无匹配分支** —— switch 的 tag 是常量、**没有任何** case 命中、
#      也没有 default(`switch 1 { case 2: … }`);
#   4. **未被调用的辅助函数** —— 函数体内有断言,但该函数在**本包内**
#      没有任何调用点(非 Test*/Benchmark*/Fuzz*/Example*/init/main)。
#
# 第 4 条的**跨文件**口径:调用点按**包**收集(包内所有 `_test.go` 文件),
# 不是按单个文件 —— 否则"定义在 helpers_test.go、调用在别的测试文件里"
# 的辅助函数会被误判成"从未被调用"(假阳性 → 门禁误红)。
#
# 已知边界(**有意取舍**,登记在案,不是缺陷):
#   - 判据是**按包**的:"1 个真断言 + 9 个零断言用例"的包照样通过;
#   - 接收者名不遵循 t/tb/tt 惯例的 testing.T 认不出来(极罕见);
#   - 断言写在**非测试文件**里的 testing 助手中(本仓库零命中)认不出来;
#   - 恒假判定只覆盖**常量**表达式:条件里一旦出现运行期变量,就保守地
#     认为该分支**可能**触发(偏松不偏严,不会造成误报);
#   - "未被调用"按**名字在本包内出现过**判定:函数值/方法值等**非调用**
#     形态也算"出现过" → 偏保守(宁可漏判,不可误报);
#   - 解析失败的测试文件按"没有断言"计(拿不到证据时偏严 → 门禁红)。
#
# 门禁与**自检**走同一个函数 —— 理由同 real_usage_verdict:自检若自己另写
# 一套判定,将来有人把这里改坏,自检照样绿,盲区就又回来了。
#
# 拿不到包清单(go list 失败)→ 返回非 0 → 门禁**红**(fail closed),
# 绝不"扫不到就当达标"。

# ASSERT_SCAN_DIR / ASSERT_SCAN_BIN 是"断言强度扫描器"的编译产物。
ASSERT_SCAN_DIR=""
ASSERT_SCAN_BIN=""

# build_assert_scanner 编译断言强度扫描器(go/ast 静态分析)。
#
# 为什么是"编译一次、多次复用":门禁与自检各要跑一次扫描,每次都
# `go run` 会重复编译(≈1s/次)。编译产物放在 /tmp,退出时清掉。
#
# 源码**内嵌在脚本里**:它是门禁判据的一部分,放成仓库里的独立包会
# 让 `go list ./...` 多出一个包(第 6 步要求每个包都有真实用例 +
# 有断言)—— 一个"只被门禁调用、自身没有用例"的工具包会与门禁
# 自己的规则打架。内嵌 + /tmp 编译是这里最省事也最不惹事的做法。
build_assert_scanner() {
	[ -n "$ASSERT_SCAN_BIN" ] && [ -x "$ASSERT_SCAN_BIN" ] && return 0
	[ -n "$SELF_GO_VER" ] || return 1
	ASSERT_SCAN_DIR=$(mktemp -d /tmp/awo-verify-assertscan.XXXXXX) || return 1
	printf 'module awo/assertscan\n\ngo %s\n' "$SELF_GO_VER" >"$ASSERT_SCAN_DIR/go.mod"
	cat >"$ASSERT_SCAN_DIR/main.go" <<'ASSERTSCAN_GOEOF'
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

// assertscan:判断测试代码里的断言**是否可能触发**。
//
// 输入(manifest):每行 "包导入路径\t测试文件的绝对路径"。
// 输出:每行 "包导入路径\t断言总数\t可能触发的断言数\t不可达原因"。

func main() {
	pkgs, err := readManifest(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "assertscan: %v\n", err)
		os.Exit(1)
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for _, pkg := range pkgs {
		total, live, reasons := scanPackage(pkg)
		fmt.Fprintf(out, "%s\t%d\t%d\t%s\n", pkg.name, total, live, strings.Join(reasons, ","))
	}
}

// readManifest 读入 "包名\t文件" 清单,按包分组(保持出现顺序)。
func readManifest(r *os.File) ([]*pkgFiles, error) {
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 1024*1024), 1024*1024)
	var order []string
	byName := map[string]*pkgFiles{}
	for in.Scan() {
		line := in.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		name, path := parts[0], strings.TrimSpace(parts[1])
		p, ok := byName[name]
		if !ok {
			p = &pkgFiles{name: name}
			byName[name] = p
			order = append(order, name)
		}
		p.paths = append(p.paths, path)
	}
	if err := in.Err(); err != nil {
		return nil, err
	}
	out := make([]*pkgFiles, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out, nil
}

// pkgFiles 是一个包的测试文件列表。
type pkgFiles struct {
	name  string
	paths []string
}

// constVal 是一个常量(布尔 / 数值 / 字符串)。
type constVal struct {
	isBool bool
	b      bool
	isNum  bool
	n      float64
	isStr  bool
	s      string
}

// scanPackage 扫描一个包的全部测试文件,返回 (断言总数, 可能触发的断言数, 不可达原因)。
//
// 解析失败的文件按"没有断言"计:拿不到证据时必须偏严(门禁红),
// 绝不能因为解析失败就当成"有断言"(门禁绿)。
func scanPackage(pkg *pkgFiles) (int, int, []string) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range pkg.paths {
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			continue
		}
		files = append(files, f)
	}
	// 包级常量(布尔/数值):`if 恒假常量 { ... }` 也是恒假分支。
	consts := map[string]constVal{}
	for _, f := range files {
		for name, v := range fileConsts(f) {
			consts[name] = v
		}
	}
	// 本包内出现过的标识符(按**包**收集,不是按文件)——
	// "在 a_test.go 里定义、在 b_test.go 里调用"的辅助函数必须算"有调用点"。
	refs := packageRefs(files)
	total, live := 0, 0
	deadReasons := map[string]bool{}
	for _, f := range files {
		c := &counter{consts: consts, refs: refs, deadReasons: map[string]bool{}}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					c.walk(d.Body, c.funcUnreachableReason(d))
				}
			default:
				c.walk(decl, "")
			}
		}
		total += c.total
		live += c.live
		for r := range c.deadReasons {
			deadReasons[r] = true
		}
	}
	reasons := make([]string, 0, len(deadReasons))
	for r := range deadReasons {
		reasons = append(reasons, r)
	}
	sortStrings(reasons)
	return total, live, reasons
}

// sortStrings 排序(避免输出顺序随 map 迭代抖动)。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// fileConsts 收集文件里包级 const 的常量值(布尔/数值)。
func fileConsts(f *ast.File) map[string]constVal {
	out := map[string]constVal{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if v, ok := constValueOf(vs.Values[i]); ok {
					out[name.Name] = v
				}
			}
		}
	}
	return out
}

// constValueOf 取一个常量表达式(布尔/数值)。
func constValueOf(e ast.Expr) (constVal, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		switch x.Name {
		case "true":
			return constVal{isBool: true, b: true}, true
		case "false":
			return constVal{isBool: true, b: false}, true
		}
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT:
			if v, err := strconv.ParseInt(x.Value, 0, 64); err == nil {
				return constVal{isNum: true, n: float64(v)}, true
			}
			if v, err := strconv.ParseUint(x.Value, 0, 64); err == nil {
				return constVal{isNum: true, n: float64(v)}, true
			}
		case token.FLOAT:
			if v, err := strconv.ParseFloat(x.Value, 64); err == nil {
				return constVal{isNum: true, n: v}, true
			}
		}
	case *ast.UnaryExpr:
		if x.Op == token.SUB {
			if v, ok := constValueOf(x.X); ok && v.isNum {
				return constVal{isNum: true, n: -v.n}, true
			}
		}
	}
	return constVal{}, false
}

// packageRefs 收集本包测试文件里出现过的标识符(计数)。
//
// **函数声明自身的名字不算引用** —— 否则每个函数都"引用自己",
// "从未被调用"的判定就永远不成立。
//
// **按包收集(不是按文件)**:辅助函数常常定义在 helpers_test.go 里、
// 在别的测试文件里调用 —— 只看单个文件会把它误判成"从未被调用"
// (假阳性 → 门禁误红)。
func packageRefs(files []*ast.File) map[string]int {
	refs := map[string]int{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				collectIdents(decl, refs)
				continue
			}
			if fd.Recv != nil {
				collectIdents(fd.Recv, refs)
			}
			collectIdents(fd.Type, refs)
			if fd.Body != nil {
				collectIdents(fd.Body, refs)
			}
		}
	}
	return refs
}

// collectIdents 把节点里出现过的标识符计进 refs。
func collectIdents(n ast.Node, refs map[string]int) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok {
			refs[id.Name]++
		}
		return true
	})
}

// counter 累计断言数。
type counter struct {
	consts map[string]constVal
	// refs 是本包内出现过的标识符计数("从未被调用"的判据)。
	refs map[string]int
	// locals 是**临时**绑定的局部常量(for 的循环变量)。
	locals map[string]constVal
	// deadReasons 记录出现过的"不可达"种类。
	deadReasons map[string]bool
	total       int
	live        int
}

// 不可达原因(自检按这些关键字断言"是被哪条规则抓到的")。
const (
	reasonFalseBranch = "恒假分支"
	reasonZeroLoop    = "零次循环"
	reasonNoCaseMatch = "无匹配分支"
	reasonUncallable  = "未被调用的辅助函数"
)

// walk 遍历 AST。dead 是"当前处在不可达处"的**原因**(空串 = 可能触发)。
//
// 不可达的种类(迭代 27 终审 P1-2 补齐的三类):
//   - 恒假分支:条件能被静态求值成 false;
//   - 零次循环:for 的条件恒假 / range 一个空字面量;
//   - 无匹配分支:switch 的 tag 是常量、没有任何 case 命中、也没有 default;
//   - 未被调用的辅助函数:见 funcUnreachableReason。
func (c *counter) walk(n ast.Node, dead string) {
	if n == nil {
		return
	}
	switch x := n.(type) {
	case *ast.FuncDecl:
		if x.Body != nil {
			c.walk(x.Body, dead)
		}
		return
	case *ast.FuncLit:
		c.walk(x.Body, dead)
		return
	case *ast.IfStmt:
		if x.Init != nil {
			c.walk(x.Init, dead)
		}
		c.walk(x.Cond, dead)
		// 条件恒假 → 整个 then 分支不可达。
		c.walk(x.Body, c.deadUnder(dead, c.staticallyFalse(x.Cond), reasonFalseBranch))
		if x.Else != nil {
			// else 分支在 then 不成立时执行:外层的 dead 仍然适用。
			c.walk(x.Else, dead)
		}
		return
	case *ast.ForStmt:
		if x.Init != nil {
			c.walk(x.Init, dead)
		}
		condFalse := false
		if x.Cond != nil {
			c.walk(x.Cond, dead)
			// 三段式 for 的条件用的是**循环变量**("for i := 0; i < 0; i++"
			// 里的 i)—— 不把它绑成常量就求不出"恒假"。
			c.withInitBindings(x.Init, func() {
				condFalse = c.staticallyFalse(x.Cond)
			})
		}
		// 条件恒假(for i := 0; i < 0; i++)→ 循环体一次都不执行。
		c.walk(x.Body, c.deadUnder(dead, condFalse, reasonZeroLoop))
		if x.Post != nil {
			c.walk(x.Post, dead)
		}
		return
	case *ast.RangeStmt:
		if x.X != nil {
			c.walk(x.X, dead)
		}
		// range 一个**空**字面量 → 零次迭代 → 循环体不可达。
		c.walk(x.Body, c.deadUnder(dead, c.rangeIsEmpty(x.X), reasonZeroLoop))
		return
	case *ast.SwitchStmt:
		if x.Init != nil {
			c.walk(x.Init, dead)
		}
		if x.Tag != nil {
			c.walk(x.Tag, dead)
		}
		if c.switchUnreachable(x) {
			// 常量 tag、没有任何 case 命中、也没有 default → 整个 switch 不可达。
			c.walk(x.Body, c.deadUnder(dead, true, reasonNoCaseMatch))
			return
		}
		c.walkSwitchBody(x, dead)
		return
	}
	ast.Inspect(n, func(m ast.Node) bool {
		if m == nil {
			return false
		}
		if m == n {
			return true
		}
		switch m.(type) {
		case *ast.IfStmt, *ast.FuncDecl, *ast.FuncLit,
			*ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt:
			// 交给 walk 处理(它要按 dead 分支传播),不再由 Inspect 下探。
			c.walk(m, dead)
			return false
		}
		if call, ok := m.(*ast.CallExpr); ok && isAssertionCall(call) {
			c.total++
			if dead == "" {
				c.live++
			} else {
				c.deadReasons[dead] = true
			}
		}
		return true
	})
}

// withInitBindings 在"把 for 的 Init 绑定到常量"的前提下执行 fn。
//
// 三段式 for 的条件用的是**循环变量**("for i := 0; i < 0; i++" 里的 i),
// 它不是包级常量 —— 不绑定就求不出"恒假"。
func (c *counter) withInitBindings(init ast.Stmt, fn func()) {
	bind := c.initBindings(init)
	if len(bind) == 0 {
		fn()
		return
	}
	if c.locals == nil {
		c.locals = map[string]constVal{}
	}
	saved := map[string]constVal{}
	for k, v := range bind {
		if old, ok := c.locals[k]; ok {
			saved[k] = old
		} else {
			saved[k] = constVal{}
		}
		c.locals[k] = v
	}
	fn()
	for k, v := range saved {
		if v == (constVal{}) {
			delete(c.locals, k)
		} else {
			c.locals[k] = v
		}
	}
}

// initBindings 取出 for 的 Init 里绑定到常量的循环变量。
func (c *counter) initBindings(init ast.Stmt) map[string]constVal {
	out := map[string]constVal{}
	as, ok := init.(*ast.AssignStmt)
	if !ok || (as.Tok != token.DEFINE && as.Tok != token.ASSIGN) {
		return out
	}
	if len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return out
	}
	name, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return out
	}
	if v, ok := constValueOf(as.Rhs[0]); ok {
		out[name.Name] = v
		return out
	}
	if id, ok := as.Rhs[0].(*ast.Ident); ok {
		if v, ok := c.consts[id.Name]; ok {
			out[name.Name] = v
		}
	}
	return out
}

// deadUnder 合并"外层不可达原因"与"本层新增的不可达原因"。
//
// 外层原因**优先**:最外层的不可达才是"这条断言为什么永远不触发"的
// 根因(内层只是叠加)。
func (c *counter) deadUnder(outer string, hit bool, reason string) string {
	if outer != "" {
		return outer
	}
	if hit {
		return reason
	}
	return ""
}

// walkSwitchBody 逐个 case 判定可达性。
//
// switch 整体可达时,某个 case 仍可能不可达(tagless switch 的
// `case false:` / 常量 tag 的无匹配 case)。
func (c *counter) walkSwitchBody(x *ast.SwitchStmt, dead string) {
	for _, stmt := range x.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			c.walk(stmt, dead)
			continue
		}
		for _, e := range cc.List {
			c.walk(e, dead)
		}
		caseDead := c.deadUnder(dead, !c.caseReachable(x, cc), reasonNoCaseMatch)
		for _, s := range cc.Body {
			c.walk(s, caseDead)
		}
	}
}

// switchUnreachable 判定 switch 是不是整体不可达。
//
// 判据:tag 是常量(且**没有任何** case 命中)、也没有 default。
// tag 求不出来 → 保守(判为可达)。
func (c *counter) switchUnreachable(x *ast.SwitchStmt) bool {
	tag, ok := c.switchTagValue(x.Tag)
	if !ok {
		return false
	}
	for _, stmt := range x.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		if len(cc.List) == 0 {
			return false // 有 default → 至少一条分支可达
		}
		for _, e := range cc.List {
			cv, known := c.caseValue(e)
			if !known {
				return false // 求不出 → 保守:认为可能命中
			}
			if constEqual(tag, cv) {
				return false // 命中 → switch 可达
			}
		}
	}
	return true
}

// caseReachable 判定某个 case 分支可达吗。
func (c *counter) caseReachable(x *ast.SwitchStmt, cc *ast.CaseClause) bool {
	if len(cc.List) == 0 {
		return true // default:总是可达
	}
	tag, ok := c.switchTagValue(x.Tag)
	if !ok {
		return true
	}
	for _, e := range cc.List {
		cv, known := c.caseValue(e)
		if !known {
			return true
		}
		if constEqual(tag, cv) {
			return true
		}
	}
	return false
}

// switchTagValue 取 switch 的 tag 常量值。
//
// tag == nil(tagless switch)→ 隐含的 tag 是 true。
func (c *counter) switchTagValue(tag ast.Expr) (constVal, bool) {
	if tag == nil {
		return constVal{isBool: true, b: true}, true
	}
	if b := c.evalBool(tag); b.known {
		return constVal{isBool: true, b: b.v}, true
	}
	if s, ok := c.evalStr(tag); ok {
		return constVal{isStr: true, s: s}, true
	}
	if n, ok := c.evalNum(tag); ok {
		return constVal{isNum: true, n: n}, true
	}
	return constVal{}, false
}

// caseValue 取一个 case 表达式的常量值。
func (c *counter) caseValue(e ast.Expr) (constVal, bool) {
	if b := c.evalBool(e); b.known {
		return constVal{isBool: true, b: b.v}, true
	}
	if s, ok := c.evalStr(e); ok {
		return constVal{isStr: true, s: s}, true
	}
	if n, ok := c.evalNum(e); ok {
		return constVal{isNum: true, n: n}, true
	}
	return constVal{}, false
}

// constEqual 比较两个常量值是否相等(类型必须一致)。
func constEqual(a, b constVal) bool {
	switch {
	case a.isBool && b.isBool:
		return a.b == b.b
	case a.isNum && b.isNum:
		return a.n == b.n
	case a.isStr && b.isStr:
		return a.s == b.s
	}
	return false
}

// rangeIsEmpty 判定 range 的对象是不是"**零元素**"的字面量。
//
// `for range []int{} {}` / `for _, v := range "" {}` 这类循环体一次都不执行
// → 里面的断言永远不会被触发。
func (c *counter) rangeIsEmpty(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.CompositeLit:
		return len(e.Elts) == 0
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			s, err := strconv.Unquote(e.Value)
			return err == nil && s == ""
		}
	}
	return false
}

// funcUnreachableReason 判定一个函数是不是"不可达",返回不可达原因。
//
// 判据:它是**辅助函数**(非 Test*/Benchmark*/Fuzz*/Example*/init/main),
// 且它的名字在**本包的所有测试文件里一次都没出现过** → 没有任何调用点
// → 函数体里的断言永远不会被触发。
//
// **为什么 Test* 排除在外**:TestXxx 由测试框架调用 —— 它就是入口。
// 同理 Benchmark*/Fuzz*/Example* 与 init/main。
//
// **为什么按包收集引用点**(见 packageRefs):跨文件调用的辅助函数
// (定义在 a_test.go、调用在 b_test.go)在本包内**有**调用点,
// 只看单个文件会误报。
func (c *counter) funcUnreachableReason(fn *ast.FuncDecl) string {
	name := fn.Name.Name
	if !isHelperFuncName(name) {
		return ""
	}
	if c.refs[name] > 0 {
		return ""
	}
	return reasonUncallable
}

// isHelperFuncName 判定一个函数名是不是"辅助函数"(而不是测试入口)。
func isHelperFuncName(name string) bool {
	if name == "init" || name == "main" {
		return false
	}
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// isAssertionCall 判断一个调用是不是"断言"。
//
// 口径与门禁原有的文本判据一致:
//
//   - 惯例接收者(t / tb / tt / t0 / t12 …)上的
//     Error / Errorf / Fatal / Fatalf / Fail / FailNow;
//   - assert. / require. 包里的任意函数调用。
func isAssertionCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if x.Name == "assert" || x.Name == "require" {
		return true
	}
	if !isTestReceiver(x.Name) {
		return false
	}
	switch sel.Sel.Name {
	case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
		return true
	}
	return false
}

// isTestReceiver 判断接收者名是否遵循 testing 惯例(t / tb / tt / tN)。
func isTestReceiver(name string) bool {
	if name == "t" || name == "tb" || name == "tt" {
		return true
	}
	if len(name) < 2 || name[0] != 't' {
		return false
	}
	for _, r := range name[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return false
}

// staticallyFalse 判断条件能否被静态求值成 false。
func (c *counter) staticallyFalse(e ast.Expr) bool {
	b := c.evalBool(e)
	return b.known && !b.v
}

// boolVal 是一个布尔常量(known=false 表示"求不出来")。
type boolVal struct {
	v     bool
	known bool
}

// evalBool 静态求值一个布尔表达式。
//
// 只处理**常量**运算:布尔字面量、数值字面量的比较、包级布尔常量、
// ! / && / ||。遇到变量一律返回"求不出来"(→ 保守地认为分支可能触发)。
func (c *counter) evalBool(e ast.Expr) boolVal {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return c.evalBool(x.X)
	case *ast.Ident:
		switch x.Name {
		case "true":
			return boolVal{v: true, known: true}
		case "false":
			return boolVal{v: false, known: true}
		}
		if v, ok := c.locals[x.Name]; ok && v.isBool {
			return boolVal{v: v.b, known: true}
		}
		if v, ok := c.consts[x.Name]; ok && v.isBool {
			return boolVal{v: v.b, known: true}
		}
	case *ast.UnaryExpr:
		if x.Op == token.NOT {
			b := c.evalBool(x.X)
			if b.known {
				return boolVal{v: !b.v, known: true}
			}
		}
	case *ast.BinaryExpr:
		switch x.Op {
		case token.LAND:
			l := c.evalBool(x.X)
			if l.known && !l.v {
				return boolVal{v: false, known: true}
			}
			r := c.evalBool(x.Y)
			if l.known && l.v {
				return r
			}
			if r.known && !r.v {
				return boolVal{v: false, known: true}
			}
			return boolVal{}
		case token.LOR:
			l := c.evalBool(x.X)
			if l.known && l.v {
				return boolVal{v: true, known: true}
			}
			r := c.evalBool(x.Y)
			if l.known && !l.v {
				return r
			}
			if r.known && r.v {
				return boolVal{v: true, known: true}
			}
			return boolVal{}
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
			return c.evalCompare(x)
		}
	}
	return boolVal{}
}

// evalCompare 比较两个常量。
func (c *counter) evalCompare(x *ast.BinaryExpr) boolVal {
	ln, lok := c.evalNum(x.X)
	rn, rok := c.evalNum(x.Y)
	if lok && rok {
		switch x.Op {
		case token.EQL:
			return boolVal{v: ln == rn, known: true}
		case token.NEQ:
			return boolVal{v: ln != rn, known: true}
		case token.LSS:
			return boolVal{v: ln < rn, known: true}
		case token.GTR:
			return boolVal{v: ln > rn, known: true}
		case token.LEQ:
			return boolVal{v: ln <= rn, known: true}
		case token.GEQ:
			return boolVal{v: ln >= rn, known: true}
		}
		return boolVal{}
	}
	ls, lsok := c.evalStr(x.X)
	rs, rsok := c.evalStr(x.Y)
	if lsok && rsok {
		switch x.Op {
		case token.EQL:
			return boolVal{v: ls == rs, known: true}
		case token.NEQ:
			return boolVal{v: ls != rs, known: true}
		}
		return boolVal{}
	}
	lb := c.evalBool(x.X)
	rb := c.evalBool(x.Y)
	if lb.known && rb.known {
		switch x.Op {
		case token.EQL:
			return boolVal{v: lb.v == rb.v, known: true}
		case token.NEQ:
			return boolVal{v: lb.v != rb.v, known: true}
		}
		return boolVal{}
	}
	if isNilIdent(x.X) && isNilIdent(x.Y) {
		switch x.Op {
		case token.EQL:
			return boolVal{v: true, known: true}
		case token.NEQ:
			return boolVal{v: false, known: true}
		}
	}
	return boolVal{}
}

// evalNum 静态求值一个数值表达式。
func (c *counter) evalNum(e ast.Expr) (float64, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return c.evalNum(x.X)
	case *ast.Ident:
		if v, ok := c.locals[x.Name]; ok && v.isNum {
			return v.n, true
		}
		if v, ok := c.consts[x.Name]; ok && v.isNum {
			return v.n, true
		}
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT:
			if v, err := strconv.ParseInt(x.Value, 0, 64); err == nil {
				return float64(v), true
			}
			if v, err := strconv.ParseUint(x.Value, 0, 64); err == nil {
				return float64(v), true
			}
		case token.FLOAT:
			if v, err := strconv.ParseFloat(x.Value, 64); err == nil {
				return v, true
			}
		}
	case *ast.UnaryExpr:
		switch x.Op {
		case token.SUB:
			if v, ok := c.evalNum(x.X); ok {
				return -v, true
			}
		case token.ADD:
			return c.evalNum(x.X)
		}
	case *ast.BinaryExpr:
		switch x.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO:
			l, lok := c.evalNum(x.X)
			r, rok := c.evalNum(x.Y)
			if lok && rok {
				switch x.Op {
				case token.ADD:
					return l + r, true
				case token.SUB:
					return l - r, true
				case token.MUL:
					return l * r, true
				case token.QUO:
					if r != 0 {
						return l / r, true
					}
				}
			}
		}
	}
	return 0, false
}

// evalStr 静态求值一个字符串常量。
func (c *counter) evalStr(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// isNilIdent 判断表达式是不是 nil 字面量。
func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}
ASSERTSCAN_GOEOF
	( cd "$ASSERT_SCAN_DIR" && go build -o "$ASSERT_SCAN_DIR/assertscan" . ) || return 1
	ASSERT_SCAN_BIN="$ASSERT_SCAN_DIR/assertscan"
	return 0
}

# assert_scan_report <模块目录> <包列表> → 打印"测试代码里没有**可能触发**的断言"的包
#   (每行 "  <pkg>: <原因>");空输出 = 全部达标。
#
# 门禁与**自检**走同一个函数 —— 理由同 real_usage_verdict:自检若自己另写
# 一套判定,将来有人把这里改坏,自检照样绿,盲区就又回来了。
#
# 拿不到包清单(go list 失败)→ 返回非 0 → 门禁**红**(fail closed),
# 绝不"扫不到就当达标"。
assert_scan_report() {
	as_dir="$1"
	as_pkgs="$2"
	build_assert_scanner || return 1
	as_list=$(cd "$as_dir" && go list -f '{{.ImportPath}}{{"\037"}}{{.Dir}}{{"\037"}}{{range .TestGoFiles}}{{.}} {{end}}{{"\037"}}{{range .XTestGoFiles}}{{.}} {{end}}' $as_pkgs 2>/dev/null) || return 1
	[ -n "$as_list" ] || return 1
	as_manifest=$(mktemp /tmp/awo-assert-manifest.XXXXXX) || return 1
	printf '%s\n' "$as_list" | while IFS=$'\037' read -r as_pkg as_pkgdir as_test as_xtest; do
		[ -n "$as_pkg" ] || continue
		# **没有测试文件的包不归这里管**:它们由"真实用例"判据报出来
		# (0 个真实用例 / 没有测试文件),不重复报。
		as_files="$as_test $as_xtest"
		[ -n "$(printf '%s' "$as_files" | tr -d ' ')" ] || continue
		for as_f in $as_files; do
			# 文件名是**相对于包目录**的(go list 给的是基名)。
			[ -f "$as_pkgdir/$as_f" ] || continue
			printf '%s\t%s\n' "$as_pkg" "$as_pkgdir/$as_f"
		done
	done >"$as_manifest"
	as_scan=$("$ASSERT_SCAN_BIN" <"$as_manifest") || {
		rm -f "$as_manifest"
		return 1
	}
	rm -f "$as_manifest"
	printf '%s\n' "$as_scan" | awk -F'\t' '
		{ tot[$1] += $2; live[$1] += $3; seen[$1] = 1
		  if ($4 != "") reason[$1] = (reason[$1] == "" ? $4 : reason[$1] "," $4) }
		END {
			for (p in seen) {
				if (live[p] > 0) continue
				if (tot[p] == 0) {
					print "  " p ": 测试代码里没有任何断言(用例跑了,但什么都没验证)"
				} else {
					print "  " p ": 测试代码里的断言都在不可达处(" reason[p] "),永远不会被触发(等价于没有断言)"
				}
			}
		}' | sort
}

NO_ASSERT=$(assert_scan_report "$ROOT" "$CHECK_PKGS") ||
	fail "第 6 步的断言扫描执行失败(拿不到包清单 —— 必须红,不能静默跳过)"
[ -z "$NO_ASSERT" ] || fail "以下包的测试代码里没有可能触发的断言:$NO_ASSERT"

# ---- 门禁自检:第 6 步的判定本身必须真的能抓到 0 用例 / 全 skip / 伪造输出 -
# 为什么必须有这一段:第 6 步是门禁里**最复杂**的一段判定(框架事件 + 顶层/
# 子用例的区分 + 窄化/兜底两段执行)。它守护的正是 #252 的绕过场景本身 ——
# 一个只含 t.Skip() 的新包。如果这一段被改坏(把判据改回解析文本、或把
# pass 事件的条件放宽),**没有任何测试会发现**:工作区里的每个包都有真
# 用例,判定逻辑错了也照样全绿。
#
# 做法:在 /tmp 里造一个**隔离的最小模块**,跑**真的** `go test`,
# 把**真的**输出喂给**同一个** real_usage_verdict,断言五件事:
#   1. 正常包(有真正执行的用例)→ **不得**被报出来(防空转/防误报);
#   2. 0 用例包(测试文件里没有 Test 函数)→ 必须被报出来,原因含"0 个真实用例";
#   3. 全 skip 包(唯一的用例被 t.Skip 跳过)→ 必须被报出来,原因含"t.Skip";
#   4. **伪造输出包**(用例自己打印 "=== RUN" / "--- PASS:" 行,真实结果是被
#      t.Skip 跳过)→ 必须被报出来 —— 这正是本轮修的绕过(终审 P2-1)。
#   5. 反向对照(对正常包断言"必须在报告里"必须失败)。
#
# 隔离模块放在 /tmp:仓库里造坏包会污染 go list 的包集合与第 1 步的
# gofmt 清单。go.mod 的 go 指令取自仓库自己的 go.mod(不硬编码版本号)。
# 成本:两次小模块的 `go test` ≈ 2s(实测)。
USAGE_CHECK_DIR=$(mktemp -d /tmp/awo-verify-usagecheck.XXXXXX)
mkdir -p "$USAGE_CHECK_DIR/good" "$USAGE_CHECK_DIR/zero" "$USAGE_CHECK_DIR/skip" "$USAGE_CHECK_DIR/fake" \
	"$USAGE_CHECK_DIR/noassert" "$USAGE_CHECK_DIR/asserted" "$USAGE_CHECK_DIR/commentonly" \
	"$USAGE_CHECK_DIR/deadassert" "$USAGE_CHECK_DIR/liveassert" \
	"$USAGE_CHECK_DIR/deadloop" "$USAGE_CHECK_DIR/deadcase" \
	"$USAGE_CHECK_DIR/deadhelper" "$USAGE_CHECK_DIR/livehelper"
printf 'module awo/usagecheck\n\ngo %s\n' "$SELF_GO_VER" >"$USAGE_CHECK_DIR/go.mod"
printf 'package good\n\nimport "testing"\n\nfunc TestGood(t *testing.T) { _ = 1 }\n' >"$USAGE_CHECK_DIR/good/good_test.go"
printf 'package zero\n\nfunc helperOnly() {}\n' >"$USAGE_CHECK_DIR/zero/zero_test.go"
printf 'package skip\n\nimport "testing"\n\nfunc TestSkipped(t *testing.T) { t.Skip("环境不具备") }\n' >"$USAGE_CHECK_DIR/skip/skip_test.go"
# 伪造包:用例被 t.Skip 跳过(什么都没验证),却自己打印框架格式的行 ——
# 迭代 14B 的文本判据会放行它,本轮的框架事件判据必须抓住它。
printf 'package fake\n\nimport (\n\t"fmt"\n\t"testing"\n)\n\nfunc TestForged(t *testing.T) {\n\tfmt.Println("=== RUN   Forged")\n\tfmt.Println("--- PASS: Forged (0.00s)")\n\tt.Skip("环境不具备")\n}\n' >"$USAGE_CHECK_DIR/fake/fake_test.go"
# 零断言包:用例**真的跑了**、真的 pass 了,但**没有任何断言** ——
# 框架照样给它顶层 pass 事件 → "真实用例"判据放行它(#289 的绕过本身)。
printf 'package noassert\n\nimport "testing"\n\nfunc TestNoAssert(t *testing.T) {\n\t_ = 1\n}\n' >"$USAGE_CHECK_DIR/noassert/noassert_test.go"
# 有断言的包:断言扫描的**对照组**(绿方向 —— 没有它,下面两条红断言是恒真的)。
#
# 注意:这里的断言**必须可能触发** —— 值取自运行期变量(got),不是常量
# 表达式。此前这个夹具写的是 `if 1 != 1 { t.Fatal("不可能") }`,那正是
# 第二轮终验 P2-1 抓到的**绕过形态本身**:断言永远不触发,判据却只看
# "存在性" → 放行。判据升级为"可能触发"后,它必须换成一条真断言。
printf 'package asserted\n\nimport "testing"\n\nfunc TestAsserted(t *testing.T) {\n\tgot := 1 + 1\n\tif got != 2 {\n\t\tt.Fatalf("1+1 != 2? got %%d", got)\n\t}\n}\n' >"$USAGE_CHECK_DIR/asserted/asserted_test.go"
# 恒不触发的断言包:唯一的断言在**恒假分支**里(永远不会被执行)——
# 这正是第二轮终验 P2-1 的绕过场景本身,判据必须把它抓出来。
printf 'package deadassert\n\nimport "testing"\n\nfunc TestDeadAssert(t *testing.T) {\n\tif 1 != 1 {\n\t\tt.Fatal("不可能")\n\t}\n}\n' >"$USAGE_CHECK_DIR/deadassert/deadassert_test.go"
# 正常断言包(绿对照):断言在**运行期才成立**的 if 分支里 ——
# 新判据绝不能把这种正常写法误判成"恒不触发"(否则正常代码全被挡在门外)。
printf 'package liveassert\n\nimport "testing"\n\nfunc TestLiveAssert(t *testing.T) {\n\tcases := []int{1, 2, 3}\n\tfor _, c := range cases {\n\t\tif c <= 0 {\n\t\t\tt.Fatalf("非正数: %%d", c)\n\t\t}\n\t}\n}\n' >"$USAGE_CHECK_DIR/liveassert/liveassert_test.go"
# 只在**注释里**提到断言的包:注释不是断言 —— 文本级扫描必须先把注释去掉,
# 否则这个包会被判成"有断言"而放行(实测:本仓库的测试文件大量在注释里
# 写着"这里应该 t.Errorf(...)",不处理注释等于判据失效)。
printf 'package commentonly\n\nimport "testing"\n\n// 这里应该 t.Errorf(但注释不是断言)\nfunc TestCommentOnly(t *testing.T) {\n\t_ = 1\n}\n' >"$USAGE_CHECK_DIR/commentonly/commentonly_test.go"
# ---- 迭代 27(终审 P1-2):三类"死断言"绕过形态的夹具 ------------------------
# 这三个夹具对应终审实测到的三种"9/9 全绿放行"的绕过:
#   B1 断言写在**永不执行**的 for 循环里(for i := 0; i < 0; i++);
#   B2 断言写在**不匹配**的 switch 分支里(常量 tag、无 default);
#   B3 断言写在**从未被调用**的辅助函数里。
# 判据必须把它们全部抓住;否则"新增一个永远不执行的断言包"就能绕过门禁。
printf 'package deadloop\n\nimport "testing"\n\nfunc TestDeadLoop(t *testing.T) {\n\tfor i := 0; i < 0; i++ {\n\t\tt.Fatal("不可能")\n\t}\n\t_ = 1\n}\n' >"$USAGE_CHECK_DIR/deadloop/deadloop_test.go"
printf 'package deadcase\n\nimport "testing"\n\nfunc TestDeadCase(t *testing.T) {\n\tswitch 1 {\n\tcase 2:\n\t\tt.Fatal("不可能")\n\t}\n\t_ = 1\n}\n' >"$USAGE_CHECK_DIR/deadcase/deadcase_test.go"
# B3:断言在**从未被调用**的辅助函数里(本包内没有任何调用点)。
printf 'package deadhelper\n\nimport "testing"\n\nfunc TestDeadHelper(t *testing.T) {\n\t_ = 1\n}\n\nfunc neverCalled(t *testing.T) {\n\tt.Fatal("不可能")\n}\n' >"$USAGE_CHECK_DIR/deadhelper/deadhelper_test.go"
# 绿对照(与 B3 同一族):辅助函数**被调用**了 → 它的断言可能触发 →
# 绝不能被"未被调用"这条新判据误报(否则新判据把正常代码全挡在门外)。
printf 'package livehelper\n\nimport "testing"\n\nfunc TestLiveHelper(t *testing.T) {\n\thasAssertion(t)\n}\n\nfunc hasAssertion(t *testing.T) {\n\tgot := 1 + 1\n\tif got != 2 {\n\t\tt.Fatalf("1+1 != 2? got %%d", got)\n\t}\n}\n' >"$USAGE_CHECK_DIR/livehelper/livehelper_test.go"
USAGE_TEXT_LOG="$USAGE_CHECK_DIR/test.log"
if ! ( cd "$USAGE_CHECK_DIR" && go test -count=1 -v ./... ) >"$USAGE_TEXT_LOG" 2>&1; then
	# 隔离模块自己跑不起来 = 自检环境不可用 → 必须红,不能静默跳过
	cat "$USAGE_TEXT_LOG" >&2
	fail "门禁自检失败:隔离模块的 go test 自身失败(自检环境不可用 —— 必须红,不能静默跳过)"
fi
USAGE_PKGS="awo/usagecheck/good awo/usagecheck/zero awo/usagecheck/skip awo/usagecheck/fake"
USAGE_REPORT=$(real_usage_verdict "$USAGE_CHECK_DIR" "$USAGE_PKGS" "$USAGE_TEXT_LOG") ||
	fail "门禁自检失败:第 6 步判定函数自身执行失败(自检环境不可用 —— 必须红,不能静默跳过)"

# usage_expect <包> <报告> <期望在报告里? yes|no> [原因关键字]
#
# 返回 0 = 符合期望;返回 1 = 不符合。
#
# **为什么是一个函数而不是两个**:两个方向必须走**同一个**判定函数 ——
# 否则"正向函数被改成恒真"这类失效就没人发现(断言助手只能守住
# selfcheck_ok / selfcheck_red 本身,守不住它们调用的判定函数)。
# 下面第 5 条场景就是**反向对照**:同一个函数,对"正常包"断言"必须在报告里"
# 必须失败 —— 它失败,才说明这个函数真的在区分。
usage_expect() {
	ue_pkg="$1"
	ue_rep="$2"
	ue_want="$3"
	ue_reason="$4"
	ue_found=1
	if printf '%s\n' "$ue_rep" | grep -qE "^  $ue_pkg: "; then
		ue_found=0
	fi
	if [ "$ue_want" = "yes" ]; then
		[ "$ue_found" -eq 0 ] || return 1
		if [ -n "$ue_reason" ]; then
			printf '%s\n' "$ue_rep" | grep -qE "^  $ue_pkg: .*$ue_reason" || return 1
		fi
		return 0
	fi
	[ "$ue_found" -eq 1 ] || return 1
	return 0
}
selfcheck_ok "第 6 步判定:正常包(有真正执行的用例)不得被误报" usage_expect "awo/usagecheck/good" "$USAGE_REPORT" "no"
selfcheck_ok "第 6 步判定:0 用例包必须被抓(否则'新增一个空测试包'就能绕过门禁)" usage_expect "awo/usagecheck/zero" "$USAGE_REPORT" "yes" "0 个真实用例"
selfcheck_ok "第 6 步判定:全 t.Skip 包必须被抓(#252 的绕过场景本身)" usage_expect "awo/usagecheck/skip" "$USAGE_REPORT" "yes" "t\.Skip"
selfcheck_ok "第 6 步判定:伪造 '=== RUN'/'--- PASS' 输出的包必须被抓(用例自己打印的行不是框架事件 —— 终审 P2-1 的绕过本身)" usage_expect "awo/usagecheck/fake" "$USAGE_REPORT" "yes"
# 反向对照:同一个判定函数,对**正常包**断言"必须在报告里"必须失败 ——
# 它若通过,说明这个函数根本没有在区分(恒真),上面几条就都是空转。
selfcheck_red "第 6 步判定必须能区分:正常包不在报告里 → 断言'在报告里'必须失败" usage_expect "awo/usagecheck/good" "$USAGE_REPORT" "yes"

# ---- 门禁自检:第 6 步的"断言扫描"必须真的能抓到零断言的包(#289)--------
# 为什么要单独一段:上面那组场景护的是"**有没有跑**";这一组护的是
# "**验证过什么**"。两者的绕过场景不同,必须各有一组自检:
#   - 用例跑了但**零断言** → 框架给顶层 pass 事件 → "真实用例"判据放行
#     (这正是 #289 的绕过本身)→ 必须由断言扫描抓住。
#
# 断言扫描会把自检模块里**所有**零断言的包都报出来(good/zero/skip/fake
# 本来就没有断言)—— 这是判据的语义,不是误报;本组只断言两件事:
#   1. 有断言的包(对照组)→ **不得**被报出来(绿方向:没有这一条,下面
#      那条红断言就是恒真的);
#   2. 零断言的包 → 必须被报出来(红方向:证明判据真的在区分);
#   3. 反向对照:对有断言的包断言"必须在报告里"必须失败(证明它能判负)。
ASSERT_REPORT=$(assert_scan_report "$USAGE_CHECK_DIR" "awo/usagecheck/good awo/usagecheck/zero awo/usagecheck/skip awo/usagecheck/fake awo/usagecheck/noassert awo/usagecheck/asserted awo/usagecheck/commentonly awo/usagecheck/deadassert awo/usagecheck/liveassert awo/usagecheck/deadloop awo/usagecheck/deadcase awo/usagecheck/deadhelper awo/usagecheck/livehelper") ||
	fail "门禁自检失败:第 6 步的断言扫描函数自身执行失败(自检环境不可用 —— 必须红,不能静默跳过)"
selfcheck_ok "第 6 步断言扫描:有断言的包不得被误报(否则新判据把正常代码挡在门外)" usage_expect "awo/usagecheck/asserted" "$ASSERT_REPORT" "no"
selfcheck_ok "第 6 步断言扫描:零断言的包必须被抓(否则'新增一个恒真测试包'就能绕过门禁 —— #289 的绕过本身)" usage_expect "awo/usagecheck/noassert" "$ASSERT_REPORT" "yes" "没有任何断言"
selfcheck_ok "第 6 步断言扫描:只在注释里提到断言的包必须被抓(注释不是断言 —— 否则文本扫描等于没有判据)" usage_expect "awo/usagecheck/commentonly" "$ASSERT_REPORT" "yes" "没有任何断言"
# 第二轮终验 P2-1 的绕过场景本身:断言**存在**,但永远不会被触发。
selfcheck_ok "第 6 步断言扫描:恒不触发的断言(在恒假分支里)必须被抓(否则'新增一个恒真测试包'就能绕过门禁 —— 终验 P2-1 的绕过本身)" usage_expect "awo/usagecheck/deadassert" "$ASSERT_REPORT" "yes" "恒假分支"
# 绿对照:正常分支里的断言不得被误判成"恒不触发"(否则新判据把正常代码全挡在门外)。
selfcheck_ok "第 6 步断言扫描:运行期才成立的正常断言不得被误判成恒不触发(新判据不能过严)" usage_expect "awo/usagecheck/liveassert" "$ASSERT_REPORT" "no"
# ---- 迭代 27(终审 P1-2):三类"死断言"绕过形态 ----------------------------
# 终审实测:这三种绕过都能让门禁 **9/9 全绿放行**(断言被计成"live")。
# 判据必须把它们抓住;每条自检都断言**具体原因**,证明是被**对应那条**
# 新规则抓到的(而不是被别的规则碰巧抓到)。
selfcheck_ok "第 6 步断言扫描:写在**永不执行**的 for 循环里的断言必须被抓(否则'把断言写进 for i:=0;i<0;i++'就能绕过门禁 —— 终审 P1-2 的 B1)" usage_expect "awo/usagecheck/deadloop" "$ASSERT_REPORT" "yes" "零次循环"
selfcheck_ok "第 6 步断言扫描:写在**不匹配**的 switch 分支里的断言必须被抓(否则'把断言写进 switch 1 { case 2: }'就能绕过门禁 —— 终审 P1-2 的 B2)" usage_expect "awo/usagecheck/deadcase" "$ASSERT_REPORT" "yes" "无匹配分支"
# B3 的自检同时断言**绿方向**:辅助函数**被调用**时,它的断言必须仍然算
# "可能触发"(否则"未被调用"这条新判据会把正常代码全挡在门外)。
assert_expect_dead() {
	aed_pkg="$1"
	aed_rep="$2"
	aed_reason="$3"
	aed_keep="$4"
	usage_expect "$aed_pkg" "$aed_rep" "yes" "$aed_reason" || return 1
	usage_expect "$aed_keep" "$aed_rep" "no" || return 1
	return 0
}
selfcheck_ok "第 6 步断言扫描:写在**从未被调用**的辅助函数里的断言必须被抓(否则'把断言写进没人调用的辅助函数'就能绕过门禁 —— 终审 P1-2 的 B3);同一条自检同时断言'辅助函数被调用时不得被误报'(绿方向)" assert_expect_dead "awo/usagecheck/deadhelper" "$ASSERT_REPORT" "未被调用的辅助函数" "awo/usagecheck/livehelper"
selfcheck_red "第 6 步断言扫描必须能区分:有断言的包不在报告里 → 断言'在报告里'必须失败" usage_expect "awo/usagecheck/asserted" "$ASSERT_REPORT" "yes"
rm -rf "$USAGE_CHECK_DIR"
USAGE_CHECK_DIR=""

# ---- 自检场景计数守卫(防"自检被删 → 门禁恒绿")-------------------------
# 每条自检场景都会给 selfcheck_count 加一;这里的期望值见 SELFCHECK_EXPECTED
# 的注释。**少一条 → 门禁红**(有人删掉了自检调用却没改期望值);
# **多一条 → 门禁红**(新增了自检却忘了同步期望值)。
# 守卫的边界:它挡得住"手滑/重构误删",挡不住"有人同时改了脚本和期望值"
# —— 那已经不是疏忽,是刻意绕过,靠 code review 与 git 历史发现。
[ "$selfcheck_count" -eq "$SELFCHECK_EXPECTED" ] ||
	fail "门禁自检失败:实际执行了 $selfcheck_count 条自检场景,期望 $SELFCHECK_EXPECTED 条 —— 有自检被删掉,或新增后没同步 SELFCHECK_EXPECTED"

echo "OK($CHECK_COUNT 个包均有真实执行并通过的用例,且测试代码里都有断言)"
echo "   自检通过:正常包不误报 / 0 用例包被抓 / 全 skip 包被抓 / 伪造输出包被抓 / 零断言包被抓"
end_step

step "7/9" "前端 lint / 构建 / vitest"
# 三个子检查**各自独立**,不再是一条 `&&` 链。
#
# 此前:`npm run lint && npm run build && npx vitest run` —— 最前面的
# lint 一失败(干净环境里 eslint 根本没装 → exit 127),后面的 tsc 与
# vitest **全被短路跳过**。失败快速用错了地方:lint 的问题不该让你
# 看不见 tsc 和 vitest 的问题,更不该让"前端构建 + 全部前端用例"在
# 任何干净环境里变成死代码。
#
# 现在:三个子检查**全部跑完**,各自记录退出码,失败项一次性全部打印,
# 最后统一判定(任一失败 → 整体失败)。lint 失败时 tsc/vitest 仍会执行。
#
# 不再把构建输出丢进 /dev/null:tsc 的报错走 stdout,丢掉之后构建失败
# 会只留一行 "npm error",排障时看不到真正的类型错误。
#
# 用 `npm run <script>` 而不是 `npx vitest`:npx 在本地找不到包时会
# **联网自动安装** —— 门禁不能有联网副作用,也不能在依赖缺失时
# 变成"临时下载一个 vitest 跑起来再报绿"。npm run 会从
# frontend/node_modules/.bin 解析,缺依赖就明确报 127。
FE_FAILED=()

fe_run() { # fe_run <标签> <命令...>
	fe_label="$1"
	shift
	echo "---- 前端 $fe_label ----"
	# 注意:退出码必须在 `||` 的右侧取 —— `if ( ... ); then ... fi` 这种
	# 写法里,条件为假时 $? 会被 `fi` 之后的 0 覆盖(没有 else 分支时
	# if 语句本身返回 0),会把"失败"报成 "exit 0"。
	fe_rc=0
	( cd frontend && "$@" ) || fe_rc=$?
	if [ "$fe_rc" -eq 0 ]; then
		echo "   ✅ 前端 $fe_label 通过"
	else
		echo "   ❌ 前端 $fe_label 失败(exit $fe_rc)"
		FE_FAILED+=("$fe_label(exit $fe_rc)")
	fi
	return 0
}

fe_run "lint" npm run lint
fe_run "build(tsc+vite)" npm run build
fe_run "vitest" npm run test

if [ "${#FE_FAILED[@]}" -gt 0 ]; then
	echo ""
	echo "---- 前端门禁失败项汇总(${#FE_FAILED[@]} 项)----"
	for f in "${FE_FAILED[@]}"; do
		echo "  ❌ 前端 $f"
	done
	fail "前端门禁失败(${#FE_FAILED[@]} 项失败:${FE_FAILED[*]})"
fi
echo "OK"
end_step

step "8/9" "冒烟:服务器模式 REST+SSE+HITL"
./scripts/smoke.sh
end_step

step "9/9" "冒烟:SSE 实时流"
./scripts/smoke_sse.sh
end_step

echo ""
echo "✅ VERIFY 全部通过(总耗时 $(( $(date +%s) - TOTAL_START ))s)"
