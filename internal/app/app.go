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
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
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
	redis := redisinfra.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer func() {
		if err := redis.Close(); err != nil {
			logger.Warn("Redis close failed", "event", "redis_close_failed")
		}
	}()
	cancel()
	if process == "worker" {
		repo := postgres.NewJobRepository(pool)
		queue := redisinfra.NewQueue(redis, cfg.RedisStream, redisinfra.Group)
		workerCtx, stop := context.WithCancel(ctx)
		defer stop()
		dispatchDone := make(chan struct{})
		go func() { defer close(dispatchDone); dispatch.New(repo, queue, logger).Run(workerCtx) }()
		logger.Info("Single worker started", "event", "worker_started")
		err := execution.New(repo, queue, execution.Sleep{}, logger).Run(workerCtx)
		stop()
		<-dispatchDone
		logger.Info("Worker stopped", "event", "worker_stopped")
		return err
	}
	repo := postgres.NewJobRepository(pool)
	router := httptransport.NewRouter(jobservice.New(repo), logger, pool.Ping, func(ctx context.Context) error { return redis.Ping(ctx).Err() },
		func(ctx context.Context) error {
			// Readiness includes the schema used by this API, not just a TCP connection.
			_, err := pool.Exec(ctx, `SELECT id, type, status, priority, payload, result, attempt_count, max_attempts, timeout,
			 idempotency_key, assigned_worker, lease_expiry, created_at, started_at, finished_at FROM jobs LIMIT 0`)
			if err == nil {
				_, err = pool.Exec(ctx, `SELECT job_id,created_at,published_at FROM job_dispatch LIMIT 0`)
			}
			return err
		})
	if err := httptransport.Run(ctx, httptransport.NewServer(cfg.HTTPAddr, router, logger), logger); err != nil {
		return errors.New("HTTP server failed to start or shut down cleanly")
	}
	return nil
}
