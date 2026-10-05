package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/dashboard"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	redisinfra "github.com/Daniel-Cpz/FlowForge/internal/infrastructure/redis"
	"github.com/Daniel-Cpz/FlowForge/internal/realtime"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	httptransport "github.com/Daniel-Cpz/FlowForge/internal/transport/http"
	wsinfra "github.com/Daniel-Cpz/FlowForge/internal/transport/websocket"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	goredis "github.com/redis/go-redis/v9"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDashboardSummaryAuthority(t *testing.T) {
	pool := migratedDatabase(t)
	r := postgres.NewJobRepository(pool)
	s := service.New(r)
	ctx := t.Context()
	empty, err := s.DashboardSummary(ctx)
	if err != nil || len(empty.Jobs) != 8 || len(empty.Workers) != 5 || len(empty.Schedules) != 2 || empty.QueueDepth != 0 {
		t.Fatal(empty, err)
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	schedulingJob(t, r, 0, &future)
	schedulingJob(t, r, 0, nil, "gpu")
	schedulingJob(t, r, 0, nil)
	id := capWorker(t, r, "cpu")
	run := schedulingJob(t, r, 100, nil, "cpu")
	if _, err := r.Claim(ctx, run.ID, id); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	cancelled := schedulingJob(t, r, 0, nil)
	if _, err := r.Cancel(ctx, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	recurring(t, r, future, 60)
	v := recurring(t, r, future, 60)
	if _, err := r.CancelSchedule(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	response := send(api(pool), "GET", "/api/v1/dashboard/summary", "")
	var got dashboard.Summary
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || response.Code != 200 {
		t.Fatal(err, response.Code)
	}
	if got.Jobs["QUEUED"] != 3 || got.Jobs["RUNNING"] != 1 || got.Jobs["CANCELLED"] != 1 || got.QueueDepth != 2 || got.ActiveJobs != 1 || got.Workers["BUSY"] != 1 || got.Schedules["ACTIVE"] != 1 || got.Schedules["CANCELLED"] != 1 {
		t.Fatal(got)
	}
	checkHTTPError(t, send(api(pool), "GET", "/api/v1/dashboard/summary?limit=1", ""), 400, "INVALID_INPUT")
	pool.Close()
	checkHTTPError(t, send(api(pool), "GET", "/api/v1/dashboard/summary", ""), 500, "INTERNAL_ERROR")
}
func TestDashboardBoundedStablePages(t *testing.T) {
	pool := migratedDatabase(t)
	r := postgres.NewJobRepository(pool)
	ctx := t.Context()
	h := api(pool)
	var workers struct {
		Workers []worker.Worker `json:"workers"`
		Next    *string         `json:"next_cursor"`
	}
	response := send(h, "GET", "/api/v1/workers", "")
	if err := json.Unmarshal(response.Body.Bytes(), &workers); err != nil || workers.Workers == nil || len(workers.Workers) != 0 {
		t.Fatal("empty worker contract", err)
	}
	for range 105 {
		capWorker(t, r, "cpu")
	}
	if _, err := pool.Exec(ctx, `UPDATE workers SET last_heartbeat='2026-10-05T07:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[uuid.UUID]bool{}
	var previous string
	for {
		path := "/api/v1/workers?limit=20"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		response = send(h, "GET", path, "")
		if err := json.Unmarshal(response.Body.Bytes(), &workers); err != nil || response.Code != 200 || len(workers.Workers) > 20 {
			t.Fatal(response.Code, err)
		}
		for _, v := range workers.Workers {
			if seen[v.ID] || (previous != "" && v.ID.String() >= previous) {
				t.Fatal("unstable order")
			}
			seen[v.ID] = true
			previous = v.ID.String()
		}
		if workers.Next == nil {
			break
		}
		cursor = *workers.Next
		if _, err := pool.Exec(ctx, `UPDATE workers SET last_heartbeat=clock_timestamp()`); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 105 {
		t.Fatal("missing workers", len(seen))
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	for range 5 {
		recurring(t, r, future, 60)
	}
	if _, err := pool.Exec(ctx, `UPDATE job_schedules SET created_at='2026-10-05T07:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	var schedules struct {
		Schedules []schedule.Schedule `json:"schedules"`
		Next      *string             `json:"next_cursor"`
	}
	seen = map[uuid.UUID]bool{}
	cursor = ""
	previous = ""
	for {
		path := "/api/v1/schedules?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		response = send(h, "GET", path, "")
		if err := json.Unmarshal(response.Body.Bytes(), &schedules); err != nil || response.Code != 200 || len(schedules.Schedules) > 2 {
			t.Fatal(err, response.Code)
		}
		for _, v := range schedules.Schedules {
			if seen[v.ID] || (previous != "" && v.ID.String() >= previous) {
				t.Fatal("schedule tie order")
			}
			seen[v.ID] = true
			previous = v.ID.String()
		}
		if schedules.Next == nil {
			break
		}
		cursor = *schedules.Next
	}
	if len(seen) != 5 {
		t.Fatal("schedule page loss")
	}
	for _, endpoint := range []string{"workers", "schedules"} {
		for _, query := range []string{"limit=0", "limit=101", "limit=1&limit=2", "cursor=garbage", "unknown=1", "cursor="} {
			checkHTTPError(t, send(h, "GET", "/api/v1/"+endpoint+"?"+query, ""), 400, "INVALID_INPUT")
		}
	}
}
func TestDurableChangesAfterCommitAndHeartbeatCoalescing(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	events := []realtime.Event{}
	r.ObserveChanges(func(kind string, id uuid.UUID, status string) {
		switch kind {
		case "job.changed":
			j, err := r.GetByID(ctx, id)
			if err != nil || string(j.Status) != status {
				t.Fatal("hint before commit", err)
			}
		case "schedule.changed":
			v, err := r.GetSchedule(ctx, id)
			if err != nil || v.Status != status {
				t.Fatal("schedule hint before commit", err)
			}
		case "worker.changed":
			var actual string
			if err := pool.QueryRow(ctx, `SELECT status FROM workers WHERE worker_id=$1`, id).Scan(&actual); err != nil || actual != status {
				t.Fatal("worker hint before commit", err)
			}
		}
		events = append(events, realtime.New(kind, id, status))
	})
	id := capWorker(t, r, "cpu")
	if err := r.Heartbeat(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	count := len(events)
	for range 3 {
		if err := r.Heartbeat(ctx, id, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != count {
		t.Fatal("heartbeat storm")
	}
	j := schedulingJob(t, r, 0, nil)
	claimed, err := r.Claim(ctx, j.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(ctx, claimed, job.Failed, []byte(`{"error":"test"}`), job.Failure{Class: job.Retryable, Code: "execution_failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET retry_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PromoteRetries(ctx, 100); err != nil {
		t.Fatal(err)
	}
	claimed, err = r.Claim(ctx, j.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(ctx, claimed, job.Failed, []byte(`{"error":"test"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RedriveDeadLetter(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = r.Claim(ctx, j.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_expiry=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecoverExpired(ctx, 100); err != nil {
		t.Fatal(err)
	}
	v := recurring(t, r, time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond), 60)
	if _, err := r.MaterializeDue(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CancelSchedule(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDraining(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := r.StopWorker(ctx, id, "graceful_shutdown"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE job_dispatch ADD CONSTRAINT ui_rollback CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	before := len(events)
	if _, err := service.New(r).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{}`)}); err == nil {
		t.Fatal("expected transaction rollback")
	}
	if len(events) != before {
		t.Fatal("failed transaction emitted")
	}
	statuses := map[string]bool{}
	for _, e := range events {
		statuses[e.Event+":"+e.Hint.Status] = true
	}
	for _, s := range []string{"job.changed:QUEUED", "job.changed:RUNNING", "job.changed:RETRYING", "job.changed:DEAD_LETTER", "schedule.changed:ACTIVE", "schedule.changed:CANCELLED", "worker.changed:ONLINE", "worker.changed:IDLE", "worker.changed:DRAINING", "worker.changed:OFFLINE"} {
		if !statuses[s] {
			t.Fatal("missing hint", s)
		}
	}
}
func TestPublishFailureCannotRollbackBusiness(t *testing.T) {
	pool := migratedDatabase(t)
	c := redisinfra.NewClient("127.0.0.1:1", "", 0)
	defer c.Close()
	var log bytes.Buffer
	b := redisinfra.NewEvents(t.Context(), c, "flowforge:test:ui:"+uuid.NewString(), slog.New(slog.NewTextHandler(&log, nil)))
	r := postgres.NewJobRepository(pool).ObserveChanges(b.Change)
	j := schedulingJob(t, r, 0, nil)
	if _, err := r.Cancel(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(550 * time.Millisecond)
	b.Close()
	v, err := r.GetByID(t.Context(), j.ID)
	if err != nil || v.Status != job.Cancelled {
		t.Fatal("publish failure changed durable result", err)
	}
	if !strings.Contains(log.String(), "ui_hint_publish_failed") {
		t.Fatal("failure was not observed")
	}
}
func TestRedisFanoutAndSubscriptionReconnect(t *testing.T) {
	_, client, key, _ := redisQueue(t)
	channel := key + ":ui:v1"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	publisher := redisinfra.NewEvents(ctx, client, channel, testLogger())
	defer publisher.Close()
	// Independent TCP clients represent two API subscribers. The process-level
	// isolated smoke additionally runs API and workers in separate containers.
	opts := *client.Options()
	c2 := redisinfra.NewClient(opts.Addr, opts.Password, opts.DB)
	defer c2.Close()
	buses := []*redisinfra.Events{publisher, redisinfra.NewEvents(ctx, c2, channel, testLogger())}
	defer buses[1].Close()
	hubs := []*wsinfra.Hub{}
	conns := []*ws.Conn{}
	done := []chan struct{}{}
	for i, b := range buses {
		h := wsinfra.New(wsinfra.DefaultLimits(), nil)
		hubs = append(hubs, h)
		server := httptest.NewServer(httptransport.NewRouterWithRealtime(service.New(postgres.NewJobRepository(migratedDatabase(t))), testLogger(), h))
		defer server.Close()
		defer h.Close()
		started := make(chan bool, 8)
		d := make(chan struct{})
		done = append(done, d)
		go func() {
			defer close(d)
			b.Subscribe(ctx, h.Broadcast, func(live bool) {
				h.SetLive(live)
				select {
				case started <- live:
				default:
				}
			})
		}()
		select {
		case live := <-started:
			if !live {
				t.Fatal("subscription not live")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("subscribe timeout", i)
		}
		c, _, err := ws.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, err := c.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.New()
	publisher.Change("job.changed", id, "RUNNING")
	for _, c := range conns {
		_, data, err := c.ReadMessage()
		e, eerr := realtime.Decode(data)
		if err != nil || eerr != nil || e.ResourceID != id {
			t.Fatal("fanout missing", err, eerr)
		}
	}
	// Kill only the uniquely named test subscription socket, not shared clients.
	unique := "ff_ui_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// A separate named subscriber is guaranteed to expose its pubsub socket.
	opts.ClientName = unique
	reconnectClient := goredis.NewClient(&opts)
	defer reconnectClient.Close()
	bus := redisinfra.NewEvents(ctx, reconnectClient, channel, testLogger())
	defer bus.Close()
	states := make(chan bool, 16)
	received := make(chan realtime.Event, 4)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		bus.Subscribe(ctx, func(e realtime.Event) { received <- e }, func(v bool) { states <- v })
	}()
	select {
	case <-states:
	case <-time.After(3 * time.Second):
		t.Fatal("named subscription failed")
	}
	list, err := client.ClientList(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	killed := false
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, "name="+unique) && strings.Contains(line, "flags=P") {
			var socketID string
			for _, f := range strings.Fields(line) {
				if strings.HasPrefix(f, "id=") {
					socketID = strings.TrimPrefix(f, "id=")
				}
			}
			if socketID != "" {
				if err := client.Do(ctx, "CLIENT", "KILL", "ID", socketID).Err(); err != nil {
					t.Fatal(err)
				}
				killed = true
			}
		}
	}
	if !killed {
		t.Fatal("isolated PubSub socket not found", fmt.Sprint(len(list)))
	}
	select {
	case live := <-states:
		if live {
			t.Fatal("disconnect not signalled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect timeout")
	}
	select {
	case live := <-states:
		if !live {
			t.Fatal("reconnect failed")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("reconnect timeout")
	}
	publisher.Change("schedule.changed", id, "CANCELLED")
	select {
	case e := <-received:
		if e.Event != "schedule.changed" {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reconnected subscriber missed hint")
	}
	cancel()
	for _, d := range append(done, joined) {
		select {
		case <-d:
		case <-time.After(3 * time.Second):
			t.Fatal("subscriber shutdown leaked")
		}
	}
}
