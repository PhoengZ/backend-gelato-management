package messaging

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	amqp "github.com/rabbitmq/amqp091-go"

	"fulfillment-service/internal/events"
)

type fakeOrders struct {
	placed    []events.OrderPlacedData
	cancelled []uuid.UUID
	err       error
}

func (f *fakeOrders) CreateFromOrderPlaced(_ context.Context, _ uuid.UUID, d events.OrderPlacedData) (bool, error) {
	f.placed = append(f.placed, d)
	return f.err == nil, f.err
}

func (f *fakeOrders) CancelByOrder(_ context.Context, _, orderID uuid.UUID) (bool, error) {
	f.cancelled = append(f.cancelled, orderID)
	return true, f.err
}

const placedBody = `{
  "specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556",
  "source":"/gelatoflow/order-service","type":"com.gelatoflow.order.placed.v1",
  "time":"2026-09-02T14:30:00Z","datacontenttype":"application/json",
  "data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","customer_id":"8dc81c31-a354-4774-b835-36ea0411bc54",
  "status":"PAID","pickup_at":"2026-09-02T15:00:00Z","total_amount_minor":1250,"currency":"THB",
  "items":[{"flavor_id":"0f3bca11-1eb2-4a86-908a-e60d7656c79b","flavor_name":"Sicilian Pistachio","portions":2,
  "unit_price_minor":625,"subtotal_minor":1250}]}}`

const cancelledBody = `{
  "specversion":"1.0","id":"11111111-1111-4111-8111-111111111111",
  "source":"/gelatoflow/order-service","type":"com.gelatoflow.order.cancelled.v1",
  "time":"2026-09-02T14:40:00Z","datacontenttype":"application/json",
  "data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","reason":"CUSTOMER_REQUEST"}}`

func TestDispatchOrderPlaced(t *testing.T) {
	h := &fakeOrders{}
	retry, err := Dispatch(context.Background(), h, []byte(placedBody))
	if err != nil || !retry {
		t.Fatalf("retry=%v err=%v", retry, err)
	}
	if len(h.placed) != 1 || len(h.placed[0].Items) != 1 || h.placed[0].Items[0].Portions != 2 {
		t.Fatalf("placed = %+v", h.placed)
	}
	if h.placed[0].CustomerID != uuid.MustParse("8dc81c31-a354-4774-b835-36ea0411bc54") {
		t.Fatalf("customer_id = %s, want the event's customer", h.placed[0].CustomerID)
	}
}

func TestDispatchOrderCancelled(t *testing.T) {
	h := &fakeOrders{}
	if _, err := Dispatch(context.Background(), h, []byte(cancelledBody)); err != nil {
		t.Fatal(err)
	}
	if len(h.cancelled) != 1 {
		t.Fatalf("cancelled = %v", h.cancelled)
	}
}

func TestDispatchStorageErrorIsRetried(t *testing.T) {
	h := &fakeOrders{err: errors.New("db down")}
	retry, err := Dispatch(context.Background(), h, []byte(placedBody))
	if err == nil || !retry {
		t.Fatalf("retry=%v err=%v, want retryable error", retry, err)
	}
}

