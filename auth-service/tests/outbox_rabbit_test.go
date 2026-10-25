package tests

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"auth-service/internal/outbox"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func authRabbitFixture(t *testing.T) (string, string, string, *amqp.Channel) {
	t.Helper()
	address := os.Getenv("TEST_AUTH_AMQP_URL")
	if address == "" {
		t.Skip("TEST_AUTH_AMQP_URL is not set")
	}
	conn, err := amqp.Dial(address)
	if err != nil {
		t.Fatal("test broker unavailable")
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal("test channel unavailable")
	}
	exchange := "auth_test_" + uuid.NewString()
	if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	queue, err := ch.QueueDeclare(exchange+"_queue", true, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanup, err := conn.Channel(); err == nil {
			_, _ = cleanup.QueueDelete(queue.Name, false, false, false)
			_ = cleanup.ExchangeDelete(exchange, false, false)
			_ = cleanup.Close()
		}
		_ = ch.Close()
	})
	return address, exchange, queue.Name, ch
}

func TestAuthRabbitUnroutableMessageStaysPendingUntilBindingExists(t *testing.T) {
	address, exchange, queue, ch := authRabbitFixture(t)
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	w := outbox.NewWorker(db, outbox.Rabbit{URL: address, Exchange: exchange}, 50*time.Millisecond, 3*time.Second)
	if worked, err := w.DispatchOne(context.Background()); !worked || !errors.Is(err, outbox.ErrUnroutable) {
		t.Fatal("unroutable ACK incorrectly accepted", err)
	}
	if published, attempts, _ := authOutboxState(t, db, e.ID); published || attempts != 1 {
		t.Fatal("unrouted row lost")
	}
	if err := ch.QueueBind(queue, outbox.RoutingKey, exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	makeAuthOutboxDue(t, db, e.ID)
	// A fresh worker and fresh AMQP connection recover the persisted attempt.
	w = outbox.NewWorker(db, outbox.Rabbit{URL: address, Exchange: exchange}, 50*time.Millisecond, 3*time.Second)
	if _, err := w.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	want, err := outbox.MessageFor(*e)
	if err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := ch.Get(queue, true)
	if err != nil || !ok || delivery.MessageId != e.ID.String() || delivery.DeliveryMode != amqp.Persistent || delivery.ContentType != "application/cloudevents+json" || !bytes.Equal(delivery.Body, want.Body) {
		t.Fatal("confirmed durable CloudEvent not received")
	}
	if published, attempts, _ := authOutboxState(t, db, e.ID); !published || attempts != 2 {
		t.Fatal("confirmed delivery was not acknowledged")
	}
	if _, err := w.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ch.Get(queue, true); err != nil || ok {
		t.Fatal("acknowledged event was published again")
	}
}

func TestAuthRabbitMissingExchangeIsNotCreatedAndWrongCredentialsRemainPending(t *testing.T) {
	address, exchange, _, ch := authRabbitFixture(t)
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	missing := exchange + "_missing"
	w := outbox.NewWorker(db, outbox.Rabbit{URL: address, Exchange: missing}, 50*time.Millisecond, time.Second)
	if _, err := w.DispatchOne(context.Background()); !errors.Is(err, outbox.ErrPublish) {
		t.Fatal("missing exchange falsely confirmed", err)
	}
	if err := ch.ExchangeDeclarePassive(missing, "topic", true, false, false, false, nil); err == nil {
		t.Fatal("publisher created shared topology")
	}
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal("test URL invalid")
	}
	u.User = url.UserPassword("nonexistent_test_user", "wrong_test_password")
	makeAuthOutboxDue(t, db, e.ID)
	w = outbox.NewWorker(db, outbox.Rabbit{URL: u.String(), Exchange: exchange}, 50*time.Millisecond, time.Second)
	if _, err := w.DispatchOne(context.Background()); !errors.Is(err, outbox.ErrPublish) {
		t.Fatal("bad credentials accepted", err)
	}
	if published, attempts, _ := authOutboxState(t, db, e.ID); published || attempts != 2 {
		t.Fatal("failed authentication lost event")
	}
}

func TestAuthRabbitSilentPeerAndCancellationAreBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := listener.Accept(); err == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = (outbox.Rabbit{URL: "amqp://guest:guest@" + listener.Addr().String() + "/", Exchange: "auth_test"}).Publish(ctx, outbox.Message{ID: uuid.New()})
	if !errors.Is(err, outbox.ErrPublish) || time.Since(start) > time.Second {
		t.Fatal("silent broker handshake was unbounded")
	}
	select {
	case c := <-accepted:
		_ = c.Close()
	default:
	}
}

