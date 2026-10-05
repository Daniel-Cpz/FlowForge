// Package websocket owns bounded browser connections and their complete lifecycle.
package websocket

import (
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/realtime"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Limits struct {
	Connections, Buffer                     int
	WriteTimeout, PingInterval, PongTimeout time.Duration
}

func DefaultLimits() Limits {
	return Limits{100, 16, 2 * time.Second, 15 * time.Second, 45 * time.Second}
}

type client struct {
	conn  *ws.Conn
	queue chan []byte
	done  chan struct{}
	once  sync.Once
}

func (c *client) stop() {
	c.once.Do(func() {
		close(c.done)
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}

type Hub struct {
	mu           sync.Mutex
	clients      map[*client]struct{}
	reserved     int
	closed, live bool
	limits       Limits
	origins      map[string]bool
	wg           sync.WaitGroup
}

func New(limits Limits, origins []string) *Hub {
	if limits.Connections < 1 || limits.Connections > 500 || limits.Buffer < 1 || limits.Buffer > 128 || limits.WriteTimeout <= 0 || limits.PingInterval <= 0 || limits.PongTimeout <= limits.PingInterval {
		panic("invalid websocket limits")
	}
	h := &Hub{clients: make(map[*client]struct{}), limits: limits, origins: make(map[string]bool)}
	for _, o := range origins {
		h.origins[o] = true
	}
	return h
}
func (h *Hub) origin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && (u.Host == r.Host || h.origins[raw])
}
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.Error(w, "invalid websocket request", 400)
		return
	}
	h.mu.Lock()
	if h.closed || h.reserved >= h.limits.Connections {
		h.mu.Unlock()
		http.Error(w, "realtime unavailable", 503)
		return
	}
	h.reserved++
	h.wg.Add(1)
	h.mu.Unlock()
	defer h.wg.Done()
	upgrade := ws.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: h.origin, ReadBufferSize: 1024, WriteBufferSize: 1024}
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		h.mu.Lock()
		h.reserved--
		h.mu.Unlock()
		return
	}
	c := &client{conn: conn, queue: make(chan []byte, h.limits.Buffer), done: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.reserved--
		h.mu.Unlock()
		c.stop()
		return
	}
	h.clients[c] = struct{}{}
	live := h.live
	status := "DEGRADED"
	if live {
		status = "LIVE"
	}
	initial, _ := json.Marshal(realtime.New("system.changed", uuid.Nil, status))
	c.queue <- initial
	h.mu.Unlock()
	written := make(chan struct{})
	go func() { defer close(written); h.write(c) }()
	defer func() {
		c.stop()
		<-written
		h.mu.Lock()
		delete(h.clients, c)
		h.reserved--
		h.mu.Unlock()
	}()
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(h.limits.PongTimeout))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(h.limits.PongTimeout)) })
	// No commands over WS. Even a well-formed application message closes the client.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteControl(ws.CloseMessage, ws.FormatCloseMessage(ws.ClosePolicyViolation, "REST commands only"), time.Now().Add(h.limits.WriteTimeout))
		return
	}
}
func (h *Hub) write(c *client) {
	defer c.stop()
	ticker := time.NewTicker(h.limits.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case data := <-c.queue:
			_ = c.conn.SetWriteDeadline(time.Now().Add(h.limits.WriteTimeout))
			if c.conn.WriteMessage(ws.TextMessage, data) != nil {
				return
			}
		case <-ticker.C:
			if c.conn.WriteControl(ws.PingMessage, nil, time.Now().Add(h.limits.WriteTimeout)) != nil {
				return
			}
		}
	}
}
func (h *Hub) Broadcast(e realtime.Event) {
	if !e.Valid() {
		return
	}
	data, err := json.Marshal(e)
	if err != nil || len(data) > 1024 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for c := range h.clients {
		select {
		case <-c.done:
			continue
		default:
		}
		select {
		case c.queue <- data:
		default:
			c.stop()
		}
	}
}
func (h *Hub) SetLive(live bool) {
	h.mu.Lock()
	changed := h.live != live
	h.live = live
	h.mu.Unlock()
	if changed {
		status := "DEGRADED"
		if live {
			status = "LIVE"
		}
		h.Broadcast(realtime.New("system.changed", uuid.Nil, status))
	}
}
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	for c := range h.clients {
		c.stop()
	}
	h.mu.Unlock()
	h.wg.Wait()
}
