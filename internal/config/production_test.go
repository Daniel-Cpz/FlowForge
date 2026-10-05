package config

import (
	"strings"
	"testing"
)

func TestProductionDependencies(t *testing.T) {
	for _, tc := range []struct {
		ssl, password string
		valid         bool
	}{
		{"disable", strings.Repeat("x", 32), false},
		{"require", "", false},
		{"require", "short", false},
		{"require", strings.Repeat(" ", 32), false},
		{"require", strings.Repeat("x", 32), true},
	} {
		t.Run(tc.ssl+tc.password, func(t *testing.T) {
			defaults(t)
			t.Setenv("FLOWFORGE_ENV", "production")
			t.Setenv("FLOWFORGE_POSTGRES_SSLMODE", tc.ssl)
			t.Setenv("FLOWFORGE_REDIS_PASSWORD", tc.password)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err != nil && tc.password != "" && strings.Contains(err.Error(), tc.password) {
				t.Fatal("password leaked")
			}
		})
	}
}
