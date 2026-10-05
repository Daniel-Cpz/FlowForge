package job

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

type repositoryStub struct {
	created *domain.Job
	rows    []domain.Job
	limit   int
	after   *domain.PageCursor
	err     error
}

func (r *repositoryStub) Create(ctx context.Context, j *domain.Job) (domain.CreateDisposition, error) {
	r.created = j
	return domain.Created, r.err
}
func (r *repositoryStub) GetByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	return nil, r.err
}
func (r *repositoryStub) List(ctx context.Context, limit int, after *domain.PageCursor) ([]domain.Job, error) {
	r.limit, r.after = limit, after
	return r.rows, r.err
}

func TestCreateInvariantsAndInputOwnership(t *testing.T) {
	repo := &repositoryStub{}
	key := " key "
	input := CreateInput{Type: "  inner  space  ", Payload: json.RawMessage(`{"x":1}`), IdempotencyKey: &key}
	j, err := New(repo).Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if j.Type != "inner  space" || j.Status != domain.Queued || j.AttemptCount != 0 ||
		j.MaxAttempts != 3 || j.Timeout != 300 || j.Priority != 0 || j.ID == uuid.Nil ||
		j.CreatedAt.IsZero() || j.CreatedAt.Location() != time.UTC || j.CreatedAt.Nanosecond()%1000 != 0 ||
		j.Result != nil || j.AssignedWorker != nil || j.LeaseExpiry != nil || j.StartedAt != nil || j.FinishedAt != nil || repo.created != j {
		t.Fatal("invalid new job", j)
	}
	input.Payload[0] = '['
	key = "changed"
	if string(j.Payload) != `{"x":1}` || *j.IdempotencyKey != " key " {
		t.Fatal("caller mutations changed job input")
	}
}

func TestInvalidServiceInputDoesNotPersist(t *testing.T) {
	zero := 0
	blank := " "
	for _, input := range []CreateInput{
		{Type: "x"}, {Payload: json.RawMessage(`null`)},
		{Type: "x", Payload: json.RawMessage(`{`)},
		{Type: "x", Payload: json.RawMessage(`null`), Priority: -1},
		{Type: "x", Payload: json.RawMessage(`null`), MaxAttempts: &zero},
		{Type: "x", Payload: json.RawMessage(`null`), Timeout: &zero},
		{Type: "x", Payload: json.RawMessage(`null`), IdempotencyKey: &blank},
	} {
		repo := &repositoryStub{}
		if _, err := New(repo).Create(context.Background(), input); !errors.Is(err, domain.ErrInvalidInput) || repo.created != nil {
			t.Fatal("invalid input persisted", err)
		}
	}
}

func TestListLookahead(t *testing.T) {
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rows := []domain.Job{{ID: uuid.New(), CreatedAt: timestamp}, {ID: uuid.New(), CreatedAt: timestamp}, {ID: uuid.New(), CreatedAt: timestamp}}
	after := &domain.PageCursor{ID: uuid.New(), CreatedAt: timestamp}
	repo := &repositoryStub{rows: rows}
	page, err := New(repo).List(context.Background(), 2, after)
	if err != nil || repo.limit != 3 || repo.after != after || len(page.Jobs) != 2 || page.NextCursor == nil ||
		page.NextCursor.ID != rows[1].ID || page.NextCursor.CreatedAt != rows[1].CreatedAt {
		t.Fatal("cursor must be last returned row", page, err)
	}
	for _, result := range [][]domain.Job{nil, {}, rows[:2]} {
		repo.rows = result
		page, err = New(repo).List(context.Background(), 2, nil)
		if err != nil || page.NextCursor != nil || page.Jobs == nil {
			t.Fatal("final or empty page", page, err)
		}
	}
	for _, limit := range []int{0, -1, 101} {
		repo.limit = 0
		if _, err := New(repo).List(context.Background(), limit, nil); !errors.Is(err, domain.ErrInvalidInput) || repo.limit != 0 {
			t.Fatal("invalid limit reached repository", err)
		}
	}
	if _, err := New(repo).List(context.Background(), 20, &domain.PageCursor{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("invalid typed boundary accepted", err)
	}
	sentinel := errors.New("dependency failure")
	repo.err = sentinel
	if _, err := New(repo).List(context.Background(), 20, nil); !errors.Is(err, sentinel) {
		t.Fatal("repository error lost", err)
	}
}
