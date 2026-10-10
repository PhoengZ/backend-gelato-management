package messaging

import (
	"context"
	"errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"net"
	"time"
)

var ErrPublish = errors.New("event publish not confirmed or unroutable")

type Rabbit struct{ URL string }

// A short-lived channel makes confirmations/returns unambiguous for each event.
// Its network deadline bounds dial, declare, publish, confirm, and close.
func (p Rabbit) Publish(ctx context.Context, id string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	conn, err := amqp.DialConfig(p.URL, amqp.Config{Heartbeat: 10 * time.Second, Dial: func(network, address string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err == nil {
			err = c.SetDeadline(deadline)
			if err != nil {
				c.Close()
			}
		}
		return c, err
	}})
	if err != nil {
		return ErrPublish
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return ErrPublish
	}
	defer ch.Close()
	if err = ch.ExchangeDeclare("inventory", "topic", true, false, false, false, nil); err != nil {
		return ErrPublish
	}
	if err = ch.Confirm(false); err != nil {
		return ErrPublish
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, "inventory", "inventory.waste", true, false, amqp.Publishing{ContentType: "application/cloudevents+json", DeliveryMode: amqp.Persistent, MessageId: id, Body: body, Timestamp: time.Now().UTC()})
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
