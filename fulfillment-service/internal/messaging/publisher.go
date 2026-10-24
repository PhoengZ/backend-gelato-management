package messaging

import (
	"context"
	"errors"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ExchangeName is the topic exchange Fulfillment owns and publishes to.
const ExchangeName = "fulfillment"

// errPublishNacked means the broker refused a message; the channel is still usable.
var errPublishNacked = errors.New("broker nacked the published message")

// Broker wraps the RabbitMQ connection and the confirm-mode channel used for
// publishing. Publishing is single-goroutine (the outbox relay).
type Broker struct {
	conn  *amqp.Connection
	pubCh *amqp.Channel
}

// Connect opens the broker connection and declares the fulfillment exchange.
// It returns (nil, nil) when url is empty so the service can run without a broker.
func Connect(url string) (*Broker, error) {
	if url == "" {
		log.Println("[messaging] RABBITMQ_URL not set, running without a message broker")
		return nil, nil
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := ch.ExchangeDeclare(ExchangeName, "topic", true, false, false, false, nil); err != nil {
		conn.Close()
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		conn.Close()
		return nil, err
	}

	return &Broker{conn: conn, pubCh: ch}, nil
}

// Publish sends a persistent JSON message and waits for the broker's confirm.
func (b *Broker) Publish(ctx context.Context, routingKey string, body []byte) error {
	return publishConfirmed(ctx, b.pubCh, ExchangeName, routingKey, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         body,
	})
}

// publishConfirmed publishes on a confirm-mode channel and waits for the ack.
func publishConfirmed(ctx context.Context, ch *amqp.Channel, exchange, key string, msg amqp.Publishing) error {
	conf, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, key, false, false, msg)
	if err != nil {
		return err
	}
	ok, err := conf.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errPublishNacked
	}
	return nil
}

func (b *Broker) Close() {
	if b == nil {
		return
	}
	b.pubCh.Close()
	b.conn.Close()
}
