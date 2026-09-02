package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync/atomic"

	"analytics-service/internal/models"
	"analytics-service/internal/service"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Consumer struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	service   service.AnalyticsService
	isClosing atomic.Bool
}

func NewConsumer(rabbitURL string, svc service.AnalyticsService) (*Consumer, error) {
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
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

	// Bind queue to routing keys
	// NOTE: Using "order.success" for backward compatibility with current
	// Order Service. If the Order Service migrates to "order.placed",
	// update this binding accordingly.
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

	// TODO: Bind "order.cancelled" when Order Service supports cancellations.
	// This will allow deducting revenue/scoops for refunded orders.

	// Set QoS to prevent one worker from receiving more workload than others.
	// A prefetch count of 10 provides a balance between workload fairness and throughput.
	err = c.ch.Qos(
		10,    // prefetch count
		0,     // prefetch size
		false, // global
	)
	if err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	consumerTag := fmt.Sprintf("analytics-worker-%s", hostname)

	msgs, err := c.ch.Consume(
		q.Name, consumerTag, false, false, false, false, nil,
	)
	if err != nil {
		return err
	}

	notifyClose := c.conn.NotifyClose(make(chan *amqp.Error, 1))

	go func() {
		for {
			select {
			case d, ok := <-msgs:
				if !ok {
					if c.isClosing.Load() {
						return
					}
					log.Printf("RabbitMQ message channel closed unexpectedly. Terminating process to trigger orchestrator restart.")
					os.Exit(1)
				}
				c.processMessage(d)
			case err := <-notifyClose:
				if c.isClosing.Load() {
					return
				}
				log.Printf("RabbitMQ connection closed: %v. Terminating process to trigger orchestrator restart.", err)
				os.Exit(1)
			}
		}
	}()

	log.Println("RabbitMQ Consumer started listening on analytics_queue")
	return nil
}

// isPermanentError returns true for errors that should not be retried
// (e.g., validation failures). Retryable errors (DB down, network issues)
// will be requeued for later processing.
func isPermanentError(err error) bool {
	return errors.Is(err, service.ErrInvalidDate) || errors.Is(err, service.ErrInvalidPeriod)
}

func (c *Consumer) processMessage(d amqp.Delivery) {
	ctx := context.Background()
	switch d.RoutingKey {
	case "order.success":
		var event models.OrderPlacedEvent
		if err := json.Unmarshal(d.Body, &event); err != nil {
			log.Printf("Permanent error unmarshaling order placed event (nacking without requeue): %v", err)
			d.Nack(false, false)
			return
		}
		if err := c.service.ProcessOrderPlaced(ctx, event); err != nil {
			log.Printf("Error processing order placed event: %v", err)
			if isPermanentError(err) {
				d.Nack(false, false) // don't requeue permanent errors
			} else {
				d.Nack(false, true) // requeue retryable errors
			}
			return
		}
		d.Ack(false)

	case "inventory.waste":
		var event models.WasteRecordedEvent
		if err := json.Unmarshal(d.Body, &event); err != nil {
			log.Printf("Permanent error unmarshaling waste recorded event (nacking without requeue): %v", err)
			d.Nack(false, false)
			return
		}
		if err := c.service.ProcessWasteRecorded(ctx, event); err != nil {
			log.Printf("Error processing waste recorded event: %v", err)
			if isPermanentError(err) {
				d.Nack(false, false)
			} else {
				d.Nack(false, true)
			}
			return
		}
		d.Ack(false)

	default:
		log.Printf("Unknown routing key: %s (nacking without requeue)", d.RoutingKey)
		d.Nack(false, false)
	}
}

func (c *Consumer) Close() {
	c.isClosing.Store(true)
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
