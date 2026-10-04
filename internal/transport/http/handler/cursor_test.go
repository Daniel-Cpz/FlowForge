package handler

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	want := domain.PageCursor{CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC), ID: uuid.New()}
	encoded, err := encodeCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(encoded, "+/=\n\r") || len(encoded) > maxCursorLength {
		t.Fatal("cursor must be bounded and URL safe")
	}
	got, err := decodeCursor(encoded)
	if err != nil || *got != want {
		t.Fatal("cursor round trip", got, err)
	}
}

func TestInvalidCursors(t *testing.T) {
	for _, encoded := range []string{"", "%", "%%%%", "e30=", strings.Repeat("A", maxCursorLength+1)} {
		if _, err := decodeCursor(encoded); err == nil {
			t.Errorf("accepted invalid encoding %q", encoded)
		}
	}
	validID := uuid.NewString()
	for _, document := range []string{
		`{`, `null`, `[]`, `{}`, `{"v":1}`, `{"v":1,"id":"` + validID + `"}`,
		`{"v":2,"created_at":"2026-01-02T03:04:05Z","id":"` + validID + `"}`,
		`{"v":null,"created_at":"2026-01-02T03:04:05Z","id":"` + validID + `"}`,
		`{"V":1,"created_at":"2026-01-02T03:04:05Z","id":"` + validID + `"}`,
		`{"v":1,"created_at":"bad","id":"` + validID + `"}`,
		`{"v":1,"created_at":null,"id":"` + validID + `"}`,
		`{"v":1,"created_at":"0001-01-01T00:00:00Z","id":"` + validID + `"}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05.000000001Z","id":"` + validID + `"}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05+00:00","id":"` + validID + `"}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05Z","id":"bad"}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05Z","id":null}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05Z","id":"00000000-0000-0000-0000-000000000000"}`,
		`{"v":1,"v":1,"created_at":"2026-01-02T03:04:05Z","id":"` + validID + `"}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05Z","id":"` + validID + `","extra":true}`,
		`{"v":1,"created_at":"2026-01-02T03:04:05Z","id":"` + validID + `"} {}`,
	} {
		t.Run(document, func(t *testing.T) {
			encoded := base64.RawURLEncoding.EncodeToString([]byte(document))
			if _, err := decodeCursor(encoded); err == nil {
				t.Fatal("accepted invalid cursor document")
			}
		})
	}
	valid, err := encodeCursor(domain.PageCursor{CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), ID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCursor(valid + "\n"); err == nil {
		t.Fatal("accepted noncanonical encoding")
	}
}
