#!/bin/bash
# SSE 端到端验证:订阅 /api/events → 触发工作流 → 事件必须出现在流上
#
# ---- 可靠性要点(本轮加固)--------------------------------------------
# 1. ROOT 由脚本位置推导:此前硬编码了某台机器的绝对路径,换机器/
#    换目录即失效(cd 失败 → set -e 直接退出,冒烟根本没跑)。
# 2. 端口预检:端口被占用时直接报错 —— 否则新起的 server 秒退,
#    健康检查打到的是上一次遗留的进程,冒烟"通过"但什么都没验。
# 3. 清理可靠:server 与所有后台 curl 都在 EXIT/INT/TERM 时被杀掉,
#    临时文件被删掉。此前 curl 子进程在异常路径上会残留。
# ------------------------------------------------------------------------
set -e
set -o pipefail

PORT=${SMOKE_SSE_PORT:-18789}
BASE="http://127.0.0.1:$PORT"
DATA=$(mktemp -d /tmp/awo-sse-e2e.XXXXXX)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$ROOT"

SERVER_PID=""
CURL_PID=""
KA_CURL=""
OUT=""
KA_OUT=""

cleanup() {
	code=$?
	for p in "$CURL_PID" "$KA_CURL"; do
		[ -n "$p" ] || continue
		kill "$p" 2>/dev/null || true
	done
	for p in "$CURL_PID" "$KA_CURL"; do
		[ -n "$p" ] || continue
		wait "$p" 2>/dev/null || true
	done
	if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		for _ in $(seq 1 50); do
			kill -0 "$SERVER_PID" 2>/dev/null || break
			sleep 0.1
		done
		kill -9 "$SERVER_PID" 2>/dev/null || true
	fi
	[ -n "$OUT" ] && rm -f "$OUT"
	[ -n "$KA_OUT" ] && rm -f "$KA_OUT"
	rm -rf "$DATA"
	return $code
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	echo "❌ SSE E2E FAIL: $1"
	[ -f "$DATA/server.log" ] && { echo "---- server 日志 ----"; cat "$DATA/server.log"; }
	exit 1
}

# 打印端口占用者 —— 排障时"端口被占用"必须告诉用户**谁**占的,
# 只报端口号等于让用户自己去猜。
# 依次尝试 lsof(macOS 自带)→ ss(Linux)→ netstat(兜底);三者都没有
# 或都拿不到(权限不足 / 端口刚被释放)时,明说"未能定位",不假装
# 打印了占用者。`lsof` 在非 root 下看不到其它用户的进程,这也是
# 退回到 ss/netstat 的原因。
print_port_owners() {
	po_port="$1"
	po_out=""
	if command -v lsof >/dev/null 2>&1; then
		po_out=$(lsof -nP -iTCP:"$po_port" -sTCP:LISTEN 2>/dev/null || true)
	fi
	if [ -z "$po_out" ] && command -v ss >/dev/null 2>&1; then
		po_out=$(ss -ltnp 2>/dev/null | grep -E "[:.]${po_port}[[:space:]]" || true)
	fi
	if [ -z "$po_out" ] && command -v netstat >/dev/null 2>&1; then
		po_out=$(netstat -anp 2>/dev/null | grep -E "[:.]${po_port}[[:space:]]" || true)
	fi
	if [ -n "$po_out" ]; then
		echo "---- 端口 $po_port 占用者 ----"
		printf '%s\n' "$po_out" | sed 's/^/    /'
	else
		echo "    (未能定位占用者进程:lsof / ss / netstat 不可用或无权限)"
	fi
}

# 端口占用预检
if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
	exec 3<&- 2>/dev/null || true
	exec 3>&- 2>/dev/null || true
	echo "❌ SSE E2E FAIL: 端口 $PORT 已被占用(可能有残留进程);请先清理再运行"
	print_port_owners "$PORT"
	exit 1
fi

go build -tags server -o bin/aw-sse-e2e . >"$DATA/build.log" 2>&1 ||
	{ cat "$DATA/build.log"; fail "server 构建失败"; }
./bin/aw-sse-e2e --server --addr "127.0.0.1:$PORT" --data "$DATA" \
	>"$DATA/server.log" 2>&1 &
SERVER_PID=$!

UP=0
for _ in $(seq 1 40); do
	if ! kill -0 "$SERVER_PID" 2>/dev/null; then
		fail "server 进程已退出(见下方日志)"
	fi
	if curl -sf "$BASE/api/health" >/dev/null 2>&1; then
		UP=1
		break
	fi
	sleep 0.25
done
[ "$UP" = "1" ] || fail "server 未在 10s 内就绪"

# 后台订阅 SSE,写入临时文件
OUT=$(mktemp /tmp/awo-sse-out-XXXXXX)
curl -sN --max-time 14 "$BASE/api/events" >"$OUT" 2>/dev/null &
CURL_PID=$!
sleep 1

# 检查连接即达的注释行(响应头立即下发)
head -2 "$OUT" | grep -q ": connected" || fail "missing : connected comment"
echo "OK: : connected 立即下发"

# 触发工作流(真实执行产生事件)
curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"sse-e2e"}' >/dev/null

# 等待事件出现在流上(workflow.started 必达)
SEEN=0
for i in $(seq 1 40); do
	if grep -q "workflow.started" "$OUT" 2>/dev/null; then
		SEEN=1
		break
	fi
	kill -0 "$SERVER_PID" 2>/dev/null || fail "server 进程在等待期间退出"
	sleep 0.5
done
[ "$SEEN" = "1" ] || fail "15s 内未见 workflow.started"

echo "OK: workflow.started 事件经 SSE 送达"
grep -m1 "node.started" "$OUT" >/dev/null && echo "OK: node.started 事件送达"

kill "$CURL_PID" 2>/dev/null || true
wait "$CURL_PID" 2>/dev/null || true
CURL_PID=""

# keepalive 检查:独立空闲连接,17s 内应收到 ": keepalive"
KA_OUT=$(mktemp /tmp/awo-sse-ka-XXXXXX)
curl -sN --max-time 17 "$BASE/api/events" >"$KA_OUT" 2>/dev/null &
KA_CURL=$!
KA_DEADLINE=$((SECONDS + 17))
KA_OK=0
while [ $SECONDS -lt $KA_DEADLINE ]; do
	grep -q ": keepalive" "$KA_OUT" 2>/dev/null && { KA_OK=1; break; }
	sleep 1
done
kill "$KA_CURL" 2>/dev/null || true
wait "$KA_CURL" 2>/dev/null || true
KA_CURL=""
[ "$KA_OK" = "1" ] || fail "17s 内未见 keepalive"
echo "OK: keepalive 注释行送达"

echo "✅ SSE E2E PASS(4 项全部通过)"
