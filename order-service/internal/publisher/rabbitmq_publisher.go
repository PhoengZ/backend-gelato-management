package publisher

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rabbitmq/amqp091-go"
)

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
	Close() error
}

type rabbitPublisher struct {
	conn    *amqp091.Connection
	ch      *amqp091.Channel
	acks    <-chan amqp091.Confirmation
	returns <-chan amqp091.Return
	mu      sync.Mutex
}

func NewRabbitMQPublisher(url string) (Publisher, error) {
	if url == "" {
		return nil, errors.New("RabbitMQ URL is required")
	}
	conn, err := amqp091.Dial(url)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err = ch.ExchangeDeclare("order", "topic", true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	if err = ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	return &rabbitPublisher{conn: conn, ch: ch, acks: ch.NotifyPublish(make(chan amqp091.Confirmation, 1)), returns: ch.NotifyReturn(make(chan amqp091.Return, 1))}, nil
}

func (p *rabbitPublisher) Publish(ctx context.Context, routingKey, eventID string, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.ch.PublishWithContext(ctx, "order", routingKey, true, false, amqp091.Publishing{
		ContentType: "application/cloudevents+json", DeliveryMode: amqp091.Persistent,
		MessageId: eventID, Type: routingKey, Body: body,
	})
	if err != nil {
		return err
	}
	select {
	case returned, ok := <-p.returns:
		if ok && returned.MessageId == eventID {
			return fmt.Errorf("RabbitMQ returned unroutable event %s", eventID)
		}
		// A return for a different id cannot occur while publishes are serialized.
		return fmt.Errorf("unexpected RabbitMQ return for event %s", returned.MessageId)
	case confirmation, ok := <-p.acks:
		if !ok {
			return errors.New("RabbitMQ publisher confirmation channel closed")
		}
		if !confirmation.Ack {
			return errors.New("RabbitMQ negatively acknowledged event")
		}
		select {
		case returned, ok := <-p.returns:
			if ok {
				return fmt.Errorf("RabbitMQ returned unroutable event %s", returned.MessageId)
			}
		default:
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *rabbitPublisher) Close() error {
	chErr := p.ch.Close()
	connErr := p.conn.Close()
	if chErr != nil {
		return chErr
	}
	return connErr
}
