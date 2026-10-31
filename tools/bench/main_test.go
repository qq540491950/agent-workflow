package main

import "testing"

// pct 的最近秩语义:分位数必须落在真实观测值上(不插值),
// 边界(空序列/0%/100%)不得越界或 panic。
func TestPctNearestRank(t *testing.T) {
	ds := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := map[float64]float64{50: 5, 95: 10, 99: 10, 100: 10, 10: 1}
	for p, want := range cases {
		if got := pct(ds, p); got != want {
			t.Errorf("pct(ds, %v) = %v, want %v", p, got, want)
		}
	}
	if got := pct(nil, 50); got != 0 {
		t.Errorf("pct(nil, 50) = %v, want 0", got)
	}
}

// summarize 的阈值裁决:P95 在阈值内 → pass;超阈值 → fail;
// 阈值为 0(未登记的指标)恒 pass —— 不把未定义阈值当回归。
func TestSummarizeThresholdVerdict(t *testing.T) {
	thresholds["test-fast"] = 5
	thresholds["test-slow"] = 5
	defer delete(thresholds, "test-fast")
	defer delete(thresholds, "test-slow")

	fast := summarize("test-fast", "", []float64{1, 2, 3, 4, 5, 1, 2, 3, 4, 5})
	if !fast.Pass {
		t.Errorf("P95=%.2f ≤ 5 应 pass, got fail", fast.P95Ms)
	}
	slow := summarize("test-slow", "", []float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 20})
	if slow.Pass {
		t.Errorf("P95=%.2f > 5 应 fail, got pass", slow.P95Ms)
	}
	unknown := summarize("test-undefined", "", []float64{100})
	if !unknown.Pass {
		t.Error("未登记阈值应恒 pass")
	}
}