func TestDispatchMalformedIsDropped(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `{`,
		"wrong version": `{"specversion":"2.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"x","data":{}}`,
		"no items":      `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"com.gelatoflow.order.placed.v1","data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","pickup_at":"2026-09-02T15:00:00Z","items":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeOrders{}
			retry, err := Dispatch(context.Background(), h, []byte(body))
			if err == nil || retry {
				t.Fatalf("retry=%v err=%v, want non-retryable error", retry, err)
			}
			if len(h.placed) != 0 {
				t.Error("handler must not be called")
			}
		})
	}
}

func TestDispatchUnknownTypeIgnored(t *testing.T) {
	body := `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"com.gelatoflow.other.v1","data":{}}`
	retry, err := Dispatch(context.Background(), &fakeOrders{}, []byte(body))
	if err != nil || retry {
		t.Fatalf("retry=%v err=%v", retry, err)
	}
}

func TestDLQConfiguration(t *testing.T) {
	if DLXExchange != "fulfillment.dlx" {
		t.Errorf("DLXExchange = %s, want fulfillment.dlx", DLXExchange)
	}
	if DLQQueue != "fulfillment_queue_dlq" {
		t.Errorf("DLQQueue = %s, want fulfillment_queue_dlq", DLQQueue)
	}
}

func TestRetryPlan(t *testing.T) {
	for name, tc := range map[string]struct {
		headers   amqp.Table
		transient bool
		next      int
		delay     time.Duration
		exhausted bool
	}{
		"first failure":                {nil, false, 1, time.Second, false},
		"fourth retry done":            {amqp.Table{retryCountHeader: int32(4)}, false, 5, 16 * time.Second, false},
		"retries exhausted":            {amqp.Table{retryCountHeader: int64(maxRetries)}, false, maxRetries, 0, true},
		"wrong header type":            {amqp.Table{retryCountHeader: "3"}, false, 1, time.Second, false},
		"transient keeps doubling":     {amqp.Table{retryCountHeader: int32(5)}, true, 6, 32 * time.Second, false},
		"transient is capped":          {amqp.Table{retryCountHeader: int32(6)}, true, 7, maxTransientDelay, false},
		"transient is never exhausted": {amqp.Table{retryCountHeader: int32(50)}, true, 51, maxTransientDelay, false},
	} {
		t.Run(name, func(t *testing.T) {
			next, delay, exhausted := retryPlan(tc.headers, tc.transient)
			if next != tc.next || delay != tc.delay || exhausted != tc.exhausted {
				t.Fatalf("retryPlan = (%d, %s, %v), want (%d, %s, %v)",
					next, delay, exhausted, tc.next, tc.delay, tc.exhausted)
			}
		})
	}
}

type fakeAck struct {
	acked, nacked, requeued bool
}

func (a *fakeAck) Ack(uint64, bool) error { a.acked = true; return nil }
func (a *fakeAck) Nack(_ uint64, _ bool, requeue bool) error {
	a.nacked, a.requeued = true, requeue
	return nil
}
func (a *fakeAck) Reject(_ uint64, requeue bool) error { return a.Nack(0, false, requeue) }

type fakeRetryPub struct {
	sent []amqp.Publishing
	err  error
}

func (p *fakeRetryPub) publishRetry(_ context.Context, msg amqp.Publishing) error {
	p.sent = append(p.sent, msg)
	return p.err
}

func TestHandleDelivery(t *testing.T) {
	dbDown := errors.New("db down")

	t.Run("success is acked", func(t *testing.T) {
		ack, pub := &fakeAck{}, &fakeRetryPub{}
		d := amqp.Delivery{Acknowledger: ack, Body: []byte(placedBody)}
		if err := handleDelivery(context.Background(), pub, d, &fakeOrders{}); err != nil {
			t.Fatal(err)
		}
		if !ack.acked || ack.nacked || len(pub.sent) != 0 {
			t.Fatalf("ack=%+v retries=%d", ack, len(pub.sent))
		}
	})

	t.Run("poison goes to DLQ", func(t *testing.T) {
		ack, pub := &fakeAck{}, &fakeRetryPub{}
		d := amqp.Delivery{Acknowledger: ack, Body: []byte(`{`)}
		_ = handleDelivery(context.Background(), pub, d, &fakeOrders{})
		if !ack.nacked || ack.requeued || len(pub.sent) != 0 {
			t.Fatalf("ack=%+v retries=%d", ack, len(pub.sent))
		}
	})

	t.Run("storage error is parked in the retry queue then acked", func(t *testing.T) {
		ack, pub := &fakeAck{}, &fakeRetryPub{}
		d := amqp.Delivery{
			Acknowledger: ack,
			Body:         []byte(placedBody),
			Headers:      amqp.Table{retryCountHeader: int32(2), "x-death": []any{}, "traceparent": "00-abc"},
		}
		if err := handleDelivery(context.Background(), pub, d, &fakeOrders{err: dbDown}); err != nil {
			t.Fatal(err)
		}
		if !ack.acked || ack.nacked || len(pub.sent) != 1 {
			t.Fatalf("ack=%+v retries=%d", ack, len(pub.sent))
		}
		msg := pub.sent[0]
		if msg.Headers[retryCountHeader] != int32(3) || msg.Expiration != "4000" || msg.DeliveryMode != amqp.Persistent {
			t.Fatalf("retry message = %+v", msg)
		}
		if _, ok := msg.Headers["x-death"]; ok {
			t.Error("broker-managed x-death header must not be copied")
		}
		if msg.Headers["traceparent"] != "00-abc" {
			t.Error("application headers must be preserved")
		}
	})

	t.Run("database outage keeps retrying past the limit", func(t *testing.T) {
		ack, pub := &fakeAck{}, &fakeRetryPub{}
		d := amqp.Delivery{Acknowledger: ack, Body: []byte(placedBody), Headers: amqp.Table{retryCountHeader: int32(10)}}
		dbUnreachable := &pgconn.PgError{Code: "57P03"} // the database system is starting up
		if err := handleDelivery(context.Background(), pub, d, &fakeOrders{err: dbUnreachable}); err != nil {
			t.Fatal(err)
		}
		if !ack.acked || ack.nacked || len(pub.sent) != 1 {
			t.Fatalf("ack=%+v retries=%d, want a scheduled retry instead of the DLQ", ack, len(pub.sent))
		}
		if pub.sent[0].Expiration != "60000" {
			t.Fatalf("expiration = %s, want the 1 minute cap", pub.sent[0].Expiration)
		}
	})

	t.Run("exhausted retries go to DLQ", func(t *testing.T) {
		ack, pub := &fakeAck{}, &fakeRetryPub{}
		d := amqp.Delivery{Acknowledger: ack, Body: []byte(placedBody), Headers: amqp.Table{retryCountHeader: int32(maxRetries)}}
		_ = handleDelivery(context.Background(), pub, d, &fakeOrders{err: dbDown})
		if !ack.nacked || ack.requeued || len(pub.sent) != 0 {
			t.Fatalf("ack=%+v retries=%d", ack, len(pub.sent))
		}
	})

	t.Run("failed retry publish requeues", func(t *testing.T) {
		ack, pub := &fakeAck{}, &fakeRetryPub{err: errors.New("channel closed")}
		d := amqp.Delivery{Acknowledger: ack, Body: []byte(placedBody)}
		if err := handleDelivery(context.Background(), pub, d, &fakeOrders{err: dbDown}); err == nil {
			t.Fatal("want the publish error returned")
		}
		if !ack.nacked || !ack.requeued || ack.acked {
			t.Fatalf("ack=%+v", ack)
		}
	})
}

type fakeDeclarer struct {
	queues map[string]amqp.Table
}

func (f *fakeDeclarer) ExchangeDeclare(string, string, bool, bool, bool, bool, amqp.Table) error {
	return nil
}

func (f *fakeDeclarer) QueueDeclare(name string, _, _, _, _ bool, args amqp.Table) (amqp.Queue, error) {
	f.queues[name] = args
	return amqp.Queue{Name: name}, nil
}

func (f *fakeDeclarer) QueueBind(string, string, string, bool, amqp.Table) error { return nil }

func TestSetupTopology(t *testing.T) {
	f := &fakeDeclarer{queues: map[string]amqp.Table{}}
	if err := SetupTopology(f); err != nil {
		t.Fatal(err)
	}

	main := f.queues[ConsumerQueue]
	if len(main) != 1 || main["x-dead-letter-exchange"] != DLXExchange {
		t.Fatalf("consumer queue args changed: %v", main)
	}
	retry, ok := f.queues[RetryQueue]
	if !ok || retry["x-dead-letter-exchange"] != "" || retry["x-dead-letter-routing-key"] != ConsumerQueue {
		t.Fatalf("retry queue args = %v", retry)
	}
	if _, ok := f.queues[DLQQueue]; !ok {
		t.Fatal("DLQ not declared")
	}
}

func TestDispatchPoisonMessagePayloads(t *testing.T) {
	poisonCases := []struct {
		name string
		body string
	}{
		{
			name: "order.placed nil order_id",
			body: `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"com.gelatoflow.order.placed.v1","data":{"order_id":"00000000-0000-0000-0000-000000000000","pickup_at":"2026-09-02T15:00:00Z","items":[{"flavor_id":"0f3bca11-1eb2-4a86-908a-e60d7656c79b","portions":1}]}}`,
		},
		{
			name: "order.placed zero pickup_at",
			body: `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"com.gelatoflow.order.placed.v1","data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","items":[{"flavor_id":"0f3bca11-1eb2-4a86-908a-e60d7656c79b","portions":1}]}}`,
		},
		{
			name: "order.cancelled nil order_id",
			body: `{"specversion":"1.0","id":"11111111-1111-4111-8111-111111111111","type":"com.gelatoflow.order.cancelled.v1","data":{"order_id":"00000000-0000-0000-0000-000000000000"}}`,
		},
	}

	for _, tc := range poisonCases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeOrders{}
			retry, err := Dispatch(context.Background(), h, []byte(tc.body))
			if err == nil {
				t.Fatalf("expected error for poison message %s", tc.name)
			}
			if retry {
				t.Fatalf("retry must be false (non-requeueable poison message routed to DLQ) for %s", tc.name)
			}
		})
	}
}
