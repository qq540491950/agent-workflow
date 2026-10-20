package api

import (
	"bytes"
	"context"
	"math"
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

// recordingWriter 记录 SSE 写出的所有字节(测试用)。
type recordingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *recordingWriter) Header() http.Header { return http.Header{} }
func (w *recordingWriter) WriteHeader(int)     {}
func (w *recordingWriter) Flush()              {}
func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *recordingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// 回归(#233):SSE 的 json.Marshal 失败被**静默丢弃**。
//
// 此前 api/server.go 的 SSE 循环里:
//
//	raw, err := json.Marshal(ev)
//	if err != nil { continue }   // ← 这一条事件不送达、且不留任何痕迹
//
// 结果:事件流里"少了一条",但没人知道是哪条、为什么(约定 14 同族:
// 该送达却没送达 = 静默丢数据)。
//
// 修法:至少记一条 WARN(事件类型/执行 ID/原因),让"少了一条"可追溯;
// 流本身继续(一条坏事件不该掐断整条流)。
func TestSSEMarshalFailureIsVisible(t *testing.T) {
	a, err := app.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)

	// 捕获日志(约定 14:降级路径必须有可验证的告警)。
	var logs bytes.Buffer
	restoreLog := logx.SetOutput(&logs)
	t.Cleanup(restoreLog)

	rw := &recordingWriter{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.handleSSE(rw, req)
		close(done)
	}()

	// 等订阅建立。
	deadline := time.Now().Add(3 * time.Second)
	for a.EventSubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.EventSubscriberCount() == 0 {
		t.Fatal("SSE 未建立订阅")
	}

	// 造一条**序列化必失败**的事件:Data 里放 NaN
	// (json.Marshal 对 NaN 返回 "unsupported value: NaN")。
	a.Bus.Emit(event.New(event.NodeStarted, "exec-bad", "n1", map[string]any{
		"ratio": math.NaN(),
	}))

	// 再造一条正常事件:用于验证"坏事件不掐断流"。
	a.Bus.Emit(event.New(event.NodeCompleted, "exec-good", "n1", map[string]any{"k": "v"}))

	// 等正常事件送达。
	deadline = time.Now().Add(3 * time.Second)
	for !strings.Contains(rw.String(), "exec-good") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	out := rw.String()
	if !strings.Contains(out, "exec-good") {
		t.Errorf("坏事件之后正常事件仍应送达(一条坏事件不得掐断整条流),实际输出: %q", out)
	}
	if strings.Contains(out, "exec-bad") {
		t.Errorf("序列化失败的事件不应被当作正常事件发出,实际输出: %q", out)
	}

	// 关键断言:必须有可追溯的告警。
	logText := logs.String()
	if !strings.Contains(logText, "SSE") || !strings.Contains(logText, "序列化") {
		t.Errorf("序列化失败必须留下可追溯的告警(事件类型/执行 ID/原因),实际日志: %q", logText)
	}
	if !strings.Contains(logText, "exec-bad") {
		t.Errorf("告警应指出是哪条事件(执行 ID),实际日志: %q", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Errorf("告警级别应为 WARN,实际日志: %q", logText)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE 流未关闭")
	}
}
