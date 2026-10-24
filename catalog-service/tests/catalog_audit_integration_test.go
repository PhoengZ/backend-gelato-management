package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/audit"
	"catalog-service/internal/auth"
	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func readAuditDocuments(t *testing.T, client *redis.Client, prefix, id string) []map[string]any {
	t.Helper()
	rows, err := client.LRange(context.Background(), prefix+":flavor_audit:"+id, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	documents := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var document map[string]any
		if err := json.Unmarshal([]byte(row), &document); err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	return documents
}

func TestCatalogAuditRecordsRESTAndGRPC(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	svc := service.NewCatalogService(repo)
	rpc := newGRPCClient(t, svc, time.Second)
	app := newCatalogHandlerApp(svc)
	manager := handlerToken(t, auth.RoleManager)
	ctx := grpcContext(t, manager)
	created, err := rpc.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: grpcInput("Audited flavor")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Flavor.Flavor.Id
	documents := readAuditDocuments(t, client, prefix, id)
	if len(documents) != 1 || documents[0]["action"] != "CREATE" {
		t.Fatalf("gRPC create needs one durable audit record, got %v", documents)
	}
	catalogRequest(t, app, "PATCH", "/api/v1/flavors/"+id, `{"allergens":["TREE_NUT"]}`, manager, 200)
	input := grpcInput("Audited flavor")
	input.Active = proto.Bool(true)
	input.Allergens = &pb.AllergenList{Values: []pb.Allergen{pb.Allergen_ALLERGEN_SOY}}
	if _, err := rpc.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: input}); err != nil {
		t.Fatal(err)
	}
	documents = readAuditDocuments(t, client, prefix, id)
	if len(documents) != 3 {
		t.Fatalf("expected three committed mutations, got %v", documents)
	}
	for i, action := range []string{"CREATE", "UPDATE", "REPLACE"} {
		document := documents[i]
		if document["action"] != action || document["actor_id"] != "76ea43cb-42fa-44c8-bb0f-168769632a7d" || document["flavor_id"] != id {
			t.Fatalf("incorrect actor/resource/action in record: %v", document)
		}
		if _, err := time.Parse(time.RFC3339Nano, document["occurred_at"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	before := documents[2]["before"].(map[string]any)["allergens"].([]any)
	after := documents[2]["after"].(map[string]any)["allergens"].([]any)
	if len(before) != 1 || before[0] != "TREE_NUT" || len(after) != 1 || after[0] != "SOY" {
		t.Fatalf("allergen history missing: before=%v after=%v", before, after)
	}
	// An identical replacement and repeated archive remain no-ops.
	if _, err := rpc.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: input}); err != nil {
		t.Fatal(err)
	}
	if len(readAuditDocuments(t, client, prefix, id)) != 3 {
		t.Fatal("identical PUT added history")
	}
	if _, err := rpc.DeleteFlavor(ctx, &pb.DeleteFlavorRequest{FlavorId: id}); err != nil {
		t.Fatal(err)
	}
	catalogRequest(t, app, "DELETE", "/api/v1/flavors/"+id, "", manager, 204)
	documents = readAuditDocuments(t, client, prefix, id)
	if len(documents) != 4 || documents[3]["action"] != "DELETE" || documents[3]["after"].(map[string]any)["active"] != false {
		t.Fatalf("archive history/no-op behavior incorrect: %v", documents)
	}
	if ttl := client.TTL(context.Background(), prefix+":flavor_audit:"+id).Val(); ttl != -1 {
		t.Fatalf("audit history unexpectedly expires: %v", ttl)
	}
}

