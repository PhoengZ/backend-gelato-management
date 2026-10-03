package client

import (
	"context"
	"errors"

	"order-service/gen/inventory/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Inventory interface {
	CheckAvailability(context.Context, *inventoryv1.CheckAvailabilityRequest) (*inventoryv1.CheckAvailabilityResponse, error)
	ReservePortions(context.Context, *inventoryv1.ReservePortionsRequest) (*inventoryv1.ReservePortionsResponse, error)
	ConfirmReservation(context.Context, *inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error)
	ReleaseReservation(context.Context, *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error)
	Close() error
}

type inventoryClient struct {
	conn  *grpc.ClientConn
	inner inventoryv1.InventoryServiceClient
	token string
}

func NewInventoryClient(address, token string) (Inventory, error) {
	if address == "" || token == "" {
		return nil, errors.New("inventory address and order service token are required")
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &inventoryClient{conn: conn, inner: inventoryv1.NewInventoryServiceClient(conn), token: token}, nil
}

func (c *inventoryClient) authenticated(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
}

func (c *inventoryClient) CheckAvailability(ctx context.Context, req *inventoryv1.CheckAvailabilityRequest) (*inventoryv1.CheckAvailabilityResponse, error) {
	return c.inner.CheckAvailability(c.authenticated(ctx), req)
}
func (c *inventoryClient) ReservePortions(ctx context.Context, req *inventoryv1.ReservePortionsRequest) (*inventoryv1.ReservePortionsResponse, error) {
	return c.inner.ReservePortions(c.authenticated(ctx), req)
}
func (c *inventoryClient) ConfirmReservation(ctx context.Context, req *inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
	return c.inner.ConfirmReservation(c.authenticated(ctx), req)
}
func (c *inventoryClient) ReleaseReservation(ctx context.Context, req *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
	return c.inner.ReleaseReservation(c.authenticated(ctx), req)
}
func (c *inventoryClient) Close() error { return c.conn.Close() }
