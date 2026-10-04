package job

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

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
	typ := strings.TrimSpace(in.Type)
	maxAttempts, timeout := 3, 300
	if in.MaxAttempts != nil {
		maxAttempts = *in.MaxAttempts
	}
	if in.Timeout != nil {
		timeout = *in.Timeout
	}
	if typ == "" || utf8.RuneCountInString(typ) > 128 || strings.ContainsRune(typ, '\x00') ||
		in.Priority < 0 || in.Priority > 100 || maxAttempts < 1 || maxAttempts > 100 ||
		timeout < 1 || timeout > 86400 || len(in.Payload) > 1<<20 || !json.Valid(in.Payload) {
		return nil, domain.ErrInvalidInput
	}
	if in.IdempotencyKey != nil && (strings.TrimSpace(*in.IdempotencyKey) == "" ||
		utf8.RuneCountInString(*in.IdempotencyKey) > 255 || strings.ContainsRune(*in.IdempotencyKey, '\x00')) {
		return nil, domain.ErrInvalidInput
	}
	j := &domain.Job{ID: uuid.New(), Type: typ, Status: domain.Queued, Priority: in.Priority,
		Payload: append(json.RawMessage(nil), in.Payload...), MaxAttempts: maxAttempts, Timeout: timeout,
		IdempotencyKey: in.IdempotencyKey, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := s.repo.Create(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]domain.Job, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.List(ctx, limit, offset)
}
