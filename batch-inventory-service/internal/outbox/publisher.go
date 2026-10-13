// Package outbox provides event routing and publishing support.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ExchangeName       = "inventory"
	RoutingKeyWaste    = "inventory.waste"
	RoutingKeyLowStock = "inventory.low_stock"
	RoutingKeyExpiring = "inventory.expiring"

	TypeWasteRecorded = "com.gelatoflow.inventory.waste-recorded.v1"
	TypeLowStock      = "com.gelatoflow.inventory.batch-low-stock.v1"
	TypeExpiring      = "com.gelatoflow.inventory.batch-expiring.v1"
)

var ErrPublish = errors.New("event publish not confirmed or unroutable")

// ResolveRoutingKey dynamically routes a CloudEvent payload to its matching topic routing key.
func ResolveRoutingKey(body []byte) string {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Type != "" {
		switch env.Type {
		case TypeWasteRecorded, RoutingKeyWaste:
			return RoutingKeyWaste
		case TypeLowStock, "com.gelatoflow.inventory.low-stock.v1", RoutingKeyLowStock:
			return RoutingKeyLowStock
		case TypeExpiring, "com.gelatoflow.inventory.expiring.v1", RoutingKeyExpiring:
			return RoutingKeyExpiring
		}
		if strings.Contains(env.Type, "waste") {
			return RoutingKeyWaste
		}
		if strings.Contains(env.Type, "low") || strings.Contains(env.Type, "stock") {
			return RoutingKeyLowStock
		}
		if strings.Contains(env.Type, "expir") {
			return RoutingKeyExpiring
		}
	}
	return RoutingKeyWaste
}

// DynamicPublisher publishes CloudEvents to RabbitMQ with dynamic topic routing.
type DynamicPublisher struct {
	URL string
}

// Publish sends a CloudEvent to the inventory exchange with the appropriate routing key.
func (p DynamicPublisher) Publish(ctx context.Context, id string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	conn, err := amqp.DialConfig(p.URL, amqp.Config{
		Heartbeat: 10 * time.Second,
		Dial: func(network, address string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err == nil {
				err = c.SetDeadline(deadline)
				if err != nil {
					c.Close()
				}
			}
			return c, err
		},
	})
	if err != nil {
		return ErrPublish
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return ErrPublish
	}
	defer ch.Close()

	if err = ch.ExchangeDeclare(ExchangeName, "topic", true, false, false, false, nil); err != nil {
		return ErrPublish
	}
	if err = ch.Confirm(false); err != nil {
		return ErrPublish
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))

	routingKey := ResolveRoutingKey(body)
	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, ExchangeName, routingKey, true, false, amqp.Publishing{
		ContentType:  "application/cloudevents+json",
		DeliveryMode: amqp.Persistent,
		MessageId:    id,
		Body:         body,
		Timestamp:    time.Now().UTC(),
	})
	if err != nil || confirmation == nil {
		return ErrPublish
	}
	ok, err := confirmation.WaitContext(ctx)
	if err != nil || !ok {
		return ErrPublish
	}
	select {
	case <-returns:
		return ErrPublish
	default:
		return nil
	}
}
