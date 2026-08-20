package messaging

import (
	"context"
	"encoding/json"
	"log"

	"analytics-service/internal/models"
	"analytics-service/internal/service"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Consumer struct {
	conn    *amqp.Connection
	ch      *amqp.Channel
	service service.AnalyticsService
}

func NewConsumer(rabbitURL string, svc service.AnalyticsService) (*Consumer, error) {
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	return &Consumer{
		conn:    conn,
		ch:      ch,
		service: svc,
	}, nil
}

func (c *Consumer) Start() error {
	// Declare exchanges
	err := c.ch.ExchangeDeclare(
		"order", "topic", true, false, false, false, nil,
	)
	if err != nil {
		return err
	}

	err = c.ch.ExchangeDeclare(
		"inventory", "topic", true, false, false, false, nil,
	)
	if err != nil {
		return err
	}

	// Declare queue
	q, err := c.ch.QueueDeclare(
		"analytics_queue", true, false, false, false, nil,
	)
	if err != nil {
		return err
	}

	// Bind queue
	err = c.ch.QueueBind(
		q.Name, "order.success", "order", false, nil,
	)
	if err != nil {
		return err
	}

	err = c.ch.QueueBind(
		q.Name, "inventory.waste", "inventory", false, nil,
	)
	if err != nil {
		return err
	}

	msgs, err := c.ch.Consume(
		q.Name, "", false, false, false, false, nil,
	)
	if err != nil {
		return err
	}

	go func() {
		for d := range msgs {
			c.processMessage(d)
		}
	}()

	log.Println("RabbitMQ Consumer started listening on analytics_queue")
	return nil
}

func (c *Consumer) processMessage(d amqp.Delivery) {
	ctx := context.Background()
	switch d.RoutingKey {
	case "order.success":
		var msg models.OrderSuccessMessage
		if err := json.Unmarshal(d.Body, &msg); err == nil {
			err = c.service.ProcessOrderSuccess(ctx, msg)
			if err != nil {
				log.Println("Error processing order success:", err)
			} else {
				d.Ack(false)
			}
		} else {
			log.Println("Error unmarshaling order success:", err)
		}

	case "inventory.waste":
		var msg models.InventoryWasteMessage
		if err := json.Unmarshal(d.Body, &msg); err == nil {
			err = c.service.ProcessInventoryWaste(ctx, msg)
			if err != nil {
				log.Println("Error processing inventory waste:", err)
			} else {
				d.Ack(false)
			}
		} else {
			log.Println("Error unmarshaling inventory waste:", err)
		}
	default:
		log.Println("Unknown routing key:", d.RoutingKey)
	}
}

func (c *Consumer) Close() {
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
