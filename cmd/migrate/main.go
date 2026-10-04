package main

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/config"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func run(ctx context.Context) bool {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		slog.Error("usage: migrate up|down")
		return false
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, cfg.PostgresURL)
	if err != nil {
		slog.Error("migration connection failed")
		return false
	}
	defer pool.Close()
	if err = migrations.Run(ctx, pool, os.Args[1]); err != nil {
		slog.Error("migration failed; transaction rolled back")
		return false
	}
	slog.Info("migration complete", "direction", os.Args[1])
	return true
}

func main() {
	slog.SetDefault(observability.NewLogger(slog.LevelInfo))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !run(ctx) {
		os.Exit(1)
	}
}
