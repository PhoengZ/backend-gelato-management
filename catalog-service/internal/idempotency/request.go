package idempotency

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"time"
)

const TTL = 24 * time.Hour

var ErrConflict = errors.New("idempotency key already used with a different payload")
var ErrUnavailable = errors.New("atomic Catalog idempotency storage is unavailable")

type Request struct {
	Key         uuid.UUID
	RawKey      string
	PayloadHash string
	// Scope is empty for the existing POST replay format. Other REST mutations
	// bind their method and canonical resource UUID without changing gRPC.
	Scope string
}
type requestKey struct{}

func WithRequest(ctx context.Context, request Request) context.Context {
	return context.WithValue(ctx, requestKey{}, request)
}
func FromContext(ctx context.Context) (Request, bool) {
	request, ok := ctx.Value(requestKey{}).(Request)
	return request, ok
}