func TestAuthRabbitNegativeConfirmKeepsEventForRetry(t *testing.T) {
	address, exchange, queue, ch := authRabbitFixture(t)
	rejected, err := ch.QueueDeclare(exchange+"_reject", true, false, false, false, amqp.Table{"x-max-length": int32(0), "x-overflow": "reject-publish"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = ch.QueueDelete(rejected.Name, false, false, false) })
	if err := ch.QueueBind(rejected.Name, outbox.RoutingKey, exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	w := outbox.NewWorker(db, outbox.Rabbit{URL: address, Exchange: exchange}, 50*time.Millisecond, 3*time.Second)
	if _, err := w.DispatchOne(context.Background()); !errors.Is(err, outbox.ErrPublish) {
		t.Fatal("broker NACK falsely accepted", err)
	}
	if published, attempts, _ := authOutboxState(t, db, e.ID); published || attempts != 1 {
		t.Fatal("NACK lost event")
	}
	if _, err := ch.QueueDelete(rejected.Name, false, false, false); err != nil {
		t.Fatal(err)
	}
	if err := ch.QueueBind(queue, outbox.RoutingKey, exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	makeAuthOutboxDue(t, db, e.ID)
	if _, err := w.DispatchOne(context.Background()); err != nil {
		t.Fatal("retry did not recover", err)
	}
	if published, _, _ := authOutboxState(t, db, e.ID); !published {
		t.Fatal("successful retry not acknowledged")
	}
}

func TestAuthRabbitCancellationInterruptsSilentHandshakeBeforeDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := listener.Accept(); err == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (outbox.Rabbit{URL: "amqp://guest:guest@" + listener.Addr().String() + "/", Exchange: "auth_test"}).Publish(ctx, outbox.Message{ID: uuid.New()})
	}()
	select {
	case connection := <-accepted:
		defer connection.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("test peer not connected")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, outbox.ErrPublish) {
			t.Fatal("cancelled publish falsely confirmed")
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt AMQP handshake")
	}
}

// A local TLS terminator forwards real AMQP to the fixture. This tests the
// publisher's CA/hostname/version enforcement rather than a mock publish call.
func TestAuthRabbitTLSVerifiesCAHostnameAndVersion(t *testing.T) {
	address, exchange, queue, ch := authRabbitFixture(t)
	if err := ch.QueueBind(queue, outbox.RoutingKey, exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	uri, err := amqp.ParseURI(address)
	if err != nil {
		t.Fatal("invalid fixture URL")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Auth fixture"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	for _, tc := range []struct {
		name, host string
		roots      *x509.CertPool
		max        uint16
		success    bool
	}{
		{"trusted", "localhost", roots, tls.VersionTLS13, true},
		{"wrong_ca", "localhost", x509.NewCertPool(), tls.VersionTLS13, false},
		{"wrong_hostname", "127.0.0.1", roots, tls.VersionTLS13, false},
		{"old_protocol", "localhost", roots, tls.VersionTLS11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS11, MaxVersion: tc.max})
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				client, err := listener.Accept()
				if err != nil {
					return
				}
				defer client.Close()
				_ = client.SetDeadline(time.Now().Add(3 * time.Second))
				if client.(*tls.Conn).Handshake() != nil {
					return
				}
				upstream, err := net.DialTimeout("tcp", net.JoinHostPort(uri.Host, strconv.Itoa(uri.Port)), time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				_ = upstream.SetDeadline(time.Now().Add(3 * time.Second))
				done := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, client); close(done) }()
				_, _ = io.Copy(client, upstream)
				_ = client.Close()
				_ = upstream.Close()
				<-done
			}()
			u, _ := url.Parse(address)
			u.Scheme = "amqps"
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			u.Host = net.JoinHostPort(tc.host, port)
			db := hardeningDatabase(t)
			e := seedAuthOutbox(t, db)
			w := outbox.NewWorker(db, outbox.Rabbit{URL: u.String(), Exchange: exchange, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: tc.roots}}, 50*time.Millisecond, 2*time.Second)
			_, publishErr := w.DispatchOne(context.Background())
			_ = listener.Close()
			wg.Wait()
			published, _, _ := authOutboxState(t, db, e.ID)
			if (publishErr == nil) != tc.success || published != tc.success {
				t.Fatal("TLS policy was not enforced", publishErr)
			}
		})
	}
}
