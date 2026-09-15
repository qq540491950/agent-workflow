#!/bin/bash
# 端到端冒烟测试:启动 server 模式 → 种子检查 → 校验 → 运行闭环 → 循环保护 → HITL 恢复 → 导出
# 用法: ./scripts/smoke.sh
set -e
PORT=${SMOKE_PORT:-18181}
BASE="http://127.0.0.1:$PORT"
DATA=$(mktemp -d /tmp/awo-smoke.XXXXXX)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$ROOT"
go build -tags server -o bin/agent-workflow-server-smoke . 2>/dev/null || go build -tags server -o bin/agent-workflow-server-smoke .
./bin/agent-workflow-server-smoke --server --addr "127.0.0.1:$PORT" --data "$DATA" >/dev/null 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null; rm -rf "$DATA"' EXIT
sleep 2

fail() { echo "❌ SMOKE FAIL: $1"; exit 1; }

# 1. 健康检查
curl -sf "$BASE/api/health" | grep -q '"ok"' || fail "health"

# 2. 示例种子
count=$(curl -sf "$BASE/api/workflows" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$count" -ge 2 ] || fail "seeded workflows = $count"

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

echo "✅ SMOKE OK(7 项全部通过)"
