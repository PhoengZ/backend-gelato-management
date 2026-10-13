package grpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	orderv1 "order-service/gen/order/v1"
	"order-service/internal/domain"
	"order-service/internal/repository"

	"github.com/google/uuid"
	grpcbase "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type mockOrderRepo struct {
	repository.OrderRepository
	mu     sync.Mutex
	orders map[string]*domain.Order
	getErr error
}

func (m *mockOrderRepo) Get(_ context.Context, id string) (*domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	order, ok := m.orders[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return order, nil
}

func setupGRPCTestServer(t *testing.T, repo repository.OrderRepository) (orderv1.OrderServiceClient, func()) {
	t.Helper()
	buffer := 1024 * 1024
	lis := bufconn.Listen(buffer)

	srv := grpcbase.NewServer()
	orderServer := NewOrderGRPCServer(repo)
	orderv1.RegisterOrderServiceServer(srv, orderServer)

	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpcbase.ErrServerStopped) {
			t.Errorf("srv.Serve err: %v", err)
		}
	}()

	conn, err := grpcbase.NewClient("passthrough://bufnet",
		grpcbase.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpcbase.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}

	client := orderv1.NewOrderServiceClient(conn)
	cleanup := func() {
		conn.Close()
		srv.Stop()
		lis.Close()
	}
	return client, cleanup
}

func TestGetOrderDetails(t *testing.T) {
	orderID := uuid.NewString()
	customerID := uuid.NewString()
	flavorID1 := uuid.NewString()
	flavorID2 := uuid.NewString()
	pickupTime := time.Now().UTC().Truncate(15 * time.Minute)

	repo := &mockOrderRepo{
		orders: map[string]*domain.Order{
			orderID: {
				ID:                orderID,
				CustomerID:        customerID,
				Status:            domain.StatusPaid,
				PickupAt:          pickupTime,
				TotalAmountMinor:  15000,
				Currency:          "THB",
				CheckoutSessionID: "cs_test_session_123",
				Items: []domain.OrderItem{
					{
						FlavorID:       flavorID1,
						FlavorName:     "Pistachio",
						Portions:       2,
						UnitPriceMinor: 5000,
						SubtotalMinor:  10000,
					},
					{
						FlavorID:       flavorID2,
						FlavorName:     "Dark Chocolate",
						Portions:       1,
						UnitPriceMinor: 5000,
						SubtotalMinor:  5000,
					},
				},
			},
		},
	}

	client, cleanup := setupGRPCTestServer(t, repo)
	defer cleanup()

	t.Run("success", func(t *testing.T) {
		resp, err := client.GetOrderDetails(context.Background(), &orderv1.GetOrderDetailsRequest{OrderId: orderID})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.OrderId != orderID {
			t.Errorf("got order ID %q, want %q", resp.OrderId, orderID)
		}
		if resp.CustomerId != customerID {
			t.Errorf("got customer ID %q, want %q", resp.CustomerId, customerID)
		}
		if resp.Status != "PAID" {
			t.Errorf("got status %q, want 'PAID'", resp.Status)
		}
		if resp.TotalAmountMinor != 15000 {
			t.Errorf("got total %d, want 15000", resp.TotalAmountMinor)
		}
		if resp.Currency != "THB" {
			t.Errorf("got currency %q, want 'THB'", resp.Currency)
		}
		if resp.PaymentIntentId != "cs_test_session_123" {
			t.Errorf("got payment intent %q, want 'cs_test_session_123'", resp.PaymentIntentId)
		}
		if len(resp.Items) != 2 {
			t.Fatalf("got %d items, want 2", len(resp.Items))
		}
		if resp.Items[0].FlavorName != "Pistachio" || resp.Items[1].FlavorName != "Dark Chocolate" {
			t.Errorf("unexpected items: %+v", resp.Items)
		}
	})

	t.Run("not found", func(t *testing.T) {
		nonExistent := uuid.NewString()
		_, err := client.GetOrderDetails(context.Background(), &orderv1.GetOrderDetailsRequest{OrderId: nonExistent})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.NotFound {
			t.Errorf("expected NotFound, got code=%v, err=%v", st.Code(), err)
		}
	})

	t.Run("invalid UUID", func(t *testing.T) {
		_, err := client.GetOrderDetails(context.Background(), &orderv1.GetOrderDetailsRequest{OrderId: "invalid-uuid"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got code=%v, err=%v", st.Code(), err)
		}
	})

	t.Run("empty order ID", func(t *testing.T) {
		_, err := client.GetOrderDetails(context.Background(), &orderv1.GetOrderDetailsRequest{OrderId: ""})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got code=%v, err=%v", st.Code(), err)
		}
	})

	t.Run("internal repository error", func(t *testing.T) {
		errRepo := &mockOrderRepo{
			getErr: errors.New("db connection failure"),
		}
		errClient, errCleanup := setupGRPCTestServer(t, errRepo)
		defer errCleanup()

		_, err := errClient.GetOrderDetails(context.Background(), &orderv1.GetOrderDetailsRequest{OrderId: orderID})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Internal {
			t.Errorf("expected Internal, got code=%v, err=%v", st.Code(), err)
		}
	})
}

