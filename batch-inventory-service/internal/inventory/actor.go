package inventory

import (
	"context"
	"github.com/google/uuid"
)

type actorKey struct{}
type actor struct{ id, role string }

// WithActor binds an identity verified by the service's transport, never JSON or
// caller-supplied identity headers. Invalid identities clear inherited context.
func WithActor(ctx context.Context, subject, role string) context.Context {
	id, err := uuid.Parse(subject)
	value := actor{}
	if err == nil && id != uuid.Nil && (role == "MANAGER" || role == "STAFF") {
		value = actor{id.String(), role}
	}
	return context.WithValue(ctx, actorKey{}, value)
}
func currentActor(ctx context.Context) actor { value, _ := ctx.Value(actorKey{}).(actor); return value }
