package loadgen

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNearestRank(t *testing.T) {
	for _, v := range []struct {
		data    []float64
		q, want float64
	}{{nil, .99, 0}, {[]float64{7}, .5, 7}, {[]float64{4, 1, 3, 2}, .5, 2}, {[]float64{4, 1, 3, 2}, .95, 4}, {[]float64{1, 2, 3, 4, 5}, .5, 3}} {
		if got := Percentile(v.data, v.q); got != v.want {
			t.Fatal(got, v.want)
		}
	}
}
func TestHTTPClassificationJSONAndBoundedPolling(t *testing.T) {
	var calls, active, peak atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		calls.Add(1)
		if r.Method == "POST" {
			w.WriteHeader(201)
		}
		now := time.Now().UTC()
		json.NewEncoder(w).Encode(jobResult{ID: "same-test-job", Status: "SUCCEEDED", Created: now.Add(-2 * time.Second), Started: ptr(now.Add(-time.Second)), Finished: ptr(now), Attempts: 1})
	}))
	defer server.Close()
	s, e := Run(t.Context(), Config{BaseURL: server.URL, Count: 9, Concurrency: 2, MaxWait: time.Second, DurationMS: 25})
	if e != nil || s.Succeeded != 9 || s.ErrorRate != 0 || s.Queue.P50 != 1 || peak.Load() > 2 {
		t.Fatal(s, e, peak.Load())
	}
	data, _ := json.Marshal(s)
	if !strings.Contains(string(data), "throughput_jobs_per_second") || math.IsNaN(s.Throughput) {
		t.Fatal(string(data))
	}
	for _, status := range []string{"FAILED", "CANCELLED", "TIMED_OUT", "DEAD_LETTER"} {
		if !Terminal(status) {
			t.Fatal(status)
		}
	}
	if Terminal("RETRYING") {
		t.Fatal("retry terminal")
	}
}
func ptr(v time.Time) *time.Time { return &v }
func TestTimeoutCancelAndHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"x","status":"QUEUED"}`))
		} else {
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	s, e := Run(t.Context(), Config{BaseURL: server.URL, Count: 3, Concurrency: 2, MaxWait: 120 * time.Millisecond})
	if e == nil || s.Unfinished != 3 || s.PollErrors > 10 {
		t.Fatal(s, e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e = Run(ctx, Config{BaseURL: server.URL, Count: 1, Concurrency: 1, MaxWait: time.Second})
	if e == nil {
		t.Fatal("cancellation")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(409) }))
	defer bad.Close()
	s, e = Run(t.Context(), Config{BaseURL: bad.URL, Count: 2, Concurrency: 1, MaxWait: time.Second})
	if e != nil || s.SubmitErrors != 2 || s.SubmitErrorRate != 1 {
		t.Fatal(s, e)
	}
}
