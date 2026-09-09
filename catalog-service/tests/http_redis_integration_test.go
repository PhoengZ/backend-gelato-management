package tests

import (
	"catalog-service/internal/auth"
	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func redisRepositoryForTest(t *testing.T) (*repository.RedisFlavorRepository, *redis.Client, string) {
	t.Helper()
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	prefix := "catalog:test:" + uuid.NewString()
	t.Cleanup(func() { deletePrefix(t, client, prefix); _ = client.Close() })
	return repository.NewRedisFlavorRepository(client, prefix), client, prefix
}

func TestCatalogHTTPRedisLifecycle(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	app := newCatalogHandlerApp(service.NewCatalogService(repo))
	manager := handlerToken(t, auth.RoleManager)
	createdJSON := catalogRequest(t, app, "POST", "/api/v1/flavors", replacementJSON, manager, 201)
	var created model.FlavorAdmin
	if err := json.Unmarshal(createdJSON, &created); err != nil {
		t.Fatal(err)
	}
	key := prefix + ":flavor:" + created.ID.String()
	storedJSON, err := client.Get(context.Background(), key).Result()
	if err != nil || storedJSON != string(createdJSON) {
		t.Fatalf("POST not persisted: %s, %v", storedJSON, err)
	}
	path := "/api/v1/flavors/" + created.ID.String()
	catalogRequest(t, app, "GET", path, "", "", 200)
	all := catalogRequest(t, app, "GET", "/api/v1/flavors", "", "", 200)
	var list struct {
		Items []model.Flavor `json:"items"`
	}
	if err := json.Unmarshal(all, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("GET ALL: %s", all)
	}
	input := validCreateInput("Updated Berry")
	active := true
	input.Active = &active
	input.Price.AmountMinor = 7500
	body, _ := json.Marshal(input)
	// Pointer fields without omitempty marshal nil image_url as null; omission is the API contract.
	var document map[string]any
	_ = json.Unmarshal(body, &document)
	delete(document, "image_url")
	body, _ = json.Marshal(document)
	updated := catalogRequest(t, app, "PUT", path, string(body), manager, 200)
	storedJSON, err = client.Get(context.Background(), key).Result()
	if err != nil || storedJSON != string(updated) {
		t.Fatalf("PUT not persisted: %s, %v", storedJSON, err)
	}
	repeated := catalogRequest(t, app, "PUT", path, string(body), manager, 200)
	if string(updated) != string(repeated) {
		t.Fatal("PUT changed on retry")
	}
	catalogRequest(t, app, "DELETE", path, "", manager, 204)
	stored, err := repo.FindByID(context.Background(), created.ID)
	if err != nil || stored.Active {
		t.Fatalf("DELETE did not archive: %+v, %v", stored, err)
	}
	all = catalogRequest(t, app, "GET", "/api/v1/flavors?active=true", "", "", 200)
	if err := json.Unmarshal(all, &list); err != nil || len(list.Items) != 0 {
		t.Fatalf("archive still active: %s", all)
	}
	catalogRequest(t, app, "DELETE", path, "", manager, 204)
	final, err := repo.FindByID(context.Background(), created.ID)
	if err != nil || !final.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatal("repeated DELETE changed state")
	}
	catalogRequest(t, app, "GET", path, "", "", 200)
}

func TestRedisConcurrentRenamesRejectStaleWritesAndPreserveIndexes(t *testing.T) {
	repo, _, _ := redisRepositoryForTest(t)
	ctx := context.Background()
	original := redisTestFlavor("Original")
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	first, second := cloneFlavor(original), cloneFlavor(original)
	first.Name, second.Name = "First", "Second"
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, candidate := range []*model.FlavorAdmin{first, second} {
		workers.Add(1)
		go func(candidate *model.FlavorAdmin) {
			defer workers.Done()
			results <- repo.Update(ctx, original, candidate)
		}(candidate)
	}
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, repository.ErrUpdateConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
	winner, err := repo.FindByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Original", "First", "Second"} {
		err := repo.Create(ctx, redisTestFlavor(name))
		if name == winner.Name {
			if !errors.Is(err, repository.ErrNameConflict) {
				t.Fatalf("winner index lost: %v", err)
			}
		} else if err != nil {
			t.Fatalf("unused name %q stayed reserved: %v", name, err)
		}
	}
}
