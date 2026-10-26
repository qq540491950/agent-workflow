package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	app "agentworkflow/app/application"
	"agentworkflow/event"
	"agentworkflow/logx"
)

// SSE 实时流:订阅建立后,总线事件必须以 data: JSON 帧行推送到客户端。
func TestSSEStream(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	// 5s 兜底超时,防止流挂死拖垮测试
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 连接建立前先安排事件:handler 立即下发响应头并完成 Subscribe,
	// 300ms 后触发的事件必须在客户端收到
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.Bus.Emit(event.New(event.NodeStarted, "exec-sse", "n1", map[string]any{"k": "v"}))
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(4 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("did not receive event within deadline")
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev event.UIEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("bad event JSON: %v (%q)", err, line)
		}
		if ev.Type == event.NodeStarted && ev.ExecutionID == "exec-sse" {
			return // 收到目标事件
		}
	}
}

// 回归:SSE 订阅回调由总线在产生事件的工作流协程里同步调用,直接写
// ResponseWriter 时,慢客户端的 TCP 背压会反压到执行引擎(一个不读数据的
// 连接即可卡住工作流)。现为每连接带缓冲队列 + 独立写协程,投递非阻塞。
//
// 判据:**Emit 必须返回**(等一个明确的完成信号),**不是**"耗时多少"。
//
// 此前这条用例断言的是"Emit 在 5s 内返回"—— 那是一条**性能断言**,
// 耗时随机器负载波动(单独跑约 0.55s,3 路并行下实测 5.409s > 5s 阈值)
// → 并行负载下**误红**,污染门禁判定,而它压根没测到要测的东西:
// "Emit 返回了"与"Emit 卡住了"是**定性差异**,用秒数当阈值把它变成了
// 定量断言。下面 30s 的超时只是**死锁兜底**(区分"返回"与"永远卡住"),
// 不是性能阈值。
func TestSSESlowClientDoesNotBlockProducer(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// 故意不读响应体:模拟挂死/极慢的客户端
	t.Cleanup(func() { resp.Body.Close() })
	// 同步点:等订阅**真的**建立(轮询真实条件,而不是睡一个固定时长)
	waitForSubscriber(t, a)

	// 事件量远超队列容量:写协程会被 socket 背压卡住,队列随之填满
	const n = 2000
	big := strings.Repeat("x", 128)
	emitted := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			a.Bus.Emit(event.New(event.NodeStarted, "exec-slow", "n1", map[string]any{"i": i, "pad": big}))
		}
		close(emitted)
	}()

	// 真正要守的不变式:**Emit 必须非阻塞返回**(慢客户端不能反压到
	// 产生事件的工作流协程)。判据是"Emit 已返回"这个信号。
	select {
	case <-emitted:
		// 不变式成立。
	case <-time.After(30 * time.Second):
		t.Fatal("Emit 长时间未返回:订阅回调仍在同步写客户端")
	}
}

// blockedWriter 是一个"写必阻塞"的 ResponseWriter:挂死客户端的等价物
// (写端永远等不到对端读 = TCP 背压)。
//
// 第 1 次写(建连注释行 ": connected")**放行**:否则 handler 连订阅都
// 建立不了,测到的就不是"慢客户端"而是"连不上的客户端"。
// 第 2 次起(第一帧事件数据)阻塞,直到 release 被关闭。
type blockedWriter struct {
	hdr http.Header

	mu     sync.Mutex
	writes int

	entered chan struct{} // 第 2 次写进入时关闭(同步点:写协程已卡住)
	release chan struct{} // 关闭后放行后续所有写
	once    sync.Once
}

func (w *blockedWriter) Header() http.Header { return w.hdr }
func (w *blockedWriter) WriteHeader(int)     {}
func (w *blockedWriter) Flush()              {}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	n := w.writes
	w.mu.Unlock()
	if n >= 2 {
		w.once.Do(func() { close(w.entered) })
		<-w.release
	}
	return len(p), nil
}

