#!/bin/bash
# 文档编号自检 —— 检查 docs/OPTIMIZATION.md 与 docs/TESTING.md 里的缺陷编号:
#   1. **无缺号**:从最小号到最大号之间**不能断档**(断档 = 有号被漏登记,
#      或者某条被删掉了但号还留在别处)。
#   2. **无重号**:同一个缺陷号不能在两处被**登记**(两处都声称"这是第 N 号缺陷")。
#   3. **交叉引用可解析**:正文里出现的每一个 `#N` 引用,都必须落在**已登记**
#      的号段里(不是随手写的号)。
#
# 用法: bash docs/check-numbering.sh
# 退出码: 0 = 全部通过;1 = 有问题(逐条打印)。
#
# 为什么需要它:本项目的缺陷号是**跨文档、跨轮次**的唯一标识,正文里到处
# 交叉引用(#245 / #251 / #258 …)。人工维护这套编号必然会漏 —— 而"漏登记"
# 与"重号"都会让后来的维护者按错误的前提追溯历史。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

OPT="docs/OPTIMIZATION.md"
TST="docs/TESTING.md"

for f in "$OPT" "$TST"; do
	if [ ! -f "$f" ]; then
		echo "❌ 缺少文档:$f"
		exit 1
	fi
done

# ---- 已知历史空号 ------------------------------------------------------
# 格式:"<号>:<为什么空着>"。空号**保留、不追认** —— 追认等于给一条
# 不存在的缺陷编故事,比留个空号更糟。
KNOWN_GAPS="25:第十五轮(轮次 25)的编号从 #24 直接跳到 #26,当年漏登记,原始记录里没有对应条目"

TMP=$(mktemp -d /tmp/awo-numbering.XXXXXX)
trap 'rm -rf "$TMP"' EXIT

# ---- 抽出所有 "#N" 与 "#N-M" 形态 --------------------------------------
# 范围形态("#95-#122" / "#142-#188")必须展开成单个号,否则会漏掉中间的号。
grep -oE '#[0-9]+(-#?[0-9]+)?' "$OPT" | sed 's/^#//' | tr -d '#' >"$TMP/opt.raw"
grep -oE '#[0-9]+(-#?[0-9]+)?' "$TST" | sed 's/^#//' | tr -d '#' >"$TMP/tst.raw"
cat "$TMP/opt.raw" "$TMP/tst.raw" >"$TMP/all.raw"

# 展开范围 → 每个号一行
expand() { # expand < raw 文件
	awk -F- '
	{
		if (NF == 2 && $1 != "" && $2 != "") {
			a = $1 + 0; b = $2 + 0
			if (a <= b) { for (i = a; i <= b; i++) print i }
			else { for (i = b; i <= a; i++) print i }
		} else if ($1 != "") {
			print $1 + 0
		}
	}'
}
expand <"$TMP/all.raw" | sort -n -u >"$TMP/all.nums"
MIN=$(head -1 "$TMP/all.nums")
MAX=$(tail -1 "$TMP/all.nums")
COUNT=$(wc -l <"$TMP/all.nums" | tr -d ' ')

echo "缺陷号区间:$MIN .. $MAX(共 $COUNT 个号被登记或引用)"
echo ""

RC=0

# ---- 1. 无缺号 --------------------------------------------------------
# 已知历史空号从"缺号"里剔除,但**必须显式报出来**(不能悄悄放过)。
KNOWN_NUMS=$(printf '%s\n' "$KNOWN_GAPS" | cut -d: -f1 | grep -E '^[0-9]+$' || true)
# 已知历史空号**始终显式报出**(哪怕它现在被文档提到了)—— 让人知道
# 这个号是"故意空着",而不是"碰巧没人提"。
printf '%s\n' "$KNOWN_GAPS" | while IFS= read -r kg; do
	[ -n "$kg" ] || continue
	echo "⚠️  已知历史空号:#${kg%%:*}(保留,不追认)—— ${kg#*:}"
