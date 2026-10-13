package client

import (
	"context"
	"fmt"
	"io"
	"log"

	orderv1 "analytics-service/gen/order/v1"
	"analytics-service/internal/models"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// OrderClient defines the interface for communicating with Order Service via gRPC.
type OrderClient interface {
	// StreamOrderItems streams multiple order IDs over a single client stream and receives consolidated items.
	StreamOrderItems(ctx context.Context, orderIDs []string) ([]models.OrderItemDetail, error)
	// GetOrderDetails fetches complete order details for a single order via unary gRPC.
	GetOrderDetails(ctx context.Context, orderID string) (*models.OrderDetails, error)
	// Close terminates the underlying gRPC connection.
	Close() error
}

type grpcOrderClient struct {
	conn   *grpc.ClientConn
	client orderv1.OrderServiceClient
}

// NewOrderClient creates a new gRPC client connection to Order Service.
func NewOrderClient(addr string, opts ...grpc.DialOption) (OrderClient, error) {
	if addr == "" {
		addr = "localhost:50051"
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	dialOpts = append(dialOpts, opts...)

	conn, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for order service: %w", err)
	}

	return &grpcOrderClient{
		conn:   conn,
		client: orderv1.NewOrderServiceClient(conn),
	}, nil
}

// StreamOrderItems streams a list of order IDs over a single HTTP/2 client stream using the generated protobuf stub.
func (c *grpcOrderClient) StreamOrderItems(ctx context.Context, orderIDs []string) ([]models.OrderItemDetail, error) {
	stream, err := c.client.StreamOrderItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gRPC client stream: %w", err)
	}

	// Stream each order ID to the server
	for _, id := range orderIDs {
		req := &orderv1.StreamOrderItemsRequest{OrderId: id}
		if err := stream.Send(req); err != nil {
			return nil, fmt.Errorf("failed to stream order id %s: %w", id, err)
		}
	}

	// Close the send direction and receive response
	resp, err := stream.CloseAndRecv()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to receive consolidated stream response: %w", err)
	}

	if resp == nil {
		return nil, nil
	}

	items := make([]models.OrderItemDetail, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, models.OrderItemDetail{
			FlavorID:       item.FlavorId,
			FlavorName:     item.FlavorName,
			Portions:       int(item.Portions),
			UnitPriceMinor: item.UnitPriceMinor,
			SubtotalMinor:  item.SubtotalMinor,
		})
	}

	return items, nil
}

// GetOrderDetails calls unary GetOrderDetails on Order Service using the generated protobuf stub.
func (c *grpcOrderClient) GetOrderDetails(ctx context.Context, orderID string) (*models.OrderDetails, error) {
	req := &orderv1.GetOrderDetailsRequest{OrderId: orderID}
	resp, err := c.client.GetOrderDetails(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("gRPC GetOrderDetails failed for %s: %w", orderID, err)
	}

	if resp == nil {
		return nil, nil
	}

	items := make([]models.OrderItemDetail, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, models.OrderItemDetail{
			FlavorID:       item.FlavorId,
			FlavorName:     item.FlavorName,
			Portions:       int(item.Portions),
			UnitPriceMinor: item.UnitPriceMinor,
			SubtotalMinor:  item.SubtotalMinor,
		})
	}

	return &models.OrderDetails{
		OrderID:          resp.OrderId,
		CustomerID:       resp.CustomerId,
		Status:           resp.Status,
		CreatedAt:        resp.PickupAt,
		TotalAmountMinor: resp.TotalAmountMinor,
		Currency:         resp.Currency,
		Items:            items,
	}, nil
}

func (c *grpcOrderClient) Close() error {
	if c.conn != nil {
		log.Println("Closing Order Service gRPC connection...")
		return c.conn.Close()
	}
	return nil
}
