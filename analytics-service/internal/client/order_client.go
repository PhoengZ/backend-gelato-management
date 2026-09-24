package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"analytics-service/internal/models"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
)

// jsonCodec implements encoding.Codec using JSON serialization for contract messages.
type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (jsonCodec) Name() string {
	return "json"
}

func init() {
	encoding.RegisterCodec(jsonCodec{})
}

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
	conn *grpc.ClientConn
}

// NewOrderClient creates a new gRPC client connection to Order Service.
func NewOrderClient(addr string, opts ...grpc.DialOption) (OrderClient, error) {
	if addr == "" {
		addr = "localhost:50051"
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((jsonCodec{}).Name())),
	}
	dialOpts = append(dialOpts, opts...)

	conn, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for order service: %w", err)
	}

	return &grpcOrderClient{conn: conn}, nil
}

// StreamOrderItemRequest message for streaming order IDs
type StreamOrderItemRequest struct {
	OrderID string `json:"order_id"`
}

// StreamOrderItemResponse message returning consolidated items
type StreamOrderItemResponse struct {
	Items []models.OrderItemDetail `json:"items"`
}

// GetOrderDetailsRequest message
type GetOrderDetailsRequest struct {
	OrderID string `json:"order_id"`
}


// StreamOrderItems streams a list of order IDs over a single HTTP/2 client stream.
func (c *grpcOrderClient) StreamOrderItems(ctx context.Context, orderIDs []string) ([]models.OrderItemDetail, error) {
	desc := &grpc.StreamDesc{
		StreamName:    "StreamOrderItems",
		ServerStreams: false,
		ClientStreams: true,
	}

	stream, err := c.conn.NewStream(ctx, desc, "/order.v1.OrderService/StreamOrderItems")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gRPC client stream: %w", err)
	}

	// Stream each order ID to the server
	for _, id := range orderIDs {
		req := StreamOrderItemRequest{OrderID: id}
		if err := stream.SendMsg(&req); err != nil {
			return nil, fmt.Errorf("failed to stream order id %s: %w", id, err)
		}
	}

	// Close the send direction
	if err := stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("failed to close send stream: %w", err)
	}

	// Receive consolidated response
	var resp StreamOrderItemResponse
	if err := stream.RecvMsg(&resp); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to receive consolidated stream response: %w", err)
	}

	return resp.Items, nil
}

// GetOrderDetails calls unary GetOrderDetails on Order Service.
func (c *grpcOrderClient) GetOrderDetails(ctx context.Context, orderID string) (*models.OrderDetails, error) {
	req := GetOrderDetailsRequest{OrderID: orderID}
	var resp models.OrderDetails

	err := c.conn.Invoke(ctx, "/order.v1.OrderService/GetOrderDetails", &req, &resp)
	if err != nil {
		return nil, fmt.Errorf("gRPC GetOrderDetails failed for %s: %w", orderID, err)
	}

	return &resp, nil
}

func (c *grpcOrderClient) Close() error {
	if c.conn != nil {
		log.Println("Closing Order Service gRPC connection...")
		return c.conn.Close()
	}
	return nil
}
