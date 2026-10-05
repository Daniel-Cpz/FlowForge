package realtime

import (
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestVersionedHints(t *testing.T) {
	for _, kind := range []string{"job.changed", "worker.changed", "schedule.changed", "system.changed"} {
		e := New(kind, uuid.New(), "")
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		v, err := Decode(data)
		if err != nil || v != e {
			t.Fatal(v, err)
		}
		for _, secret := range []string{"payload", "result", "key", "credentials", "error"} {
			if strings.Contains(string(data), secret) {
				t.Fatal("hint leaked entity data")
			}
		}
	}
	e := New("job.changed", uuid.New(), "RUNNING")
	e.Version = 2
	data, _ := json.Marshal(e)
	if _, err := Decode(data); err == nil {
		t.Fatal("future version accepted")
	}
	for _, data := range []string{`{}`, `not json`, strings.Repeat("x", 1025), `{"version":1,"event":"unknown"}`} {
		if _, err := Decode([]byte(data)); err == nil {
			t.Fatal("bad event accepted")
		}
	}
	e = New("job.changed", uuid.Nil, "RUNNING")
	if e.Valid() {
		t.Fatal("missing identity")
	}
	e = New("job.changed", uuid.New(), "raw secret")
	if e.Valid() {
		t.Fatal("raw status accepted")
	}
}
