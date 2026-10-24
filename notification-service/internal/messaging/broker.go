package messaging

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Broker wraps the RabbitMQ connection used for consuming.
type Broker struct {
	conn *amqp.Connection
}

// Connect opens the broker connection. It returns (nil, nil) when url is empty
// so the service can run without a broker.
func Connect(url string) (*Broker, error) {
	if url == "" {
		log.Println("[messaging] RABBITMQ_URL not set, running without a message broker")
		return nil, nil
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	return &Broker{conn: conn}, nil
}

func (b *Broker) Close() {
	if b == nil {
		return
	}
	b.conn.Close()
}

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Run keeps a consumer attached to the broker until ctx is cancelled. A failed dial
// or a dropped connection is retried with capped exponential backoff; messages wait
// in the durable queue meanwhile. connected is true only while deliveries flow.
func Run(ctx context.Context, url string, h Events, connected *atomic.Bool) {
	backoff := minBackoff
	for ctx.Err() == nil {
		b, err := Connect(url)
		if err == nil {
			err = b.consume(ctx, h, func() {
				connected.Store(true)
				backoff = minBackoff
			})
			connected.Store(false)
			b.Close()
		}
		if ctx.Err() != nil {
			return
		}
		log.Printf("[messaging] broker unavailable, retrying in %s: %v", backoff, err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
