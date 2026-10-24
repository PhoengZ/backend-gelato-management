package messaging

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Run keeps the consumer and the outbox relay attached to the broker until ctx
// is cancelled. A failed dial or a dropped connection is retried with capped
// exponential backoff; incoming messages wait in the durable queue and outgoing
// events wait in the outbox meanwhile. connected is true only while deliveries flow.
func Run(ctx context.Context, url string, h OrderEvents, db *pgxpool.Pool, connected *atomic.Bool) {
	if url == "" {
		return
	}
	backoff := minBackoff
	for ctx.Err() == nil {
		b, err := Connect(url)
		if err == nil {
			err = b.serve(ctx, h, db, func() {
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

// serve runs the relay and the consumer on one connection. When either stops,
// the other is stopped too so the whole session is rebuilt on reconnect.
func (b *Broker) serve(ctx context.Context, h OrderEvents, db *pgxpool.Pool, ready func()) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errc := make(chan error, 2)
	go func() { errc <- NewRelay(db, b).Run(sctx) }()
	go func() { errc <- b.consume(sctx, h, ready) }()

	err := <-errc
	cancel()
	<-errc
	return err
}
