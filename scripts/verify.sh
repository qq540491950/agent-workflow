#!/bin/bash
# 一键完整验证门禁:vet + 全量 race 测试 + 前端构建/单测 + 双冒烟
# 用法: ./scripts/verify.sh   (任一步失败即非零退出)
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "== 1/6 gofmt =="
UNFORMATTED=$(gofmt -l workflow/ agent/ skill/ persistence/ event/ template/ \
	permission/ git/ contextx/ logx/ api/ app/ main.go 2>/dev/null)
[ -z "$UNFORMATTED" ] || { echo "未格式化: $UNFORMATTED"; exit 1; }
echo "OK"

echo "== 2/6 go vet =="
go vet ./workflow/... ./agent/... ./skill/... ./persistence/... ./event/... \
	./template/... ./permission/... ./git/... ./contextx/... ./logx/... ./api/... ./app/... .
echo "OK"

echo "== 3/6 go test -race =="
go test -count=1 -race ./...
echo "OK"

echo "== 4/6 前端构建 + vitest =="
(cd frontend && npm run build >/dev/null && npx vitest run)
echo "OK"

echo "== 5/6 冒烟:服务器模式 REST+SSE+HITL =="
./scripts/smoke.sh

echo "== 6/6 冒烟:SSE 实时流 =="
./scripts/smoke_sse.sh

echo ""
echo "✅ VERIFY 全部通过"
