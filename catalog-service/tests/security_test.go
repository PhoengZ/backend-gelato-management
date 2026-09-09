package tests

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"catalog-service/internal/auth"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"
)

func TestArchivedFlavorReadAuthorization(t *testing.T) {
	repo := newMemoryFlavorRepository()
	catalog := service.NewCatalogService(repo)
	active, err := catalog.Create(context.Background(), validCreateInput("Active"))
	if err != nil {
		t.Fatal(err)
	}
	archived, err := catalog.Create(context.Background(), validCreateInput("Archived secret name"))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Archive(context.Background(), archived.ID); err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(catalog)
	for _, actor := range []struct {
		name string
		role auth.Role
	}{
		{"anonymous", ""}, {"customer", auth.RoleCustomer}, {"staff", auth.RoleStaff}, {"manager", auth.RoleManager},
	} {
		t.Run(actor.name, func(t *testing.T) {
			token := ""
			if actor.role != "" {
				token = handlerToken(t, actor.role)
			}
			wantGet, wantInactive, wantCount := 404, 403, 1
			if actor.role == "" {
				wantInactive = 401
			}
			if actor.role == auth.RoleManager {
				wantGet, wantInactive, wantCount = 200, 200, 2
			}
			path := "/api/v1/flavors/" + archived.ID.String()
			body := catalogRequest(t, app, "GET", path, "", token, wantGet)
			if actor.role != auth.RoleManager && strings.Contains(string(body), archived.Name) {
				t.Fatal("archived metadata leaked")
			}
			for _, method := range []string{"GET", "HEAD"} {
				catalogRequest(t, app, method, path, "", token, wantGet)
				catalogRequest(t, app, method, path+"/recipe", "", token, wantInactive)
			}
			catalogRequest(t, app, "GET", "/api/v1/flavors/"+active.ID.String(), "", token, 200)
			body = catalogRequest(t, app, "GET", "/api/v1/flavors", "", token, 200)
			var list struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(body, &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Items) != wantCount {
				t.Fatalf("want %d visible flavors, got %s", wantCount, body)
			}
			for _, item := range list.Items {
				if _, ok := item["recipe"]; ok {
					t.Fatal("metadata endpoint leaked recipe")
				}
			}
			catalogRequest(t, app, "GET", "/api/v1/flavors?active=false", "", token, wantInactive)
			body = catalogRequest(t, app, "GET", "/api/v1/flavors?active=true", "", token, 200)
			if strings.Contains(string(body), archived.ID.String()) {
				t.Fatal("active filter leaked archive")
			}
		})
	}
	stored, err := repo.FindByID(context.Background(), archived.ID)
	if err != nil || stored.Active || stored.Recipe != archived.Recipe {
		t.Fatalf("archive history lost: %v", err)
	}
}

func TestServiceUnknownReadScopesHideArchivedData(t *testing.T) {
	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	created, err := catalog.Create(context.Background(), validCreateInput("Archived"))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Archive(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	inactive, active := false, true
	for _, scope := range []service.ReadScope{service.PublicRead, service.ReadScope(255)} {
		if _, err := catalog.Get(context.Background(), created.ID, scope); !errors.Is(err, repository.ErrFlavorNotFound) {
			t.Fatalf("scope %d disclosed archive: %v", scope, err)
		}
		for _, active := range []*bool{nil, &inactive, &active} {
			items, err := catalog.List(context.Background(), active, scope)
			if err != nil || len(items) != 0 {
				t.Fatalf("scope %d disclosed archived list: %v", scope, err)
			}
		}
	}
}

func TestEveryCatalogWriteRequiresVerifiedManager(t *testing.T) {
	repo := newMemoryFlavorRepository()
	catalog := service.NewCatalogService(repo)
	created, err := catalog.Create(context.Background(), validCreateInput("Protected"))
	if err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(catalog)
	for _, token := range []string{"", "invalid", handlerToken(t, auth.RoleCustomer), handlerToken(t, auth.RoleStaff)} {
		want := 403
		if token == "" || token == "invalid" {
			want = 401
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			path := "/api/v1/flavors/" + created.ID.String()
			if method == "POST" {
				path = "/api/v1/flavors"
			}
			catalogRequest(t, app, method, path, replacementJSON, token, want)
		}
	}
	stored, err := repo.FindByID(context.Background(), created.ID)
	if err != nil || !reflect.DeepEqual(stored, created) {
		t.Fatalf("unauthorized write modified data: %v", err)
	}
	items, err := repo.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("unauthorized create modified catalog: %v", err)
	}
}

func TestPublicReadsRejectInvalidAuthenticationAndSpoofedRoles(t *testing.T) {
	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	created, err := catalog.Create(context.Background(), validCreateInput("Archived"))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Archive(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(catalog)
	path := "/api/v1/flavors/" + created.ID.String()
	for _, target := range []string{"/api/v1/flavors", path, "/api/v1/flavors?active=false"} {
		catalogRequest(t, app, "GET", target, "", "invalid.token.signature", 401)
	}
	for _, target := range []string{path, path + "/recipe", "/api/v1/flavors?active=false"} {
		req := httptest.NewRequest("GET", target, nil)
		req.Header.Set("X-User-Role", "MANAGER")
		req.Header.Set("X-User-ID", created.ID.String())
		req.Header.Set("Cookie", "role=MANAGER")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := 401
		if target == path {
			want = 404
		}
		if resp.StatusCode != want {
			t.Fatalf("spoofed role: %s returned %d", target, resp.StatusCode)
		}
	}
}

func TestCatalogResponsesCannotBeSharedOrStoredByCaches(t *testing.T) {
	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	created, err := catalog.Create(context.Background(), validCreateInput("Vanilla"))
	if err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(catalog)
	for _, target := range []string{"/api/v1/flavors", "/api/v1/flavors/" + created.ID.String(), "/api/v1/flavors/" + created.ID.String() + "/recipe"} {
		for _, token := range []string{"", handlerToken(t, auth.RoleManager), "bad-token"} {
			req := httptest.NewRequest("GET", target, nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
				t.Errorf("%s (%d) allows caching", target, resp.StatusCode)
			}
			if !strings.Contains(strings.ToLower(resp.Header.Get("Vary")), "authorization") {
				t.Errorf("%s does not vary by authorization", target)
			}
		}
	}
}
