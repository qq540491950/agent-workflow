package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	app "agentworkflow/app/application"
	"agentworkflow/event"
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
func TestSSESlowClientDoesNotBlockProducer(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	time.Sleep(200 * time.Millisecond) // 等订阅建立

	// 事件量远超队列容量:写协程会被 socket 背压卡住,队列随之填满
	const n = 2000
	big := strings.Repeat("x", 128)
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for i := 0; i < n; i++ {
			a.Bus.Emit(event.New(event.NodeStarted, "exec-slow", "n1", map[string]any{"i": i, "pad": big}))
		}
		done <- time.Since(start)
	}()

	// 真正要守的不变式:**Emit 必须非阻塞返回**(慢客户端不能反压到
	// 产生事件的工作流协程)。用"能否在合理时间内返回"判定,而非
	// 精确耗时 —— 2000 个事件每次都要过一次脱敏,耗时随机器负载波动:
	// 单独跑约 0.55s,而全量并发(CPU 被其它包抢占)时会到 2.3s。
	// 原先 2s 的阈值在并发下偶发失败(EXIT=1 却无法复现单跑),是
	// 脆弱的性能断言而非回归。阈值放宽到 5s:既能稳定容纳负载抖动,
	// 又与"被真正阻塞"有量级差异(阻塞版会一直挂到 10s 超时分支)。
	const maxEmitDuration = 5 * time.Second
	select {
	case elapsed := <-done:
		if elapsed > maxEmitDuration {
			t.Errorf("Emit 被慢客户端阻塞 %v(应非阻塞投递,阈值 %v)", elapsed, maxEmitDuration)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Emit 长时间未返回:订阅回调仍在同步写客户端")
	}
}
