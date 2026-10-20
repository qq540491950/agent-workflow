#!/bin/bash
# 一键完整验证门禁:
#   gofmt → go vet → go build → 全量 race 测试 → 每个包都必须有真实用例
#   → 前端 lint/构建/vitest → 冒烟(REST+SSE+HITL)→ 冒烟(SSE 实时流)
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
cleanup() {
	if [ -n "$BUILD_LOG" ]; then rm -f "$BUILD_LOG"; fi
	if [ -n "$TEST_LOG" ]; then rm -f "$TEST_LOG"; fi
	if [ -n "$BUILD_OUT" ]; then rm -rf "$BUILD_OUT"; fi
	return 0
}
trap cleanup EXIT

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

step "1/8" "gofmt"
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
UNFORMATTED=$(gofmt -l $GOFMT_FILES 2>/dev/null || true)
[ -z "$UNFORMATTED" ] || fail "未格式化: $UNFORMATTED"
echo "OK($GOFMT_COUNT 个文件)"
end_step

step "2/8" "go vet"
go vet $PKGS
echo "OK"
end_step

step "3/8" "go build"
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

step "4/8" "go test -count=1 -race"
TEST_LOG=$(mktemp /tmp/awo-verify-test.XXXXXX)
# 退出码直接取自 go test(不经管道):管道会把"grep 没匹配到行"的退出码
# 混进来,让一次成功的测试被误判为失败。完整日志落盘(第 5 步要用),
# 控制台只滤掉 macOS SDK 版本告警 —— Wails/objc 的链接产物每次刷十几行,
# 与被测代码无关。
if ! go test -count=1 -race $PKGS >"$TEST_LOG" 2>&1; then
	cat "$TEST_LOG"
	fail "go test 失败"
fi
grep -v "was built for newer 'macOS' version" "$TEST_LOG" || true
echo "OK"
end_step

step "5/8" "每个包都必须有真实用例"
# 逐包核对第 4 步的测试结果行。go test 的输出变体:
#   ok   <pkg> 0.12s                    → 有用例
#   ok   <pkg> 0.00s [no tests to run]  → 包在,但一条用例都没跑(静默 0 用例)
#   ?    <pkg> [no test files]           → 包里根本没有测试文件
#   FAIL <pkg> 0.12s                    → 测试失败(第 4 步已拦,这里是兜底)
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

step "6/8" "前端 lint + 构建 + vitest"
# 不再把构建输出丢进 /dev/null:tsc 的报错走 stdout,丢掉之后
# 构建失败会只留一行 "npm error",排障时看不到真正的类型错误。
# 用 if 包裹而不是裸子 shell:`set -e` 在 if 条件里对被测命令不生效,
# 靠 && 链把三步的失败码传出来,再由这里显式退出。
if ! ( cd frontend && npm run lint && npm run build && npx vitest run ); then
	fail "前端门禁失败(lint / build / vitest)"
fi
echo "OK"
end_step

step "7/8" "冒烟:服务器模式 REST+SSE+HITL"
./scripts/smoke.sh
end_step

step "8/8" "冒烟:SSE 实时流"
./scripts/smoke_sse.sh
end_step

echo ""
echo "✅ VERIFY 全部通过(总耗时 $(( $(date +%s) - TOTAL_START ))s)"
