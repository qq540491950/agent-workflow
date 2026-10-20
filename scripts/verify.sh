#!/bin/bash
# 一键完整验证门禁:
#   gofmt → go vet → go build → 全量 race 测试 → 关键包用例存在性
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

# ---- 本项目的包列表(排除 build/ 与 node_modules/)-----------------------
if ! PKGS=$(go list ./... | grep -vE '(^|/)(build|node_modules)/'); then
	echo "❌ VERIFY FAIL: go list ./... 失败(代码树当前不可编译?),先修代码再跑门禁" >&2
	exit 1
fi
PKG_COUNT=$(printf '%s\n' "$PKGS" | grep -c .)
echo "包列表: $PKG_COUNT 个(已排除 build/ 与 node_modules/)"

# ---- 关键包:这些包必须有真实用例(防"静默 0 用例")-------------------
# 新增包时若忘了写测试,或者测试全被 build 标签跳过,`go test` 会输出
# "[no tests to run]" —— 不检查就会一路绿灯通过。这里显式点名。
KEY_PKGS="agentworkflow \
agentworkflow/agent \
agentworkflow/agent/claude \
agentworkflow/agent/mock \
agentworkflow/agent/pi \
agentworkflow/api \
agentworkflow/app/application \
agentworkflow/contextx \
agentworkflow/event \
agentworkflow/git \
agentworkflow/logx \
agentworkflow/permission \
agentworkflow/persistence \
agentworkflow/skill \
agentworkflow/skill/builtin \
agentworkflow/template \
agentworkflow/workflow/compiler \
agentworkflow/workflow/dsl \
agentworkflow/workflow/model \
agentworkflow/workflow/runtime \
agentworkflow/workflow/validator"

step "1/8" "gofmt"
# 用包列表推导文件清单(此前是手写目录列表,新增包会漏)。
# 注意:必须按**文件**而不是按目录传给 gofmt —— gofmt 遇到目录参数会递归
# 整个目录树,传模块根目录会把 Wails 脚手架(build/ios)一起卷进来
# (AGENTS.md:gofmt -l 应为空,build/ 目录的脚手架除外)。
GOFMT_FILES=$(go list -f '{{range .GoFiles}}{{$.Dir}}/{{.}}
{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}
{{end}}{{range .XTestGoFiles}}{{$.Dir}}/{{.}}
{{end}}' $PKGS)
UNFORMATTED=$(gofmt -l $GOFMT_FILES 2>/dev/null || true)
[ -z "$UNFORMATTED" ] || fail "未格式化: $UNFORMATTED"
echo "OK"
end_step

step "2/8" "go vet"
go vet $PKGS
echo "OK"
end_step

step "3/8" "go build"
# 此前门禁只有 vet 没有 build:vet 能发现类型/语法错,但链接期错误
# (缺 main、符号冲突、平台专属代码)只有 build 才暴露。
# 日志落盘再回显:Wails/objc 的链接产物带 macOS SDK 版本告警,每次
# 都会刷十几行;失败时全量打印,成功时只回显非噪声告警。
BUILD_LOG=$(mktemp /tmp/awo-verify-build.XXXXXX)
trap 'rm -f "$BUILD_LOG" ${TEST_LOG:-}' EXIT
if ! go build $PKGS >"$BUILD_LOG" 2>&1; then
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

step "5/8" "关键包用例存在性"
# 逐个核对关键包的测试结果行:
#   ok <pkg> 0.12s              → 有用例;
#   ok <pkg> 0.00s [no tests to run] → 包在,但一条用例都没跑(静默 0 用例)。
MISSING=""
for p in $KEY_PKGS; do
	LINE=$(grep -E "^ok[[:space:]]+${p}[[:space:]]" "$TEST_LOG" | head -1 || true)
	if [ -z "$LINE" ]; then
		MISSING="$MISSING\n  $p: 测试结果行缺失(包不在测试列表里?)"
	elif echo "$LINE" | grep -q "no tests to run"; then
		MISSING="$MISSING\n  $p: 0 个用例(测试被 build 标签跳过或文件为空)"
	fi
done
[ -z "$MISSING" ] || fail "关键包缺少真实用例:$MISSING"
KEY_COUNT=$(printf '%s\n' "$KEY_PKGS" | grep -c .)
echo "OK($KEY_COUNT 个关键包均有真实用例)"
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
