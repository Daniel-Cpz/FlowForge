package redis

import (
	"context"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"sync"
	"time"
)

var configureLogger sync.Once

type clientLogger struct{}

func (clientLogger) Printf(ctx context.Context, _ string, _ ...interface{}) {
	// Client diagnostics may contain addresses or server-supplied text. Keep only
	// the event classification; startup/readiness report the operation outcome.
	slog.DebugContext(ctx, "Redis client diagnostic", "event", "redis_client_diagnostic")
}

// NewClient allows the worker to start its bounded retry loops during an outage.
func NewClient(addr, password string, db int) *redis.Client {
	return NewClientWithPoolSize(addr, password, db, 10)
}

// The worker reserves C blocking reads plus four nonblocking operation sockets.
func NewClientWithPoolSize(addr, password string, db, size int) *redis.Client {
	configureLogger.Do(func() { redis.SetLogger(clientLogger{}) })
	return redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db,
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: size, MaxActiveConns: size, PoolTimeout: 3 * time.Second})
}

func Open(ctx context.Context, addr, password string, db int) (*redis.Client, error) {
	c := NewClient(addr, password, db)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
