package transport

import (
	pb "batch-inventory-service/gen/inventory/v1"
	"batch-inventory-service/internal/inventory"
	"context"
	"crypto/subtle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"time"
)

type grpcHandler struct {
	pb.UnimplementedInventoryServiceServer
	svc *inventory.Service
}

func NewGRPC(svc *inventory.Service, orderToken string, timeout time.Duration) *grpc.Server {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.MaxSendMsgSize(4<<20), grpc.MaxHeaderListSize(16<<10), grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("authorization")
		if len(values) != 1 {
			return nil, status.Error(codes.Unauthenticated, "Order service credential required")
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(orderToken) < 32 || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(orderToken)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "Invalid service credential")
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		res, err := next(ctx, req)
		if err != nil && ctx.Err() != nil {
			return nil, rpcError(ctx.Err())
		}
		return res, err
	}))
	pb.RegisterInventoryServiceServer(server, &grpcHandler{svc: svc})
	return server
}
func itemsFromProto(items []*pb.PortionRequest) []inventory.Item {
	out := make([]inventory.Item, len(items))
	for i, v := range items {
		out[i] = inventory.Item{FlavorID: v.GetFlavorId(), Portions: int64(v.GetPortions())}
	}
	return out
}
func toProto(r inventory.Reservation) *pb.Reservation {
	out := &pb.Reservation{ReservationId: r.ID, OrderId: r.OrderID, Status: pb.ReservationStatus(pb.ReservationStatus_value["RESERVATION_STATUS_"+r.Status]), CreatedAt: timestamppb.New(r.CreatedAt), ExpiresAt: timestamppb.New(r.ExpiresAt)}
	for _, a := range r.Allocations {
		out.Allocations = append(out.Allocations, &pb.BatchAllocation{FlavorId: a.FlavorID, BatchId: a.BatchID, Portions: int32(a.Portions)})
	}
	return out
}
func (h *grpcHandler) CheckAvailability(ctx context.Context, r *pb.CheckAvailabilityRequest) (*pb.CheckAvailabilityResponse, error) {
	items := itemsFromProto(r.GetItems())
	out, t, err := h.svc.Check(ctx, items)
	if err != nil {
		return nil, rpcError(err)
	}
	result := &pb.CheckAvailabilityResponse{AllAvailable: true, AsOf: timestamppb.New(t)}
	for i, a := range out {
		if a.Available > inventory.MaxPortions {
			return nil, status.Error(codes.OutOfRange, "Availability exceeds v1 int32 capacity")
		}
		sufficient := a.Available >= items[i].Portions
		result.AllAvailable = result.AllAvailable && sufficient
		result.Items = append(result.Items, &pb.FlavorAvailability{FlavorId: a.FlavorID, RequestedPortions: int32(items[i].Portions), AvailablePortions: int32(a.Available), Sufficient: sufficient})
	}
	return result, nil
}
func (h *grpcHandler) ReservePortions(ctx context.Context, r *pb.ReservePortionsRequest) (*pb.ReservePortionsResponse, error) {
	result, err := h.svc.Reserve(ctx, inventory.ReserveInput{OrderID: r.GetOrderId(), Key: r.GetIdempotencyKey(), Items: itemsFromProto(r.GetItems())})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.ReservePortionsResponse{Reservation: toProto(result)}, nil
}
func (h *grpcHandler) ConfirmReservation(ctx context.Context, r *pb.ConfirmReservationRequest) (*pb.ConfirmReservationResponse, error) {
	result, err := h.svc.Transition(ctx, true, inventory.TransitionInput{ReservationID: r.GetReservationId(), OrderID: r.GetOrderId(), Key: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.ConfirmReservationResponse{Reservation: toProto(result)}, nil
}
func (h *grpcHandler) ReleaseReservation(ctx context.Context, r *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {
	result, err := h.svc.Transition(ctx, false, inventory.TransitionInput{ReservationID: r.GetReservationId(), OrderID: r.GetOrderId(), Key: r.GetIdempotencyKey(), Reason: r.GetReason()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.ReleaseReservationResponse{Reservation: toProto(result)}, nil
}
