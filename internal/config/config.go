package config

import (
	"fmt"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env               string
	HTTPAddr          string
	PostgresURL       string
	RedisAddr         string
	RedisPassword     string
	RedisDB           int
	RedisStream       string
	LogLevel          slog.Level
	WorkerConcurrency int
	LeasePolicy       worker.LeasePolicy
}

func value(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func Load() (Config, error) {
	c := Config{Env: value("FLOWFORGE_ENV", "development"), HTTPAddr: value("FLOWFORGE_HTTP_ADDR", ":8080"),
		RedisAddr: value("FLOWFORGE_REDIS_ADDR", "localhost:6379"), RedisPassword: os.Getenv("FLOWFORGE_REDIS_PASSWORD"),
		RedisStream: value("FLOWFORGE_REDIS_STREAM", "flowforge:jobs:v1")}
	concurrency := value("FLOWFORGE_WORKER_CONCURRENCY", "1")
	var err error
	c.WorkerConcurrency, err = strconv.Atoi(concurrency)
	if err != nil || c.WorkerConcurrency < 1 || c.WorkerConcurrency > 32 || strings.IndexFunc(concurrency, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return c, fmt.Errorf("FLOWFORGE_WORKER_CONCURRENCY must be an integer in 1..32")
	}
	c.LeasePolicy = worker.DefaultLeasePolicy()
	for _, setting := range []struct {
		key    string
		target *time.Duration
	}{
		{"FLOWFORGE_LEASE_SECONDS", &c.LeasePolicy.LeaseDuration},
		{"FLOWFORGE_RENEW_SECONDS", &c.LeasePolicy.RenewInterval},
		{"FLOWFORGE_HEARTBEAT_SECONDS", &c.LeasePolicy.HeartbeatInterval},
		{"FLOWFORGE_OFFLINE_SECONDS", &c.LeasePolicy.OfflineAfter},
		{"FLOWFORGE_RECOVERY_SECONDS", &c.LeasePolicy.RecoveryInterval},
	} {
		raw := value(setting.key, strconv.Itoa(int(*setting.target/time.Second)))
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 1 || seconds > 600 || strings.IndexFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return c, fmt.Errorf("invalid %s", setting.key)
		}
		*setting.target = time.Duration(seconds) * time.Second
	}
	if err := c.LeasePolicy.Validate(); err != nil {
		return c, err
	}
	if strings.TrimSpace(c.RedisStream) == "" || len(c.RedisStream) > 256 || strings.IndexFunc(c.RedisStream, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return c, fmt.Errorf("invalid FLOWFORGE_REDIS_STREAM")
	}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, fmt.Errorf("invalid FLOWFORGE_ENV")
	}
	for key, addr := range map[string]string{"FLOWFORGE_HTTP_ADDR": c.HTTPAddr, "FLOWFORGE_REDIS_ADDR": c.RedisAddr} {
		_, port, err := net.SplitHostPort(addr)
		p, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || p < 1 || p > 65535 {
			return c, fmt.Errorf("invalid %s", key)
		}
	}
	password := os.Getenv("FLOWFORGE_POSTGRES_PASSWORD")
	if strings.TrimSpace(password) == "" {
		return c, fmt.Errorf("FLOWFORGE_POSTGRES_PASSWORD is required")
	}
	host, user, db := value("FLOWFORGE_POSTGRES_HOST", "localhost"), value("FLOWFORGE_POSTGRES_USER", "flowforge"), value("FLOWFORGE_POSTGRES_DB", "flowforge")
	if strings.TrimSpace(host) == "" || strings.TrimSpace(user) == "" || strings.TrimSpace(db) == "" {
		return c, fmt.Errorf("PostgreSQL host, user and database must not be empty")
	}
	port, err := strconv.Atoi(value("FLOWFORGE_POSTGRES_PORT", "5432"))
	if err != nil || port < 1 || port > 65535 {
		return c, fmt.Errorf("invalid FLOWFORGE_POSTGRES_PORT")
	}
	sslmode := value("FLOWFORGE_POSTGRES_SSLMODE", "disable")
	if sslmode != "disable" && sslmode != "require" && sslmode != "verify-ca" && sslmode != "verify-full" {
		return c, fmt.Errorf("invalid FLOWFORGE_POSTGRES_SSLMODE")
	}
	if c.Env == "production" && sslmode == "disable" {
		return c, fmt.Errorf("PostgreSQL TLS must be enabled in production")
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + db}
	q := url.Values{"sslmode": {sslmode}, "timezone": {"UTC"}}
	u.RawQuery = q.Encode()
	c.PostgresURL = u.String()
	c.RedisDB, err = strconv.Atoi(value("FLOWFORGE_REDIS_DB", "0"))
	if err != nil || c.RedisDB < 0 || c.RedisDB > 15 {
		return c, fmt.Errorf("invalid FLOWFORGE_REDIS_DB")
	}
	if err = c.LogLevel.UnmarshalText([]byte(value("FLOWFORGE_LOG_LEVEL", "INFO"))); err != nil {
		return c, fmt.Errorf("invalid FLOWFORGE_LOG_LEVEL")
	}
	return c, nil
}