func TestCatalogAuditRejectsMissingActorAndFailedMutations(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	flavor := redisTestFlavor("Attribution required")
	for _, ctx := range []context.Context{context.Background(), audit.WithActor(context.Background(), uuid.NewString(), "CUSTOMER")} {
		if err := repo.Create(ctx, flavor); !errors.Is(err, audit.ErrActorRequired) {
			t.Fatalf("unaudited write allowed: %v", err)
		}
	}
	if exists := client.Exists(context.Background(), prefix+":flavor:"+flavor.ID.String()).Val(); exists != 0 {
		t.Fatal("rejected write committed data")
	}
	svc := service.NewCatalogService(repo)
	rpc := newGRPCClient(t, svc, time.Second)
	app := newCatalogHandlerApp(svc)
	manager := handlerToken(t, auth.RoleManager)
	created, err := rpc.CreateFlavor(grpcContext(t, manager), &pb.CreateFlavorRequest{Flavor: grpcInput("Existing audited flavor")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Flavor.Flavor.Id
	_, err = rpc.CreateFlavor(grpcContext(t, manager), &pb.CreateFlavorRequest{Flavor: grpcInput("Existing audited flavor")})
	wantCode(t, err, codes.AlreadyExists)
	_, err = rpc.UpdateFlavor(grpcContext(t, handlerToken(t, auth.RoleCustomer)), &pb.UpdateFlavorRequest{FlavorId: id, Flavor: grpcInput("Forbidden")})
	wantCode(t, err, codes.PermissionDenied)
	catalogRequest(t, app, "PATCH", "/api/v1/flavors/"+id, `{"allergens":["UNKNOWN"]}`, manager, 400)
	catalogRequest(t, app, "DELETE", "/api/v1/flavors/"+id, "", "", 401)
	if history := readAuditDocuments(t, client, prefix, id); len(history) != 1 {
		t.Fatalf("failed/denied request appended history: %v", history)
	}
}

func TestCatalogAuditUsesVerifiedActorAndMinimizesStoredData(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	svc := service.NewCatalogService(repo)
	app := newCatalogHandlerApp(svc)
	rpc := newGRPCClient(t, svc, time.Second)
	manager := handlerToken(t, auth.RoleManager)
	request := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(`{"name":"private-person@example.invalid","description":"private-description","price":{"amount_minor":6000,"currency":"THB"},"allergens":[],"recipe":"private-recipe"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+manager)
	request.Header.Set("X-User-ID", "spoofed-person@example.invalid")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("legitimate create failed: %d", response.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	ctx := metadata.AppendToOutgoingContext(grpcContext(t, manager), "x-user-id", "spoofed-person@example.invalid")
	if _, err := rpc.DeleteFlavor(ctx, &pb.DeleteFlavorRequest{FlavorId: created.ID}); err != nil {
		t.Fatal(err)
	}
	for _, document := range readAuditDocuments(t, client, prefix, created.ID) {
		if document["actor_id"] != "76ea43cb-42fa-44c8-bb0f-168769632a7d" {
			t.Fatalf("unverified actor entered ledger: %v", document)
		}
		body, _ := json.Marshal(document)
		for _, value := range []string{"private-", "example.invalid", manager, "spoofed-"} {
			if strings.Contains(string(body), value) {
				t.Fatalf("unnecessary sensitive value entered ledger: %s", body)
			}
		}
	}
}

func TestCatalogAuditPreflightPreventsPartialCreate(t *testing.T) {
	for _, corrupted := range []string{"audit", "ids"} {
		t.Run(corrupted, func(t *testing.T) {
			repo, client, prefix := redisRepositoryForTest(t)
			flavor := redisTestFlavor("Atomic create")
			key := prefix + ":flavor_audit:" + flavor.ID.String()
			if corrupted == "ids" {
				key = prefix + ":flavor_ids"
			}
			if err := client.Set(context.Background(), key, "wrong storage type", 0).Err(); err != nil {
				t.Fatal(err)
			}
			ctx := repositoryManagerContext(context.Background())
			if err := repo.Create(ctx, flavor); !errors.Is(err, repository.ErrAuditStorage) {
				t.Fatalf("expected storage preflight failure, got %v", err)
			}
			if _, err := repo.FindByID(ctx, flavor.ID); !errors.Is(err, repository.ErrFlavorNotFound) {
				t.Fatalf("partial flavor write: %v", err)
			}
			if err := client.Del(ctx, key).Err(); err != nil {
				t.Fatal(err)
			}
			if err := repo.Create(ctx, flavor); err != nil {
				t.Fatalf("failed create reserved name/index: %v", err)
			}
			if history := readAuditDocuments(t, client, prefix, flavor.ID.String()); len(history) != 1 {
				t.Fatal(history)
			}
		})
	}
}

func TestCatalogAuditPreflightPreventsPartialRename(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	ctx := repositoryManagerContext(context.Background())
	original := redisTestFlavor("Original atomic flavor")
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	key := prefix + ":flavor_audit:" + original.ID.String()
	if err := client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, key, "wrong storage type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	updated := cloneFlavor(original)
	updated.Name = "Rename must not commit"
	updated.Allergens = []model.Allergen{model.AllergenSoy}
	if err := repo.Update(ctx, original, updated); !errors.Is(err, repository.ErrAuditStorage) {
		t.Fatalf("expected audit storage failure: %v", err)
	}
	stored, err := repo.FindByID(ctx, original.ID)
	if err != nil || stored.Name != original.Name || stored.Allergens[0] != original.Allergens[0] {
		t.Fatalf("partial state change: %+v %v", stored, err)
	}
	if err := repo.Create(ctx, redisTestFlavor(original.Name)); !errors.Is(err, repository.ErrNameConflict) {
		t.Fatalf("old name index lost: %v", err)
	}
	if err := repo.Create(ctx, redisTestFlavor(updated.Name)); err != nil {
		t.Fatalf("failed rename reserved new name: %v", err)
	}
}

func TestCatalogAuditSupportsLegacyRecordsWithoutHistory(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	ctx := repositoryManagerContext(context.Background())
	flavor := redisTestFlavor("Legacy flavor")
	if err := repo.Create(ctx, flavor); err != nil {
		t.Fatal(err)
	}
	// Previous versions persisted exactly this FlavorAdmin JSON, without audit keys.
	if err := client.Del(ctx, prefix+":flavor_audit:"+flavor.ID.String()).Err(); err != nil {
		t.Fatal(err)
	}
	svc := service.NewCatalogService(repo)
	input := service.UpdateFlavorInput{Allergens: &[]model.Allergen{model.AllergenSoy}}
	if _, err := svc.Update(ctx, flavor.ID, input); err != nil {
		t.Fatalf("legacy CAS compatibility failed: %v", err)
	}
	history := readAuditDocuments(t, client, prefix, flavor.ID.String())
	if len(history) != 1 || history[0]["before"].(map[string]any)["allergens"].([]any)[0] != "MILK" {
		t.Fatalf("legacy before snapshot missing: %v", history)
	}
}

func TestCatalogAuditPermissionFailureCannotCommitUnauditedMutation(t *testing.T) {
	repo, client, prefix := redisRepositoryForTest(t)
	ctx := repositoryManagerContext(context.Background())
	username := "catalog_audit_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := uuid.NewString()
	if err := client.Do(ctx, "ACL", "SETUSER", username, "on", ">"+password, "~"+prefix+":*", "+@all", "-rpush").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Do(context.Background(), "ACL", "DELUSER", username).Err(); err != nil {
			t.Error(err)
		}
	})
	options := *client.Options()
	options.Username, options.Password = username, password
	restricted := redis.NewClient(&options)
	defer restricted.Close()
	limitedRepo := repository.NewRedisFlavorRepository(restricted, prefix)
	flavor := redisTestFlavor("Permission denied create")
	if err := limitedRepo.Create(ctx, flavor); !errors.Is(err, repository.ErrAuditStorage) {
		t.Fatalf("missing audit permission was not rejected: %v", err)
	}
	if _, err := repo.FindByID(ctx, flavor.ID); !errors.Is(err, repository.ErrFlavorNotFound) {
		t.Fatalf("unaudited create committed: %v", err)
	}
	if err := repo.Create(ctx, flavor); err != nil {
		t.Fatalf("denied create left partial indexes: %v", err)
	}
	updated := cloneFlavor(flavor)
	updated.Name = "Permission denied rename"
	if err := limitedRepo.Update(ctx, flavor, updated); !errors.Is(err, repository.ErrAuditStorage) {
		t.Fatalf("missing audit permission was not rejected: %v", err)
	}
	stored, err := repo.FindByID(ctx, flavor.ID)
	if err != nil || stored.Name != flavor.Name {
		t.Fatalf("unaudited rename committed: %+v %v", stored, err)
	}
	if len(readAuditDocuments(t, client, prefix, flavor.ID.String())) != 1 {
		t.Fatal("failed mutation changed audit ledger")
	}
}
