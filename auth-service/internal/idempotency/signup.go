// Package idempotency binds an optional signup operation key to private context.
package idempotency

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrConflict    = errors.New("signup idempotency key conflicts with original input")
	ErrUnavailable = errors.New("signup replay storage unavailable")
)

type keyContext struct{}

func WithKey(ctx context.Context, key uuid.UUID) context.Context {
	return context.WithValue(ctx, keyContext{}, key)
}

func Key(ctx context.Context) (uuid.UUID, bool) {
	key, ok := ctx.Value(keyContext{}).(uuid.UUID)
	return key, ok && key != uuid.Nil
}
