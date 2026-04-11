package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedRecorder struct {
	rec *httptest.ResponseRecorder
	mu  sync.Mutex
}

func newLockedRecorder() *lockedRecorder {
	return &lockedRecorder{rec: httptest.NewRecorder()}
}

func (w *lockedRecorder) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rec.Header()
}

func (w *lockedRecorder) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rec.Write(b)
}

func (w *lockedRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rec.WriteHeader(statusCode)
}

func (w *lockedRecorder) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rec.Flush()
}

func (w *lockedRecorder) BodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rec.Body.String()
}

func TestEventBus_PubSub(t *testing.T) {
	eb := NewEventBus()

	// 1. 订阅
	ch1 := eb.Subscribe()
	ch2 := eb.Subscribe()

	// 2. 发布事件
	eb.PublishJSON("test_event", map[string]string{"foo": "bar"})

	// 3. 验证接收
	checkRecv := func(ch <-chan SSEEvent, name, expectedType, expectedData string) {
		select {
		case ev := <-ch:
			if ev.Type != expectedType {
				t.Errorf("%s expected to receive %s, got %s", name, expectedType, ev.Type)
			}
			if !strings.Contains(ev.Data, expectedData) {
				t.Errorf("%s data mismatch: %s", name, ev.Data)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("%s did not receive event", name)
		}
	}

	checkRecv(ch1, "ch1", "test_event", `"foo":"bar"`)
	checkRecv(ch2, "ch2", "test_event", `"foo":"bar"`)

	// 4. 退订
	eb.Unsubscribe(ch1)

	// 验证退订后的通道不应再收到新事件
	eb.PublishJSON("hello", "world")
	checkRecv(ch2, "ch2", "hello", `"world"`)

	select {
	case ev, ok := <-ch1:
		if ok {
			t.Errorf("ch1 already unsubscribed, should not receive valid events: %v", ev)
		}
	case <-time.After(50 * time.Millisecond):
		// 正常，没有事件
		// 正常，没有事件
	}
}

func TestEventBus_PublishTimeout(t *testing.T) {
	eb := NewEventBus()

	// 订阅一个通道但故意不读
	ch := eb.Subscribe()

	// 连续发送超过缓冲区 (100) 的消息，触发 Publish 的 select default 分支
	// 这里发 150 个
	for i := 0; i < 150; i++ {
		eb.Publish(SSEEvent{Type: "spam"})
	}

	// 检查通道里面应该只有 64 个
	count := 0
loop:
	for {
		select {
		case <-ch:
			count++
		default:
			break loop
		}
	}

	if count != 64 {
		t.Errorf("expected channel to be full with 64, got %d", count)
	}
}

func TestHandleSSE_DisconnectCleanup(t *testing.T) {
	s := New(0)
	// mock auth: SSE 不需要认证 (实际中前面会有 RequireAuth)，这里直接调 handleSSE

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req = req.WithContext(ctx)

	// 为了拦截 writer，我们手写个 response recorder，支持 closeNotify (虽然 http.ResponseWriter 已经不再推荐，但在测试请求中断时，Cancel / Context Done 是主要方式)
	w := newLockedRecorder()

	// 启动 handleSSE 会阻塞，所以放进 goroutine
	done := make(chan struct{})
	go func() {
		s.handleSSE(w, req)
		close(done)
	}()

	// 确认订阅数增加
	time.Sleep(50 * time.Millisecond)
	s.events.mu.RLock()
	subCount := len(s.events.subscribers)
	s.events.mu.RUnlock()
	if subCount != 1 {
		t.Errorf("expected one subscriber, got %d", subCount)
	}

	body := w.BodyString()
	if !strings.Contains(body, "event: ready\ndata: {}\n\n") {
		t.Fatalf("expected ready event immediately after SSE connection, actual body: %q", body)
	}

	if !strings.Contains(body, "event: snapshot\n") ||
		!strings.Contains(body, `"clients":`) ||
		!strings.Contains(body, `"server_status":`) {
		t.Fatalf("expected full snapshot immediately after SSE connection, actual body: %q", body)
	}

	// 发送事件
	s.events.PublishJSON("foo", "bar")
	time.Sleep(50 * time.Millisecond)

	body = w.BodyString()
	if !strings.Contains(body, "event: foo\ndata: \"bar\"\n\n") {
		t.Fatalf("expected to receive business event, actual body: %q", body)
	}

	// 模拟客户端断开连接 (Cancel context)
	cancel()

	// 等待 handleSSE 退出
	select {
	case <-done:
		// 正常退出
	case <-time.After(1 * time.Second):
		t.Fatal("handleSSE did not exit correctly when client disconnected")
	}

	// 确认订阅数减少为 0
	s.events.mu.RLock()
	subCount = len(s.events.subscribers)
	s.events.mu.RUnlock()
	if subCount != 0 {
		t.Errorf("subscription should be cleaned up after client disconnect, remaining: %d", subCount)
	}
}
