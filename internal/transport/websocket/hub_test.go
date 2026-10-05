package websocket

import (
	"github.com/Daniel-Cpz/FlowForge/internal/realtime"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testHub(t *testing.T, limits Limits) (*Hub, string) {
	t.Helper()
	h := New(limits, []string{"http://localhost:5173"})
	s := httptest.NewServer(h)
	t.Cleanup(func() { h.Close(); s.Close() })
	return h, "ws" + strings.TrimPrefix(s.URL, "http")
}
func dial(t *testing.T, url string) *ws.Conn {
	t.Helper()
	c, _, err := ws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := c.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestLimitsOriginsReconnectAndShutdown(t *testing.T) {
	limits := DefaultLimits()
	limits.Connections = 2
	h, url := testHub(t, limits)
	for _, origin := range []string{"https://evil.invalid", "http://localhost:5173/path", "http://user@localhost:5173"} {
		_, response, err := ws.DefaultDialer.Dial(url, http.Header{"Origin": {origin}})
		if err == nil || response.StatusCode != 403 {
			t.Fatal("origin accepted")
		}
		response.Body.Close()
	}
	c := dial(t, url)
	other := dial(t, url)
	_, response, err := ws.DefaultDialer.Dial(url, nil)
	if err == nil || response.StatusCode != 503 {
		t.Fatal("limit failed")
	}
	response.Body.Close()
	h.Broadcast(realtime.New("job.changed", uuid.New(), "RUNNING"))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := realtime.Decode(data); err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		n := h.reserved
		h.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unregister leaked")
		}
		time.Sleep(time.Millisecond)
	}
	_ = dial(t, url)
	done := make(chan struct{})
	go func() { h.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) != 0 || h.reserved != 0 {
		t.Fatal("connection leaked")
	}
}
func TestBoundedSlowClientBurstAndConcurrentClose(t *testing.T) {
	h := New(DefaultLimits(), nil)
	c := &client{queue: make(chan []byte, 16), done: make(chan struct{})}
	h.clients[c] = struct{}{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				h.Broadcast(realtime.New("job.changed", uuid.New(), "RUNNING"))
			}
		})
	}
	wg.Wait()
	if len(c.queue) != 16 {
		t.Fatal("buffer was not bounded", len(c.queue))
	}
	select {
	case <-c.done:
	default:
		t.Fatal("slow client not disconnected")
	}
	wg.Go(h.Close)
	wg.Go(func() {
		for range 100 {
			h.Broadcast(realtime.New("worker.changed", uuid.New(), "OFFLINE"))
		}
	})
	wg.Wait()
}
func TestNoCommandsMalformedOversizedAndIdle(t *testing.T) {
	for _, mode := range []string{"command", "oversized", "unmasked", "idle"} {
		t.Run(mode, func(t *testing.T) {
			limits := DefaultLimits()
			limits.PingInterval = 10 * time.Millisecond
			limits.PongTimeout = 60 * time.Millisecond
			h, url := testHub(t, limits)
			c := dial(t, url)
			switch mode {
			case "command":
				_ = c.WriteMessage(ws.TextMessage, []byte(`{"cancel":"x"}`))
			case "oversized":
				_ = c.WriteMessage(ws.TextMessage, []byte(strings.Repeat("x", 2048)))
			case "unmasked":
				_, _ = c.UnderlyingConn().Write([]byte{0x81, 0x01, 'x'})
			case "idle":
				time.Sleep(100 * time.Millisecond)
			}
			_, _, err := c.ReadMessage()
			if err == nil {
				t.Fatal("invalid or idle peer remained connected")
			}
			deadline := time.Now().Add(time.Second)
			for {
				h.mu.Lock()
				n := h.reserved
				h.mu.Unlock()
				if n == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("peer cleanup leaked")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
