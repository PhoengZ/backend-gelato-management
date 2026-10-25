package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"catalog-service/internal/audit"
	"catalog-service/internal/auth"
	"catalog-service/internal/idempotency"
	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestMutationKeyBindsManagerAndMethodWithoutBindingTokenBytes(t *testing.T) {
	repo, _, _ := redisRepositoryForTest(t)
	app := newCatalogHandlerApp(service.NewCatalogService(repo))
	manager := handlerToken(t, auth.RoleManager)
	created := createMutationFlavor(t, app, manager, "Principal binding")
	path, key, body := "/api/v1/flavors/"+created.ID.String(), uuid.NewString(), `{"description":"Bound result"}`
	first := keyedMutation(app, "PATCH", path, key, body, manager)
	if first.status != 200 {
		t.Fatal(first)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if got := keyedMutation(app, method, path, key, mutationBody(method, "Another operation"), manager); got.status != 409 {
			t.Fatal("key transferred to another method", got)
		}
	}
	for _, samePrincipal := range []bool{true, false} {
		subject := "76ea43cb-42fa-44c8-bb0f-168769632a7d"
		if !samePrincipal {
			subject = uuid.NewString()
		}
		claims := auth.Claims{Role: auth.RoleManager, RegisteredClaims: jwt.RegisteredClaims{
			Subject: subject, Issuer: "gelatoflow-auth", Audience: jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Second)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), ID: uuid.NewString(),
		}}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(handlerTestSecret))
		if err != nil {
			t.Fatal(err)
		}
		got := keyedMutation(app, "PATCH", path, key, body, token)
		if (samePrincipal && (got.status != 200 || got.body != first.body)) || (!samePrincipal && got.status != 409) {
			t.Fatal("incorrect verified-principal binding", samePrincipal, got)
		}
	}
}

