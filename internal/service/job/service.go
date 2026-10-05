package job

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

type Service struct{ repo domain.Repository }

func New(repo domain.Repository) *Service { return &Service{repo: repo} }

type CreateInput struct {
	Type           string          `json:"type"`
	Priority       int             `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	MaxAttempts    *int            `json:"max_attempts"`
	Timeout        *int            `json:"timeout"`
	IdempotencyKey *string         `json:"idempotency_key"`
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*domain.Job, error) {
	j, _, err := s.CreateWithDisposition(ctx, in)
	return j, err
}

func (s *Service) CreateWithDisposition(ctx context.Context, in CreateInput) (*domain.Job, domain.CreateDisposition, error) {
	if len(in.Payload) > 1<<20 {
		return nil, "", domain.ErrInvalidInput
	}
	typ := strings.TrimSpace(in.Type)
	maxAttempts, timeout := 3, 300
	if in.MaxAttempts != nil {
		maxAttempts = *in.MaxAttempts
	}
	if in.Timeout != nil {
		timeout = *in.Timeout
	}
	j := &domain.Job{ID: uuid.New(), Type: typ, Status: domain.Queued, Priority: in.Priority,
		Payload: append(json.RawMessage(nil), in.Payload...), MaxAttempts: maxAttempts, Timeout: timeout,
		IdempotencyKey: in.IdempotencyKey, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if in.IdempotencyKey != nil {
		key := *in.IdempotencyKey
		j.IdempotencyKey = &key
	}
	if err := j.Validate(); err != nil {
		return nil, "", err
	}
	disposition, err := s.repo.Create(ctx, j)
	if err != nil {
		return nil, "", err
	}
	return j, disposition, nil
}

func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, limit int, after *domain.PageCursor) (*domain.Page, error) {
	if limit < 1 || limit > 100 || (after != nil && !after.Valid()) {
		return nil, domain.ErrInvalidInput
	}
	jobs, err := s.repo.List(ctx, limit+1, after)
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []domain.Job{}
	}
	page := &domain.Page{Jobs: jobs}
	if len(jobs) > limit {
		page.Jobs = jobs[:limit]
		last := page.Jobs[limit-1]
		page.NextCursor = &domain.PageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}
