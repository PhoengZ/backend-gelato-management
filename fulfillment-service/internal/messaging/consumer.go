package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"fulfillment-service/internal/events"
	"fulfillment-service/internal/repository"
)

const (
	// OrderExchange is the topic exchange owned by Order Service.
	OrderExchange = "order"
	// ConsumerQueue is the durable queue Fulfillment owns for order events.
	ConsumerQueue = "fulfillment_queue"
	// DLXExchange is the Dead Letter Exchange for fulfillment poison messages.
	DLXExchange = "fulfillment.dlx"
	// DLQQueue is the Dead Letter Queue for fulfillment poison messages.
	DLQQueue = "fulfillment_queue_dlq"
	// RetryQueue holds failed messages until their per-message TTL expires, then
	// dead-letters them back to ConsumerQueue through the default exchange.
	RetryQueue = "fulfillment_queue_retry"

	retryCountHeader = "x-retry-count"
	// maxRetries delayed redeliveries (1s, 2s, 4s, 8s, 16s) before a message that
	// keeps failing for a non-transient reason goes to the DLQ.
	maxRetries     = 5
	baseRetryDelay = time.Second
	// maxTransientDelay caps the backoff while the database is unavailable. Those
	// failures retry forever so a paid order never lands in the DLQ during an
	// outage. Because per-message TTLs expire only at the queue head, the cap is
	// also the most a message can wait behind another once the database is back.
	maxTransientDelay = time.Minute
)

// OrderEvents is what the consumer needs to apply order events.
type OrderEvents interface {
	CreateFromOrderPlaced(ctx context.Context, eventID uuid.UUID, d events.OrderPlacedData) (bool, error)
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
		created, err := h.CreateFromOrderPlaced(ctx, env.ID, d)
		if err == nil && !created {
			log.Printf("[consumer] no ticket created for order %s (duplicate, already exists, or cancelled first)", d.OrderID)
		}
		return true, err

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

// TopologyDeclarer abstracts the RabbitMQ channel operations required to declare topology.
type TopologyDeclarer interface {
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
}

// SetupTopology declares the DLX and DLQ, the order exchange, the consumer queue,
// and the retry queue, then binds order.placed and order.cancelled. The consumer
// queue arguments are unchanged from earlier releases so redeclaring an existing
// queue does not fail with PRECONDITION_FAILED.
func SetupTopology(ch TopologyDeclarer) error {
	if err := ch.ExchangeDeclare(DLXExchange, "fanout", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(DLQQueue, true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.QueueBind(DLQQueue, "", DLXExchange, false, nil); err != nil {
		return err
	}

	if err := ch.ExchangeDeclare(OrderExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(ConsumerQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": DLXExchange,
	}); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(RetryQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": ConsumerQueue,
	}); err != nil {
		return err
	}

	for _, key := range []string{events.RoutingKeyOrderPlaced, events.RoutingKeyOrderCancel} {
		if err := ch.QueueBind(ConsumerQueue, key, OrderExchange, false, nil); err != nil {
			return err
		}
	}
	return nil
}

// retryPlan reads how many delayed retries a message has had and decides the
// next one. delay doubles per attempt starting at baseRetryDelay. Transient
// failures are never exhausted and their delay is capped at maxTransientDelay.
func retryPlan(headers amqp.Table, transient bool) (next int, delay time.Duration, exhausted bool) {
	n := retryCount(headers)
	if transient {
		delay = maxTransientDelay
		if n < 16 { // 1s << 16 is far above the cap; avoids shift overflow
			delay = min(baseRetryDelay<<n, maxTransientDelay)
		}
		return n + 1, delay, false
	}
	if n >= maxRetries {
		return n, 0, true
	}
	return n + 1, baseRetryDelay << n, false
}

func retryCount(headers amqp.Table) int {
	switch v := headers[retryCountHeader].(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// retryPublisher sends a message to the retry queue and waits for the broker confirm.
type retryPublisher interface {
	publishRetry(ctx context.Context, msg amqp.Publishing) error
}

type channelRetryPublisher struct{ ch *amqp.Channel }

func (p channelRetryPublisher) publishRetry(ctx context.Context, msg amqp.Publishing) error {
	return publishConfirmed(ctx, p.ch, "", RetryQueue, msg)
}

// handleDelivery applies one delivery. Storage failures are parked in the retry
// queue with a growing TTL and acked only after the broker confirms the copy;
// after maxRetries, or for poison messages, the delivery is rejected to the DLQ.
// A crash between the retry publish and the ack redelivers the message, which
// the processed_events dedupe turns into a no-op.
//
// Per-message TTLs only expire at the head of a classic queue, so a long delay
// can hold back a shorter one behind it; that delays a retry but never loses it.
func handleDelivery(ctx context.Context, pub retryPublisher, d amqp.Delivery, h OrderEvents) error {
	retry, err := Dispatch(ctx, h, d.Body)
	switch {
	case err == nil:
		return d.Ack(false)
	case !retry:
		log.Printf("[consumer] rejecting poison message %s to DLQ: %v", d.RoutingKey, err)
		return d.Nack(false, false)
	}

	transient := repository.IsTransient(err)
	next, delay, exhausted := retryPlan(d.Headers, transient)
	if exhausted {
		log.Printf("[consumer] %s failed after %d retries, moving to DLQ: %v", d.RoutingKey, maxRetries, err)
		return d.Nack(false, false)
	}

	headers := amqp.Table{}
	for k, v := range d.Headers {
		if !strings.HasPrefix(k, "x-") { // drop broker-managed x-death and our old count
			headers[k] = v
		}
	}
	headers[retryCountHeader] = int32(next)

	if perr := pub.publishRetry(ctx, amqp.Publishing{
		Headers:      headers,
		ContentType:  d.ContentType,
		DeliveryMode: amqp.Persistent,
		Expiration:   strconv.FormatInt(delay.Milliseconds(), 10),
		Body:         d.Body,
	}); perr != nil {
		log.Printf("[consumer] could not schedule retry for %s, requeueing: %v", d.RoutingKey, perr)
		_ = d.Nack(false, true)
		return perr
	}
	if transient {
		log.Printf("[consumer] %s: database unavailable, retry %d in %s: %v", d.RoutingKey, next, delay, err)
	} else {
		log.Printf("[consumer] %s failed, retry %d/%d in %s: %v", d.RoutingKey, next, maxRetries, delay, err)
	}
	return d.Ack(false)
}

// consume declares the topology and processes messages until ctx is cancelled
// or the channel closes. ready runs once deliveries have started.
func (b *Broker) consume(ctx context.Context, h OrderEvents, ready func()) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := SetupTopology(ch); err != nil {
		return err
	}
	if err := ch.Qos(10, 0, false); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}

	deliveries, err := ch.Consume(ConsumerQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	log.Printf("[consumer] consuming %s (retry %s, DLX %s -> %s)", ConsumerQueue, RetryQueue, DLXExchange, DLQQueue)
	ready()

	pub := channelRetryPublisher{ch: ch}
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return amqp.ErrClosed
			}
			if err := handleDelivery(ctx, pub, d, h); err != nil && ch.IsClosed() {
				return err
			}
		}
	}
}