func TestAtomicMutationRevalidatesFullCachedResponseAfterReadMiss(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	app := newCatalogHandlerApp(service.NewCatalogService(repo))
	manager := handlerToken(t, auth.RoleManager)
	created := createMutationFlavor(t, app, manager, "Cached reply validation")
	key, body := uuid.NewString(), `{"description":"First result"}`
	if got := keyedMutation(app, "PATCH", "/api/v1/flavors/"+created.ID.String(), key, body, manager); got.status != 200 {
		t.Fatal(got)
	}
	ctx := context.Background()
	cacheKey := prefix + ":mutation_replay:" + key
	var cached map[string]any
	if err := json.Unmarshal([]byte(client.Get(ctx, cacheKey).Val()), &cached); err != nil {
		t.Fatal(err)
	}
	cached["status_code"] = 204
	raw, _ := json.Marshal(cached)
	if err := client.Set(ctx, cacheKey, raw, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	previous, err := repo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := *previous
	next.UpdatedAt = next.UpdatedAt.Add(time.Millisecond)
	hash := sha256.Sum256([]byte(body))
	ctx = audit.WithActor(ctx, "76ea43cb-42fa-44c8-bb0f-168769632a7d", "MANAGER")
	ctx = audit.WithAction(ctx, audit.Update)
	ctx = idempotency.WithRequest(ctx, idempotency.Request{Key: uuid.MustParse(key), RawKey: key,
		PayloadHash: hex.EncodeToString(hash[:]), Scope: "PATCH:" + created.ID.String()})
	// Exercise the atomic path directly: another writer could have populated
	// the cache after the application's earlier read-only optimization missed.
	if err := repo.Update(ctx, previous, &next); err != idempotency.ErrUnavailable {
		t.Fatal("atomic replay trusted a corrupt status/body pair", err)
	}
	current, err := repo.FindByID(ctx, created.ID)
	if err != nil || !current.UpdatedAt.Equal(previous.UpdatedAt) || len(readAuditDocuments(t, client, prefix, created.ID.String())) != 2 {
		t.Fatal("corrupt replay changed state/history", current, err)
	}
}

func keyedMutation(app *fiber.App, method, path, key, body, token string) createResponse {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		return createResponse{err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return createResponse{resp.StatusCode, string(raw), resp.Header.Get("X-Idempotent-Replay"), err}
}

func mutationBody(method, name string) string {
	switch method {
	case "PATCH":
		return fmt.Sprintf(`{"name":%q,"allergens":["SOY"]}`, name)
	case "PUT":
		return strings.TrimSuffix(flavorBody(name), "}") + `,"active":true}`
	default:
		return ""
	}
}

func createMutationFlavor(t *testing.T, app *fiber.App, manager, name string) model.FlavorAdmin {
	t.Helper()
	var created model.FlavorAdmin
	if err := json.Unmarshal(catalogRequest(t, app, "POST", "/api/v1/flavors", flavorBody(name), manager, 201), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestMutationReplayKeepsOriginalResponseAcrossLaterWritesAndInstances(t *testing.T) {
	for _, method := range []string{"PATCH", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			repo, client, prefix := redisRepositoryForTest(t)
			app := newCatalogHandlerApp(service.NewCatalogService(repo))
			manager := handlerToken(t, auth.RoleManager)
			created := createMutationFlavor(t, app, manager, "Before "+method)
			path, key := "/api/v1/flavors/"+created.ID.String(), uuid.NewString()
			body, status := mutationBody(method, "First result"), 200
			if method == "DELETE" {
				status = 204
			}
			first := keyedMutation(app, method, path, key, body, manager)
			if first.err != nil || first.status != status || first.replay != "" {
				t.Fatal(first)
			}
			catalogRequest(t, app, "PATCH", path, `{"name":"Intervening write","active":true}`, manager, 200)
			restarted := newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))
			retry := keyedMutation(restarted, method, path, strings.ToUpper(key), body, manager)
			if retry.err != nil || retry.status != status || retry.body != first.body || (status == 200 && retry.replay != "true") {
				t.Fatalf("original result changed: first=%+v retry=%+v", first, retry)
			}
			current, err := repo.FindByID(context.Background(), created.ID)
			if err != nil || current.Name != "Intervening write" || !current.Active {
				t.Fatal("replay undid a later write", current, err)
			}
			if len(readAuditDocuments(t, client, prefix, created.ID.String())) != 3 {
				t.Fatal("replay added a duplicate audit")
			}
			conflict := keyedMutation(app, method, path, key, body+" ", manager)
			if conflict.status != 409 || !strings.Contains(conflict.body, "IDEMPOTENCY_CONFLICT") {
				t.Fatal("changed request reused its key", conflict)
			}
			other := createMutationFlavor(t, app, manager, "Another resource")
			if got := keyedMutation(app, method, "/api/v1/flavors/"+other.ID.String(), key, body, manager); got.status != 409 {
				t.Fatal("key transferred to another resource", got)
			}
			if ttl := client.PTTL(context.Background(), prefix+":mutation_replay:"+key).Val(); ttl <= 23*time.Hour || ttl > 24*time.Hour {
				t.Fatal("incorrect replay retention", ttl)
			}
			for _, token := range []string{"", handlerToken(t, auth.RoleStaff)} {
				got := keyedMutation(app, method, path, key, body, token)
				if got.status != 401 && got.status != 403 {
					t.Fatal("replay bypassed authorization", got)
				}
			}
		})
	}
}

func TestMutationReplaySerializesConcurrentRequests(t *testing.T) {
	for _, method := range []string{"PATCH", "PUT", "DELETE"} {
		for _, same := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/same_%v", method, same), func(t *testing.T) {
				repo, client, prefix := redisRepositoryForTest(t)
				apps := []*fiber.App{newCatalogHandlerApp(service.NewCatalogService(repo)), newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(client, prefix)))}
				manager := handlerToken(t, auth.RoleManager)
				created := createMutationFlavor(t, apps[0], manager, "Concurrent "+method)
				path, key := "/api/v1/flavors/"+created.ID.String(), uuid.NewString()
				start, results := make(chan struct{}), make(chan createResponse, 20)
				for i := 0; i < 20; i++ {
					go func(i int) {
						<-start
						name := "One result"
						if !same {
							name = fmt.Sprintf("Result %d", i)
						}
						body := mutationBody(method, name)
						if method == "DELETE" && !same {
							body = fmt.Sprintf(`{"ignored":%d}`, i)
						}
						results <- keyedMutation(apps[i%2], method, path, key, body, manager)
					}(i)
				}
				close(start)
				successes, conflicts, original := 0, 0, ""
				want := 200
				if method == "DELETE" {
					want = 204
				}
				for i := 0; i < 20; i++ {
					got := <-results
					if got.err != nil {
						t.Fatal(got.err)
					}
					if got.status == want {
						if successes == 0 {
							original = got.body
						} else if got.body != original {
							t.Fatal("concurrent replay changed the response", got)
						}
						successes++
					} else if got.status == 409 {
						conflicts++
					} else {
						t.Fatal(got)
					}
				}
				if (same && (successes != 20 || conflicts != 0)) || (!same && (successes != 1 || conflicts != 19)) {
					t.Fatal("concurrent key committed inconsistent results", successes, conflicts)
				}
				if count := len(readAuditDocuments(t, client, prefix, created.ID.String())); count != 2 {
					t.Fatal("duplicate mutation history", count)
				}
			})
		}
	}
}

