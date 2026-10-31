#!/bin/bash
# 端到端冒烟测试:启动 server 模式 → 种子检查 → limit 截断头 → 校验 → 运行闭环
#                 → 循环保护 → HITL 恢复 → 导出 → 负路径 → 失败重试/跳过
#                 → 声明式入参 inputs → 每工作流并发上限
#                 → 事件游标分页 / 全局排队 / 保留清理 / 安全负路径
#                 → 工作区租约冲突 / 失败分支端到端(运行时加固)
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

# python 解释器探测:Linux/macOS 叫 python3,Windows(无 MS Store 别名时)
# 只有 python。占位符(打印提示后退出非 0)会被 command -v 误认,所以再
# 试跑一次真实版本输出。
PY=""
for cand in python3 python; do
	if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'pass' >/dev/null 2>&1; then
		PY="$cand"
		break
	fi
done
[ -n "$PY" ] || { echo "❌ SMOKE FAIL: 找不到可用的 python 解释器(python3 / python 都不可用)"; exit 1; }

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
count=$(curl -sf "$BASE/api/workflows" | ${PY} -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$count" -ge 2 ] || fail "seeded workflows = $count"

# 2b. limit 截断头:显式 limit 超上限 → 必须打截断标记(静默截断会让消费方
# 把"返回了 500 条"误读成"总共 500 条");未截断不得带头(存在即截断)。
hdrs=$(curl -sf -D - -o /dev/null "$BASE/api/executions?limit=999999")
echo "$hdrs" | grep -qi '^X-Limit-Truncated: true' || fail "limit=999999 缺 X-Limit-Truncated 头"
echo "$hdrs" | grep -qi '^X-Limit-Applied: 500' || fail "limit=999999 缺 X-Limit-Applied: 500 头"
hdrs2=$(curl -sf -D - -o /dev/null "$BASE/api/executions?limit=50")
echo "$hdrs2" | grep -qi '^X-Limit-Truncated:' && fail "limit=50 不应带截断头"
echo "OK: limit 截断头(超限打标 / 未超限无标)"

# 3. 正常闭环:plan→execute→review(REJ)→fix→review(APP)→submit
exec_id=$(curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"smoke 正常闭环"}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$exec_id" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
[ "$state" = "COMPLETED" ] || fail "正常闭环 state=$state"

# 4. 循环保护:review 永远拒绝 → WAITING_USER
curl -sf -X PUT "$BASE/api/agents/mock-claude/config" -H 'Content-Type: application/json' \
	-d '{"behavior":{"review":{"decisions":["REJECTED"]}}}' >/dev/null
exec2=$(curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"smoke 循环保护"}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$exec2" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in WAITING_USER|FAILED|CANCELLED|COMPLETED) break;; esac
	sleep 0.2
done
[ "$state" = "WAITING_USER" ] || fail "循环保护 state=$state"

# 5. HITL 恢复(approve)
curl -sf -X POST "$BASE/api/executions/$exec2/input" -H 'Content-Type: application/json' -d '{"response":"approve"}' >/dev/null
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$exec2" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
[ "$state" = "COMPLETED" ] || fail "HITL 恢复 state=$state"

# 6. 恢复 mock 默认 + 导出记录
curl -sf -X PUT "$BASE/api/agents/mock-claude/config" -H 'Content-Type: application/json' -d '{"behavior":null}' >/dev/null
curl -sf "$BASE/api/executions/$exec2/export" | ${PY} -c 'import json,sys; d=json.load(sys.stdin); assert "events" in d and "nodes" in d' || fail "export"

# 7. 事件流非空
ev=$(curl -sf "$BASE/api/events/all?limit=10" | ${PY} -c 'import json,sys; print(len(json.load(sys.stdin)))')
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
EXEC3=$(curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"smoke 失败重试"}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
STATE3=""
for i in $(seq 1 60); do
	STATE3=$(curl -sf "$BASE/api/executions/$EXEC3" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	[ "$STATE3" = "FAILED" ] && break
	sleep 0.5
done
[ "$STATE3" = "FAILED" ] || fail "禁用 submit 后未失败 state=$STATE3"
curl -sf -X POST "$BASE/api/executions/$EXEC3/retry" -H 'Content-Type: application/json' -d '{"skip":true}' >/dev/null
for i in $(seq 1 60); do
	STATE3=$(curl -sf "$BASE/api/executions/$EXEC3" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	[ "$STATE3" = "COMPLETED" ] && break
	sleep 0.5
done
[ "$STATE3" = "COMPLETED" ] || fail "跳过重试后 state=$STATE3"
curl -sf -X POST "$BASE/api/skills/submit/enable" -H 'Content-Type: application/json' -d '{"enabled":true}' >/dev/null

# 10. 声明式入参 inputs:必填缺失 → 400 INPUT_REQUIRED;类型不符 → 400
#     INPUT_TYPE_MISMATCH;提供后 → 200 且缺省项兜 default(启动校验经真实
#     server 的整条链路,与 api 层 mock 测试互补)。
IN_WF='{"id":"smoke-inputs","name":"Smoke Inputs",
	"inputs":[
		{"name":"repo","type":"string","required":true,"description":"目标仓库"},
		{"name":"limit","type":"number","default":3}
	],
	"nodes":[{"id":"a","name":"A","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],
	"edges":[],"enabled":true}'
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows" -H 'Content-Type: application/json' -d "$IN_WF")
[ "$code" = "200" ] || fail "smoke-inputs 种子 → $code, want 200"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows/smoke-inputs/run" -H 'Content-Type: application/json' -d '{"variables":{"limit":1}}')
[ "$code" = "400" ] || fail "必填缺失 run → $code, want 400"
curl -s -X POST "$BASE/api/workflows/smoke-inputs/run" -H 'Content-Type: application/json' -d '{"variables":{"limit":1}}' | grep -q 'INPUT_REQUIRED' || fail "缺 INPUT_REQUIRED"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows/smoke-inputs/run" -H 'Content-Type: application/json' -d '{"variables":{"repo":"x","limit":"3"}}')
[ "$code" = "400" ] || fail "类型不符 run → $code, want 400"
curl -s -X POST "$BASE/api/workflows/smoke-inputs/run" -H 'Content-Type: application/json' -d '{"variables":{"repo":"x","limit":"3"}}' | grep -q 'INPUT_TYPE_MISMATCH' || fail "缺 INPUT_TYPE_MISMATCH"
resp=$(curl -sf -X POST "$BASE/api/workflows/smoke-inputs/run" -H 'Content-Type: application/json' -d '{"variables":{"repo":"smoke-repo"}}')
echo "$resp" | ${PY} -c 'import json,sys; d=json.load(sys.stdin); assert d["variables"]["repo"]=="smoke-repo" and d["variables"]["limit"]==3, d["variables"]' || fail "缺省项未兜 default"
SMOKE_INPUT_EXEC=$(echo "$resp" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$SMOKE_INPUT_EXEC" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
[ "$state" = "COMPLETED" ] || fail "inputs 执行 state=$state"
echo "OK: 声明式入参(必填 400 / 类型 400 / 缺省兜底 / 执行 COMPLETED)"

# 11. 每工作流并发上限:max_concurrent=1 + human 节点占住 WAITING_USER →
#     第二次启动 409 WORKFLOW_BUSY(请求合法但现在不能跑)。
MC_WF='{"id":"smoke-mc","name":"Smoke MC","settings":{"max_concurrent":1},
	"nodes":[{"id":"gate","name":"G","type":"human","config":{"prompt":"等待审批"}}],
	"edges":[],"enabled":true}'
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows" -H 'Content-Type: application/json' -d "$MC_WF")
[ "$code" = "200" ] || fail "smoke-mc 种子 → $code, want 200"
MC_EXEC=$(curl -sf -X POST "$BASE/api/workflows/smoke-mc/run" -H 'Content-Type: application/json' -d '{"task":"first"}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$MC_EXEC" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	[ "$state" = "WAITING_USER" ] && break
	sleep 0.2
done
[ "$state" = "WAITING_USER" ] || fail "首个执行未到 WAITING_USER state=$state"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows/smoke-mc/run" -H 'Content-Type: application/json' -d '{"task":"second"}')
[ "$code" = "409" ] || fail "并发上限 run → $code, want 409"
curl -s -X POST "$BASE/api/workflows/smoke-mc/run" -H 'Content-Type: application/json' -d '{"task":"second"}' | grep -q 'WORKFLOW_BUSY' || fail "缺 WORKFLOW_BUSY"
# 释放额度(取消占位执行)→ 再次启动成功
curl -sf -X POST "$BASE/api/executions/$MC_EXEC/cancel" >/dev/null
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$MC_EXEC" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in CANCELLED|FAILED|COMPLETED) break;; esac
	sleep 0.2
done
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows/smoke-mc/run" -H 'Content-Type: application/json' -d '{"task":"third"}')
[ "$code" = "200" ] || fail "释放后 run → $code, want 200"
echo "OK: 每工作流并发上限(占位 409 / 释放后 200)"

# 12. 运行时加固项:事件 seq 游标分页 / 全局排队(QUEUED+202+取消)/
#     保留策略清理 dry_run / runtime/stats / 健康检查 DB ping。
#     (安全边界 401/403/415 由 api/security_test.go 以 httptest 全链路覆盖:
#     冒烟进程监听 127.0.0.1,Origin/Host 天然受信,这里只补非边界项。)
# 12a. 事件 seq 游标:after_seq 取其后、before_seq 取其前、互斥 → 400。
#      注意:无游标的默认页是"最近一页"(内部 seq DESC 再翻转成升序),
#      所以第一条/最后一条 seq 必须从**全量页**里取,limit=1 拿到的是最近一条。
EV_ALL=$(curl -sf "$BASE/api/executions/$exec_id/events?limit=2000")
TOTAL=$(echo "$EV_ALL" | ${PY} -c 'import json,sys; print(len(json.load(sys.stdin)))')
FIRST_SEQ=$(echo "$EV_ALL" | ${PY} -c 'import json,sys; evs=json.load(sys.stdin); assert evs, "events empty"; print(evs[0]["seq"])')
LAST_SEQ=$(echo "$EV_ALL" | ${PY} -c 'import json,sys; evs=json.load(sys.stdin); print(evs[-1]["seq"])')
AFTER_PAGE=$(curl -sf "$BASE/api/executions/$exec_id/events?after_seq=$FIRST_SEQ&limit=2000" | ${PY} -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$AFTER_PAGE" = "$((TOTAL-1))" ] || fail "after_seq=$FIRST_SEQ 页大小 $AFTER_PAGE != $((TOTAL-1))"
code=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/executions/$exec_id/events?after_seq=1&before_seq=2")
[ "$code" = "400" ] || fail "after_seq+before_seq → $code, want 400"
hdrs=$(curl -sf -D - -o /dev/null "$BASE/api/executions/$exec_id/events?before_seq=$LAST_SEQ&limit=2")
echo "$hdrs" | grep -qi '^X-Has-More: true' || fail "before_seq 截断页缺 X-Has-More"
echo "OK: 事件 seq 游标分页(after/before/互斥 400/X-Has-More)"

# 12b. 全局排队:max_active_executions=1 + 排队开启 → 第二条执行 QUEUED/202,
#      取消排队(QUEUED → cancel → CANCELLED)后恢复设置。
curl -sf -X POST "$BASE/api/settings" -H 'Content-Type: application/json' \
	-d '{"max_active_executions":1,"queue_enabled":true,"max_queued_executions":10,"queue_policy":"fair"}' >/dev/null
Q_EXEC1=$(curl -sf -X POST "$BASE/api/workflows/smoke-mc/run" -H 'Content-Type: application/json' -d '{"task":"queue-holder"}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
Q_EXEC2_RESP=$(curl -s -D /tmp/smoke-q-hdr -X POST "$BASE/api/workflows/smoke-mc/run" -H 'Content-Type: application/json' -d '{"task":"queued-second"}')
code=$(head -1 /tmp/smoke-q-hdr | grep -o '[0-9]\{3\}')
[ "$code" = "202" ] || fail "排队 run → $code, want 202"
echo "$Q_EXEC2_RESP" | ${PY} -c 'import json,sys; d=json.load(sys.stdin); assert d["state"]=="QUEUED", d["state"]' || fail "排队执行 state != QUEUED"
Q_EXEC2=$(echo "$Q_EXEC2_RESP" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
# runtime/stats 反映排队
curl -sf "$BASE/api/runtime/stats" | ${PY} -c 'import json,sys; s=json.load(sys.stdin); assert s["queued"]>=1, s' || fail "runtime/stats 缺 queued"
# 取消排队 → CANCELLED
curl -sf -X POST "$BASE/api/executions/$Q_EXEC2/cancel" >/dev/null
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$Q_EXEC2" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	[ "$state" = "CANCELLED" ] && break
	sleep 0.2
done
[ "$state" = "CANCELLED" ] || fail "取消排队 state=$state"
curl -sf -X POST "$BASE/api/executions/$Q_EXEC1/cancel" >/dev/null
curl -sf -X POST "$BASE/api/settings" -H 'Content-Type: application/json' \
	-d '{"max_active_executions":0,"queue_enabled":false}' >/dev/null
echo "OK: 全局排队(QUEUED+202 / stats.queued / 取消排队 CANCELLED)"

# 12c. 保留策略清理:dry_run 只统计不删除,响应形状固定(含估算字节数)。
curl -sf -X POST "$BASE/api/maintenance/cleanup" -H 'Content-Type: application/json' -d '{"dry_run":true}' \
	| ${PY} -c 'import json,sys; d=json.load(sys.stdin); assert set(d)=={"events","artifacts","nodes","executions","estimated_bytes"}, d' \
	|| fail "cleanup dry_run 形状"
echo "OK: 保留策略清理(dry_run 统计+估算字节)"

# 12d. 安全边界负路径(正路径已由前面所有请求覆盖):
#      跨源 Origin → 403 FORBIDDEN_ORIGIN;写方法带非 JSON body → 415;
#      token 模式起第二个实例:无 token → 401,带 token → 200(health 豁免)。
code=$(curl -s -o /dev/null -w "%{http_code}" -H "Origin: http://evil.example" "$BASE/api/workflows")
[ "$code" = "403" ] || fail "跨源 Origin → $code, want 403"
curl -s -H "Origin: http://evil.example" "$BASE/api/workflows" | grep -q 'FORBIDDEN_ORIGIN' || fail "缺 FORBIDDEN_ORIGIN"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/settings" -H 'Content-Type: text/plain' -d '{}')
[ "$code" = "415" ] || fail "非 JSON 写请求 → $code, want 415"

TOK_PORT=$((PORT+1))
TOK_BASE="http://127.0.0.1:$TOK_PORT"
TOK_DATA=$(mktemp -d /tmp/awo-smoke-tok.XXXXXX)
./bin/agent-workflow-server-smoke --server --addr "127.0.0.1:$TOK_PORT" --data "$TOK_DATA" --auth-token smoke-secret \
	>"$TOK_DATA/server.log" 2>&1 &
TOK_PID=$!
tok_cleanup() {
	kill "$TOK_PID" 2>/dev/null || true
	for _ in $(seq 1 30); do kill -0 "$TOK_PID" 2>/dev/null || break; sleep 0.1; done
	kill -9 "$TOK_PID" 2>/dev/null || true
	rm -rf "$TOK_DATA"
}
trap 'tok_cleanup; cleanup' EXIT
for i in $(seq 1 40); do
	curl -sf "$TOK_BASE/api/health" >/dev/null 2>&1 && break
	sleep 0.25
done
code=$(curl -s -o /dev/null -w "%{http_code}" "$TOK_BASE/api/settings")
[ "$code" = "401" ] || fail "无 token 访问 → $code, want 401"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer smoke-secret" "$TOK_BASE/api/settings")
[ "$code" = "200" ] || fail "带 token 访问 → $code, want 200"
code=$(curl -s -o /dev/null -w "%{http_code}" "$TOK_BASE/api/health")
[ "$code" = "200" ] || fail "health 豁免 → $code, want 200"
tok_cleanup
trap cleanup EXIT
echo "OK: 安全边界负路径(Origin 403 / 415 / token 401+200+health 豁免)"

# 12e. 工作区租约:同目录并发的第二个启动 → 409 WORKSPACE_BUSY(票 03)。
curl -sf -X POST "$BASE/api/settings" -H 'Content-Type: application/json' -d '{"allow_script_nodes":true}' >/dev/null
mkdir -p "$DATA/wspace"
# 服务进程是原生二进制:Git Bash 的 /tmp 路径要转成它能解析的原生形式
# (Windows 用 cygpath -m;Linux 原生路径原样)。目录必须真实存在,
# 否则脚本 chdir 失败立即结束,租约秒释,第二个启动就不会撞上。
WS_DIR=$(cygpath -m "$DATA/wspace" 2>/dev/null || echo "$DATA/wspace")
WS_WF='{"id":"smoke-ws","name":"Smoke WS",
	"nodes":[{"id":"s","name":"S","type":"script","config":{"command":"sleep 2","shell":"sh","working_dir":"'$WS_DIR'"}}],
	"edges":[],"enabled":true}'
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows" -H 'Content-Type: application/json' -d "$WS_WF")
[ "$code" = "200" ] || fail "smoke-ws 种子 → $code, want 200"
WS_EXEC1=$(curl -sf -X POST "$BASE/api/workflows/smoke-ws/run" -H 'Content-Type: application/json' -d '{}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows/smoke-ws/run" -H 'Content-Type: application/json' -d '{}')
[ "$code" = "409" ] || fail "同目录并发 → $code, want 409"
curl -s -X POST "$BASE/api/workflows/smoke-ws/run" -H 'Content-Type: application/json' -d '{}' | grep -q 'WORKSPACE_BUSY' || fail "缺 WORKSPACE_BUSY"
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$WS_EXEC1" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
echo "OK: 工作区租约(同目录 409 WORKSPACE_BUSY)"

# 12f. 失败分支端到端(票 05-07):节点失败 → on_failure 边接管 → 执行
#      完成(COMPLETED)且 via_failure_branch 记录路径。
FB_WF='{"id":"smoke-fb","name":"Smoke FB",
	"nodes":[{"id":"bad","name":"B","type":"script","config":{"command":"exit 1","shell":"sh"}},
	         {"id":"recover","name":"R","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],
	"edges":[{"from":"bad","to":"recover","on_failure":true}],"enabled":true}'
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/workflows" -H 'Content-Type: application/json' -d "$FB_WF")
[ "$code" = "200" ] || fail "smoke-fb 种子 → $code, want 200"
FB_EXEC=$(curl -sf -X POST "$BASE/api/workflows/smoke-fb/run" -H 'Content-Type: application/json' -d '{}' | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["id"])')
state=""
for i in $(seq 1 60); do
	state=$(curl -sf "$BASE/api/executions/$FB_EXEC" | ${PY} -c 'import json,sys; print(json.load(sys.stdin)["state"])')
	case "$state" in COMPLETED|FAILED|CANCELLED) break;; esac
	sleep 0.2
done
[ "$state" = "COMPLETED" ] || fail "失败分支执行 state=$state(应经失败分支完成)"
curl -sf "$BASE/api/executions/$FB_EXEC" | ${PY} -c 'import json,sys; d=json.load(sys.stdin); assert d.get("via_failure_branch")==["bad"], d.get("via_failure_branch")' \
	|| fail "via_failure_branch 未记录失败路径"
curl -sf -X POST "$BASE/api/settings" -H 'Content-Type: application/json' -d '{"allow_script_nodes":false}' >/dev/null
echo "OK: 失败分支端到端(经 on_failure 完成 + via_failure_branch)"

echo "✅ SMOKE OK(16 项全部通过)"
