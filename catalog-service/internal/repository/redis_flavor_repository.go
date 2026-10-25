package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"catalog-service/internal/audit"
	"catalog-service/internal/idempotency"
	"catalog-service/internal/model"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrFlavorNotFound = errors.New("flavor not found")
	ErrNameConflict   = errors.New("flavor name already exists")
	ErrUpdateConflict = errors.New("flavor changed during update")
	ErrAuditStorage   = errors.New("Catalog mutation audit storage is unavailable")
)

var createFlavorScript = redis.NewScript(`
if ARGV[4] ~= "" then
  for _, key in ipairs({KEYS[5], KEYS[6]}) do
    local actual = redis.call("TYPE", key).ok
    if actual ~= "none" and actual ~= "string" then return -4 end
    local previous = redis.call("GET", key)
    if previous then
      local ok, record = pcall(cjson.decode, previous)
      if not ok or type(record) ~= "table" or type(record.payload_hash) ~= "string" or
         type(record.body) ~= "string" or record.status_code ~= 201 then return -4 end
      if record.payload_hash ~= ARGV[4] then return -5 end
      return {2, record.body}
    end
  end
  if not redis.acl_check_cmd("SET", KEYS[5], ARGV[5], "PX", ARGV[6]) then return -4 end
end
local expected = {"string", "string", "set", "list"}
for i, kind in ipairs(expected) do
  local actual = redis.call("TYPE", KEYS[i]).ok
  if actual ~= "none" and actual ~= kind then return -3 end
end
if redis.call("EXISTS", KEYS[2]) == 1 then
  return 0
end
if redis.call("EXISTS", KEYS[1]) == 1 then return -2 end
if not redis.acl_check_cmd("SET", KEYS[1], ARGV[1]) or
   not redis.acl_check_cmd("SET", KEYS[2], ARGV[2]) or
   not redis.acl_check_cmd("SADD", KEYS[3], ARGV[2]) or
   not redis.acl_check_cmd("RPUSH", KEYS[4], ARGV[3]) then return -3 end
redis.call("SET", KEYS[1], ARGV[1])
redis.call("SET", KEYS[2], ARGV[2])
redis.call("SADD", KEYS[3], ARGV[2])
redis.call("RPUSH", KEYS[4], ARGV[3])
if ARGV[4] ~= "" then redis.call("SET", KEYS[5], ARGV[5], "PX", ARGV[6]) end
return {1, ARGV[1]}
`)

var updateFlavorScript = redis.NewScript(`
local expected = {"string", "string", "string", "list"}
for i, kind in ipairs(expected) do
  local actual = redis.call("TYPE", KEYS[i]).ok
  if actual ~= "none" and actual ~= kind then return -3 end
end
local current = redis.call("GET", KEYS[1])
if not current then
  return -1
end
if current ~= ARGV[3] then
  return -2
end
local owner = redis.call("GET", KEYS[3])
if owner and owner ~= ARGV[2] then
  return 0
end
if not redis.acl_check_cmd("SET", KEYS[1], ARGV[1]) or
   not redis.acl_check_cmd("SET", KEYS[3], ARGV[2]) or
   not redis.acl_check_cmd("RPUSH", KEYS[4], ARGV[4]) then return -3 end
if KEYS[2] ~= KEYS[3] then
  if redis.call("GET", KEYS[2]) == ARGV[2] then
    if not redis.acl_check_cmd("DEL", KEYS[2]) then return -3 end
    redis.call("DEL", KEYS[2])
  end
end
redis.call("SET", KEYS[3], ARGV[2])
redis.call("SET", KEYS[1], ARGV[1])
redis.call("RPUSH", KEYS[4], ARGV[4])
return 1
`)

type RedisFlavorRepository struct {
	client      *redis.Client
	prefix      string
	legacyMu    sync.Mutex
	legacyReady bool
}

