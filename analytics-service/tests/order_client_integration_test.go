package tests

import (
	"context"
	"io"
	"net"
	"testing"

	"analytics-service/internal/client"
	"analytics-service/internal/models"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type inMemoryOrderServer struct{}

func startInMemoryOrderGRPCServer(t *testing.T) (*bufconn.Listener, func()) {
	lis := bufconn.Listen(bufSize)
	baseServer := grpc.NewServer()

	// Register generic stream handler for client-streaming StreamOrderItems
	streamDesc := grpc.StreamDesc{
		StreamName:    "StreamOrderItems",
		ServerStreams: false,
		ClientStreams: true,
		Handler: func(srv any, stream grpc.ServerStream) error {
			var receivedIDs []string
			for {
				var req client.StreamOrderItemRequest
				err := stream.RecvMsg(&req)
				if err == io.EOF {
					// Client finished streaming; return consolidated response
					resp := client.StreamOrderItemResponse{
						Items: []models.OrderItemDetail{
							{FlavorID: "flv_01", FlavorName: "Pistachio", Portions: 3, SubtotalMinor: 15000},
							{FlavorID: "flv_02", FlavorName: "Strawberry", Portions: 2, SubtotalMinor: 10000},
						},
					}
					return stream.SendMsg(&resp)
				}
				if err != nil {
					return err
				}
				receivedIDs = append(receivedIDs, req.OrderID)
			}
		},
	}

	// Register unary handler for GetOrderDetails
	unaryHandler := func(srv any, ctx context.Context, dec func(any) error, interp grpc.UnaryServerInterceptor) (any, error) {
		var req client.GetOrderDetailsRequest
		if err := dec(&req); err != nil {
			return nil, err
		}
		return &models.OrderDetails{
			OrderID:          req.OrderID,
			CustomerID:       "cust_123",
			Status:           "PAID",
			CreatedAt:        "2026-08-20T10:00:00Z",
			TotalAmountMinor: 25000,
			Currency:         "THB",
			Items: []models.OrderItemDetail{
				{FlavorID: "flv_01", FlavorName: "Pistachio", Portions: 3, SubtotalMinor: 15000},
				{FlavorID: "flv_02", FlavorName: "Strawberry", Portions: 2, SubtotalMinor: 10000},
			},
		}, nil
	}

	methodDesc := grpc.MethodDesc{
		MethodName: "GetOrderDetails",
		Handler:    unaryHandler,
	}

	sd := grpc.ServiceDesc{
		ServiceName: "order.v1.OrderService",
		HandlerType: (*any)(nil),
		Methods:     []grpc.MethodDesc{methodDesc},
		Streams:     []grpc.StreamDesc{streamDesc},
	}
	baseServer.RegisterService(&sd, &inMemoryOrderServer{})

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
	})
}
