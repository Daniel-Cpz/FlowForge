package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

func Open(ctx context.Context, connectionString string) (*pgxpool.Pool, error) {
	return OpenWithMaxConns(ctx, connectionString, 10)
}

func OpenWithMaxConns(ctx context.Context, connectionString string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
