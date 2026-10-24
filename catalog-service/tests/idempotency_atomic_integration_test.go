package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"catalog-service/internal/auth"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type createResponse struct {
	status int
	body   string
	replay string
	err    error
}

func keyedCreate(app *fiber.App, key, body, token string) createResponse {
	req := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	if err != nil {
		return createResponse{err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return createResponse{resp.StatusCode, string(raw), resp.Header.Get("X-Idempotent-Replay"), err}
}

func flavorBody(name string) string {
	return fmt.Sprintf(`{"name":%q,"description":"Atomic create","price":{"amount_minor":6000,"currency":"THB"},"allergens":["MILK"],"recipe":"test"}`, name)
}

func TestIdempotencyConcurrentRequestsAcrossApplicationInstances(t *testing.T) {
	for _, samePayload := range []bool{true, false} {
		t.Run(fmt.Sprintf("same_payload_%v", samePayload), func(t *testing.T) {
			repo, client, prefix := redisRepositoryForTest(t)
			apps := []*fiber.App{newCatalogHandlerApp(service.NewCatalogService(repo)), newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))}
			key := uuid.NewString()
			t.Cleanup(func() { _ = client.Del(context.Background(), "idempotency:"+key).Err() })
			manager := handlerToken(t, auth.RoleManager)
			start := make(chan struct{})
			responses := make(chan createResponse, 20)
			for i := 0; i < 20; i++ {
				go func(i int) {
					<-start
					name := "One flavor"
					if !samePayload {
						name = fmt.Sprintf("Flavor %d", i)
					}
					responses <- keyedCreate(apps[i%2], key, flavorBody(name), manager)
				}(i)
			}
			close(start)
			created, conflicts, replays := 0, 0, 0
			original := ""
			for i := 0; i < 20; i++ {
				response := <-responses
				if response.err != nil {
					t.Fatal(response.err)
				}
				if response.status == 201 {
					created++
					if original == "" {
						original = response.body
					}
					if samePayload && response.body != original {
						t.Fatal("replay changed original response")
					}
					if response.replay == "true" {
						replays++
					}
				} else if response.status == 409 && strings.Contains(response.body, "IDEMPOTENCY_CONFLICT") {
					conflicts++
				} else {
					t.Fatalf("unexpected response: %+v", response)
				}
			}
			if samePayload && (created != 20 || replays != 19 || conflicts != 0) {
				t.Fatalf("same-key replay failed: created=%d replay=%d conflict=%d", created, replays, conflicts)
			}
			if !samePayload && (created != 1 || conflicts != 19) {
				t.Fatalf("conflicting key committed twice: created=%d conflict=%d", created, conflicts)
			}
			if count := client.SCard(context.Background(), prefix+":flavor_ids").Val(); count != 1 {
				t.Fatal("duplicate flavor committed", count)
			}
			var flavor struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(original), &flavor); err != nil {
				t.Fatal(err)
			}
			if len(readAuditDocuments(t, client, prefix, flavor.ID)) != 1 {
				t.Fatal("replay appended duplicate audit")
			}
			if ttl := client.PTTL(context.Background(), "idempotency:"+key).Val(); ttl <= 23*time.Hour || ttl > 24*time.Hour {
				t.Fatal("incorrect idempotency retention", ttl)
			}
		})
	}
}

