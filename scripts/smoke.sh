#!/bin/bash
# 端到端冒烟测试:启动 server 模式 → 种子检查 → limit 截断头 → 校验 → 运行闭环
#                 → 循环保护 → HITL 恢复 → 导出 → 负路径 → 失败重试/跳过
# 用法: ./scripts/smoke.sh
#
# ---- 可靠性要点(本轮加固)--------------------------------------------
# 1. 端口预检:端口已被占用时**直接报错**。此前不检查端口,新起的
#    server 因 "address already in use" 秒退,而健康检查打到的仍是
#    上一次遗留的进程 —— 门禁"绿"得毫无意义(假通过)。
# 2. 进程存活检查:每轮等待都确认 server 进程还在,进程没了就打印
#    服务端日志并失败,而不是把超时误报成业务断言失败。
# 3. 可靠清理:EXIT/INT/TERM 都走同一个 cleanup —— 杀 server、等它真的
#    退出(必要时 -9)、删临时数据目录。残留进程会占住端口,让下一次
#    冒烟连到旧进程(回到第 1 条的假通过)。
# ------------------------------------------------------------------------
set -e
set -o pipefail

PORT=${SMOKE_PORT:-18181}
BASE="http://127.0.0.1:$PORT"
DATA=$(mktemp -d /tmp/awo-smoke.XXXXXX)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$ROOT"

SERVER_PID=""

cleanup() {
	code=$?
	if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		# 等它真的退出:立刻返回会让端口仍被占用(残留进程)。
		for _ in $(seq 1 50); do
			kill -0 "$SERVER_PID" 2>/dev/null || break
			sleep 0.1
		done
		kill -9 "$SERVER_PID" 2>/dev/null || true
	fi
	rm -rf "$DATA"
	return $code
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	echo "❌ SMOKE FAIL: $1"
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

# 端口占用预检:能连上说明有人(多半是上次的残留进程)占着端口。
if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
	exec 3<&- 2>/dev/null || true
	exec 3>&- 2>/dev/null || true
	echo "❌ SMOKE FAIL: 端口 $PORT 已被占用(可能有残留进程);请先清理再运行"
	print_port_owners "$PORT"
	exit 1
fi

go build -tags server -o bin/agent-workflow-server-smoke . >"$DATA/build.log" 2>&1 ||
	{ cat "$DATA/build.log"; fail "server 构建失败"; }
./bin/agent-workflow-server-smoke --server --addr "127.0.0.1:$PORT" --data "$DATA" \
	>"$DATA/server.log" 2>&1 &
SERVER_PID=$!

# 等待就绪:每轮先确认进程还活着。
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

# 1. 健康检查
curl -sf "$BASE/api/health" | grep -q '"ok"' || fail "health"

# 2. 示例种子
count=$(curl -sf "$BASE/api/workflows" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$count" -ge 2 ] || fail "seeded workflows = $count"

# 2b. limit 截断头:显式 limit 超上限 → 必须打截断标记(静默截断会让消费方
# 把"返回了 1000 条"误读成"总共 1000 条");未截断不得带头(存在即截断)。
hdrs=$(curl -sf -D - -o /dev/null "$BASE/api/executions?limit=999999")
echo "$hdrs" | grep -qi '^X-Limit-Truncated: true' || fail "limit=999999 缺 X-Limit-Truncated 头"
echo "$hdrs" | grep -qi '^X-Limit-Applied: 1000' || fail "limit=999999 缺 X-Limit-Applied: 1000 头"
hdrs2=$(curl -sf -D - -o /dev/null "$BASE/api/executions?limit=50")
echo "$hdrs2" | grep -qi '^X-Limit-Truncated:' && fail "limit=50 不应带截断头"
echo "OK: limit 截断头(超限打标 / 未超限无标)"

# 3. 正常闭环:plan→execute→review(REJ)→fix→review(APP)→submit
exec_id=$(curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"smoke 正常闭环"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$exec_id" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
[ "$state" = "COMPLETED" ] || fail "正常闭环 state=$state"

# 4. 循环保护:review 永远拒绝 → WAITING_USER
curl -sf -X PUT "$BASE/api/agents/mock-claude/config" -H 'Content-Type: application/json' \
	-d '{"behavior":{"review":{"decisions":["REJECTED"]}}}' >/dev/null
exec2=$(curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"smoke 循环保护"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$exec2" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in WAITING_USER|FAILED|CANCELLED|COMPLETED) break;; esac
	sleep 0.2
done
[ "$state" = "WAITING_USER" ] || fail "循环保护 state=$state"

# 5. HITL 恢复(approve)
curl -sf -X POST "$BASE/api/executions/$exec2/input" -H 'Content-Type: application/json' -d '{"response":"approve"}' >/dev/null
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$exec2" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
[ "$state" = "COMPLETED" ] || fail "HITL 恢复 state=$state"

# 6. 恢复 mock 默认 + 导出记录
curl -sf -X PUT "$BASE/api/agents/mock-claude/config" -H 'Content-Type: application/json' -d '{"behavior":null}' >/dev/null
curl -sf "$BASE/api/executions/$exec2/export" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert "events" in d and "nodes" in d' || fail "export"

# 7. 事件流非空
ev=$(curl -sf "$BASE/api/events/all?limit=10" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$ev" -ge 10 ] || fail "events = $ev"

# 8. 负路径语义:缺失资源 404+NOT_FOUND;终态恢复 400+INVALID_RESUME;幂等 DELETE
code=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/workflows/ghost")
[ "$code" = "404" ] || fail "ghost workflow → $code, want 404"
curl -s "$BASE/api/workflows/ghost" | grep -q '"code":"NOT_FOUND"' || fail "ghost workflow 缺 NOT_FOUND code"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/executions/$exec2/input" -H 'Content-Type: application/json' -d '{"response":"approve"}')
[ "$code" = "400" ] || fail "恢复已完成执行 → $code, want 400"
curl -s -X POST "$BASE/api/executions/$exec2/input" -H 'Content-Type: application/json' -d '{"response":"approve"}' | grep -q 'INVALID_RESUME' || fail "缺 INVALID_RESUME"
code=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE/api/executions/ghost")
[ "$code" = "200" ] || fail "幂等 DELETE → $code, want 200"

# 9. 失败重试/跳过:禁用 submit 技能 → FAILED → 跳过重试 → COMPLETED → 重新启用
curl -sf -X POST "$BASE/api/skills/submit/enable" -H 'Content-Type: application/json' -d '{"enabled":false}' >/dev/null
EXEC3=$(curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"smoke 失败重试"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
STATE3=""
for i in $(seq 1 60); do
	STATE3=$(curl -sf "$BASE/api/executions/$EXEC3" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	[ "$STATE3" = "FAILED" ] && break
	sleep 0.5
done
[ "$STATE3" = "FAILED" ] || fail "禁用 submit 后未失败 state=$STATE3"
curl -sf -X POST "$BASE/api/executions/$EXEC3/retry" -H 'Content-Type: application/json' -d '{"skip":true}' >/dev/null
for i in $(seq 1 60); do
	STATE3=$(curl -sf "$BASE/api/executions/$EXEC3" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	[ "$STATE3" = "COMPLETED" ] && break
	sleep 0.5
done
[ "$STATE3" = "COMPLETED" ] || fail "跳过重试后 state=$STATE3"
curl -sf -X POST "$BASE/api/skills/submit/enable" -H 'Content-Type: application/json' -d '{"enabled":true}' >/dev/null

echo "✅ SMOKE OK(10 项全部通过)"
