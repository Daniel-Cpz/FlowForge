// Package migrations embeds the versioned schema. Up applies pending migrations;
// down rolls back one version. Both are serialized and fully transactional.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

//go:embed *.sql
var files embed.FS

var versions = []string{"000001_create_jobs", "000002_create_job_attempts", "000003_create_job_dispatch", "000004_worker_leases", "000005_retry_idempotency", "000006_execution_control"}

func Run(ctx context.Context, pool *pgxpool.Pool, direction string) error {
	if direction != "up" && direction != "down" {
		return fmt.Errorf("direction must be up or down")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(704621830)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	var current, count int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0), COUNT(*) FROM schema_migrations`).Scan(&current, &count); err != nil {
		return err
	}
	if current < 0 || current > len(versions) || current != count {
		return fmt.Errorf("unknown or noncontiguous migration history")
	}
	apply := func(v int, suffix string) error {
		data, err := files.ReadFile(versions[v-1] + "." + suffix + ".sql")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, string(data), pgx.QueryExecModeSimpleProtocol)
		return err
	}
	if direction == "up" {
		for v := current + 1; v <= len(versions); v++ {
			if err = apply(v, "up"); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, v); err != nil {
				return err
			}
		}
	} else if current > 0 {
		if err = apply(current, "down"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, current); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
