#!/bin/bash
# 一键完整验证门禁:
#   gofmt → go vet → go build → Windows 交叉编译 → 全量 race 测试
#   → 每个包都必须有真实用例 → 前端 lint/构建/vitest(各自独立)
#   → 冒烟(REST+SSE+HITL)→ 冒烟(SSE 实时流)
#
# 用法: ./scripts/verify.sh   (任一步失败即非零退出)
#
# ---- 包列表为什么要过滤 ------------------------------------------------
# `go list ./...` 会把两类**不属于本项目**的目录也列进来:
#
#   1. build/          —— Wails 脚手架。build/ios 带 `//go:build ios`,
#                         但 `go list` 仍会枚举它;在 macOS 上
#                         `go build ./...` 直接失败:
#                         "function main is undeclared in the main package"。
#                         (`go test` 把它当 "no test files" 侥幸通过。)
#   2. node_modules/   —— 根目录误装的 npm 产物里夹带了 Go 代码
#                         (node_modules/flatted/golang/pkg/flatted),
#                         `go test ./...` 会把它捎带进去。
#
# 两者都没有测试、也不参与本项目构建,必须排除,否则:
#   - 门禁第一步 `go build` 必然失败(与被测代码无关);
#   - `go test` 的包列表里混进两个空包,掩盖"哪几个包真的跑了测试"。
# 这不是把问题藏起来:Wails 脚手架的跨平台编译问题已登记在
# docs/OPTIMIZATION.md 的"仍然开放"一节,见那里的说明。
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

# ---- 本项目的包列表(排除 build/ 与 node_modules/)-----------------------
if ! PKGS=$(go list ./... | grep -vE '(^|/)(build|node_modules)/'); then
	echo "❌ VERIFY FAIL: go list ./... 失败(代码树当前不可编译?),先修代码再跑门禁" >&2
	exit 1
