package config

import (
	"os"
	"strconv"
	"testing"
)

func TestWorkerConcurrency(t *testing.T) {
	for _, value := range []string{"default", "1", "2", "4", "32", "", "0", "-1", "33", "+2", " 2", "2.0", "2e0", "invalid", "99999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			defaults(t)
			t.Setenv("FLOWFORGE_WORKER_CONCURRENCY", value)
			if value == "default" {
				if err := os.Unsetenv("FLOWFORGE_WORKER_CONCURRENCY"); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Load()
			expected, parseErr := strconv.Atoi(value)
			if value == "default" {
				expected, parseErr = 1, nil
			}
			valid := parseErr == nil && expected >= 1 && expected <= 32 && value != "+2"
			if valid {
				if err != nil || c.WorkerConcurrency != expected {
					t.Fatal(c.WorkerConcurrency, err)
				}
			} else if err == nil {
				t.Fatal("invalid concurrency accepted")
			}
		})
	}
}
