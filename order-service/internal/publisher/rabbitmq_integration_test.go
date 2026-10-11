package publisher

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Use an isolated broker and a test account permitted to declare/bind queues.
func TestRabbitMQRoutingAndReconnect(t *testing.T) {
	url := os.Getenv("ORDER_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("ORDER_TEST_RABBITMQ_URL is not set")
	}
	pub, err := NewRabbitMQPublisher(url)
	if err != nil {
		t.Fatal("connect test publisher:", err)
	}
	defer pub.Close()
	p := pub.(*rabbitPublisher)
	admin, err := amqp.Dial(url)
	if err != nil {
		t.Fatal("connect test consumer:", err)
	}
	defer admin.Close()
	ch, err := admin.Channel()
	if err != nil {
		t.Fatal(err)
	}
	queue, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := "publisher-test." + uuid.NewString()
	if err := ch.QueueBind(queue.Name, key, "order", false, nil); err != nil {
		t.Fatal(err)
	}
	deliveries, err := ch.Consume(queue.Name, "", true, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(key, id string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return p.Publish(ctx, key, id, []byte(`{"test":true}`))
	}
	assertDelivery := func(id string) {
		t.Helper()
		select {
		case msg := <-deliveries:
			if msg.MessageId != id {
				t.Fatalf("got event %q, expected %q", msg.MessageId, id)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("confirmed message was not delivered")
		}
	}
	// Each returned publish produces a basic.return AND basic.ack. The next
	// routed publish must never use that ACK to report a false success.
	for i := 0; i < 10; i++ {
		if err := publish(key+".unbound", fmt.Sprintf("unroutable-%d", i)); err == nil {
			t.Fatal("unroutable event reported success")
		}
		id := fmt.Sprintf("routed-%d", i)
		if err := publish(key, id); err != nil {
			t.Fatal(err)
		}
		assertDelivery(id)
	}
	// Break this publisher's TCP connection only; leave broker/consumer running.
	if err := p.transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err := publish(key, "after-disconnect"); err == nil {
		t.Fatal("broken connection reported success")
	}
	if err := publish(key, "after-disconnect"); err != nil {
		t.Fatal("reconnect failed:", err)
	}
	assertDelivery("after-disconnect")
}
