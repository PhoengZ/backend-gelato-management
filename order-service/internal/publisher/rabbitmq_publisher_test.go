package publisher

import (
	"context"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type fakeChannel struct {
	seq        uint64
	onPublish  func(amqp.Publishing)
	closed     bool
	publishErr error
}

func (c *fakeChannel) GetNextPublishSeqNo() uint64 { return c.seq }
func (c *fakeChannel) Close() error                { c.closed = true; return nil }
func (c *fakeChannel) PublishWithContext(_ context.Context, _, _ string, _, _ bool, msg amqp.Publishing) error {
	if c.publishErr != nil {
		return c.publishErr
	}
	c.seq++
	if c.onPublish != nil {
		c.onPublish(msg)
	}
	return nil
}

func TestPublishDoesNotAcceptStaleACK(t *testing.T) {
	acks := make(chan amqp.Confirmation, 2)
	ch := &fakeChannel{seq: 2}
	ch.onPublish = func(amqp.Publishing) { acks <- amqp.Confirmation{DeliveryTag: 1, Ack: true} }
	p := &rabbitPublisher{ch: ch, acks: acks, returns: make(chan amqp.Return, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.Publish(ctx, "order.placed", "second", nil); err == nil {
		t.Fatal("stale ACK marked the second event as successfully published")
	}
}

func TestPublishRetiresFailedSessionAndReconnects(t *testing.T) {
	for _, failure := range []string{"timeout", "return_then_ack", "closed_acks", "closed_returns", "nack", "publish_error", "wrong_tag"} {
		t.Run(failure, func(t *testing.T) {
			acks := make(chan amqp.Confirmation, 1)
			returns := make(chan amqp.Return, 1)
			old := &fakeChannel{seq: 1}
			old.onPublish = func(msg amqp.Publishing) {
				switch failure {
				case "return_then_ack":
					returns <- amqp.Return{MessageId: msg.MessageId}
					acks <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
				case "closed_acks":
					close(acks)
				case "closed_returns":
					close(returns)
				case "nack":
					acks <- amqp.Confirmation{DeliveryTag: 1, Ack: false}
				case "wrong_tag":
					acks <- amqp.Confirmation{DeliveryTag: 99, Ack: true}
				}
			}
			if failure == "publish_error" {
				old.publishErr = errors.New("connection lost")
			}
			freshACKs := make(chan amqp.Confirmation, 1)
			fresh := &fakeChannel{seq: 1}
			fresh.onPublish = func(msg amqp.Publishing) {
				if msg.MessageId != "event" {
					t.Fatal("retry changed event ID")
				}
				freshACKs <- amqp.Confirmation{DeliveryTag: fresh.seq - 1, Ack: true}
			}
			connects := 0
			p := &rabbitPublisher{ch: old, acks: acks, returns: returns, connect: func(context.Context) (*rabbitPublisher, error) {
				connects++
				return &rabbitPublisher{ch: fresh, acks: freshACKs, returns: make(chan amqp.Return, 1)}, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := p.Publish(ctx, "order.placed", "event", nil); err == nil {
				t.Fatal("failed publish reported success")
			}
			if !old.closed {
				t.Fatal("failed session was retained")
			}
			if failure == "timeout" {
				acks <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
			}
			if err := p.Publish(context.Background(), "order.placed", "event", nil); err != nil {
				t.Fatal(err)
			}
			if connects != 1 || fresh.seq != 2 {
				t.Fatal("retry did not publish on a fresh session")
			}
		})
	}
}

func TestPublishRequiresFreshConfirmationAfterTimeout(t *testing.T) {
	oldACKs := make(chan amqp.Confirmation, 1)
	old := &fakeChannel{seq: 1}
	newCh := &fakeChannel{seq: 1}
	p := &rabbitPublisher{ch: old, acks: oldACKs, returns: make(chan amqp.Return, 1), connect: func(context.Context) (*rabbitPublisher, error) {
		return &rabbitPublisher{ch: newCh, acks: make(chan amqp.Confirmation, 1), returns: make(chan amqp.Return, 1)}, nil
	}}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := p.Publish(ctx, "order.placed", "event", nil)
		cancel()
		if err == nil {
			t.Fatal("publish succeeded without its own confirmation")
		}
		if attempt == 0 {
			oldACKs <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
		}
	}
}

func TestCloseIsFinalAndIdempotent(t *testing.T) {
	p := &rabbitPublisher{ch: &fakeChannel{seq: 1}}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Publish(context.Background(), "order.placed", "event", nil); err == nil {
		t.Fatal("closed publisher accepted publish")
	}
}

func TestPublishRetriesAfterReconnectFailure(t *testing.T) {
	acks := make(chan amqp.Confirmation, 1)
	ch := &fakeChannel{seq: 1}
	ch.onPublish = func(amqp.Publishing) { acks <- amqp.Confirmation{DeliveryTag: 1, Ack: true} }
	connects := 0
	p := &rabbitPublisher{connect: func(context.Context) (*rabbitPublisher, error) {
		connects++
		if connects < 3 {
			return nil, errors.New("broker unavailable")
		}
		return &rabbitPublisher{ch: ch, acks: acks, returns: make(chan amqp.Return, 1)}, nil
	}}
	for i := 0; i < 2; i++ {
		if err := p.Publish(context.Background(), "order.placed", "event", nil); err == nil {
			t.Fatal("failed reconnect reported success")
		}
	}
	if err := p.Publish(context.Background(), "order.placed", "event", nil); err != nil {
		t.Fatal(err)
	}
	if connects != 3 {
		t.Fatal("publisher stopped retrying reconnect")
	}
}
