package publisher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
	Close() error
}

type publishChannel interface {
	PublishWithContext(context.Context, string, string, bool, bool, amqp091.Publishing) error
	GetNextPublishSeqNo() uint64
	Close() error
}

type rabbitPublisher struct {
	conn      *amqp091.Connection
	transport net.Conn
	ch        publishChannel
	acks      <-chan amqp091.Confirmation
	returns   <-chan amqp091.Return
	mu        sync.Mutex
	connect   func(context.Context) (*rabbitPublisher, error)
	closed    bool
}

func NewRabbitMQPublisher(url string) (Publisher, error) {
	if url == "" {
		return nil, errors.New("RabbitMQ URL is required")
	}
	p := &rabbitPublisher{connect: func(ctx context.Context) (*rabbitPublisher, error) {
		return connectRabbitMQ(ctx, url)
	}}
	if err := p.ensureConnected(context.Background()); err != nil {
		return nil, err
	}
	return p, nil
}

func connectRabbitMQ(ctx context.Context, url string) (*rabbitPublisher, error) {
	// Bound both TCP connection and AMQP handshake/setup by the publish deadline.
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var transport net.Conn
	conn, err := amqp091.DialConfig(url, amqp091.Config{Dial: func(network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{Deadline: deadline}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if err := c.SetDeadline(deadline); err != nil {
			_ = c.Close()
			return nil, err
		}
		transport = c
		return c, nil
	}})
	if err != nil {
		return nil, err
	}
	// Dial clears the handshake deadline; restore it for the setup RPCs.
	if err := transport.SetDeadline(deadline); err != nil {
		_ = conn.CloseDeadline(deadline)
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.CloseDeadline(deadline)
		return nil, err
	}
	if err = ch.ExchangeDeclare("order", "topic", true, false, false, false, nil); err != nil {
		_ = conn.CloseDeadline(deadline)
		return nil, err
	}
	if err = ch.Confirm(false); err != nil {
		_ = conn.CloseDeadline(deadline)
		return nil, err
	}
	if err := transport.SetDeadline(time.Time{}); err != nil {
		_ = conn.CloseDeadline(deadline)
		return nil, err
	}
	return &rabbitPublisher{conn: conn, transport: transport, ch: ch, acks: ch.NotifyPublish(make(chan amqp091.Confirmation, 1)), returns: ch.NotifyReturn(make(chan amqp091.Return, 1))}, nil
}

// Called under mu. A new channel starts its own delivery-tag sequence and listeners.
func (p *rabbitPublisher) ensureConnected(ctx context.Context) error {
	if p.closed {
		return errors.New("RabbitMQ publisher is closed")
	}
	if p.ch != nil {
		return nil
	}
	session, err := p.connect(ctx)
	if err != nil {
		return err
	}
	p.conn, p.ch, p.acks, p.returns = session.conn, session.ch, session.acks, session.returns
	p.transport = session.transport
	return nil
}

func (p *rabbitPublisher) discardConnection() error {
	var err error
	if p.conn != nil {
		err = p.conn.CloseDeadline(time.Now().Add(time.Second))
	} else if p.ch != nil {
		err = p.ch.Close()
	}
	p.conn, p.ch, p.acks, p.returns = nil, nil, nil, nil
	p.transport = nil
	return err
}

func (p *rabbitPublisher) Publish(ctx context.Context, routingKey, eventID string, body []byte) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}
	// Any failure retires this session, so late confirms/returns cannot acknowledge
	// a later retry. The outbox worker retries with the same CloudEvent ID.
	defer func() {
		if err == nil && p.transport != nil {
			err = p.transport.SetDeadline(time.Time{})
		}
		if err != nil {
			_ = p.discardConnection()
		}
	}()
	if p.transport != nil {
		deadline := time.Now().Add(5 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := p.transport.SetDeadline(deadline); err != nil {
			return err
		}
	}
	seq := p.ch.GetNextPublishSeqNo()
	err = p.ch.PublishWithContext(ctx, "order", routingKey, true, false, amqp091.Publishing{
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
		if confirmation.DeliveryTag != seq {
			return fmt.Errorf("unexpected RabbitMQ confirmation tag %d, expected %d", confirmation.DeliveryTag, seq)
		}
		if !confirmation.Ack {
			return errors.New("RabbitMQ negatively acknowledged event")
		}
		// RabbitMQ sends basic.return before basic.ack for mandatory publishes.
		// amqp091 dispatches them in wire order, buffering the return before the
		// confirmation reaches us; check it even if select chose the ACK first.
		select {
		case returned, ok := <-p.returns:
			if !ok {
				return errors.New("RabbitMQ publisher return channel closed")
			}
			return fmt.Errorf("RabbitMQ returned unroutable event %s", returned.MessageId)
		default:
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *rabbitPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.discardConnection()
}