// safeBuffer 是并发安全的日志缓冲(日志来自多个 goroutine)。
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForSubscriber 等订阅**真的**建立(轮询真实条件,不等固定时长)。
func waitForSubscriber(t *testing.T, a *app.App) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for a.EventSubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.EventSubscriberCount() == 0 {
		t.Fatal("SSE 订阅未建立")
	}
}

// 回归(确定性版):慢客户端**把队列灌满**时,Emit 仍然必须返回。
//
// 与上面的"真实 socket"版互补:那一条的背压取决于操作系统 socket 缓冲
// (2000 个事件通常能填满,但不是保证);本条用 blockedWriter 把
// "客户端卡住"变成**确定的事实**,并用两个同步点替代任何计时:
//
//	同步点 1:写协程已经卡在 Write 里(等价于 TCP 背压)——
//	         等 blockedWriter.entered,不是等一个时长;
//	同步点 2:生产者那一轮 Emit 已经全部返回 ——
//	         等 emitted 被关闭,不是"Emit 用了多久"。
//
// 两条同步点之间的超时只是**死锁兜底**(区分"返回了"与"永远卡住"),
// 不是性能阈值。
func TestSSESlowClientQueueOverflowDoesNotBlockProducer(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)

	bw := &blockedWriter{
		hdr:     http.Header{},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	handlerDone := make(chan struct{})
	go func() {
		s.handleSSE(bw, req)
		close(handlerDone)
	}()

	// 同步点 1a:订阅建立
	waitForSubscriber(t, a)

	// 同步点 1b:写协程卡在 Write 里(客户端已挂死)。
	// 用一个事件把它推进阻塞写 —— 不等固定时长。
	//
	// **触发事件必须在独立 goroutine 里发**:如果订阅回调回到"同步写客户端"
	// 的历史形态(本次造坏验证复现的就是它),这次 Emit 本身就会阻塞 ——
	// 放在测试 goroutine 里会把整个用例挂死,只能靠整包超时收场(拿不到
	// 可读的失败原因)。放到 goroutine 里之后,阻塞会被下面的 30s 死锁
	// 兜底接住,失败信息能直接指出"Emit 被慢客户端阻塞"。
	triggerReturned := make(chan struct{})
	go func() {
		a.Bus.Emit(event.New(event.NodeStarted, "exec-slow", "n1", map[string]any{"i": 0}))
		close(triggerReturned)
	}()
	select {
	case <-bw.entered:
		// 写协程已卡在 Write 里(慢客户端成立)。
	case <-time.After(10 * time.Second):
		t.Fatal("写协程没有进入 Write(慢客户端场景没有建立起来)")
	}
	// 不变式本身:那次触发事件的 Emit 必须**已经返回**。
	//
	// 顺序是确定的:非阻塞投递时,Emit 先把事件塞进队列就返回,写协程
	// **之后**才取走事件并卡在 Write 里 → 所以到了这一步,Emit 一定
	// 已经返回过。若订阅回调回到"同步写客户端"的历史形态,这次 Emit
	// 会一直卡在写里 → 10s 后给出可读的失败原因(而不是整包超时)。
	select {
	case <-triggerReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("Emit 被慢客户端阻塞(触发事件的那次 Emit 没有返回)")
	}

	// 队列容量是 256:再发足够多的事件把队列灌满 → 走"丢弃"分支。
	// 少了这一步,测到的只是"队列没满时的非阻塞投递",与慢客户端无关。
	logs := &safeBuffer{}
	restoreLog := logx.SetOutput(logs)
	defer restoreLog()

	const n = 1000
	emitted := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			a.Bus.Emit(event.New(event.NodeStarted, "exec-slow", "n1", map[string]any{"i": i}))
		}
		close(emitted)
	}()

	// 同步点 2:等"Emit 已返回"这个信号
	select {
	case <-emitted:
	case <-time.After(30 * time.Second):
		t.Fatal("Emit 长时间未返回:订阅回调仍在同步写客户端")
	}

	// 队列确实被灌满了(丢弃分支被走到)—— 否则本用例没有覆盖背压场景。
	if !strings.Contains(logs.String(), "SSE 客户端消费过慢") {
		t.Errorf("慢客户端没有触发队列丢弃分支(队列没满 = 没测到背压): %s", logs.String())
	}

	// 收尾:放行写协程 → 结束流 → 等 handler 退出(不留 goroutine 泄漏)
	close(bw.release)
	cancel()
	select {
	case <-handlerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("SSE handler 未退出")
	}
}

