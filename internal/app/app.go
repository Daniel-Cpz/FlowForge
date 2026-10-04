// Package app is the composition root. It owns resources and process lifecycle.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/config"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	redisinfra "github.com/Daniel-Cpz/FlowForge/internal/infrastructure/redis"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	jobservice "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	httptransport "github.com/Daniel-Cpz/FlowForge/internal/transport/http"
)

func Run(ctx context.Context, process string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(cfg.LogLevel).With("process", process, "env", cfg.Env)
	slog.SetDefault(logger)
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := postgres.Open(startup, cfg.PostgresURL)
	if err != nil {
		return errors.New("PostgreSQL startup connection failed")
	}
	defer pool.Close()
	redis, err := redisinfra.Open(startup, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return errors.New("Redis startup connection failed")
	}
	defer func() {
		if err := redis.Close(); err != nil {
			logger.Warn("Redis close failed", "event", "redis_close_failed")
		}
	}()
	cancel()
	if process == "worker" {
		logger.Info("Worker skeleton started; job execution is not implemented", "event", "worker_started")
		<-ctx.Done()
		logger.Info("Worker stopped", "event", "worker_stopped")
		return nil
	}
	repo := postgres.NewJobRepository(pool)
	router := httptransport.NewRouter(jobservice.New(repo), logger, pool.Ping, func(ctx context.Context) error { return redis.Ping(ctx).Err() },
		func(ctx context.Context) error {
			// Readiness includes the schema used by this API, not just a TCP connection.
			_, err := pool.Exec(ctx, `SELECT id, type, status, priority, payload, result, attempt_count, max_attempts, timeout,
			 idempotency_key, assigned_worker, lease_expiry, created_at, started_at, finished_at FROM jobs LIMIT 0`)
			return err
		})
	if err := httptransport.Run(ctx, httptransport.NewServer(cfg.HTTPAddr, router, logger), logger); err != nil {
		return errors.New("HTTP server failed to start or shut down cleanly")
	}
	return nil
}
