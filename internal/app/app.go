// Package app is the composition root. It owns resources and process lifecycle.
package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/config"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	redisinfra "github.com/Daniel-Cpz/FlowForge/internal/infrastructure/redis"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	jobservice "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	httptransport "github.com/Daniel-Cpz/FlowForge/internal/transport/http"
	websockettransport "github.com/Daniel-Cpz/FlowForge/internal/transport/websocket"
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
	pgConnections, redisConnections := 10, 10
	if process == "worker" {
		pgConnections, redisConnections = cfg.WorkerConcurrency+4, cfg.WorkerConcurrency+4
	}
	pool, err := postgres.OpenWithMaxConns(startup, cfg.PostgresURL, int32(pgConnections))
	if err != nil {
		return errors.New("PostgreSQL startup connection failed")
	}
	defer pool.Close()
	redis := redisinfra.NewClientWithPoolSize(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, redisConnections)
	defer func() {
		if err := redis.Close(); err != nil {
			logger.Warn("Redis close failed", "event", "redis_close_failed")
		}
	}()
	cancel()
	// UI transport has its own two-connection budget; a lossy publication must
	// not compete with business Stream reads/ACKs/dispatcher connection slots.
	uiRedis := redisinfra.NewClientWithPoolSize(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, 2)
	defer uiRedis.Close()
	events := redisinfra.NewEvents(ctx, uiRedis, cfg.RedisStream+":ui:v1", logger)
	defer events.Close()
	if process == "worker" {
		repo, err := postgres.NewJobRepositoryWithPolicies(pool, cfg.LeasePolicy, cfg.RetryPolicy, nil)
		if err != nil {
			return err
		}
		repo.ObserveChanges(events.Change)
		queue := redisinfra.NewQueue(redis, cfg.RedisStream, redisinfra.Group)
		worker, err := execution.NewWithLeasePolicy(repo, queue, execution.Sleep{}, logger, cfg.WorkerConcurrency, cfg.LeasePolicy, cfg.WorkerCapabilities...)
		if err != nil {
			return err
		}
		return worker.RunWithDispatcher(ctx, dispatch.New(repo, queue, logger).Run)
	}
	repo := postgres.NewJobRepository(pool).ObserveChanges(events.Change)
	hub := websockettransport.New(websockettransport.DefaultLimits(), strings.Split(cfg.WSOrigins, ","))
	subCtx, endSubscription := context.WithCancel(ctx)
	subDone := make(chan struct{})
	go func() { defer close(subDone); events.Subscribe(subCtx, hub.Broadcast, hub.SetLive) }()
	defer func() { endSubscription(); hub.Close(); <-subDone }()
	router := httptransport.NewRouterWithRealtime(jobservice.New(repo), logger, hub, pool.Ping, func(ctx context.Context) error { return redis.Ping(ctx).Err() },
		func(ctx context.Context) error {
			// Readiness includes the schema used by this API, not just a TCP connection.
			_, err := pool.Exec(ctx, `SELECT id, type, status, priority, payload, result, attempt_count, max_attempts, timeout,
			 idempotency_key, assigned_worker, lease_expiry, retry_at, cancel_requested_at, submission_max_attempts, scheduled_at,required_capabilities,schedule_id,scheduled_for,created_at, started_at, finished_at FROM jobs LIMIT 0`)
			if err == nil {
				_, err = pool.Exec(ctx, `SELECT job_id,created_at,published_at FROM job_dispatch LIMIT 0`)
			}
			if err == nil {
				_, err = pool.Exec(ctx, `SELECT worker_id,status,last_heartbeat,concurrency,active_jobs,capabilities FROM workers LIMIT 0`)
			}
			if err == nil {
				_, err = pool.Exec(ctx, `SELECT id,status,next_run_at,interval_seconds FROM job_schedules LIMIT 0`)
			}
			return err
		})
	if err := httptransport.Run(ctx, httptransport.NewServer(cfg.HTTPAddr, router, logger), logger); err != nil {
		return errors.New("HTTP server failed to start or shut down cleanly")
	}
	return nil
}
