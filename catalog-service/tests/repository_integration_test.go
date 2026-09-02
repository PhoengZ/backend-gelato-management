package tests

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"catalog-service/internal/model"
	"catalog-service/internal/repository"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisFlavorRepository(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse test Redis URL: %v", err)
	}
	client := redis.NewClient(options)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	prefix := "catalog:test:" + uuid.NewString()
	t.Cleanup(func() {
		deletePrefix(t, client, prefix)
		_ = client.Close()
	})
	repo := repository.NewRedisFlavorRepository(client, prefix)

	first := redisTestFlavor("Pistachio")
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	duplicate := redisTestFlavor(" pistachio ")
	if err := repo.Create(ctx, duplicate); !errors.Is(err, repository.ErrNameConflict) {
		t.Fatalf("expected case-insensitive name conflict, got %v", err)
	}

	found, err := repo.FindByID(ctx, first.ID)
	if err != nil || found.Recipe != first.Recipe {
		t.Fatalf("FindByID returned flavor=%+v err=%v", found, err)
	}
	items, err := repo.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("List returned items=%+v err=%v", items, err)
	}

	oldName := first.Name
	first.Name = "Roasted Pistachio"
	first.UpdatedAt = first.UpdatedAt.Add(time.Minute)
	if err := repo.Update(ctx, oldName, first); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	reusedOldName := redisTestFlavor("Pistachio")
	if err := repo.Create(ctx, reusedOldName); err != nil {
		t.Fatalf("old name was not released after rename: %v", err)
	}
	first.Name = reusedOldName.Name
	if err := repo.Update(ctx, "Roasted Pistachio", first); !errors.Is(err, repository.ErrNameConflict) {
		t.Fatalf("expected rename conflict, got %v", err)
	}
	if _, err := repo.FindByID(ctx, uuid.New()); !errors.Is(err, repository.ErrFlavorNotFound) {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestRedisCreateIsAtomicForConcurrentDuplicateNames(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse test Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	prefix := "catalog:test:" + uuid.NewString()
	t.Cleanup(func() {
		deletePrefix(t, client, prefix)
		_ = client.Close()
	})
	repo := repository.NewRedisFlavorRepository(client, prefix)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var successes atomic.Int32
	var conflicts atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := repo.Create(ctx, redisTestFlavor("Last Scoop"))
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, repository.ErrNameConflict):
				conflicts.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || conflicts.Load() != 11 || unexpected.Load() != 0 {
		t.Fatalf("expected 1 success and 11 conflicts, got success=%d conflict=%d unexpected=%d", successes.Load(), conflicts.Load(), unexpected.Load())
	}
}

func redisTestFlavor(name string) *model.FlavorAdmin {
	now := time.Now().UTC()
	return &model.FlavorAdmin{
		Flavor: model.Flavor{
			ID:          uuid.New(),
			Name:        name,
			Description: "Integration test flavor",
			Price:       model.Money{AmountMinor: 10000, Currency: "THB"},
			Allergens:   []model.Allergen{model.AllergenMilk},
			Active:      true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Recipe: "milk; sugar; test flavor",
	}
}

func deletePrefix(t *testing.T, client *redis.Client, prefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			t.Errorf("scan cleanup keys: %v", err)
			return
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Errorf("delete cleanup keys: %v", err)
				return
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}