func TestIdempotencyReplaySurvivesRenameArchiveAndNewInstance(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	app := newCatalogHandlerApp(service.NewCatalogService(repo))
	key := uuid.New()
	t.Cleanup(func() { _ = client.Del(context.Background(), "idempotency:"+key.String()).Err() })
	manager := handlerToken(t, auth.RoleManager)
	body := flavorBody("Original response")
	first := keyedCreate(app, key.String(), body, manager)
	if first.status != 201 {
		t.Fatal(first)
	}
	var flavor struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(first.body), &flavor); err != nil {
		t.Fatal(err)
	}
	catalogRequest(t, app, "PATCH", "/api/v1/flavors/"+flavor.ID, `{"name":"Changed current state"}`, manager, 200)
	catalogRequest(t, app, "DELETE", "/api/v1/flavors/"+flavor.ID, "", manager, 204)
	app = newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))
	for _, alias := range []string{key.String(), strings.ToUpper(key.String()), key.URN(), strings.ReplaceAll(key.String(), "-", "")} {
		replay := keyedCreate(app, alias, body, manager)
		if replay.status != 201 || replay.body != first.body || replay.replay != "true" {
			t.Fatalf("lost original response for UUID alias: %+v", replay)
		}
	}
	if response := keyedCreate(app, key.String(), body, ""); response.status != 401 {
		t.Fatal("replay bypassed authentication", response)
	}
	if response := keyedCreate(app, key.String(), body, handlerToken(t, auth.RoleCustomer)); response.status != 403 {
		t.Fatal("replay bypassed role", response)
	}
	if len(readAuditDocuments(t, client, prefix, flavor.ID)) != 3 {
		t.Fatal("replay produced extra audit after archive")
	}
}

func TestIdempotencyStorageWriteFailureLeavesNoPartialCreate(t *testing.T) {
	_, client, prefix := redisRepositoryForTest(t)
	ctx := context.Background()
	username := "idem_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := uuid.NewString()
	// All commands may access all keys, but SET is permitted only for flavor
	// keys, not response-cache keys. This reproduces the old post-create SET failure.
	if err := client.Do(ctx, "ACL", "SETUSER", username, "on", ">"+password, "~*", "+@all", "-set", "(+set ~"+prefix+":*)").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Do(context.Background(), "ACL", "DELUSER", username).Err() })
	options := *client.Options()
	options.Username, options.Password = username, password
	limited := redis.NewClient(&options)
	defer limited.Close()
	app := newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(limited, prefix)))
	key := uuid.NewString()
	t.Cleanup(func() { _ = client.Del(context.Background(), "idempotency:"+key).Err() })
	body := flavorBody("Recoverable creation")
	manager := handlerToken(t, auth.RoleManager)
	first := keyedCreate(app, key, body, manager)
	if first.status != 503 {
		t.Fatalf("cache write failure did not fail closed: %+v", first)
	}
	if count := client.SCard(ctx, prefix+":flavor_ids").Val(); count != 0 {
		t.Fatal("flavor committed without replay record", count)
	}
	if err := client.Do(ctx, "ACL", "SETUSER", username, "+set").Err(); err != nil {
		t.Fatal(err)
	}
	created := keyedCreate(app, key, body, manager)
	retry := keyedCreate(app, key, body, manager)
	if created.status != 201 || retry.status != 201 || retry.body != created.body || retry.replay != "true" {
		t.Fatalf("recovered retry changed outcome: created=%+v retry=%+v", created, retry)
	}
}

func TestIdempotencyCorruptStorageCannotCreateAFlavor(t *testing.T) {
	_, client, prefix := redisRepositoryForTest(t)
	key := uuid.NewString()
	cache := "idempotency:" + key
	t.Cleanup(func() { _ = client.Del(context.Background(), cache).Err() })
	if err := client.RPush(context.Background(), cache, "wrong-type").Err(); err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))
	response := keyedCreate(app, key, flavorBody("No partial data"), handlerToken(t, auth.RoleManager))
	if response.status != 503 || client.SCard(context.Background(), prefix+":flavor_ids").Val() != 0 {
		t.Fatal("corrupt cache failed open", response)
	}
}

