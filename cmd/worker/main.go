package main

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/app"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(observability.NewLogger(slog.LevelInfo))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, "worker"); err != nil {
		slog.Error("Worker stopped", "event", "worker_failed", "error", err)
		os.Exit(1)
	}
}
