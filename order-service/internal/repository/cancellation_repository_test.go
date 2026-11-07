package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"order-service/internal/domain"

	"github.com/google/uuid"
)

func expireOrder(t *testing.T, repo OrderRepository, id string) {
	t.Helper()
	db := repo.(*gormOrderRepository).db
	if err := db.Model(&domain.Order{}).Where("id = ?", id).Update("reservation_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
}

func payOrder(t *testing.T, repo OrderRepository, id string, paidAt time.Time) {
	t.Helper()
	if _, err := repo.BeginTransition(context.Background(), id, domain.StatusPaymentConfirming); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkPaidWithOutbox(context.Background(), id, paidAt, &domain.OutboxEvent{ID: uuid.NewString(), OrderID: id, RoutingKey: "order.placed", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresExpiredPendingOrdersPagesOnlyOverdueUnpaidOrders(t *testing.T) {
	repo, _ := testRepository(t)
	var overdue []string
	for n := 0; n < 5; n++ {
		order := persistedOrder(t, repo)
		expireOrder(t, repo, order.ID)
		overdue = append(overdue, order.ID)
	}
	persistedOrder(t, repo) // still within its stock hold
	confirming := persistedOrder(t, repo)
	expireOrder(t, repo, confirming.ID)
	if _, err := repo.BeginTransition(context.Background(), confirming.ID, domain.StatusPaymentConfirming); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	after := ""
	for page := 0; page < 10; page++ {
		orders, err := repo.ExpiredPendingOrders(context.Background(), after, time.Now().UTC(), 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(orders) == 0 {
			break
		}
		if len(orders) > 2 {
			t.Fatalf("page size %d exceeds limit", len(orders))
		}
		for _, order := range orders {
			if order.ID <= after || seen[order.ID] {
				t.Fatal("cursor did not advance by order id")
			}
			seen[order.ID] = true
		}
		after = orders[len(orders)-1].ID
	}
	if len(seen) != len(overdue) {
		t.Fatalf("found %d overdue orders, want %d", len(seen), len(overdue))
	}
	for _, id := range overdue {
		if !seen[id] {
			t.Fatalf("overdue order %s not found", id)
		}
	}
}

func TestPostgresExpiryClaimLosesToConcurrentPaymentClaim(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	if _, err := repo.BeginExpiry(context.Background(), order.ID, time.Now().UTC()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("claimed an order whose hold has not expired: %v", err)
	}
	expireOrder(t, repo, order.ID)
	if _, err := repo.BeginTransition(context.Background(), order.ID, domain.StatusPaymentConfirming); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BeginExpiry(context.Background(), order.ID, time.Now().UTC()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expiry claimed a PAYMENT_CONFIRMING order: %v", err)
	}
	current, _ := repo.Get(context.Background(), order.ID)
	if current.Status != domain.StatusPaymentConfirming {
		t.Fatalf("status = %s", current.Status)
	}
}

func TestPostgresExpiryAndPaymentClaimOneWinner(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	expireOrder(t, repo, order.ID)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := map[bool]int{}
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			var err error
			if n%2 == 0 {
				_, err = repo.BeginExpiry(context.Background(), order.ID, time.Now().UTC())
			} else {
				var claimed *domain.Order
				claimed, err = repo.BeginTransition(context.Background(), order.ID, domain.StatusPaymentConfirming)
				if err == nil && claimed.Status != domain.StatusPaymentConfirming {
					err = ErrInvalidTransition
				}
			}
			if err == nil {
				mu.Lock()
				winners[n%2 == 0]++
				mu.Unlock()
			} else if !errors.Is(err, ErrInvalidTransition) {
				t.Error(err)
			}
		}(n)
	}
	close(start)
	wg.Wait()
	current, _ := repo.Get(context.Background(), order.ID)
	if current.Status != domain.StatusCancelPending && current.Status != domain.StatusPaymentConfirming {
		t.Fatalf("status = %s", current.Status)
	}
	if current.Status == domain.StatusCancelPending && current.CancellationReason != domain.ReasonReservationExpired {
		t.Fatalf("expiry reason not persisted: %q", current.CancellationReason)
	}
	if winners[true] > 0 && winners[false] > 0 {
		t.Fatalf("expiry and payment both claimed the order: %v", winners)
	}
	if winners[true] > 1 {
		t.Fatalf("expiry claimed the order %d times", winners[true])
	}
	if winners[true]+winners[false] == 0 {
		t.Fatal("no workflow claimed the order")
	}
}

func TestPostgresExpiryClaimPersistsReasonAndIsRecoverable(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	expireOrder(t, repo, order.ID)
	claimed, err := repo.BeginExpiry(context.Background(), order.ID, time.Now().UTC())
	if err != nil || claimed.Status != domain.StatusCancelPending || claimed.CancellationReason != domain.ReasonReservationExpired || len(claimed.Items) != 1 {
		t.Fatal(err, claimed)
	}
	pending, err := repo.PendingTransitions(context.Background(), "", 100)
	if err != nil || len(pending) != 1 || pending[0].CancellationReason != domain.ReasonReservationExpired {
		t.Fatal("expiry intent is not recoverable with its reason", err)
	}
}

// legacyOrder is the orders table shape deployed before paid_at and
// cancellation_reason existed.
type legacyOrder struct {
	ID                    string `gorm:"primaryKey;type:uuid"`
	CustomerID            string `gorm:"type:uuid;not null"`
	PickupAt              time.Time
	Status                string `gorm:"not null;index"`
	IdempotencyKey        string `gorm:"not null;size:255"`
	RequestHash           string `gorm:"not null;size:64"`
	ReservationID         string `gorm:"type:uuid;not null;uniqueIndex"`
	ReservationExpiresAt  time.Time
	ConfirmIdempotencyKey string `gorm:"not null;size:36"`
	ReleaseIdempotencyKey string `gorm:"not null;size:36"`
	TotalAmountMinor      int64  `gorm:"not null"`
	Currency              string `gorm:"not null;size:3"`
	CheckoutSessionID     string `gorm:"size:255"`
	CheckoutURL           string `gorm:"type:text"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (legacyOrder) TableName() string { return "orders" }

func TestPostgresAutoMigrateAddsCancellationColumnsToExistingRows(t *testing.T) {
	_, db := testRepository(t)
	if err := db.Migrator().DropTable(&domain.OutboxEvent{}, &domain.OrderItem{}, &domain.Order{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyOrder{}); err != nil {
		t.Fatal(err)
	}
	legacy := legacyOrder{ID: uuid.NewString(), CustomerID: uuid.NewString(), PickupAt: time.Now().UTC(), Status: string(domain.StatusPaid),
		IdempotencyKey: uuid.NewString(), RequestHash: "h", ReservationID: uuid.NewString(), ReservationExpiresAt: time.Now().UTC(),
		ConfirmIdempotencyKey: uuid.NewString(), ReleaseIdempotencyKey: uuid.NewString(), TotalAmountMinor: 100, Currency: "THB"}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Order{}, &domain.OrderItem{}, &domain.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	repo, _ := NewOrderRepository(db)
	current, err := repo.Get(context.Background(), legacy.ID)
	if err != nil || current.Status != domain.StatusPaid || current.PaidAt != nil || current.CancellationReason != "" {
		t.Fatal(err, current)
	}
	claimed, err := repo.BeginStoreCancellation(context.Background(), legacy.ID)
	if err != nil || claimed.Status != domain.StatusStoreCancelPending {
		t.Fatal("legacy paid order cannot be store-cancelled", err)
	}
}

func TestPostgresMarkPaidRecordsPaidAt(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	paidAt := time.Date(2026, 9, 2, 14, 30, 0, 0, time.UTC)
	payOrder(t, repo, order.ID, paidAt)
	current, err := repo.Get(context.Background(), order.ID)
	if err != nil || current.Status != domain.StatusPaid || current.PaidAt == nil || !current.PaidAt.Equal(paidAt) {
		t.Fatal(err, current)
	}
}

func TestPostgresStoreCancellationClaimsOnlyPaidOrders(t *testing.T) {
	repo, _ := testRepository(t)
	order := persistedOrder(t, repo)
	if _, err := repo.BeginStoreCancellation(context.Background(), order.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("store-cancelled an unpaid order: %v", err)
	}
	payOrder(t, repo, order.ID, time.Now().UTC())
	claimed, err := repo.BeginStoreCancellation(context.Background(), order.ID)
	if err != nil || claimed.Status != domain.StatusStoreCancelPending || claimed.CancellationReason != domain.ReasonStoreCancelled {
		t.Fatal(err, claimed)
	}
	again, err := repo.BeginStoreCancellation(context.Background(), order.ID)
	if err != nil || again.Status != domain.StatusStoreCancelPending {
		t.Fatal("repeated claim not idempotent", err)
	}
	if _, err := repo.BeginTransition(context.Background(), order.ID, domain.StatusCancelPending); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("customer cancellation took a store-cancel intent: %v", err)
	}
	pending, err := repo.PendingTransitions(context.Background(), "", 100)
	if err != nil || len(pending) != 1 || pending[0].Status != domain.StatusStoreCancelPending {
		t.Fatal("store cancellation intent not recoverable", err)
	}
	if err := repo.MarkCancelled(context.Background(), order.ID, &domain.OutboxEvent{ID: uuid.NewString(), OrderID: order.ID, RoutingKey: "order.cancelled", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	done, err := repo.BeginStoreCancellation(context.Background(), order.ID)
	if err != nil || done.Status != domain.StatusCancelled {
		t.Fatal("finished store cancellation is not an idempotent no-op", err)
	}
	if count, _ := repo.CountOrdersInSlot(context.Background(), order.PickupAt); count != 0 {
		t.Fatalf("cancelled order still occupies its slot: %d", count)
	}
}
