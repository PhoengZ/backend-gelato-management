package messaging

import (
	"context"
	"encoding/json"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

const exchangeName = "gelato.events" // topic exchange, matches infra/rabbitmq setup

// Publisher wraps a RabbitMQ channel. It is optional for this assignment:
// if RabbitMQ isn't configured/running, NewPublisher returns nil and every
// handler call just skips publishing instead of failing the request.
type Publisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewPublisher connects to RabbitMQ and declares the topic exchange.
// Returns (nil, nil) if url is empty, so the caller can run without a broker.
func NewPublisher(url string) (*Publisher, error) {
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

	if err := ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return nil, err
	}

	return &Publisher{conn: conn, ch: ch}, nil
}

// Publish sends a JSON-encoded event with the given routing key, e.g.
// "fulfillment.ticket_created" or "fulfillment.ticket_ready".
func (p *Publisher) Publish(ctx context.Context, routingKey string, payload any) {
	if p == nil {
		return // messaging disabled, nothing to do
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[messaging] failed to marshal event %s: %v", routingKey, err)
		return
	}

	err = p.ch.PublishWithContext(ctx, exchangeName, routingKey, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
	if err != nil {
		log.Printf("[messaging] failed to publish event %s: %v", routingKey, err)
	}
}

func (p *Publisher) Close() {
	if p == nil {
		return
	}
	p.ch.Close()
	p.conn.Close()
}
