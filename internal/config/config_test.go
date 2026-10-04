package config

import (
	"net/url"
	"strings"
	"testing"
)

func defaults(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"FLOWFORGE_ENV": "test", "FLOWFORGE_HTTP_ADDR": ":8080", "FLOWFORGE_POSTGRES_HOST": "localhost",
		"FLOWFORGE_POSTGRES_PORT": "5432", "FLOWFORGE_POSTGRES_USER": "flowforge", "FLOWFORGE_POSTGRES_DB": "flowforge",
		"FLOWFORGE_POSTGRES_PASSWORD": "test-only:@/?#%", "FLOWFORGE_POSTGRES_SSLMODE": "disable", "FLOWFORGE_REDIS_ADDR": "localhost:6379",
		"FLOWFORGE_REDIS_DB": "0", "FLOWFORGE_LOG_LEVEL": "INFO",
	} {
		t.Setenv(k, v)
	}
}

func TestLoad(t *testing.T) {
	defaults(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.PostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := u.User.Password()
	if p != "test-only:@/?#%" || u.Query().Get("timezone") != "UTC" {
		t.Fatal("connection URL corrupted")
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"FLOWFORGE_POSTGRES_PASSWORD", ""}, {"FLOWFORGE_POSTGRES_PORT", "abc"}, {"FLOWFORGE_POSTGRES_PORT", "0"},
		{"FLOWFORGE_POSTGRES_HOST", ""}, {"FLOWFORGE_REDIS_DB", "-1"}, {"FLOWFORGE_REDIS_DB", "16"},
		{"FLOWFORGE_HTTP_ADDR", "bad"}, {"FLOWFORGE_REDIS_ADDR", "localhost:0"}, {"FLOWFORGE_LOG_LEVEL", "nope"},
		{"FLOWFORGE_ENV", "invalid"}, {"FLOWFORGE_ENV", "production"}, {"FLOWFORGE_POSTGRES_SSLMODE", "nonsense"},
		{"FLOWFORGE_REDIS_STREAM", ""}, {"FLOWFORGE_REDIS_STREAM", "\n"}, {"FLOWFORGE_REDIS_STREAM", strings.Repeat("a", 257)},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			defaults(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil {
				t.Fatal("expected rejection")
			}
			if strings.Contains(err.Error(), "test-only") {
				t.Fatal("secret leaked")
			}
		})
	}
}
