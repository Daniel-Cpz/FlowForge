// Package loadgen measures black-box immediate single-attempt SLEEP work.
package loadgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	BaseURL                        string
	Count, Concurrency, DurationMS int
	MaxWait                        time.Duration
	Idempotent                     bool
}
type Quantiles struct {
	P50 float64 `json:"p50_seconds"`
	P95 float64 `json:"p95_seconds"`
	P99 float64 `json:"p99_seconds"`
}

// Nearest-rank quantile: sorted[ceil(q*N)-1]; empty returns zero, not NaN.
func Percentile(samples []float64, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	v := append([]float64(nil), samples...)
	sort.Float64s(v)
	i := int(math.Ceil(q*float64(len(v)))) - 1
	return v[max(0, min(len(v)-1, i))]
}
func quantiles(s []float64) Quantiles {
	return Quantiles{Percentile(s, .5), Percentile(s, .95), Percentile(s, .99)}
}

type Summary struct {
	Jobs              int       `json:"jobs"`
	Requests          int64     `json:"submit_requests"`
	SubmitErrors      int64     `json:"submit_errors"`
	Completed         int       `json:"completed"`
	Succeeded         int       `json:"succeeded"`
	Failed            int       `json:"failed"`
	DeadLetter        int       `json:"dead_letter"`
	Cancelled         int       `json:"cancelled"`
	TimedOut          int       `json:"timed_out"`
	Unfinished        int       `json:"unfinished"`
	PollErrors        int64     `json:"poll_errors"`
	Elapsed           float64   `json:"wall_seconds"`
	Throughput        float64   `json:"throughput_jobs_per_second"`
	SubmitErrorRate   float64   `json:"submit_error_rate"`
	TerminalErrorRate float64   `json:"terminal_error_rate"`
	ErrorRate         float64   `json:"error_rate"`
	Queue             Quantiles `json:"queue_latency"`
	EndToEnd          Quantiles `json:"end_to_end_latency"`
}
type jobResult struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Created   time.Time  `json:"created_at"`
	Scheduled *time.Time `json:"scheduled_at"`
	Started   *time.Time `json:"started_at"`
	Finished  *time.Time `json:"finished_at"`
	Attempts  int        `json:"attempt_count"`
}

func Terminal(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "DEAD_LETTER", "CANCELLED", "TIMED_OUT":
		return true
	}
	return false
}
func (c Config) Validate() error {
	u, e := url.Parse(c.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || c.Count < 1 || c.Count > 100000 || c.Concurrency < 1 || c.Concurrency > 128 || c.DurationMS < 0 || c.DurationMS > 10000 || c.MaxWait <= 0 || c.MaxWait > time.Hour {
		return errors.New("invalid load generator configuration")
	}
	return nil
}
func Run(parent context.Context, c Config) (Summary, error) {
	s := Summary{Jobs: c.Count}
	if err := c.Validate(); err != nil {
		return s, err
	}
	ctx, cancel := context.WithTimeout(parent, c.MaxWait)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{MaxConnsPerHost: c.Concurrency, MaxIdleConnsPerHost: c.Concurrency}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	request := func(method, path, body string) (jobResult, error) {
		req, e := http.NewRequestWithContext(ctx, method, c.BaseURL+path, strings.NewReader(body))
		if e != nil {
			return jobResult{}, e
		}
		req.Header.Set("Content-Type", "application/json")
		r, e := client.Do(req)
		if e != nil {
			return jobResult{}, errors.New("request failed")
		}
		defer r.Body.Close()
		if r.StatusCode != 200 && r.StatusCode != 201 {
			io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
			return jobResult{}, fmt.Errorf("HTTP status %d", r.StatusCode)
		}
		var j jobResult
		d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if d.Decode(&j) != nil || j.ID == "" {
			return j, errors.New("invalid Job response")
		}
		return j, nil
	}
	results := make([]jobResult, c.Count)
	var requests, submitErrors, pollErrors atomic.Int64
	pool := func(fn func(int)) {
		indices := make(chan int)
		var wg sync.WaitGroup
		for n := 0; n < c.Concurrency; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range indices {
					fn(i)
				}
			}()
		}
		for i := 0; i < c.Count; i++ {
			indices <- i
		}
		close(indices)
		wg.Wait()
	}
	start := time.Now()
	runID := uuid.NewString()
	pool(func(i int) {
		body := fmt.Sprintf(`{"type":"SLEEP","payload":{"duration_ms":%d},"max_attempts":1}`, c.DurationMS)
		if c.Idempotent {
			body = fmt.Sprintf(`{"type":"SLEEP","payload":{"duration_ms":%d},"max_attempts":1,"idempotency_key":"loadgen-%s-%d"}`, c.DurationMS, runID, i)
		}
		requests.Add(1)
		j, e := request("POST", "/api/v1/jobs", body)
		if e != nil {
			submitErrors.Add(1)
			return
		}
		results[i] = j
	})
	// Fixed 50ms floor, fixed concurrency and deadline; no per-job goroutine/timer.
	pool(func(i int) {
		if results[i].ID == "" {
			return
		}
		for ctx.Err() == nil {
			j, e := request("GET", "/api/v1/jobs/"+results[i].ID, "")
			if e == nil {
				results[i] = j
				if Terminal(j.Status) {
					return
				}
			} else {
				pollErrors.Add(1)
			}
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
	s.Elapsed = time.Since(start).Seconds()
	s.Requests = requests.Load()
	s.SubmitErrors = submitErrors.Load()
	s.PollErrors = pollErrors.Load()
	var queue, end []float64
	for _, j := range results {
		if !Terminal(j.Status) {
			s.Unfinished++
			continue
		}
		s.Completed++
		switch j.Status {
		case "SUCCEEDED":
			s.Succeeded++
		case "FAILED":
			s.Failed++
		case "DEAD_LETTER":
			s.DeadLetter++
		case "CANCELLED":
			s.Cancelled++
		case "TIMED_OUT":
			s.TimedOut++
		}
		if j.Attempts == 1 && j.Started != nil && j.Finished != nil && !j.Created.IsZero() {
			eligible := j.Created
			if j.Scheduled != nil && j.Scheduled.After(eligible) {
				eligible = *j.Scheduled
			}
			queue = append(queue, max(0, j.Started.Sub(eligible).Seconds()))
			end = append(end, max(0, j.Finished.Sub(j.Created).Seconds()))
		}
	}
	s.Queue = quantiles(queue)
	s.EndToEnd = quantiles(end)
	s.Throughput = float64(s.Succeeded) / s.Elapsed
	s.SubmitErrorRate = float64(s.SubmitErrors) / float64(s.Requests)
	if s.Completed > 0 {
		s.TerminalErrorRate = float64(s.Completed-s.Succeeded) / float64(s.Completed)
	}
	s.ErrorRate = float64(c.Count-s.Succeeded) / float64(c.Count)
	if ctx.Err() != nil {
		return s, ctx.Err()
	}
	return s, nil
}
