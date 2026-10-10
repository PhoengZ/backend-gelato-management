package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"notification-service/internal/events"
)

const (
	// ConsumerQueue is the durable queue Notification owns.
	ConsumerQueue = "notification_queue"

	// Topic exchanges owned by the publishing services.
	OrderExchange       = "order"
	FulfillmentExchange = "fulfillment"
	InventoryExchange   = "inventory"
)

// bindings maps each exchange to the routing keys Notification consumes.
var bindings = map[string][]string{
	OrderExchange:       {events.RoutingKeyOrderPlaced, events.RoutingKeyOrderCancel},
	FulfillmentExchange: {events.RoutingKeyOrderReady},
	InventoryExchange:   {events.RoutingKeyBatchLowStock, events.RoutingKeyBatchExpiring},
}

// Events is what the consumer needs to apply incoming events.
type Events interface {
	OrderPlaced(env events.Envelope, d events.OrderPlacedData)
	OrderReady(env events.Envelope, d events.OrderReadyData)
	OrderCancelled(env events.Envelope, d events.OrderCancelledData)
	BatchLowStock(env events.Envelope, d events.BatchLowStockData)
	BatchExpiring(env events.Envelope, d events.BatchExpiringData)
}

// Dispatch applies one incoming message. retry is always false today because
// applying an event only touches memory; a malformed message can never succeed
// on redelivery, so it is dropped rather than requeued.
func Dispatch(h Events, body []byte) (retry bool, err error) {
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
		if d.OrderID == uuid.Nil || d.CustomerID == uuid.Nil {
			return false, errors.New("order.placed payload missing order_id or customer_id")
		}
		h.OrderPlaced(env, d)

	case events.TypeOrderReady:
		var d events.OrderReadyData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		if d.OrderID == uuid.Nil {
			return false, errors.New("order-ready payload missing order_id")
		}
		h.OrderReady(env, d)

	case events.TypeOrderCancelled:
		var d events.OrderCancelledData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		if d.OrderID == uuid.Nil {
			return false, errors.New("order.cancelled payload missing order_id")
		}
		h.OrderCancelled(env, d)

	case events.TypeBatchLowStock:
		var d events.BatchLowStockData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		h.BatchLowStock(env, d)

	case events.TypeBatchExpiring:
		var d events.BatchExpiringData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		h.BatchExpiring(env, d)

	default:
		// Not for us (e.g. a legacy or future type on a shared key): ack and ignore.
	}
	return false, nil
}

// Consume declares the exchanges and queue, binds the routing keys, and
// processes messages until ctx is cancelled or the channel closes.
func (b *Broker) Consume(ctx context.Context, h Events) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if _, err := ch.QueueDeclare(ConsumerQueue, true, false, false, false, nil); err != nil {
		return err
	}
	for exchange, keys := range bindings {
		if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
			return err
		}
		for _, key := range keys {
			if err := ch.QueueBind(ConsumerQueue, key, exchange, false, nil); err != nil {
				return err
			}
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
			retry, err := Dispatch(h, d.Body)
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
