package outbox

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrPublish    = errors.New("Auth outbox delivery not confirmed")
	ErrUnroutable = errors.New("Auth outbox delivery has no route")
)

type Rabbit struct {
	URL       string
	Exchange  string
	TLSConfig *tls.Config
}

// Each attempt has its own connection/channel, so reconnects and return/confirm
// correlation cannot inherit another event's state. The caller bounds the attempt.
func (p Rabbit) Publish(ctx context.Context, message Message) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	var tlsConfig *tls.Config
	if p.TLSConfig != nil {
		if p.TLSConfig.InsecureSkipVerify {
			return ErrPublish
		}
		tlsConfig = p.TLSConfig.Clone()
		tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS12)
	}
	var socket net.Conn
	var stopCancellation func() bool
	connection, err := amqp.DialConfig(p.URL, amqp.Config{
		Heartbeat:       10 * time.Second,
		TLSClientConfig: tlsConfig,
		Dial: func(network, address string) (net.Conn, error) {
			var dialErr error
			socket, dialErr = (&net.Dialer{}).DialContext(ctx, network, address)
			if dialErr != nil {
				return nil, dialErr
			}
			if dialErr = socket.SetDeadline(deadline); dialErr != nil {
				_ = socket.Close()
				return nil, dialErr
			}
			stopCancellation = context.AfterFunc(ctx, func() { _ = socket.Close() })
			return socket, nil
		},
	})
	defer func() {
		if stopCancellation != nil {
			stopCancellation()
		}
		if socket != nil {
			_ = socket.Close()
		}
	}()
	if err != nil {
		return ErrPublish
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return ErrPublish
	}
	defer channel.Close()
	// Topology is provisioned by its owner. Auth never creates or rebinds it.
	if err := channel.ExchangeDeclarePassive(p.Exchange, "topic", true, false, false, false, nil); err != nil {
		return ErrPublish
	}
	if err := channel.Confirm(false); err != nil {
		return ErrPublish
	}
	returns := channel.NotifyReturn(make(chan amqp.Return, 1))
	confirmation, err := channel.PublishWithDeferredConfirmWithContext(ctx, p.Exchange, RoutingKey, true, false, amqp.Publishing{
		ContentType: "application/cloudevents+json", DeliveryMode: amqp.Persistent,
		MessageId: message.ID.String(), Type: EventType, AppId: "auth-service",
		Timestamp: message.CreatedAt, Body: message.Body,
	})
	if err != nil || confirmation == nil {
		return ErrPublish
	}
	ack, err := confirmation.WaitContext(ctx)
	if err != nil || !ack {
		return ErrPublish
	}
	// RabbitMQ sends basic.return before the confirm. amqp091-go dispatches
	// that return to this buffered channel before completing the confirmation.
	select {
	case _, open := <-returns:
		if open {
			return ErrUnroutable
		}
		return ErrPublish
	default:
		return nil
	}
}
