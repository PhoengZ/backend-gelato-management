package messaging

import (
	"log"

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
