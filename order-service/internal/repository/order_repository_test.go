package repository

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"order-service/internal/domain"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Each test owns a fresh schema; ORDER_TEST_DATABASE_URL must point to a test DB.
func testRepository(t *testing.T) (OrderRepository, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("ORDER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ORDER_TEST_DATABASE_URL is not set")
	}
	connectionURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	namespace := "order_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := connectionURL.Query()
	query.Set("search_path", namespace)
	connectionURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(connectionURL.String()), &gorm.Config{})
	if err != nil {
		t.Fatal("cannot connect to test PostgreSQL")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE SCHEMA " + namespace).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DROP SCHEMA " + namespace + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	if err := db.AutoMigrate(&domain.Order{}, &domain.OrderItem{}, &domain.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewOrderRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repo, db
}

func persistedOrder(t *testing.T, repo OrderRepository) *domain.Order {
	t.Helper()
	order := &domain.Order{ID: uuid.NewString(), CustomerID: uuid.NewString(), Status: domain.StatusPendingPayment,
		PickupAt: time.Now().UTC().Add(time.Hour), ReservationID: uuid.NewString(),
		ReservationExpiresAt: time.Now().UTC().Add(10 * time.Minute), IdempotencyKey: uuid.NewString(),
		ConfirmIdempotencyKey: uuid.NewString(), ReleaseIdempotencyKey: uuid.NewString(),
		Currency: "THB", TotalAmountMinor: 7500,
		Items: []domain.OrderItem{{FlavorID: uuid.NewString(), FlavorName: "Vanilla", Portions: 1, UnitPriceMinor: 7500, SubtotalMinor: 7500}},
	}
	if err := repo.Create(context.Background(), order); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestPostgresLifecycleClaimSerializesCompetingWorkflows(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	if _, err := repo.BindCheckout(context.Background(), order.ID, "cs_test", "https://checkout.stripe.com/pay/test"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan domain.OrderStatus, 32)
	failures := make(chan error, 32)
	var workers sync.WaitGroup
	for n := 0; n < 32; n++ {
		target := domain.StatusCancelPending
		if n%2 == 0 {
			target = domain.StatusPaymentConfirming
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			claimed, err := repo.BeginTransition(context.Background(), order.ID, target)
			if errors.Is(err, ErrInvalidTransition) {
				return
			}
			if err != nil {
				failures <- err
				return
			}
			results <- claimed.Status
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	winner := domain.OrderStatus("")
	for result := range results {
		if winner == "" {
			winner = result
		}
		if result != winner {
			t.Fatalf("both workflows claimed the order: %s and %s", winner, result)
		}
	}
	if winner == "" {
		t.Fatal("no workflow claimed order")
	}
	current, err := repo.Get(context.Background(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != winner || current.CheckoutURL != "" || len(current.Items) != 1 {
		t.Fatal("claim did not persist or retain order facts")
	}
	if _, err := repo.BindCheckout(context.Background(), order.ID, "cs_test", "https://checkout.stripe.com/pay/test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("checkout binding accepted after claim: %v", err)
	}
}

func TestPostgresCancellationFinalizationRollsBackWithOutboxFailure(t *testing.T) {
	repo, db := testRepository(t)
	order := persistedOrder(t, repo)
	if _, err := repo.BeginTransition(context.Background(), order.ID, domain.StatusCancelPending); err != nil {
		t.Fatal(err)
	}
	invalid := &domain.OutboxEvent{ID: "not-a-uuid", OrderID: order.ID, RoutingKey: "order.cancelled", Payload: []byte(`{}`)}
	if err := repo.MarkCancelled(context.Background(), order.ID, invalid); err == nil {
		t.Fatal("invalid outbox insert should fail")
	}
	current, err := repo.Get(context.Background(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusCancelPending {
		t.Fatal("failed transaction discarded cancellation intent")
	}
	event := &domain.OutboxEvent{ID: uuid.NewString(), OrderID: order.ID, RoutingKey: "order.cancelled", Payload: []byte(`{}`)}
	if err := repo.MarkCancelled(context.Background(), order.ID, event); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkCancelled(context.Background(), order.ID, &domain.OutboxEvent{ID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&domain.OutboxEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate cancellation outbox events: %d", count)
	}
	current, err = repo.Get(context.Background(), order.ID)
	if err != nil || current.Status != domain.StatusCancelled {
		t.Fatal("cancellation did not finish")
	}
}

func TestPostgresPaymentRejectionChangesOnlyConfirmationIntent(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	if err := repo.RejectPaymentConfirmation(context.Background(), order.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("unclaimed order accepted rejection")
	}
	if _, err := repo.BeginTransition(context.Background(), order.ID, domain.StatusPaymentConfirming); err != nil {
		t.Fatal(err)
	}
	if err := repo.RejectPaymentConfirmation(context.Background(), order.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RejectPaymentConfirmation(context.Background(), order.ID); err != nil {
		t.Fatal(err)
	}
	orders, err := repo.PendingTransitions(context.Background(), "", 100)
	if err != nil || len(orders) != 1 || orders[0].Status != domain.StatusCancelPending {
		t.Fatal("rejected confirmation not discoverable for recovery")
	}
	orders, err = repo.PendingTransitions(context.Background(), order.ID, 100)
	if err != nil || len(orders) != 0 {
		t.Fatal("recovery cursor did not advance")
	}
}