func NewRedisFlavorRepository(client *redis.Client, prefix string) *RedisFlavorRepository {
	return &RedisFlavorRepository{
		client: client,
		prefix: strings.TrimSuffix(prefix, ":"),
	}
}

func (r *RedisFlavorRepository) Create(ctx context.Context, flavor *model.FlavorAdmin) error {
	_, _, err := r.create(ctx, flavor, nil)
	return err
}

func (r *RedisFlavorRepository) CreateIdempotent(ctx context.Context, flavor *model.FlavorAdmin, request idempotency.Request) (*model.FlavorAdmin, bool, error) {
	return r.create(ctx, flavor, &request)
}

func (r *RedisFlavorRepository) create(ctx context.Context, flavor *model.FlavorAdmin, request *idempotency.Request) (*model.FlavorAdmin, bool, error) {
	record, err := audit.NewRecord(ctx, audit.Create, nil, flavor)
	if err != nil {
		return nil, false, err
	}
	history, err := json.Marshal(record)
	if err != nil {
		return nil, false, fmt.Errorf("encode Catalog audit: %w", err)
	}
	payload, err := json.Marshal(flavor)
	if err != nil {
		return nil, false, fmt.Errorf("encode flavor: %w", err)
	}
	key, legacyKey := r.prefix+":unused_idempotency:"+flavor.ID.String(), r.prefix+":unused_idempotency:"+flavor.ID.String()
	hash, cached := "", []byte{}
	if request != nil {
		if len(request.PayloadHash) != 64 {
			return nil, false, idempotency.ErrUnavailable
		}
		if _, err := hex.DecodeString(request.PayloadHash); err != nil {
			return nil, false, idempotency.ErrUnavailable
		}
		parsed, err := uuid.Parse(request.RawKey)
		if err != nil || parsed != request.Key {
			return nil, false, idempotency.ErrUnavailable
		}
		key, legacyKey = "idempotency:"+request.Key.String(), "idempotency:"+request.RawKey
		if err := r.PrepareIdempotency(ctx); err != nil {
			return nil, false, idempotency.ErrUnavailable
		}
		hash = request.PayloadHash
		cached, err = json.Marshal(struct {
			PayloadHash string            `json:"payload_hash"`
			StatusCode  int               `json:"status_code"`
			Headers     map[string]string `json:"headers"`
			Body        string            `json:"body"`
		}{hash, 201, map[string]string{"Content-Type": "application/json"}, string(payload)})
		if err != nil {
			return nil, false, idempotency.ErrUnavailable
		}
	}
	result, err := createFlavorScript.Run(
		ctx,
		r.client,
		[]string{r.flavorKey(flavor.ID), r.nameKey(flavor.Name), r.idsKey(), r.auditKey(flavor.ID), key, legacyKey},
		payload,
		flavor.ID.String(),
		history,
		hash, cached, idempotency.TTL.Milliseconds(),
	).Result()
	if err != nil {
		if request != nil {
			return nil, false, fmt.Errorf("%w: Redis create failed", idempotency.ErrUnavailable)
		}
		return nil, false, fmt.Errorf("create flavor in Redis: %w", err)
	}
	if code, ok := result.(int64); ok {
		switch code {
		case -5:
			return nil, false, idempotency.ErrConflict
		case -4:
			return nil, false, idempotency.ErrUnavailable
		case -3:
			return nil, false, ErrAuditStorage
		case -2:
			return nil, false, ErrUpdateConflict
		case 0:
			return nil, false, ErrNameConflict
		}
		return nil, false, idempotency.ErrUnavailable
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return nil, false, idempotency.ErrUnavailable
	}
	if values[0] == int64(1) {
		return flavor, false, nil
	}
	body, ok := values[1].(string)
	if values[0] != int64(2) || !ok {
		return nil, false, idempotency.ErrUnavailable
	}
	var original model.FlavorAdmin
	if err := json.Unmarshal([]byte(body), &original); err != nil || original.ID == uuid.Nil {
		return nil, false, idempotency.ErrUnavailable
	}
	return &original, true, nil
}