func TestStreamOrderItems(t *testing.T) {
	orderID1 := uuid.NewString()
	orderID2 := uuid.NewString()

	repo := &mockOrderRepo{
		orders: map[string]*domain.Order{
			orderID1: {
				ID: orderID1,
				Items: []domain.OrderItem{
					{FlavorID: "f1", FlavorName: "Hazelnut", Portions: 1, UnitPriceMinor: 4000, SubtotalMinor: 4000},
				},
			},
			orderID2: {
				ID: orderID2,
				Items: []domain.OrderItem{
					{FlavorID: "f2", FlavorName: "Mango", Portions: 3, UnitPriceMinor: 4500, SubtotalMinor: 13500},
				},
			},
		},
	}

	client, cleanup := setupGRPCTestServer(t, repo)
	defer cleanup()

	t.Run("stream multiple orders", func(t *testing.T) {
		stream, err := client.StreamOrderItems(context.Background())
		if err != nil {
			t.Fatalf("StreamOrderItems failed: %v", err)
		}

		// Send order 1
		if err := stream.Send(&orderv1.StreamOrderItemsRequest{OrderId: orderID1}); err != nil {
			t.Fatalf("send order 1 failed: %v", err)
		}
		// Send empty order ID (should be ignored)
		if err := stream.Send(&orderv1.StreamOrderItemsRequest{OrderId: ""}); err != nil {
			t.Fatalf("send empty order failed: %v", err)
		}
		// Send order 2
		if err := stream.Send(&orderv1.StreamOrderItemsRequest{OrderId: orderID2}); err != nil {
			t.Fatalf("send order 2 failed: %v", err)
		}

		resp, err := stream.CloseAndRecv()
		if err != nil {
			t.Fatalf("CloseAndRecv failed: %v", err)
		}
		if len(resp.Items) != 2 {
			t.Fatalf("got %d items, want 2", len(resp.Items))
		}
		if resp.Items[0].FlavorName != "Hazelnut" || resp.Items[1].FlavorName != "Mango" {
			t.Errorf("unexpected items in stream: %+v", resp.Items)
		}
	})

	t.Run("stream with invalid UUID", func(t *testing.T) {
		stream, err := client.StreamOrderItems(context.Background())
		if err != nil {
			t.Fatalf("StreamOrderItems failed: %v", err)
		}

		if err := stream.Send(&orderv1.StreamOrderItemsRequest{OrderId: "invalid"}); err != nil {
			t.Fatalf("send failed: %v", err)
		}

		_, err = stream.CloseAndRecv()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got code=%v, err=%v", st.Code(), err)
		}
	})

	t.Run("stream with not found order", func(t *testing.T) {
		stream, err := client.StreamOrderItems(context.Background())
		if err != nil {
			t.Fatalf("StreamOrderItems failed: %v", err)
		}

		if err := stream.Send(&orderv1.StreamOrderItemsRequest{OrderId: uuid.NewString()}); err != nil {
			t.Fatalf("send failed: %v", err)
		}

		_, err = stream.CloseAndRecv()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.NotFound {
			t.Errorf("expected NotFound, got code=%v, err=%v", st.Code(), err)
		}
	})
}

func TestFallbackStubs(t *testing.T) {
	client, cleanup := setupGRPCTestServer(t, &mockOrderRepo{})
	defer cleanup()

	t.Run("BindPaymentIntent returns Unimplemented", func(t *testing.T) {
		_, err := client.BindPaymentIntent(context.Background(), &orderv1.BindPaymentIntentRequest{
			OrderId:         uuid.NewString(),
			PaymentIntentId: "pi_123",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unimplemented {
			t.Errorf("expected Unimplemented, got code=%v", st.Code())
		}
	})

	t.Run("ReportPaymentResult returns Unimplemented", func(t *testing.T) {
		_, err := client.ReportPaymentResult(context.Background(), &orderv1.ReportPaymentResultRequest{
			OrderId:         uuid.NewString(),
			PaymentIntentId: "pi_123",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unimplemented {
			t.Errorf("expected Unimplemented, got code=%v", st.Code())
		}
	})
}
