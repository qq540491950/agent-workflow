// Command bench 是运行时加固的性能基准脚本(ticket 16)。
//
// 构造 1 万执行 / 100 万事件的数据集,测量执行列表分页(摘要 DTO + 游标)、
// 执行详情、事件 tail / after_seq 分页与事件写入的 P50/P95/P99,输出
// Markdown / JSON 报告(含数据集规模、机器信息、阈值与逐项通过与否)。
//
// **不进日常门禁** —— 延迟对机器与磁盘敏感,阈值只是"明显回归"的护栏:
// 只有显式传 --check 才按阈值裁决并返回非零;默认只出报告。
//
// 测量全部走仓储层真实路径(persistence.DB 的摘要 DTO、游标分页、
// 只读连接池、SaveEvent 写路径),不绕过、不伪造。种子数据是 mock
// (无 Agent / CLI),按批次事务灌入(种子不是测量对象)。
//
// 用法:
//
//	go run ./tools/bench                          # 默认规模,Markdown 报告
//	go run ./tools/bench --check --format json --out bench.json
//	go run ./tools/bench --executions 1000 --events-per 10   # 快速冒烟
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"agentworkflow/event"
	"agentworkflow/persistence"

	_ "modernc.org/sqlite"
)

// metric 是一项测量的完整结果(时长序列的分位数 + 阈值裁决)。
type metric struct {
	Name      string  `json:"name"`
	Desc      string  `json:"desc"`
	Iter      int     `json:"iterations"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
	MaxMs     float64 `json:"max_ms"`
	ThreshP95 float64 `json:"threshold_p95_ms"`
	Pass      bool    `json:"pass"`
}

// report 是输出到 JSON / Markdown 的完整报告。
type report struct {
	GeneratedAt string      `json:"generated_at"`
	Dataset     datasetInfo `json:"dataset"`
	Machine     machineInfo `json:"machine"`
	Iterations  int         `json:"iterations"`
	SeedMs      float64     `json:"seed_ms"`
	Metrics     []metric    `json:"metrics"`
	AllPass     bool        `json:"all_pass"`
}

type datasetInfo struct {
	Executions int `json:"executions"`
	Events     int `json:"events"`
	EventsPer  int `json:"events_per_execution"`
}

type machineInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	CPUs int    `json:"cpus"`
	Go   string `json:"go"`
}

// thresholds 是"明显回归"护栏:P95 超过即视为不通过。
// 宽松是有意的 —— 本脚本不进日常门禁,阈值只抓量级回归
// (索引丢了、全表扫描回来了),不追毫秒级抖动。
var thresholds = map[string]float64{
	"list_summaries_first_page": 50,
	"list_summaries_mid_cursor": 50,
	"get_execution":             20,
	"events_tail_page":          50,
	"events_after_seq":          50,
	"save_event":                10,
}

func main() {
	var (
		executions = flag.Int("executions", 10000, "种子执行数")
		eventsPer  = flag.Int("events-per", 100, "每个执行的事件数(总事件 = executions × events-per)")
		iterations = flag.Int("iterations", 200, "每项测量的迭代次数")
		dataDir    = flag.String("data-dir", "", "数据目录(缺省 = 临时目录,退出自动清理)")
		keepData   = flag.Bool("keep", false, "保留数据目录(默认自动清理)")
		format     = flag.String("format", "md", "报告格式:md | json")
		out        = flag.String("out", "", "报告写入文件(缺省打印到 stdout)")
		check      = flag.Bool("check", false, "回归模式:任一 P95 超阈值返回非零")
	)
	flag.Parse()

	if *executions <= 0 || *eventsPer < 0 || *iterations <= 0 {
		fmt.Fprintln(os.Stderr, "参数非法:executions/iterations 必须为正,events-per 不得为负")
		os.Exit(2)
	}

	dir := *dataDir
	autoClean := false
	if dir == "" {
		tmp, err := os.MkdirTemp("", "awo-bench-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "创建临时目录:", err)
			os.Exit(2)
		}
		dir = tmp
		autoClean = !*keepData
	}
	if autoClean {
		defer os.RemoveAll(dir)
	}

	dbPath := filepath.Join(dir, "bench.db")
	// 先开持久层把 schema 迁移好;种子走 raw 批量事务(快),测量走
	// persistence.DB 的真实读写路径。
	db, err := persistence.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开数据库:", err)
		os.Exit(2)
	}
	defer db.Close()

	t0 := time.Now()
	if err := seed(dbPath, *executions, *eventsPer); err != nil {
		fmt.Fprintln(os.Stderr, "灌入种子数据:", err)
		os.Exit(2)
	}
	seedMs := float64(time.Since(t0).Microseconds()) / 1000

	metrics, err := measure(db, *executions, *iterations)
	if err != nil {
		fmt.Fprintln(os.Stderr, "测量失败:", err)
		os.Exit(2)
	}

	rep := report{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Dataset:     datasetInfo{Executions: *executions, Events: *executions * *eventsPer, EventsPer: *eventsPer},
		Machine:     machineInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Go: runtime.Version()},
		Iterations:  *iterations,
		SeedMs:      round2(seedMs),
		Metrics:     metrics,
		AllPass:     true,
	}
	for _, m := range metrics {
		if !m.Pass {
			rep.AllPass = false
		}
	}

	text, err := render(rep, *format)
	if err != nil {
		fmt.Fprintln(os.Stderr, "渲染报告:", err)
		os.Exit(2)
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写报告:", err)
			os.Exit(2)
		}
		fmt.Println("报告已写入", *out)
	} else {
		fmt.Print(text)
	}

	if *check && !rep.AllPass {
		fmt.Fprintln(os.Stderr, "回归裁决:有指标 P95 超阈值")
		os.Exit(1)
	}
}

// seed 用批量事务灌入种子数据。
//
// 种子不是测量对象,走 raw SQL 批量插入(逐条 SaveEvent 灌 100 万条要
// 几十分钟,基准要可重复就得让准备阶段可承受);**测量**仍走仓储层。
func seed(dbPath string, executions, eventsPer int) error {
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return err
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)

	execStmt := `INSERT INTO executions
		(id, workflow_id, workflow_version, workflow_name, state, task, variables_json,
		 current_node_id, iterations_json, created_at, started_at, finished_at, error,
		 node_states_json, snapshot_json, state_json, workspace_json, via_failure_branch_json,
		 queued_at, usage_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	evStmt := `INSERT INTO events (execution_id, node_id, type, data_json, created_at) VALUES (?,?,?,?,?)`

	// 每个事务灌一个执行 + 它的全部事件:事务太大 WAL 膨胀,太小提交开销大。
	base := time.Now().Add(-72 * time.Hour)
	for i := 0; i < executions; i++ {
		tx, err := raw.Begin()
		if err != nil {
			return err
		}
		id := fmt.Sprintf("bench-exec-%06d", i)
		created := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		_, err = tx.Exec(execStmt,
			id, "bench-wf", 1, "基准工作流", "COMPLETED", "bench task", "{}",
			"", "{}", created, created, created, "",
			"{}", "", "{}", "[]", "[]", "", "{}")
		if err != nil {
			tx.Rollback()
			return err
		}
		for j := 0; j < eventsPer; j++ {
			ts := base.Add(time.Duration(i)*time.Second + time.Duration(j)*time.Millisecond).Format(time.RFC3339Nano)
			if _, err := tx.Exec(evStmt, id, "n1", "node.started", `{"k":"v"}`, ts); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// measure 在仓储层真实路径上采集时长序列并算分位数。
func measure(db *persistence.DB, executions, iterations int) ([]metric, error) {
	// 深游标:先翻 100 页拿到"中部"游标,反复测那一页(浅深各测一次)。
	midCursor := ""
	c := ""
	for i := 0; i < 100; i++ {
		_, next, err := db.ListExecutionSummaries("", 50, c)
		if err != nil {
			return nil, fmt.Errorf("预走游标: %w", err)
		}
		if next == "" {
			break
		}
		c = next
		midCursor = next
	}

	// 事件 tail / after_seq 用第 0 号执行(事件 seq 连续可预期)。
	sampleExec := "bench-exec-000000"

	var metrics []metric
	add := func(name, desc string, fn func() error) error {
		ds := make([]float64, 0, iterations)
		for i := 0; i < iterations; i++ {
			t := time.Now()
			if err := fn(); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			ds = append(ds, float64(time.Since(t).Microseconds())/1000)
		}
		m := summarize(name, desc, ds)
		metrics = append(metrics, m)
		return nil
	}

	if err := add("list_summaries_first_page", "执行列表首页(摘要 DTO,50 条)", func() error {
		_, _, err := db.ListExecutionSummaries("", 50, "")
		return err
	}); err != nil {
		return nil, err
	}
	if err := add("list_summaries_mid_cursor", "执行列表中部游标页(50 条)", func() error {
		_, _, err := db.ListExecutionSummaries("", 50, midCursor)
		return err
	}); err != nil {
		return nil, err
	}
	if err := add("get_execution", "执行详情", func() error {
		_, err := db.GetExecution(sampleExec)
		return err
	}); err != nil {
		return nil, err
	}
	if err := add("events_tail_page", "事件最近页(无游标,500 条)", func() error {
		_, _, _, err := db.ListEventsPage(sampleExec, 0, 0, 500)
		return err
	}); err != nil {
		return nil, err
	}
	if err := add("events_after_seq", "事件 after_seq 增量页(500 条)", func() error {
		_, _, _, err := db.ListEventsPage(sampleExec, 100, 0, 500)
		return err
	}); err != nil {
		return nil, err
	}
	if err := add("save_event", "事件写入(单条)", func() error {
		return db.SaveEvent(event.UIEvent{
			Type: "node.completed", ExecutionID: sampleExec, NodeID: "n1",
			Timestamp: time.Now(), Data: map[string]any{"k": "v"},
		})
	}); err != nil {
		return nil, err
	}
	return metrics, nil
}

// summarize 把时长序列收敛成 P50/P95/P99 + 阈值裁决。
func summarize(name, desc string, ds []float64) metric {
	sort.Float64s(ds)
	th := thresholds[name]
	m := metric{
		Name: name, Desc: desc, Iter: len(ds),
		P50Ms: round2(pct(ds, 50)), P95Ms: round2(pct(ds, 95)),
		P99Ms: round2(pct(ds, 99)), MaxMs: round2(ds[len(ds)-1]),
		ThreshP95: th,
	}
	m.Pass = th <= 0 || m.P95Ms <= th
	return m
}

// pct 取排序后序列的 p 分位数(最近秩法)。
func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// render 输出 Markdown 或 JSON 报告。
func render(rep report, format string) (string, error) {
	if format == "json" {
		b, err := json.MarshalIndent(rep, "", "  ")
		return string(b) + "\n", err
	}
	if format != "md" {
		return "", fmt.Errorf("未知格式 %q(只支持 md / json)", format)
	}
	out := fmt.Sprintf("# 性能基准报告\n\n"+
		"- 生成时间:%s\n"+
		"- 数据集:%d 执行 / %d 事件(每执行 %d 条)\n"+
		"- 机器:%s/%s,%d CPU,%s\n"+
		"- 迭代:%d 次/项;种子耗时 %.1f s\n"+
		"- 总体:%s\n\n"+
		"| 指标 | 说明 | P50(ms) | P95(ms) | P99(ms) | Max(ms) | 阈值 P95(ms) | 通过 |\n"+
		"| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |\n",
		rep.GeneratedAt, rep.Dataset.Executions, rep.Dataset.Events, rep.Dataset.EventsPer,
		rep.Machine.OS, rep.Machine.Arch, rep.Machine.CPUs, rep.Machine.Go,
		rep.Iterations, rep.SeedMs/1000,
		map[bool]string{true: "✅ 全部通过", false: "❌ 有指标超阈值"}[rep.AllPass])
	for _, m := range rep.Metrics {
		out += fmt.Sprintf("| %s | %s | %.2f | %.2f | %.2f | %.2f | %.1f | %s |\n",
			m.Name, m.Desc, m.P50Ms, m.P95Ms, m.P99Ms, m.MaxMs, m.ThreshP95,
			map[bool]string{true: "✅", false: "❌"}[m.Pass])
	}
	out += "\n> 本基准不进日常门禁(机器差异会造成假红);`--check` 显式按阈值裁决。\n"
	return out, nil
}
