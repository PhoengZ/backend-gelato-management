package transport

import (
	"batch-inventory-service/internal/inventory"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"strings"
)

func classify(err error) (int, codes.Code, string, string) {
	var ne net.Error
	var pe *pgconn.PgError
	var ce *pgconn.ConnectError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return 504, codes.DeadlineExceeded, "DEADLINE_EXCEEDED", "Request timed out"
	case errors.Is(err, context.Canceled):
		return 408, codes.Canceled, "CANCELLED", "Request cancelled"
	case errors.Is(err, inventory.ErrInvalid):
		return 400, codes.InvalidArgument, "INVALID_ARGUMENT", "Invalid request"
	case errors.Is(err, inventory.ErrNotFound):
		return 404, codes.NotFound, "NOT_FOUND", "Resource not found"
	case errors.Is(err, inventory.ErrConflict):
		return 409, codes.AlreadyExists, "IDEMPOTENCY_CONFLICT", "Key or order already used with another request"
	case errors.Is(err, inventory.ErrStock):
		return 409, codes.FailedPrecondition, "INSUFFICIENT_STOCK", "Insufficient available portions"
	case errors.Is(err, inventory.ErrState):
		return 409, codes.FailedPrecondition, "INVALID_STATE", "Invalid or expired reservation state"
	case errors.Is(err, inventory.ErrDependency), errors.As(err, &ne), errors.As(err, &ce):
		return 503, codes.Unavailable, "DEPENDENCY_UNAVAILABLE", "Dependency unavailable"
	case errors.As(err, &pe):
		if strings.HasPrefix(pe.Code, "08") || pe.Code == "57P01" || pe.Code == "57P02" || pe.Code == "57P03" || pe.Code == "53300" {
			return 503, codes.Unavailable, "DEPENDENCY_UNAVAILABLE", "Dependency unavailable"
		}
		if pe.Code == "40001" || pe.Code == "40P01" || pe.Code == "55P03" {
			return 503, codes.Unavailable, "RETRYABLE_CONFLICT", "Retry with the same idempotency key"
		}
	}
	return 500, codes.Internal, "INTERNAL_ERROR", "Inventory operation failed"
}
func rpcError(err error) error {
	if err == nil {
		return nil
	}
	_, code, _, message := classify(err)
	return status.Error(code, message)
}
