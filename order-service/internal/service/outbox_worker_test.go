package service

import (
	"context"
	"errors"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/repository"
)

type outboxTestRepo struct {
	repository.OrderRepository
	event  domain.OutboxEvent
	marked []string
}

func (r *outboxTestRepo) PendingOutbox(context.Context, int) ([]domain.OutboxEvent, error) {
	return []domain.OutboxEvent{r.event}, nil
}
func (r *outboxTestRepo) MarkOutboxPublished(_ context.Context, id string) error {
	r.marked = append(r.marked, id)
	return nil
}

type outboxTestPublisher struct {
	err error
	ids []string
}

func (p *outboxTestPublisher) Publish(_ context.Context, _, id string, _ []byte) error {
	p.ids = append(p.ids, id)
	return p.err
}
func (p *outboxTestPublisher) Close() error { return nil }

func TestOutboxMarksOnlyConfirmedPublishAndRetriesSameID(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, errors.New("unroutable"), errors.New("connection lost")} {
		t.Run(failure.Error(), func(t *testing.T) {
			repo := &outboxTestRepo{event: domain.OutboxEvent{ID: "event", RoutingKey: "order.placed"}}
			pub := &outboxTestPublisher{err: failure}
			if err := publishPending(context.Background(), repo, pub); !errors.Is(err, failure) {
				t.Fatalf("got %v", err)
			}
			if len(repo.marked) != 0 {
				t.Fatal("failed publish marked outbox as published")
			}
			pub.err = nil
			if err := publishPending(context.Background(), repo, pub); err != nil {
				t.Fatal(err)
			}
			if len(repo.marked) != 1 || repo.marked[0] != "event" {
				t.Fatal("confirmed event not marked")
			}
			if len(pub.ids) != 2 || pub.ids[0] != pub.ids[1] {
				t.Fatal("retry changed event ID")
			}
		})
	}
}
