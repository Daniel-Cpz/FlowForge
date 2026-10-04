package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env           string
	HTTPAddr      string
	PostgresURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	LogLevel      slog.Level
}

func value(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func Load() (Config, error) {
	c := Config{Env: value("FLOWFORGE_ENV", "development"), HTTPAddr: value("FLOWFORGE_HTTP_ADDR", ":8080"),
		RedisAddr: value("FLOWFORGE_REDIS_ADDR", "localhost:6379"), RedisPassword: os.Getenv("FLOWFORGE_REDIS_PASSWORD")}
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