func TestLegacyReplayRecordIsReadWithoutCreatingOrAuditingAgain(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	app := newCatalogHandlerApp(service.NewCatalogService(repo))
	key := uuid.New()
	raw := strings.ToUpper(key.String())
	canonical := "idempotency:" + key.String()
	legacy := "idempotency:" + raw
	t.Cleanup(func() { _ = client.Del(context.Background(), canonical, legacy).Err() })
	body := flavorBody("Legacy cached result")
	manager := handlerToken(t, auth.RoleManager)
	first := keyedCreate(app, key.String(), body, manager)
	if first.status != 201 {
		t.Fatal(first)
	}
	if err := client.Rename(context.Background(), canonical, legacy).Err(); err != nil {
		t.Fatal(err)
	}
	// The new binary discovers every old spelling before accepting requests.
	app = newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))
	replay := keyedCreate(app, raw, body, manager)
	if replay.status != 201 || replay.body != first.body || replay.replay != "true" {
		t.Fatal("legacy replay lost", replay)
	}
	if retry := keyedCreate(app, key.String(), flavorBody("Conflicting legacy alias"), manager); retry.status != 409 {
		t.Fatal("legacy UUID alias created a second flavor", retry)
	}
	if client.SCard(context.Background(), prefix+":flavor_ids").Val() != 1 {
		t.Fatal("legacy retry created again")
	}
}

func TestLegacyUUIDSpellingsAreReconciledBeforeCanonicalRetry(t *testing.T) {
	for _, form := range []string{"uppercase", "mixed", "urn", "compact", "braces", "parser_wrapper"} {
		t.Run(form, func(t *testing.T) {
			repo, client, prefix := redisRepositoryForTest(t)
			key := uuid.New()
			mixed := key.String()[:18] + strings.ToUpper(key.String()[18:])
			raw := map[string]string{
				"uppercase": strings.ToUpper(key.String()), "mixed": mixed,
				"urn": "UrN:UuId:" + mixed, "compact": strings.ReplaceAll(mixed, "-", ""),
				"braces": "{" + mixed + "}", "parser_wrapper": "!" + mixed + "?",
			}[form]
			canonical, legacy := "idempotency:"+key.String(), "idempotency:"+raw
			t.Cleanup(func() { _ = client.Del(context.Background(), canonical, legacy).Err() })
			manager := handlerToken(t, auth.RoleManager)
			body := flavorBody("Upgrade spelling")
			first := keyedCreate(newCatalogHandlerApp(service.NewCatalogService(repo)), key.String(), body, manager)
			if first.status != 201 {
				t.Fatal(first)
			}
			if err := client.Rename(context.Background(), canonical, legacy).Err(); err != nil {
				t.Fatal(err)
			}
			if err := client.PExpire(context.Background(), legacy, 17*time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			app := newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))
			if retry := keyedCreate(app, key.String(), flavorBody("Different body"), manager); retry.status != 409 {
				t.Fatal("canonical retry bypassed legacy record", retry)
			}
			if replay := keyedCreate(app, key.String(), body, manager); replay.status != 201 || replay.body != first.body || replay.replay != "true" {
				t.Fatal("upgrade lost original result", replay)
			}
			if ttl := client.PTTL(context.Background(), canonical).Val(); ttl <= 16*time.Minute || ttl > 17*time.Minute {
				t.Fatal("upgrade refreshed or lost original expiry", ttl)
			}
			if client.SCard(context.Background(), prefix+":flavor_ids").Val() != 1 {
				t.Fatal("upgrade retry created twice")
			}
		})
	}
}

func TestConflictingLegacyUUIDRecordsFailClosed(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	key := uuid.New()
	canonical, legacy := "idempotency:"+key.String(), "idempotency:"+strings.ToUpper(key.String())
	t.Cleanup(func() { _ = client.Del(context.Background(), canonical, legacy).Err() })
	body, manager := flavorBody("Historical success"), handlerToken(t, auth.RoleManager)
	first := keyedCreate(newCatalogHandlerApp(service.NewCatalogService(repo)), key.String(), body, manager)
	if first.status != 201 {
		t.Fatal(first)
	}
	var cached map[string]any
	if err := json.Unmarshal([]byte(client.Get(context.Background(), canonical).Val()), &cached); err != nil {
		t.Fatal(err)
	}
	cached["body"] = strings.ReplaceAll(first.body, "Historical success", "Other historical success")
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(context.Background(), legacy, raw, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))
	if response := keyedCreate(app, key.String(), body, manager); response.status != 503 {
		t.Fatal("ambiguous historical results did not fail closed", response)
	}
	if client.SCard(context.Background(), prefix+":flavor_ids").Val() != 1 {
		t.Fatal("historical conflict created more data")
	}
}
