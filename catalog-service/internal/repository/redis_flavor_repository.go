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

	"catalog-service/internal/model"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrFlavorNotFound = errors.New("flavor not found")
	ErrNameConflict   = errors.New("flavor name already exists")
	ErrUpdateConflict = errors.New("flavor changed during update")
)

var createFlavorScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[2]) == 1 then
  return 0
end
redis.call("SET", KEYS[1], ARGV[1])
redis.call("SET", KEYS[2], ARGV[2])
redis.call("SADD", KEYS[3], ARGV[2])
return 1
`)

var updateFlavorScript = redis.NewScript(`
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
if KEYS[2] ~= KEYS[3] then
  if redis.call("GET", KEYS[2]) == ARGV[2] then
    redis.call("DEL", KEYS[2])
  end
end
redis.call("SET", KEYS[3], ARGV[2])
redis.call("SET", KEYS[1], ARGV[1])
return 1
`)

type RedisFlavorRepository struct {
	client *redis.Client
	prefix string
}

func NewRedisFlavorRepository(client *redis.Client, prefix string) *RedisFlavorRepository {
	return &RedisFlavorRepository{
		client: client,
		prefix: strings.TrimSuffix(prefix, ":"),
	}
}

func (r *RedisFlavorRepository) Create(ctx context.Context, flavor *model.FlavorAdmin) error {
	payload, err := json.Marshal(flavor)
	if err != nil {
		return fmt.Errorf("encode flavor: %w", err)
	}
	result, err := createFlavorScript.Run(
		ctx,
		r.client,
		[]string{r.flavorKey(flavor.ID), r.nameKey(flavor.Name), r.idsKey()},
		payload,
		flavor.ID.String(),
	).Int()
	if err != nil {
		return fmt.Errorf("create flavor in Redis: %w", err)
	}
	if result == 0 {
		return ErrNameConflict
	}
	return nil
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
		[]string{r.flavorKey(flavor.ID), r.nameKey(previous.Name), r.nameKey(flavor.Name)},
		payload,
		flavor.ID.String(),
		expected,
	).Int()
	if err != nil {
		return fmt.Errorf("update flavor in Redis: %w", err)
	}
	switch result {
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