func (r *RedisFlavorRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.FlavorAdmin, error) {
	payload, err := r.client.Get(ctx, r.flavorKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrFlavorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read flavor from Redis: %w", err)
	}

	var flavor model.FlavorAdmin
	if err := json.Unmarshal(payload, &flavor); err != nil {
		return nil, fmt.Errorf("decode stored flavor: %w", err)
	}
	return &flavor, nil
}

func (r *RedisFlavorRepository) List(ctx context.Context) ([]*model.FlavorAdmin, error) {
	ids, err := r.client.SMembers(ctx, r.idsKey()).Result()
	if err != nil {
		return nil, fmt.Errorf("list flavor IDs from Redis: %w", err)
	}
	if len(ids) == 0 {
		return []*model.FlavorAdmin{}, nil
	}

	sort.Strings(ids)
	pipe := r.client.Pipeline()
	commands := make([]*redis.StringCmd, 0, len(ids))
	for _, rawID := range ids {
		id, parseErr := uuid.Parse(rawID)
		if parseErr != nil {
			continue
		}
		commands = append(commands, pipe.Get(ctx, r.flavorKey(id)))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read flavors from Redis: %w", err)
	}

	flavors := make([]*model.FlavorAdmin, 0, len(commands))
	for _, command := range commands {
		payload, err := command.Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read flavor from Redis: %w", err)
		}
		var flavor model.FlavorAdmin
		if err := json.Unmarshal(payload, &flavor); err != nil {
			return nil, fmt.Errorf("decode stored flavor: %w", err)
		}
		flavors = append(flavors, &flavor)
	}

	sort.Slice(flavors, func(i, j int) bool {
		left := strings.ToLower(flavors[i].Name)
		right := strings.ToLower(flavors[j].Name)
		if left == right {
			return flavors[i].ID.String() < flavors[j].ID.String()
		}
		return left < right
	})
	return flavors, nil
}

func (r *RedisFlavorRepository) Update(ctx context.Context, previous, flavor *model.FlavorAdmin) error {
	if request, ok := idempotency.FromContext(ctx); ok && request.Scope != "" {
		return r.updateIdempotent(ctx, previous, flavor, request)
	}
	record, err := audit.NewRecord(ctx, audit.MutationAction(ctx), previous, flavor)
	if err != nil {
		return err
	}
	history, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode Catalog audit: %w", err)
	}
	payload, err := json.Marshal(flavor)
	if err != nil {
		return fmt.Errorf("encode flavor: %w", err)
	}
	expected, err := json.Marshal(previous)
	if err != nil {
		return fmt.Errorf("encode previous flavor: %w", err)
	}
	result, err := updateFlavorScript.Run(
		ctx,
		r.client,
		[]string{r.flavorKey(flavor.ID), r.nameKey(previous.Name), r.nameKey(flavor.Name), r.auditKey(flavor.ID)},
		payload,
		flavor.ID.String(),
		expected,
		history,
	).Int()
	if err != nil {
		return fmt.Errorf("update flavor in Redis: %w", err)
	}
	switch result {
	case -3:
		return ErrAuditStorage
	case -2:
		return ErrUpdateConflict
	case -1:
		return ErrFlavorNotFound
	case 0:
		return ErrNameConflict
	default:
		return nil
	}
}

func (r *RedisFlavorRepository) auditKey(id uuid.UUID) string {
	return r.prefix + ":flavor_audit:" + id.String()
}

func (r *RedisFlavorRepository) flavorKey(id uuid.UUID) string {
	return r.prefix + ":flavor:" + id.String()
}

func (r *RedisFlavorRepository) idsKey() string {
	return r.prefix + ":flavor_ids"
}

func (r *RedisFlavorRepository) nameKey(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	digest := sha256.Sum256([]byte(normalized))
	return r.prefix + ":flavor_name:" + hex.EncodeToString(digest[:])
}
