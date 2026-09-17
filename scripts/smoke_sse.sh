#!/bin/bash
# SSE 端到端验证:订阅 /api/events → 触发工作流 → 事件必须出现在流上
set -e
PORT=18789
BASE="http://127.0.0.1:$PORT"
DATA=$(mktemp -d /tmp/awo-sse-e2e.XXXXXX)
cd /Users/liruitao/Desktop/go-project/agent-workflow
go build -tags server -o bin/aw-sse-e2e . 2>/dev/null
./bin/aw-sse-e2e --server --addr "127.0.0.1:$PORT" --data "$DATA" >/dev/null 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null; rm -rf "$DATA"' EXIT
for i in $(seq 1 20); do curl -sf "$BASE/api/health" >/dev/null 2>&1 && break; sleep 0.5; done
curl -sf "$BASE/api/health" >/dev/null || { echo "FAIL: server not up"; exit 1; }

# 后台订阅 SSE,写入临时文件
OUT=$(mktemp /tmp/awo-sse-out-XXXXXX)
curl -sN --max-time 14 "$BASE/api/events" > "$OUT" 2>/dev/null &
CURL_PID=$!
sleep 1

# 检查连接即达的注释行(响应头立即下发)
head -2 "$OUT" | grep -q ": connected" || { echo "FAIL: missing : connected comment"; kill $CURL_PID 2>/dev/null; exit 1; }
echo "OK: : connected 立即下发"

# keepalive:空闲连接 16s 内应收到 ": keepalive" 注释行
# 触发工作流(真实执行产生事件)
curl -sf -X POST "$BASE/api/workflows/coding-task/run" -H 'Content-Type: application/json' -d '{"task":"sse-e2e"}' >/dev/null

# 等待事件出现在流上(workflow.started 必达)
for i in $(seq 1 40); do
  if grep -q "workflow.started" "$OUT" 2>/dev/null; then
    echo "OK: workflow.started 事件经 SSE 送达"
    grep -m1 "node.started" "$OUT" >/dev/null && echo "OK: node.started 事件送达"
    kill $CURL_PID 2>/dev/null || true; wait $CURL_PID 2>/dev/null || true

    # keepalive 检查:独立空闲连接,17s 内应收到 ": keepalive"
    KA_OUT=$(mktemp /tmp/awo-sse-ka-XXXXXX)
    curl -sN --max-time 17 "$BASE/api/events" > "$KA_OUT" 2>/dev/null &
    KA_CURL=$!
    KA_DEADLINE=$((SECONDS + 17))
    KA_OK=0
    while [ $SECONDS -lt $KA_DEADLINE ]; do
      grep -q ": keepalive" "$KA_OUT" 2>/dev/null && { KA_OK=1; break; }
      sleep 1
    done
    kill $KA_CURL 2>/dev/null || true; wait $KA_CURL 2>/dev/null || true
    [ "$KA_OK" = "1" ] || { echo "FAIL: 17s 内未见 keepalive"; exit 1; }
    echo "OK: keepalive 注释行送达"
    echo "✅ SSE E2E PASS"
    exit 0
  fi
  sleep 0.5
done
echo "FAIL: 15s 内未见 workflow.started"
kill $CURL_PID 2>/dev/null
exit 1