done
GAPS=$(awk '
	NR == 1 { prev = $1; next }
	{ if ($1 != prev + 1) { for (i = prev + 1; i < $1; i++) print i } prev = $1 }
' "$TMP/all.nums")
NEW_GAPS=""
for g in $GAPS; do
	if printf '%s\n' "$KNOWN_NUMS" | grep -qx "$g"; then
		: # 已知空号:已在上方报出,不算"缺号"
	else
		NEW_GAPS="$NEW_GAPS $g"
	fi
done
if [ -n "$NEW_GAPS" ]; then
	echo "❌ 缺号(区间内没有任何文档登记或引用过):$NEW_GAPS"
	RC=1
else
	echo "✅ 无缺号:$MIN .. $MAX 连续(已知历史空号已单列,不算缺号)"
fi

# ---- 2. 无重号(登记位唯一) --------------------------------------------
# 登记位 = OPTIMIZATION.md 表格行的**首列**编号(那里是"缺陷登记表")。
grep -oE '^\| #[0-9]+ \|' "$OPT" | grep -oE '[0-9]+' | sort -n >"$TMP/defs.nums"
DUP=$(uniq -d "$TMP/defs.nums")
if [ -n "$DUP" ]; then
	echo "❌ 重号(同一个缺陷号在登记表里出现了两次):"
	echo "$DUP" | tr '\n' ' ' | fold -w 96 | sed 's/^/   /'
	echo ""
	RC=1
else
	echo "✅ 无重号:登记表里 $(wc -l <"$TMP/defs.nums" | tr -d ' ') 个编号各出现一次"
fi

# ---- 3. 交叉引用可解析 --------------------------------------------------
# "已登记"= ①OPT 表格首列 ②TESTING.md 的条目式登记("- **#N …" 与
# "### #N …" 小节标题)与汇总表 ③文档里出现过的**号段**(如 "#142-#188 共 47 条")。
#
# **注意 `#N` 在本项目有三种语义**,本脚本把它们都纳入统计,但"已登记"的
# 判定方式不同:
#   1. **缺陷号** —— 本脚本的主对象(#1 起,#263 止);
#   2. **早期轮次的 "Bug #N" 标签**(轮次 1-22 的老写法,如 "Bug #6");
#   3. **AGENTS.md 的"约定 #N"**(`#1`-`#15`,是**硬性约定**的编号,
#      不是缺陷号)—— 约定清单在 `AGENTS.md`,因此 1-15 视为已登记。
{
	cat "$TMP/defs.nums"
	# ② 条目式登记:TESTING.md 的 "- **#N …" 与 "### #N …"
	grep -oE '^- \*\*#[0-9]+' "$TST" | grep -oE '[0-9]+' || true
	grep -oE '^#+ #[0-9]+' "$TST" | grep -oE '[0-9]+' || true
	# 早期轮次的 "Bug #N" 标签
	grep -oE '\*\*Bug #[0-9]+' "$TST" | grep -oE '[0-9]+' || true
	# 汇总表里的逐条登记
	sed -n '/^## 汇总/,$p' "$TST" | grep -oE '#[0-9]+' | tr -d '#' || true
	# ③ 号段
	awk -F- 'NF==2 && $1 != "" && $2 != "" { a=$1+0; b=$2+0; if (a<=b) { for (i=a;i<=b;i++) print i } else { for (i=b;i<=a;i++) print i } }' "$TMP/all.raw"
	# AGENTS.md 的硬性约定编号(#1-#15):约定清单在 AGENTS.md,视为已登记
	for i in $(seq 1 15); do echo "$i"; done
	# 已知历史空号:它们**被文档显式登记为"空号"**,视为已登记(否则文档里
	# 一旦提到这个号,就会被判成"只被引用、从未被登记")
	printf '%s\n' "$KNOWN_NUMS"
} | sort -n -u >"$TMP/registered.nums"

comm -23 "$TMP/all.nums" "$TMP/registered.nums" >"$TMP/unresolved.nums"
if [ -s "$TMP/unresolved.nums" ]; then
	echo "❌ 交叉引用不可解析(这些号只被引用、从未被登记):"
	tr '\n' ' ' <"$TMP/unresolved.nums" | fold -w 96 | sed 's/^/   /'
	echo ""
	RC=1
else
	echo "✅ 交叉引用可解析:$(wc -l <"$TMP/all.nums" | tr -d ' ') 个被引用的号全部落在已登记的号段内"
fi

if [ "$RC" -eq 0 ]; then
	echo ""
	echo "✅ 编号自检全部通过"
else
	echo ""
	echo "❌ 编号自检未通过"
fi
exit "$RC"
