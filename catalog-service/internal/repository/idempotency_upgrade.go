package repository

import (
	"context"
	"strings"

	"catalog-service/internal/idempotency"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The old middleware stored every spelling accepted by uuid.Parse, including
// mixed case, compact UUIDs, URNs and arbitrary 38-byte wrappers. Enumerating
// spellings at request time is incomplete. Copy legacy entries to their canonical
// key before serving keyed creates, preserving both their result and expiry.
// Conflicting historical results block that UUID rather than choosing a winner.
var reconcileIdempotencyScript = redis.NewScript(`
local sourceType = redis.call("TYPE", KEYS[1]).ok
if sourceType == "none" then return 0 end
local destinationType = redis.call("TYPE", KEYS[2]).ok
if destinationType ~= "none" and destinationType ~= "string" then return 0 end
local sourceTTL = redis.call("PTTL", KEYS[1])
if sourceTTL == -2 then return 0 end
local destinationTTL = redis.call("PTTL", KEYS[2])
local ttl = sourceTTL
if destinationTTL == -1 or sourceTTL == -1 then ttl = -1
elseif destinationTTL > ttl then ttl = destinationTTL end
local source = nil
if sourceType == "string" then source = redis.call("GET", KEYS[1]) end
local destination = redis.call("GET", KEYS[2])
local function decode(value)
  if not value then return nil end
  local ok, record = pcall(cjson.decode, value)
  if ok and type(record) == "table" and type(record.payload_hash) == "string" and
     type(record.body) == "string" and record.status_code == 201 then return record end
  return nil
end
local record, previous = decode(source), decode(destination)
local value = source
if not record or (destination and (not previous or
   previous.payload_hash ~= record.payload_hash or previous.body ~= record.body)) then
  value = '{"legacy_conflict":true}'
end
if ttl == -1 then redis.call("SET", KEYS[2], value)
elseif ttl > 0 then redis.call("SET", KEYS[2], value, "PX", ttl) end
return 1
`)

// PrepareIdempotency must finish before opening listeners after an upgrade.
// Stop old Catalog writers before cutover: their non-atomic write path cannot
// participate in the new invariant. The lazy guard also protects embedded users.
// Only SCAN is used for discovery; legacy keys are retained until their original
// expiry, so concurrent scans cannot miss a key removed by another upgrader.
func (r *RedisFlavorRepository) PrepareIdempotency(ctx context.Context) error {
	r.legacyMu.Lock()
	defer r.legacyMu.Unlock()
	if r.legacyReady {
		return nil
	}
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, "idempotency:*", 200).Result()
		if err != nil {
			return idempotency.ErrUnavailable
		}
		for _, key := range keys {
			id, err := uuid.Parse(strings.TrimPrefix(key, "idempotency:"))
			if err != nil || key == "idempotency:"+id.String() {
				continue
			}
			if err := reconcileIdempotencyScript.Run(ctx, r.client, []string{key, "idempotency:" + id.String()}).Err(); err != nil {
				return idempotency.ErrUnavailable
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	r.legacyReady = true
	return nil
}
