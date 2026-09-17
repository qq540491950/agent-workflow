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
