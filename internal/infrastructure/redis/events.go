package redis

import (
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/realtime"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Events uses a separate transient Pub/Sub channel. Exactly one publisher
// goroutine drains a bounded queue; saturation/failure drops UI hints only.
type Events struct {
	client  *goredis.Client
	channel string
	logger  *slog.Logger
	queue   chan realtime.Event
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
	dropped atomic.Uint64
}

func NewEvents(ctx context.Context, c *goredis.Client, channel string, logger *slog.Logger) *Events {
	ctx, cancel := context.WithCancel(ctx)
	b := &Events{client: c, channel: channel, logger: logger, queue: make(chan realtime.Event, 128), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go b.publish()
	return b
}
func (b *Events) Change(kind string, id uuid.UUID, status string) {
	e := realtime.New(kind, id, status)
	if !e.Valid() {
		return
	}
	select {
	case <-b.ctx.Done():
		return
	default:
	}
	select {
	case b.queue <- e:
	default:
		// Producer must not wait even on the log sink; publisher reports drops.
		b.dropped.Add(1)
	}
}
func (b *Events) publish() {
	defer close(b.done)
	for {
		if n := b.dropped.Swap(0); n > 0 {
			b.logger.Warn("UI hints dropped", "event", "ui_hint_queue_full", "dropped", n)
		}
		select {
		case <-b.ctx.Done():
			return
		case e := <-b.queue:
			data, _ := json.Marshal(e)
			ctx, cancel := context.WithTimeout(b.ctx, 250*time.Millisecond)
			err := b.client.Publish(ctx, b.channel, data).Err()
			cancel()
			if err != nil && b.ctx.Err() == nil {
				b.logger.Warn("UI hint publish failed", "event", "ui_hint_publish_failed")
			}
		}
	}
}
func (b *Events) Close() { b.once.Do(b.cancel); <-b.done }

// Subscribe recreates the subscription after transport errors. Subscription
// recovery triggers a system hint so already-connected browsers resnapshot too.
func (b *Events) Subscribe(ctx context.Context, broadcast func(realtime.Event), state func(bool)) {
	defer state(false)
	for ctx.Err() == nil {
		ps := b.client.Subscribe(ctx, b.channel)
		stopClose := context.AfterFunc(ctx, func() { _ = ps.Close() })
		for ctx.Err() == nil {
			msg, err := ps.Receive(ctx)
			if err != nil {
				state(false)
				break
			}
			switch v := msg.(type) {
			case *goredis.Subscription:
				state(true)
			case *goredis.Message:
				if e, err := realtime.Decode([]byte(v.Payload)); err == nil {
					broadcast(e)
				}
			}
		}
		stopClose()
		_ = ps.Close()
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