func TestNoopMutationKeysAreRememberedWithoutAddingAudit(t *testing.T) {
	for _, method := range []string{"PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			repo, client, prefix := redisRepositoryForTest(t)
			app := newCatalogHandlerApp(service.NewCatalogService(repo))
			manager := handlerToken(t, auth.RoleManager)
			created := createMutationFlavor(t, app, manager, "Noop")
			path, key := "/api/v1/flavors/"+created.ID.String(), uuid.NewString()
			want := 200
			if method == "DELETE" {
				catalogRequest(t, app, method, path, "", manager, 204)
				want = 204
			}
			before := len(readAuditDocuments(t, client, prefix, created.ID.String()))
			body := mutationBody(method, "Noop")
			first := keyedMutation(app, method, path, key, body, manager)
			if first.status != want || len(readAuditDocuments(t, client, prefix, created.ID.String())) != before {
				t.Fatal("no-op changed history", first)
			}
			catalogRequest(t, app, "PATCH", path, `{"name":"After noop","active":true}`, manager, 200)
			second := keyedMutation(app, method, path, key, body, manager)
			current, err := repo.FindByID(context.Background(), created.ID)
			if second.status != want || second.body != first.body || err != nil || current.Name != "After noop" || !current.Active {
				t.Fatal("no-op replay overwrote later state", second, current, err)
			}
		})
	}
}

func TestMutationReplayStorageFailureLeavesFlavorAndAuditUnchanged(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	app := newCatalogHandlerApp(service.NewCatalogService(repo))
	manager := handlerToken(t, auth.RoleManager)
	created := createMutationFlavor(t, app, manager, "Storage failure")
	key, path := uuid.NewString(), "/api/v1/flavors/"+created.ID.String()
	ctx := context.Background()
	user, password := "mutation_"+strings.ReplaceAll(uuid.NewString(), "-", ""), uuid.NewString()
	// SET is allowed on flavors/name indexes but denied on replay keys. Without
	// the preflight, a post-mutation response write could lose the replay result.
	selector := "(~" + prefix + ":flavor:* ~" + prefix + ":flavor_name:* +set)"
	if err := client.Do(ctx, "ACL", "SETUSER", user, "on", ">"+password, "~"+prefix+":*", "+@all", "-set", selector).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Do(ctx, "ACL", "DELUSER", user).Err() })
	options := *client.Options()
	options.Username, options.Password = user, password
	limited := redis.NewClient(&options)
	t.Cleanup(func() { _ = limited.Close() })
	if err := limited.Set(ctx, prefix+":flavor:test_permission", "allowed", 0).Err(); err != nil {
		t.Fatal("control: flavor SET should be allowed", err)
	}
	limitedApp := newCatalogHandlerApp(service.NewCatalogService(repository.NewRedisFlavorRepository(limited, prefix)))
	got := keyedMutation(limitedApp, "PATCH", path, key, `{"name":"Must not commit"}`, manager)
	current, err := repo.FindByID(ctx, created.ID)
	if got.status != 503 || err != nil || current.Name != created.Name || len(readAuditDocuments(t, client, prefix, created.ID.String())) != 1 {
		t.Fatal("replay storage failure committed data", got, current, err)
	}
	// Existing callers without a key do not touch the new replay namespace.
	catalogRequest(t, limitedApp, "PATCH", path, `{"description":"Legacy caller still works"}`, manager, 200)
	got = keyedMutation(app, "PATCH", path, key, `{"name":"Must not commit"}`, manager)
	if got.status != 200 {
		t.Fatal("recovery did not execute the uncommitted key", got)
	}
}