fi
PKG_COUNT=$(printf '%s\n' "$PKGS" | grep -c . || true)
[ "$PKG_COUNT" -gt 0 ] || fail "包列表为空(go list ./... 过滤后一个包都不剩?)"
echo "包列表: $PKG_COUNT 个(已排除 build/ 与 node_modules/)"

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
#   - build/ 是 Wails 脚手架,按 AGENTS.md 的约定排除。
#   - 因此"文件已 git add"不再是前提:未跟踪的新增 .go 文件同样会被检查。
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	fail "不在 git 工作树里,无法枚举 Go 文件(gofmt 清单依赖 git ls-files)"
fi
GOFMT_FILES=$(
	{
		git ls-files '*.go'
		git ls-files --others --exclude-standard '*.go'
	} | grep -vE '^(build|node_modules)/' | while IFS= read -r f; do
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
# 注意:这里**不能**在 $( ) 里写 case —— macOS 自带的 bash 3.2 解析不了
# (实测:bash: syntax error near unexpected token `;;')。用 awk + grep 代替。
ORPHAN_CANDIDATES=$(
	printf '%s\n' "$GOFMT_FILES" |
		awk -F/ 'NF==1 {print "."; next} {d=$1; for (i=2;i<NF;i++) d=d"/"$i; print d}' |
		sort -u |
		grep -vE '(^|/)[_.]' |
		grep -vE '(^|/)testdata(/|$)' || true
)
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
# 改漏了 → 门禁红(这正是这个守卫的作用)。当前 16 条:
#   1-3  交叉检查:windows 测试文件对照 / 测试文件类型错 / 源文件类型错
#   4-9  孤儿包检查:windows·freebsd·js 各一条绿 + 一条红
#   10   孤儿包检查:目录混包(两个 package 名)必须被抓
#   11   目标平台枚举:必须是 go 工具全集(不是手写清单)
#   12   目标平台枚举断言的反向对照:三平台手写清单必须通不过
#   13-16 第 6 步"真实用例"判定:正常包不误报 / 0 用例包被抓 / 全 skip 包被抓
#          / 反向对照(对正常包断言"必须在报告里"必须失败)
SELFCHECK_EXPECTED=16

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
# 逐包核对第 5 步的测试结果行 **+ 真正执行过的用例数**。
#
# go test 的输出变体:
#   ok   <pkg> 0.12s                    → 有用例
#   ok   <pkg> 0.00s [no tests to run]  → 包在,但一条用例都没跑(静默 0 用例)
#   ?    <pkg> [no test files]           → 包里根本没有测试文件
#   FAIL <pkg> 0.12s                    → 测试失败(第 5 步已拦,这里是兜底)
#   ok   <pkg> (cached)                 → 有用例(只是命中缓存)
#
# **"有 ok 行"不等于"有真实用例"**(迭代 14B 修的绕过):
# 一个只含 `t.Skip()` 的新包,输出同样是 `ok <pkg> 0.00s` —— 门禁据此
# 判"有真实用例"并全绿(终审实测:9 步全绿 = 绕过)。而 `t.Skip()` 掉的
# 用例**什么都没验证**:包看起来有测试,实际一条没跑,将来它自己坏了也
# 不会有人发现。
#
# 因此本步要求:**至少有一条真正执行并通过的用例**(第 5 步用 -v 才会
# 打印 `--- PASS:` / `--- SKIP:`,两者在不带 -v 的输出里无法区分)。
# 判定口径:
#   - 有 `--- PASS:` 行 → 真有用例(通过);
#   - 只有 `--- SKIP:` 行 → **全部被跳过**(新原因:skipall);
#   - 一条 `--- PASS:`/`--- SKIP:` 都没有 → 0 个真实用例(zero)。
#
# 匹配用 awk 的"第二列精确等于包路径",不用正则/前缀匹配:
# `agentworkflow` 是 `agentworkflow/api` 的前缀,前缀匹配会把两个包搞混。
# `--- PASS:` / `--- SKIP:` 只认**行首无缩进**的顶层用例(子用例带缩进;
# 父用例被跳过时子用例也算跳过,按父用例计数即可)。
#
# 另:必须同时看到该包自己的 `=== RUN` 行,才算"测试框架真的跑过用例"。
# 否则用例自己往 stdout 打印一行 "--- PASS: xxx" 就能骗过检查(误判成
# 有真实用例)。看不到 RUN 行 → 该包的 PASS 一律不计数(fail closed)。
#
# 抽成 real_usage_report 而不是就地写死:门禁与**自检**必须走**同一条代码
# 路径** —— 自检若自己另写一套判定,将来有人把这里的判据改坏,自检照样
# 绿,盲区就又回来了(与 win_check 抽函数的理由同源)。
#
# real_usage_report <测试日志> <包列表(空格分隔)>
#   输出"缺少真实执行用例"的清单(每行 "  <pkg>: <原因>");空输出 = 全部达标。
real_usage_report() {
	awk -v pkgs="$2" '
BEGIN {
	n = split(pkgs, a, " ")
	for (i = 1; i <= n; i++) if (a[i] != "") want[a[i]] = 1
	reason["zero"]    = "0 个真实用例(测试文件里没有 Test 函数,或被 build 标签跳过)"
	reason["skipall"] = "全部用例都被 t.Skip 跳过(没有一条真正执行过 —— 看着有测试,实际什么都没验证)"
	reason["nofiles"] = "没有测试文件([no test files])"
	reason["fail"]    = "测试失败"
	reason["missing"] = "测试结果行缺失(包没进测试列表?)"
}
/^=== RUN / { nrun++; next }
/^--- PASS: / { npass++; next }
/^--- SKIP: / { nskip++; next }
/^--- FAIL: / { nfail++; next }
/^(ok|FAIL|\?)[[:space:]]/ {
	if ($2 in want) {
		p = $2
		if ($1 == "ok") st[p] = "ok"
		else if ($1 == "FAIL") st[p] = "fail"
		else st[p] = "nofiles"
		if (index($0, "no tests to run") > 0) st[p] = "zero"
		# 框架没跑过任何用例 → PASS 行不可信(可能是用例自己打印的)
		np[p] = (nrun > 0) ? npass : 0
		ns[p] = nskip
		nrun = 0
		npass = 0
		nskip = 0
		nfail = 0
		next
	}
}
END {
	for (p in want) {
		s = (p in st) ? st[p] : "missing"
		if (s == "ok") {
			if (np[p] == 0) s = (ns[p] > 0) ? "skipall" : "zero"
		}
		if (s != "ok") printf "  %s: %s\n", p, reason[s]
	}
}
' "$1" | sort
}
MISSING=$(real_usage_report "$TEST_LOG" "$CHECK_PKGS")
[ -z "$MISSING" ] || fail "以下包缺少真实执行的用例:$MISSING"

# ---- 门禁自检:第 6 步的判定本身必须真的能抓到 0 用例 / 全 skip ---------
# 为什么必须有这一段:第 6 步是门禁里**最复杂**的一段判定(PASS/SKIP/RUN
# 三种行 + "no tests to run" 文本 + "必须看到 RUN 行"的 fail closed 兜底)。
# 它守护的正是 #252 的绕过场景本身 —— 一个只含 t.Skip() 的新包。
# 如果这一段被改坏(例如有人删掉 `--- SKIP:` 的计数、或把 RUN 行的兜底
# 去掉),**没有任何测试会发现**:工作区里的每个包都有真用例,判定逻辑
# 错了也照样全绿。
#
# 做法:在 /tmp 里造一个**隔离的最小模块**,跑**真的** `go test -v`,
# 把**真的**输出喂给**同一个** real_usage_report,断言三件事:
#   1. 正常包(有真正执行的用例)→ **不得**被报出来(防空转/防误报);
#   2. 0 用例包(测试文件里没有 Test 函数)→ 必须被报出来,原因含"0 个真实用例";
#   3. 全 skip 包(唯一的用例被 t.Skip 跳过)→ 必须被报出来,原因含"t.Skip"。
#
# 隔离模块放在 /tmp:仓库里造坏包会污染 go list 的包集合与第 1 步的
# gofmt 清单。go.mod 的 go 指令取自仓库自己的 go.mod(不硬编码版本号)。
# 成本:一次小模块的 `go test` ≈ 1s(实测)。
USAGE_CHECK_DIR=$(mktemp -d /tmp/awo-verify-usagecheck.XXXXXX)
mkdir -p "$USAGE_CHECK_DIR/good" "$USAGE_CHECK_DIR/zero" "$USAGE_CHECK_DIR/skip"
printf 'module awo/usagecheck\n\ngo %s\n' "$SELF_GO_VER" >"$USAGE_CHECK_DIR/go.mod"
printf 'package good\n\nimport "testing"\n\nfunc TestGood(t *testing.T) { _ = 1 }\n' >"$USAGE_CHECK_DIR/good/good_test.go"
printf 'package zero\n\nfunc helperOnly() {}\n' >"$USAGE_CHECK_DIR/zero/zero_test.go"
printf 'package skip\n\nimport "testing"\n\nfunc TestSkipped(t *testing.T) { t.Skip("环境不具备") }\n' >"$USAGE_CHECK_DIR/skip/skip_test.go"
USAGE_LOG="$USAGE_CHECK_DIR/test.log"
if ! ( cd "$USAGE_CHECK_DIR" && go test -count=1 -v ./... ) >"$USAGE_LOG" 2>&1; then
	# 隔离模块自己跑不起来 = 自检环境不可用 → 必须红,不能静默跳过
	cat "$USAGE_LOG" >&2
	fail "门禁自检失败:隔离模块的 go test 自身失败(自检环境不可用 —— 必须红,不能静默跳过)"
fi
USAGE_PKGS="awo/usagecheck/good awo/usagecheck/zero awo/usagecheck/skip"
USAGE_REPORT=$(real_usage_report "$USAGE_LOG" "$USAGE_PKGS")

# usage_expect <包> <报告> <期望在报告里? yes|no> [原因关键字]
#
# 返回 0 = 符合期望;返回 1 = 不符合。
#
# **为什么是一个函数而不是两个**:两个方向必须走**同一个**判定函数 ——
# 否则"正向函数被改成恒真"这类失效就没人发现(断言助手只能守住
# selfcheck_ok / selfcheck_red 本身,守不住它们调用的判定函数)。
# 下面第 4 条场景就是**反向对照**:同一个函数,对"正常包"断言"必须在报告里"
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
# 反向对照:同一个判定函数,对**正常包**断言"必须在报告里"必须失败 ——
# 它若通过,说明这个函数根本没有在区分(恒真),上面三条就都是空转。
selfcheck_red "第 6 步判定必须能区分:正常包不在报告里 → 断言'在报告里'必须失败" usage_expect "awo/usagecheck/good" "$USAGE_REPORT" "yes"
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

echo "OK($CHECK_COUNT 个包均有真实执行并通过的用例)"
echo "   自检通过:正常包不误报 / 0 用例包被抓 / 全 skip 包被抓"
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
