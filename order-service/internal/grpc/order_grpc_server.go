package grpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	orderv1 "order-service/gen/order/v1"
	"order-service/internal/repository"

	"github.com/google/uuid"
	grpcbase "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ orderv1.OrderServiceServer = (*OrderGRPCServer)(nil)

type OrderGRPCServer struct {
	orderv1.UnimplementedOrderServiceServer
	repo repository.OrderRepository
}

func NewOrderGRPCServer(repo repository.OrderRepository) *OrderGRPCServer {
	return &OrderGRPCServer{
		repo: repo,
	}
}

// StreamOrderItems is a client-streaming RPC. Reads streamed order IDs,
// queries order items from repository, and returns consolidated StreamOrderItemsResponse.
func (s *OrderGRPCServer) StreamOrderItems(stream grpcbase.ClientStreamingServer[orderv1.StreamOrderItemsRequest, orderv1.StreamOrderItemsResponse]) error {
	var consolidatedItems []*orderv1.OrderItem
	ctx := stream.Context()

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&orderv1.StreamOrderItemsResponse{
				Items: consolidatedItems,
			})
		}
		if err != nil {
			return status.Errorf(codes.Internal, "recv error: %v", err)
		}

		orderID := strings.TrimSpace(req.GetOrderId())
		if orderID == "" {
			continue
		}
		if _, err := uuid.Parse(orderID); err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid order_id %q: must be a UUID", orderID)
		}

		order, err := s.repo.Get(ctx, orderID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return status.Errorf(codes.NotFound, "order %q not found", orderID)
			}
			return status.Errorf(codes.Internal, "failed to get order %q: %v", orderID, err)
		}

		for _, item := range order.Items {
			consolidatedItems = append(consolidatedItems, &orderv1.OrderItem{
				FlavorId:       item.FlavorID,
				FlavorName:     item.FlavorName,
				Portions:       item.Portions,
				UnitPriceMinor: item.UnitPriceMinor,
				SubtotalMinor:  item.SubtotalMinor,
			})
		}
	}
}

// GetOrderDetails is a unary RPC. Validates UUID, queries order details,
// and returns GetOrderDetailsResponse.
func (s *OrderGRPCServer) GetOrderDetails(ctx context.Context, req *orderv1.GetOrderDetailsRequest) (*orderv1.GetOrderDetailsResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "request is required")
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "order_id is required")
	}
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid order_id %q: must be a UUID", orderID)
	}

	order, err := s.repo.Get(ctx, orderID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "order %q not found", orderID)
		}
		return nil, status.Errorf(codes.Internal, "failed to get order: %v", err)
	}

	items := make([]*orderv1.OrderItem, 0, len(order.Items))
	for _, it := range order.Items {
		items = append(items, &orderv1.OrderItem{
			FlavorId:       it.FlavorID,
			FlavorName:     it.FlavorName,
			Portions:       it.Portions,
			UnitPriceMinor: it.UnitPriceMinor,
			SubtotalMinor:  it.SubtotalMinor,
		})
	}

	return &orderv1.GetOrderDetailsResponse{
		OrderId:          order.ID,
		CustomerId:       order.CustomerID,
		Status:           string(order.Status),
		PickupAt:         order.PickupAt.UTC().Format(time.RFC3339),
		TotalAmountMinor: order.TotalAmountMinor,
		Currency:         order.Currency,
		Items:            items,
		PaymentIntentId:  order.CheckoutSessionID,
	}, nil
}

// BindPaymentIntent is a fallback stub returning codes.Unimplemented.
func (s *OrderGRPCServer) BindPaymentIntent(ctx context.Context, req *orderv1.BindPaymentIntentRequest) (*orderv1.BindPaymentIntentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "BindPaymentIntent is not yet implemented")
}

// ReportPaymentResult is a fallback stub returning codes.Unimplemented.
func (s *OrderGRPCServer) ReportPaymentResult(ctx context.Context, req *orderv1.ReportPaymentResultRequest) (*orderv1.ReportPaymentResultResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ReportPaymentResult is not yet implemented")
}
