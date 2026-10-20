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
GOFMT_OUT=""
GOFMT_ERR=""
EMBED_PLACEHOLDER=""
cleanup() {
	if [ -n "$BUILD_LOG" ]; then rm -f "$BUILD_LOG"; fi
	if [ -n "$TEST_LOG" ]; then rm -f "$TEST_LOG"; fi
	if [ -n "$BUILD_OUT" ]; then rm -rf "$BUILD_OUT"; fi
	if [ -n "$WIN_LOG" ]; then rm -f "$WIN_LOG"; fi
	if [ -n "$WIN_OUT" ]; then rm -rf "$WIN_OUT"; fi
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

step "4/9" "Windows 交叉编译(GOOS=windows go build)"
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
# 不污染工作区:-o 指向临时目录,退出时删掉(多包调用时 go build 本来
# 就不落产物,但根 main 包会落一个 .exe —— 用 -o 目录钉死)。
# GOOS=windows 会自动把 CGO_ENABLED 置 0;本项目无 cgo 文件
# (grep 'import "C"' 无命中),交叉编译不需要 C 工具链。
WIN_OUT=$(mktemp -d /tmp/awo-verify-winbuild.XXXXXX)
WIN_LOG=$(mktemp /tmp/awo-verify-winbuild-log.XXXXXX)
if ! GOOS=windows go build -o "$WIN_OUT/" $PKGS >"$WIN_LOG" 2>&1; then
	cat "$WIN_LOG" >&2
	fail "GOOS=windows go build 失败:Windows 专属文件在当前平台不会被其它任何步骤编译到"
fi
echo "OK"
end_step

step "5/9" "go test -count=1 -race"
TEST_LOG=$(mktemp /tmp/awo-verify-test.XXXXXX)
# 退出码直接取自 go test(不经管道):管道会把"grep 没匹配到行"的退出码
# 混进来,让一次成功的测试被误判为失败。完整日志落盘(第 6 步要用),
# 控制台只滤掉 macOS SDK 版本告警 —— Wails/objc 的链接产物每次刷十几行,
# 与被测代码无关。
if ! go test -count=1 -race $PKGS >"$TEST_LOG" 2>&1; then
	cat "$TEST_LOG"
	fail "go test 失败"
fi
grep -v "was built for newer 'macOS' version" "$TEST_LOG" || true
echo "OK"
end_step

step "6/9" "每个包都必须有真实用例"
# 逐包核对第 4 步的测试结果行。go test 的输出变体:
#   ok   <pkg> 0.12s                    → 有用例
#   ok   <pkg> 0.00s [no tests to run]  → 包在,但一条用例都没跑(静默 0 用例)
#   ?    <pkg> [no test files]           → 包里根本没有测试文件
#   FAIL <pkg> 0.12s                    → 测试失败(第 5 步已拦,这里是兜底)
#   ok   <pkg> (cached)                 → 有用例(只是命中缓存)
#
# 匹配用 awk 的"第二列精确等于包路径",不用正则/前缀匹配:
# `agentworkflow` 是 `agentworkflow/api` 的前缀,前缀匹配会把两个包搞混。
MISSING=$(awk -v pkgs="$CHECK_PKGS" '
BEGIN {
	n = split(pkgs, a, " ")
	for (i = 1; i <= n; i++) if (a[i] != "") want[a[i]] = 1
	reason["zero"]    = "0 个用例(测试文件里没有 Test 函数,或被 build 标签跳过)"
	reason["nofiles"] = "没有测试文件([no test files])"
	reason["fail"]    = "测试失败"
	reason["missing"] = "测试结果行缺失(包没进测试列表?)"
}
$1 == "ok"   { if ($2 in want) st[$2] = (index($0, "no tests to run") > 0) ? "zero" : "ok"; next }
$1 == "?"   { if ($2 in want) st[$2] = "nofiles"; next }
$1 == "FAIL" { if ($2 in want) st[$2] = "fail"; next }
END {
	for (p in want) {
		s = (p in st) ? st[p] : "missing"
		if (s != "ok") printf "  %s: %s\n", p, reason[s]
	}
}
' "$TEST_LOG" | sort)
[ -z "$MISSING" ] || fail "以下包缺少真实用例:$MISSING"
echo "OK($CHECK_COUNT 个包均有真实用例)"
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
