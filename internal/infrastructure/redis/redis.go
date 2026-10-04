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

// Open verifies connectivity only; Redis does not carry jobs in Phase 0.
func Open(ctx context.Context, addr, password string, db int) (*redis.Client, error) {
	configureLogger.Do(func() { redis.SetLogger(clientLogger{}) })
	c := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db,
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		ContextTimeoutEnabled: true, MaxRetries: -1})
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
