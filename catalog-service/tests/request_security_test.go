package tests

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"catalog-service/internal/auth"
	"catalog-service/internal/service"
)

func TestAmbiguousJSONCannotChangeFlavor(t *testing.T) {
	token := handlerToken(t, auth.RoleManager)
	for _, body := range []string{
		`{"Price":{"currency":"THB"}}`,
		`{"price":{"Amount_Minor":0,"currency":"THB"}}`,
		`{"description":"one","description":"two"}`,
		`{"description":"one","descrip\u0074ion":"two"}`,
		`{"active":false,"Active":true}`,
		`{"price":{"amount_minor":100,"amount_minor":0,"currency":"THB"}}`,
		`{"id":"00000000-0000-4000-8000-000000000001"}`,
		`{"role":"MANAGER"}`,
		`{"created_at":"2020-01-01T00:00:00Z"}`,
		`{"allergens":[null]}`,
		`{"description":null}`,
		`{"image_url":"javascript:alert(1)"}`,
		`{"description":"ok"} {"active":false}`,
		`{"description":"unfinished"`,
		`{"allergens":` + strings.Repeat("[", 20) + `"MILK"` + strings.Repeat("]", 20) + `}`,
	} {
		t.Run(body, func(t *testing.T) {
			repo := newMemoryFlavorRepository()
			catalog := service.NewCatalogService(repo)
			created, err := catalog.Create(context.Background(), validCreateInput("Original"))
			if err != nil {
				t.Fatal(err)
			}
			app := newCatalogHandlerApp(catalog)
			catalogRequest(t, app, "PATCH", "/api/v1/flavors/"+created.ID.String(), body, token, 400)
			stored, err := repo.FindByID(context.Background(), created.ID)
			if err != nil || !reflect.DeepEqual(stored, created) {
				t.Fatalf("rejected body modified data: %v", err)
			}
		})
	}
}

func TestDuplicateAuthorizationAndActiveFiltersFailClosed(t *testing.T) {
	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	app := newCatalogHandlerApp(catalog)
	manager := "Bearer " + handlerToken(t, auth.RoleManager)
	for _, headers := range [][]string{{manager, "Bearer invalid"}, {"Bearer invalid", manager}, {manager, manager}, {manager + ", " + manager}} {
		for _, target := range []string{"/api/v1/flavors", "/api/v1/flavors?active=false"} {
			req := httptest.NewRequest("GET", target, nil)
			for _, value := range headers {
				req.Header.Add("Authorization", value)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 401 {
				t.Fatalf("ambiguous Authorization accepted (%d)", resp.StatusCode)
			}
		}
	}
	for _, query := range []string{"active=false&active=true", "active=true&active=false", "active=true&%61ctive=false", "active=", "active=FALSE"} {
		catalogRequest(t, app, "GET", "/api/v1/flavors?"+query, "", "", 400)
	}
	// Unrecognized query/header names cannot opt a caller into Manager visibility.
	for _, header := range []string{"X-User-Role", "X-Role", "X-Authenticated-Role"} {
		req := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(replacementJSON))
		req.Header.Set(header, "MANAGER")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s bypassed authentication", header)
		}
	}
}
