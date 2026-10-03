package client

import (
	"context"
	"errors"

	catalogv1 "order-service/gen/catalog/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Catalog interface {
	BatchGetFlavors(context.Context, []string) ([]*catalogv1.Flavor, error)
	Close() error
}

type authorizationKey struct{}

func WithUserAuthorization(ctx context.Context, bearer string) context.Context {
	return context.WithValue(ctx, authorizationKey{}, bearer)
}

type catalogClient struct {
	conn  *grpc.ClientConn
	inner catalogv1.CatalogServiceClient
}

func NewCatalogClient(address string) (Catalog, error) {
	if address == "" {
		return nil, errors.New("catalog gRPC address is required")
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &catalogClient{conn: conn, inner: catalogv1.NewCatalogServiceClient(conn)}, nil
}

func (c *catalogClient) BatchGetFlavors(ctx context.Context, ids []string) ([]*catalogv1.Flavor, error) {
	callCtx := ctx
	if bearer, ok := ctx.Value(authorizationKey{}).(string); ok && bearer != "" {
		callCtx = metadata.AppendToOutgoingContext(ctx, "authorization", bearer)
	}
	resp, err := c.inner.BatchGetFlavors(callCtx, &catalogv1.BatchGetFlavorsRequest{FlavorIds: ids})
	if err != nil {
		return nil, err
	}
	return resp.GetItems(), nil
}

func (c *catalogClient) Close() error { return c.conn.Close() }
