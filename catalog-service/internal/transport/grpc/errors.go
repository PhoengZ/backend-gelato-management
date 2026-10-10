package cataloggrpc

import (
	"context"
	"errors"
	"io"
	"net"

	"catalog-service/internal/repository"
	"catalog-service/internal/service"

	"github.com/redis/go-redis/v9"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func rpcError(code codes.Code, reason, message string) error {
	s := status.New(code, message)
	withDetails, err := s.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "catalog.gelatoflow"})
	if err == nil {
		return withDetails.Err()
	}
	return s.Err()
}

func invalidInput(message string) error {
	return rpcError(codes.InvalidArgument, "INVALID_ARGUMENT", message)
}

// serviceError never sends Redis errors, connection details or stored data to callers.
func serviceError(err error) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return rpcError(codes.Canceled, "CANCELLED", "Request cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return rpcError(codes.DeadlineExceeded, "DEADLINE_EXCEEDED", "Request deadline exceeded")
	case errors.Is(err, service.ErrInvalidInput):
		return invalidInput("Invalid flavor input")
	case errors.Is(err, repository.ErrFlavorNotFound):
		return rpcError(codes.NotFound, "FLAVOR_NOT_FOUND", "Flavor not found")
	case errors.Is(err, repository.ErrNameConflict):
		return rpcError(codes.AlreadyExists, "FLAVOR_NAME_CONFLICT", "Flavor name already exists")
	case errors.Is(err, repository.ErrUpdateConflict):
		return rpcError(codes.Aborted, "FLAVOR_UPDATE_CONFLICT", "Flavor changed during update; read again before retrying")
	case errors.As(err, &netErr), errors.Is(err, io.EOF), errors.Is(err, redis.ErrClosed):
		return rpcError(codes.Unavailable, "CATALOG_UNAVAILABLE", "Catalog storage is unavailable")
	default:
		return rpcError(codes.Internal, "INTERNAL_ERROR", "Catalog operation failed")
	}
}