// failingWriter 是一个"写必失败"的 ResponseWriter(模拟客户端断开/TCP 错误)。
// failAfter 之前的写入正常,之后全部失败。
type failingWriter struct {
	hdr       http.Header
	writes    int
	failAfter int
}

func (f *failingWriter) Header() http.Header { return f.hdr }
func (f *failingWriter) WriteHeader(int)     {}
func (f *failingWriter) Flush()              {}
func (f *failingWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > f.failAfter {
		return 0, errors.New("connection reset by peer")
	}
	return len(p), nil
}

// 回归:写失败必须关闭 SSE 流。
// 此前写失败只记一条 Debug 日志就继续循环 —— 连接既不释放(订阅不注销、
// goroutine 常驻、每 15s 继续写),客户端也永远等不到流结束。
// 写失败意味着对端已不可达,继续写毫无意义,必须结束流并释放资源。
func TestSSEWriteFailureClosesStream(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)

	// 第 1 次写(建连注释行)成功,之后全部失败
	fw := &failingWriter{hdr: http.Header{}, failAfter: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.handleSSE(fw, req)
		close(done)
	}()

	// 等订阅建立后产生一个事件,触发失败写入
	time.Sleep(150 * time.Millisecond)
	a.Bus.Emit(event.New(event.NodeStarted, "exec-w", "n1", map[string]any{"k": "v"}))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("写失败后 SSE 流未关闭(连接与订阅都没释放)")
	}
}

// 回归:SSE 客户端断开后必须注销订阅(否则每次断开泄漏一个 handler,
// 事件扇出列表无限增长)。
func TestSSEDisconnectReleasesSubscription(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE 连接: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	// 等订阅建立
	deadline := time.Now().Add(3 * time.Second)
	for a.EventSubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if a.EventSubscriberCount() == 0 {
		t.Fatal("SSE 未建立订阅")
	}

	// 断开客户端
	cancel()
	_ = resp.Body.Close()

	deadline = time.Now().Add(5 * time.Second)
	for a.EventSubscriberCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := a.EventSubscriberCount(); n != 0 {
		t.Errorf("断开后订阅未释放,残留 %d 个 handler", n)
	}
}

// TestSSEStatsEndpointReflectsSubscriptions:GET /api/sse/stats 是订阅计数的
// **HTTP 可观测点** —— 冒烟脚本(smoke_sse.sh)靠它在真实进程 + 真实 TCP
// 断开下断言"断开后归零";本用例锁住端点语义(计数随订阅建立/释放变化)。
func TestSSEStatsEndpointReflectsSubscriptions(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	stats := func() int {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/sse/stats")
		if err != nil {
			t.Fatalf("GET /api/sse/stats: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stats → %d,期望 200", resp.StatusCode)
		}
		var body struct {
			Subscribers int `json:"subscribers"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("解析 stats 响应: %v", err)
		}
		return body.Subscribers
	}

	if n := stats(); n != 0 {
		t.Fatalf("初始订阅数应为 0,实际 %d", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE 连接: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	deadline := time.Now().Add(3 * time.Second)
	for stats() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := stats(); n != 1 {
		t.Fatalf("订阅建立后 stats 应为 1,实际 %d", n)
	}

	cancel()
	_ = resp.Body.Close()

	deadline = time.Now().Add(5 * time.Second)
	for stats() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := stats(); n != 0 {
		t.Errorf("断开后 stats 应归零(订阅释放),实际 %d", n)
	}
}
