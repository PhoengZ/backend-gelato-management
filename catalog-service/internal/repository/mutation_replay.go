package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"catalog-service/internal/audit"
	"catalog-service/internal/idempotency"
	"catalog-service/internal/model"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The unkeyed update script stays unchanged. This script commits a keyed
// mutation, its audit and its original response together, including no-op PUTs
// and archives. Every fallible type/ACL check precedes the first write.
var mutationReplayScript = redis.NewScript(`
local cacheType = redis.call("TYPE", KEYS[5]).ok
if cacheType ~= "none" and cacheType ~= "string" then return -4 end
local cached = redis.call("GET", KEYS[5])
if cached then
  local ok, record = pcall(cjson.decode, cached)
  if not ok or type(record) ~= "table" or type(record.payload_hash) ~= "string" or
     type(record.body) ~= "string" or (record.status_code ~= 200 and record.status_code ~= 204) then return -4 end
  if record.payload_hash ~= ARGV[5] then return -5 end
  return {2, cached}
end
if not redis.acl_check_cmd("SET", KEYS[5], ARGV[6], "PX", ARGV[7]) then return -4 end
local flavorType = redis.call("TYPE", KEYS[1]).ok
if flavorType ~= "none" and flavorType ~= "string" then return -3 end
local current = redis.call("GET", KEYS[1])
if not current then return -1 end
if current ~= ARGV[3] then return -2 end
if ARGV[1] == ARGV[3] then
  redis.call("SET", KEYS[5], ARGV[6], "PX", ARGV[7])
  return {1, ARGV[8]}
end
local expected = {"string", "string", "string", "list"}
for i, kind in ipairs(expected) do
  local actual = redis.call("TYPE", KEYS[i]).ok
  if actual ~= "none" and actual ~= kind then return -3 end
end
local owner = redis.call("GET", KEYS[3])
if owner and owner ~= ARGV[2] then return 0 end
if not redis.acl_check_cmd("SET", KEYS[1], ARGV[1]) or
   not redis.acl_check_cmd("SET", KEYS[3], ARGV[2]) or
   not redis.acl_check_cmd("RPUSH", KEYS[4], ARGV[4]) then return -3 end
if KEYS[2] ~= KEYS[3] and redis.call("GET", KEYS[2]) == ARGV[2] then
  if not redis.acl_check_cmd("DEL", KEYS[2]) then return -3 end
end
if KEYS[2] ~= KEYS[3] and redis.call("GET", KEYS[2]) == ARGV[2] then redis.call("DEL", KEYS[2]) end
redis.call("SET", KEYS[3], ARGV[2])
redis.call("SET", KEYS[1], ARGV[1])
redis.call("RPUSH", KEYS[4], ARGV[4])
redis.call("SET", KEYS[5], ARGV[6], "PX", ARGV[7])
return {1, ARGV[8]}
`)

type mutationReply struct {
	PayloadHash string `json:"payload_hash"`
	StatusCode  int    `json:"status_code"`
	Body        string `json:"body"`
}

func mutationFingerprint(ctx context.Context, id uuid.UUID, request idempotency.Request) (string, int, error) {
	actor, err := audit.ManagerID(ctx)
	if err != nil {
		return "", 0, err
	}
	method, status := "", 200
	switch audit.MutationAction(ctx) {
	case audit.Update:
		method = "PATCH"
	case audit.Replace:
		method = "PUT"
	case audit.Archive:
		method, status = "DELETE", 204
	default:
		return "", 0, idempotency.ErrUnavailable
	}
	parsed, err := uuid.Parse(request.RawKey)
	if err != nil || request.Key == uuid.Nil || parsed != request.Key || id == uuid.Nil || request.Scope != method+":"+id.String() || len(request.PayloadHash) != 64 {
		return "", 0, idempotency.ErrUnavailable
	}
	if _, err := hex.DecodeString(request.PayloadHash); err != nil {
		return "", 0, idempotency.ErrUnavailable
	}
	hash := sha256.Sum256([]byte(actor.String() + "\n" + request.Scope + "\n" + request.PayloadHash))
	return hex.EncodeToString(hash[:]), status, nil
}

