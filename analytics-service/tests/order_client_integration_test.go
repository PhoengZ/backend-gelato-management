package tests

import (
	"context"
	"io"
	"net"
	"testing"

	orderv1 "analytics-service/gen/order/v1"
	"analytics-service/internal/client"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type inMemoryOrderServer struct {
	orderv1.UnimplementedOrderServiceServer
}

func (s *inMemoryOrderServer) StreamOrderItems(stream orderv1.OrderService_StreamOrderItemsServer) error {
	var receivedIDs []string
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&orderv1.StreamOrderItemsResponse{
				Items: []*orderv1.OrderItem{
					{
						FlavorId:       "flv_01",
						FlavorName:     "Pistachio",
						Portions:       3,
						UnitPriceMinor: 5000,
						SubtotalMinor:  15000,
					},
					{
						FlavorId:       "flv_02",
						FlavorName:     "Strawberry",
						Portions:       2,
						UnitPriceMinor: 5000,
						SubtotalMinor:  10000,
					},
				},
			})
		}
		if err != nil {
			return err
		}
		receivedIDs = append(receivedIDs, req.OrderId)
	}
}

func (s *inMemoryOrderServer) GetOrderDetails(ctx context.Context, req *orderv1.GetOrderDetailsRequest) (*orderv1.GetOrderDetailsResponse, error) {
	return &orderv1.GetOrderDetailsResponse{
		OrderId:          req.OrderId,
		CustomerId:       "cust_123",
		Status:           "PAID",
		PickupAt:         "2026-08-20T10:00:00Z",
		TotalAmountMinor: 25000,
		Currency:         "THB",
		Items: []*orderv1.OrderItem{
			{
				FlavorId:       "flv_01",
				FlavorName:     "Pistachio",
				Portions:       3,
				UnitPriceMinor: 5000,
				SubtotalMinor:  15000,
			},
			{
				FlavorId:       "flv_02",
				FlavorName:     "Strawberry",
				Portions:       2,
				UnitPriceMinor: 5000,
				SubtotalMinor:  10000,
			},
		},
	}, nil
}

func startInMemoryOrderGRPCServer(t *testing.T) (*bufconn.Listener, func()) {
	lis := bufconn.Listen(bufSize)
	baseServer := grpc.NewServer()

	orderv1.RegisterOrderServiceServer(baseServer, &inMemoryOrderServer{})

	go func() {
		_ = baseServer.Serve(lis)
	}()

	cleanup := func() {
		baseServer.Stop()
		_ = lis.Close()
	}

	return lis, cleanup
}

func TestOrderClient_Bufconn_Integration(t *testing.T) {
	lis, cleanup := startInMemoryOrderGRPCServer(t)
	defer cleanup()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	orderClient, err := client.NewOrderClient("passthrough://bufnet", grpc.WithContextDialer(dialer))
	if err != nil {
		t.Fatalf("Failed to initialize order client with bufconn: %v", err)
	}
	defer orderClient.Close()

	ctx := context.Background()

	// 1. Test Client-Streaming RPC over the in-memory wire
	t.Run("ClientStreaming_StreamOrderItems", func(t *testing.T) {
		orderIDs := []string{"ord_001", "ord_002", "ord_003"}
		items, err := orderClient.StreamOrderItems(ctx, orderIDs)
		if err != nil {
			t.Fatalf("StreamOrderItems failed: %v", err)
		}

		if len(items) != 2 {
			t.Fatalf("Expected 2 consolidated items, got %d", len(items))
		}
		if items[0].FlavorID != "flv_01" || items[0].SubtotalMinor != 15000 {
			t.Errorf("Unexpected first item: %+v", items[0])
		}
	})

	// 2. Test Unary RPC over the in-memory wire
	t.Run("Unary_GetOrderDetails", func(t *testing.T) {
		orderDetails, err := orderClient.GetOrderDetails(ctx, "ord_001")
		if err != nil {
			t.Fatalf("GetOrderDetails failed: %v", err)
		}

		if orderDetails == nil || orderDetails.OrderID != "ord_001" {
			t.Fatalf("Expected OrderID 'ord_001', got %+v", orderDetails)
		}
		if orderDetails.TotalAmountMinor != 25000 {
			t.Errorf("Expected TotalAmountMinor 25000, got %d", orderDetails.TotalAmountMinor)
		}
		if len(orderDetails.Items) != 2 {
			t.Errorf("Expected 2 items, got %d", len(orderDetails.Items))
		}
	})
}
