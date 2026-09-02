package client

import (
	"context"
	"log"

	"order-service/pkg/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type InventoryClient interface {
	ReservePortions(ctx context.Context, req *pb.ReserveRequest) (*pb.ReserveResponse, error)
	Close() error
}

type inventoryClientImpl struct {
	conn   *grpc.ClientConn
	client pb.InventoryServiceClient
}

func NewInventoryClient(address string) (InventoryClient, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	
	client := pb.NewInventoryServiceClient(conn)
	log.Println("Connected to Batch Inventory Service via gRPC at", address)
	
	return &inventoryClientImpl{
		conn:   conn,
		client: client,
	}, nil
}

func (c *inventoryClientImpl) ReservePortions(ctx context.Context, req *pb.ReserveRequest) (*pb.ReserveResponse, error) {
	return c.client.ReservePortions(ctx, req)
}

func (c *inventoryClientImpl) Close() error {
	return c.conn.Close()
}
