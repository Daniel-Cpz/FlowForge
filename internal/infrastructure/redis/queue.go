package redis

import (
	"context"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	redisclient "github.com/redis/go-redis/v9"
	"time"
)

const Group = "flowforge-workers-v1"

type Queue struct {
	client        *redisclient.Client
	stream, group string
}

func NewQueue(client *redisclient.Client, stream, group string) *Queue {
	return &Queue{client, stream, group}
}
func (q *Queue) Publish(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return q.client.XAdd(ctx, &redisclient.XAddArgs{Stream: q.stream, Values: map[string]any{"v": "1", "job_id": id.String()}}).Err()
}
func (q *Queue) ensure(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, q.stream, q.group, "0").Err()
	if err != nil && !redisclient.HasErrorPrefix(err, "BUSYGROUP") {
		return err
	}
	return nil
}
func (q *Queue) Receive(ctx context.Context, consumer string) (*job.Delivery, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := q.ensure(ctx); err != nil {
		return nil, err
	}
	streams, err := q.client.XReadGroup(ctx, &redisclient.XReadGroupArgs{Group: q.group, Consumer: consumer,
		Streams: []string{q.stream, ">"}, Count: 1, Block: 500 * time.Millisecond}).Result()
	if errors.Is(err, redisclient.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return nil, nil
	}
	m := streams[0].Messages[0]
	id, idOK := m.Values["job_id"].(string)
	version, vOK := m.Values["v"].(string)
	return &job.Delivery{MessageID: m.ID, JobID: id, Version: version, Malformed: !idOK || !vOK || len(m.Values) != 2}, nil
}
func (q *Queue) Ack(ctx context.Context, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Zero means already acknowledged: idempotent ACK success.
	return q.client.XAck(ctx, q.stream, q.group, message).Err()
}
