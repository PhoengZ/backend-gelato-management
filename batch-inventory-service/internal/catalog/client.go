package catalog

import (
	pb "batch-inventory-service/gen/catalog/v1"
	"batch-inventory-service/internal/inventory"
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

type Client struct {
	RPC     pb.CatalogServiceClient
	Timeout time.Duration
}

func New(conn grpc.ClientConnInterface, timeout time.Duration) *Client {
	return &Client{RPC: pb.NewCatalogServiceClient(conn), Timeout: timeout}
}
func (c *Client) CheckActive(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	// Public Catalog projection validates active flavors without granting this
	// service access to Manager writes or recipes.
	r, err := c.RPC.GetFlavor(ctx, &pb.GetFlavorRequest{FlavorId: id})
	if status.Code(err) == codes.NotFound {
		return fmt.Errorf("%w: flavor is missing or inactive", inventory.ErrInvalid)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return inventory.ErrDependency
	}
	if r.GetFlavor() == nil || r.Flavor.Id != id || !r.Flavor.Active {
		return fmt.Errorf("%w: flavor is inactive", inventory.ErrInvalid)
	}
	return nil
}