func (r *RedisFlavorRepository) mutationKey(request idempotency.Request) string {
	return r.prefix + ":mutation_replay:" + request.Key.String()
}

func decodeMutationReply(raw []byte, hash string, status int, id uuid.UUID) (*model.FlavorAdmin, error) {
	var reply mutationReply
	if json.Unmarshal(raw, &reply) != nil || len(reply.PayloadHash) != 64 || (reply.StatusCode != 200 && reply.StatusCode != 204) {
		return nil, idempotency.ErrUnavailable
	}
	if reply.PayloadHash != hash {
		return nil, idempotency.ErrConflict
	}
	if reply.StatusCode != status {
		return nil, idempotency.ErrUnavailable
	}
	if status == 204 {
		if reply.Body != "" {
			return nil, idempotency.ErrUnavailable
		}
		return nil, nil
	}
	var original model.FlavorAdmin
	if json.Unmarshal([]byte(reply.Body), &original) != nil || original.ID != id {
		return nil, idempotency.ErrUnavailable
	}
	original.IdempotentReplay = true
	return &original, nil
}

// LookupMutation never changes state. The Lua script repeats this decision
// atomically so a cache miss here cannot permit concurrent duplicate commits.
func (r *RedisFlavorRepository) LookupMutation(ctx context.Context, id uuid.UUID, request idempotency.Request) (*model.FlavorAdmin, bool, error) {
	hash, status, err := mutationFingerprint(ctx, id, request)
	if err != nil {
		return nil, false, err
	}
	raw, err := r.client.Get(ctx, r.mutationKey(request)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	original, err := decodeMutationReply(raw, hash, status, id)
	return original, err == nil, err
}

func (r *RedisFlavorRepository) updateIdempotent(ctx context.Context, previous, flavor *model.FlavorAdmin, request idempotency.Request) error {
	hash, status, err := mutationFingerprint(ctx, flavor.ID, request)
	if err != nil {
		return err
	}
	record, err := audit.NewRecord(ctx, audit.MutationAction(ctx), previous, flavor)
	if err != nil {
		return err
	}
	history, err := json.Marshal(record)
	if err != nil {
		return idempotency.ErrUnavailable
	}
	payload, err := json.Marshal(flavor)
	if err != nil {
		return idempotency.ErrUnavailable
	}
	expected, err := json.Marshal(previous)
	if err != nil {
		return idempotency.ErrUnavailable
	}
	body := string(payload)
	if status == 204 {
		body = ""
	}
	cached, err := json.Marshal(mutationReply{hash, status, body})
	if err != nil {
		return idempotency.ErrUnavailable
	}
	result, err := mutationReplayScript.Run(ctx, r.client,
		[]string{r.flavorKey(flavor.ID), r.nameKey(previous.Name), r.nameKey(flavor.Name), r.auditKey(flavor.ID), r.mutationKey(request)},
		payload, flavor.ID.String(), expected, history, hash, cached, idempotency.TTL.Milliseconds(), body).Result()
	if err != nil {
		return idempotency.ErrUnavailable
	}
	if code, ok := result.(int64); ok {
		switch code {
		case -5:
			return idempotency.ErrConflict
		case -4:
			return idempotency.ErrUnavailable
		case -3:
			return ErrAuditStorage
		case -2:
			return ErrUpdateConflict
		case -1:
			return ErrFlavorNotFound
		case 0:
			return ErrNameConflict
		}
		return idempotency.ErrUnavailable
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 || (values[0] != int64(1) && values[0] != int64(2)) {
		return idempotency.ErrUnavailable
	}
	if values[0] == int64(2) {
		cached, ok := values[1].(string)
		if !ok {
			return idempotency.ErrUnavailable
		}
		original, err := decodeMutationReply([]byte(cached), hash, status, flavor.ID)
		if err != nil {
			return err
		}
		if original != nil {
			*flavor = *original
		} else {
			flavor.IdempotentReplay = true
		}
	}
	return nil
}
