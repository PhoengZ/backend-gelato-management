package publisher

import (
	"context"
	"encoding/json"
	"log"

	"order-service/internal/domain"

	amqp "github.com/rabbitmq/amqp091-go"
)

type RabbitMQPublisher interface {
	PublishOrderPlaced(ctx context.Context, event domain.OrderPlacedEvent) error
	PublishOrderCancelled(ctx context.Context, event domain.OrderCancelledEvent) error
	Close() error
}

type rabbitmqPublisherImpl struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewRabbitMQPublisher(url string) (RabbitMQPublisher, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	// Declare exchange
	err = ch.ExchangeDeclare(
		"orders_exchange", // name
		"topic",           // type
		true,              // durable
		false,             // auto-deleted
		false,             // internal
		false,             // no-wait
		nil,               // arguments
	)
	if err != nil {
		return nil, err
	}

	log.Println("Connected to RabbitMQ at", url)
	return &rabbitmqPublisherImpl{
		conn: conn,
		ch:   ch,
	}, nil
}

func (p *rabbitmqPublisherImpl) PublishOrderPlaced(ctx context.Context, event domain.OrderPlacedEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return p.ch.PublishWithContext(ctx,
		"orders_exchange", // exchange
		"order.placed",    // routing key
		false,             // mandatory
		false,             // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
}

func (p *rabbitmqPublisherImpl) PublishOrderCancelled(ctx context.Context, event domain.OrderCancelledEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return p.ch.PublishWithContext(ctx,
		"orders_exchange",  // exchange
		"order.cancelled",  // routing key
		false,              // mandatory
		false,              // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
}

func (p *rabbitmqPublisherImpl) Close() error {
	p.ch.Close()
	return p.conn.Close()
}
