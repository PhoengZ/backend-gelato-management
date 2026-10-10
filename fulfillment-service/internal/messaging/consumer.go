package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"fulfillment-service/internal/events"
)

const (
	// OrderExchange is the topic exchange owned by Order Service.
	OrderExchange = "order"
	// ConsumerQueue is the durable queue Fulfillment owns for order events.
	ConsumerQueue = "fulfillment_queue"
)

// OrderEvents is what the consumer needs to apply order events.
type OrderEvents interface {
	CreateFromOrderPlaced(ctx context.Context, eventID uuid.UUID, d events.OrderPlacedData) error
	CancelByOrder(ctx context.Context, eventID, orderID uuid.UUID) (bool, error)
}

// Dispatch applies one incoming message. retry is true only for failures worth
// redelivering (storage errors); malformed messages are dropped.
func Dispatch(ctx context.Context, h OrderEvents, body []byte) (retry bool, err error) {
	env, err := events.Parse(body)
	if err != nil {
		return false, err
	}

	switch env.Type {
	case events.TypeOrderPlaced:
		var d events.OrderPlacedData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		if d.OrderID == uuid.Nil || d.PickupAt.IsZero() || len(d.Items) == 0 {
			return false, errors.New("order.placed payload missing order_id, pickup_at or items")
		}
		return true, h.CreateFromOrderPlaced(ctx, env.ID, d)

	case events.TypeOrderCancelled:
		var d events.OrderCancelledData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		if d.OrderID == uuid.Nil {
			return false, errors.New("order.cancelled payload missing order_id")
		}
		cancelled, err := h.CancelByOrder(ctx, env.ID, d.OrderID)
		if err == nil && !cancelled {
			log.Printf("[consumer] order %s not cancelled (unknown, already ready, or duplicate)", d.OrderID)
		}
		return true, err

	default:
		return false, nil // not for us; ignore
	}
}

// Consume declares the queue, binds order.placed and order.cancelled, and
// processes messages until ctx is cancelled or the channel closes.
func (b *Broker) Consume(ctx context.Context, h OrderEvents) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(OrderExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(ConsumerQueue, true, false, false, false, nil); err != nil {
		return err
	}
	for _, key := range []string{events.RoutingKeyOrderPlaced, events.RoutingKeyOrderCancel} {
		if err := ch.QueueBind(ConsumerQueue, key, OrderExchange, false, nil); err != nil {
			return err
		}
	}
	if err := ch.Qos(10, 0, false); err != nil {
		return err
	}

	deliveries, err := ch.Consume(ConsumerQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	log.Printf("[consumer] consuming %s", ConsumerQueue)

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return amqp.ErrClosed
			}
			retry, err := Dispatch(ctx, h, d.Body)
			switch {
			case err == nil:
				_ = d.Ack(false)
			case retry:
				log.Printf("[consumer] %s failed, requeueing: %v", d.RoutingKey, err)
				_ = d.Nack(false, true)
			default:
				log.Printf("[consumer] dropping %s: %v", d.RoutingKey, err)
				_ = d.Nack(false, false)
			}
		}
	}
}
