package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/config"
	"os"
	"reflect"
	"testing"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each test uses a random schema, never dropping application tables or databases.
func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("FLOWFORGE_TEST_POSTGRES_URL")
	if url == "" && os.Getenv("FLOWFORGE_INTEGRATION") == "1" {
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		url = cfg.PostgresURL
	}
	if url == "" {
		t.Skip("FLOWFORGE_TEST_POSTGRES_URL is unset; PostgreSQL integration NOT EXECUTED")
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	schema := "test_" + uuid.New().String()
	schema = "\"" + schema + "\""
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPersistenceAndMigrations(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	for range 2 {
		if err := migrations.Run(ctx, pool, "up"); err != nil {
			t.Fatal(err)
		}
	}
	repo := postgres.NewJobRepository(pool)
	s := service.New(repo)
	key := "reserved-key"
	j, err := s.Create(ctx, service.CreateInput{Type: "example", Payload: json.RawMessage(`{"nested":{"ok":true},"items":[1,2]}`), Priority: 7, IdempotencyKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err = json.Unmarshal(j.Payload, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(got.Payload, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("payload changed")
	}
	got.Payload = j.Payload
	if !reflect.DeepEqual(j, got) {
		t.Fatalf("persistence mismatch\n%+v\n%+v", j, got)
	}
	if _, err = s.Create(ctx, service.CreateInput{Type: "example", Payload: json.RawMessage(`null`), IdempotencyKey: &key}); err != nil {
		t.Fatal("idempotency must not be enforced", err)
	}
	list, err := s.List(ctx, 20, 0)
	if err != nil || len(list) != 2 {
		t.Fatal("list", len(list), err)
	}
	if _, err = repo.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("not found mapping", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.GetByID(cancelled, j.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not preserved", err)
	}
	if _, err = s.Create(ctx, service.CreateInput{Type: "example", Payload: json.RawMessage(`{"nul":"\u0000"}`)}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("JSONB incompatibility must be invalid input", err)
	}
	for _, query := range []string{`UPDATE jobs SET attempt_count=-1`, `UPDATE jobs SET max_attempts=0`, `UPDATE jobs SET priority=101`, `UPDATE jobs SET status='UNKNOWN'`, `UPDATE jobs SET timeout=0`} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("constraint missing", query)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO job_attempts(id,job_id,worker_id,attempt_number,status,started_at) VALUES($1,$2,$3,1,$4,$5)`, uuid.New(), j.ID, uuid.New(), domain.Running, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO job_attempts(id,job_id,worker_id,attempt_number,status,started_at) VALUES($1,$2,$3,1,$4,$5)`, uuid.New(), j.ID, uuid.New(), domain.Running, time.Now().UTC())
	if err == nil {
		t.Fatal("duplicate attempt number accepted")
	}
	for range 2 {
		if err = migrations.Run(ctx, pool, "down"); err != nil {
			t.Fatal(err)
		}
	}
	if err = migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal("schema recreation failed", err)
	}
}
