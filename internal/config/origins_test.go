package config

import "testing"

func TestOriginAllowlist(t *testing.T) {
	t.Setenv("FLOWFORGE_POSTGRES_PASSWORD", "test-only")
	for _, v := range []string{"*", "https://example.com/path", "https://user:pass@example.com", "https://example.com?token=x", "http://", "ftp://example.com"} {
		t.Setenv("FLOWFORGE_WS_ORIGINS", v)
		if _, err := Load(); err == nil {
			t.Fatal("invalid allowlist accepted")
		}
	}
	for _, v := range []string{"", "http://localhost:5173,https://example.com"} {
		t.Setenv("FLOWFORGE_WS_ORIGINS", v)
		if _, err := Load(); err != nil {
			t.Fatal(err)
		}
	}
}
