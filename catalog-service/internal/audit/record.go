// Package audit carries verified request identity and records committed Catalog
// mutations without copying credentials, recipes, or freeform text into logs.
package audit

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"catalog-service/internal/model"
	"github.com/google/uuid"
)

type Action string

const (
	Create  Action = "CREATE"
	Update  Action = "UPDATE"
	Replace Action = "REPLACE"
	Archive Action = "DELETE"
)

var ErrActorRequired = errors.New("verified Catalog manager identity is required for audit")

type actorKey struct{}
type actionKey struct{}
type actor struct {
	id   uuid.UUID
	role string
}

// ManagerID reads only identity bound by the locally verified transport.
func ManagerID(ctx context.Context) (uuid.UUID, error) {
	a, ok := ctx.Value(actorKey{}).(actor)
	if !ok || a.id == uuid.Nil || a.role != "MANAGER" {
		return uuid.Nil, ErrActorRequired
	}
	return a.id, nil
}

// WithActor must be called only after JWT verification, never from identity
// headers, query parameters, or user-controlled request bodies.
func WithActor(ctx context.Context, subject, role string) context.Context {
	id, err := uuid.Parse(subject)
	if err != nil || id == uuid.Nil || role != "MANAGER" {
		return context.WithValue(ctx, actorKey{}, actor{})
	}
	return context.WithValue(ctx, actorKey{}, actor{id: id, role: role})
}

func WithAction(ctx context.Context, action Action) context.Context {
	return context.WithValue(ctx, actionKey{}, action)
}

func MutationAction(ctx context.Context) Action {
	if action, ok := ctx.Value(actionKey{}).(Action); ok {
		return action
	}
	return Update
}

// State retains the safety-critical before/after facts. Other changes are
// identified by field name only, avoiding copies of recipe/text/URL values.
type State struct {
	Allergens []model.Allergen `json:"allergens"`
	Active    bool             `json:"active"`
}

type Record struct {
	ID            uuid.UUID `json:"id"`
	ActorID       uuid.UUID `json:"actor_id"`
	ActorRole     string    `json:"actor_role"`
	Action        Action    `json:"action"`
	FlavorID      uuid.UUID `json:"flavor_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	ChangedFields []string  `json:"changed_fields"`
	Before        *State    `json:"before"`
	After         State     `json:"after"`
}

func NewRecord(ctx context.Context, action Action, before, after *model.FlavorAdmin) (Record, error) {
	a, ok := ctx.Value(actorKey{}).(actor)
	if !ok || a.id == uuid.Nil || a.role != "MANAGER" {
		return Record{}, ErrActorRequired
	}
	if action != Create && action != Update && action != Replace && action != Archive {
		return Record{}, errors.New("invalid Catalog audit action")
	}
	if after == nil || after.ID == uuid.Nil || (before != nil && before.ID != after.ID) || (action != Create && before == nil) {
		return Record{}, errors.New("invalid Catalog audit resource")
	}
	record := Record{ID: uuid.New(), ActorID: a.id, ActorRole: a.role, Action: action,
		FlavorID: after.ID, OccurredAt: time.Now().UTC(), ChangedFields: []string{}, After: state(after)}
	if before == nil {
		record.ChangedFields = []string{"name", "description", "price", "image_url", "allergens", "recipe", "active"}
		return record, nil
	}
	snapshot := state(before)
	record.Before = &snapshot
	for _, change := range []struct {
		field   string
		changed bool
	}{
		{"name", before.Name != after.Name}, {"description", before.Description != after.Description},
		{"price", before.Price != after.Price}, {"image_url", before.ImageURL != after.ImageURL},
		{"allergens", !slices.Equal(before.Allergens, after.Allergens)},
		{"recipe", before.Recipe != after.Recipe}, {"active", before.Active != after.Active},
	} {
		if change.changed {
			record.ChangedFields = append(record.ChangedFields, change.field)
		}
	}
	return record, nil
}

func state(flavor *model.FlavorAdmin) State {
	return State{Allergens: append([]model.Allergen{}, flavor.Allergens...), Active: flavor.Active}
}

// LogCommitted is an operational receipt, not the durable ledger. The Redis
// repository stores the authoritative record atomically with the mutation.
func LogCommitted(ctx context.Context, action Action, id uuid.UUID) {
	a, ok := ctx.Value(actorKey{}).(actor)
	if !ok || a.id == uuid.Nil || a.role != "MANAGER" || id == uuid.Nil {
		return
	}
	log.Printf("[AUDIT] action=%s actor_id=%s resource=flavor resource_id=%s", action, a.id, id)
}
