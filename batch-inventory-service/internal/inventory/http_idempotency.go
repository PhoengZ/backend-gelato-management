package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

type restKeyContext struct{}

// WithRESTKey adds an optional UUID header without replacing the canonical
// Waste body key or changing the reservation gRPC contract.
func WithRESTKey(ctx context.Context, raw string) (context.Context, error) {
	key, err := ID(raw)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, restKeyContext{}, key), nil
}

func restKey(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(restKeyContext{}).(string)
	return key, ok && key != ""
}

// A read-only optimization lets a committed retry succeed when Catalog is now
// unavailable/archived. mutate repeats the key/hash decision inside its stock
// transaction, so concurrent cache misses cannot duplicate production.
func lookupRESTMutation[T any](ctx context.Context, s *Service, op, key string, input any) (T, bool, error) {
	var result T
	body, err := json.Marshal(input)
	if err != nil {
		return result, false, err
	}
	hash := sha256.Sum256(body)
	var savedHash, response []byte
	err = s.DB.QueryRow(ctx, "SELECT request_hash,response FROM idempotency_records WHERE operation=$1 AND key=$2", op, key).Scan(&savedHash, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if !bytes.Equal(hash[:], savedHash) {
		return result, false, ErrConflict
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return result, false, err
	}
	return result, true, nil
}
