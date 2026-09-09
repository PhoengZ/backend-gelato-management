package tests

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"

	"github.com/google/uuid"
)

type memoryFlavorRepository struct {
	records map[uuid.UUID]*model.FlavorAdmin
}

func newMemoryFlavorRepository() *memoryFlavorRepository {
	return &memoryFlavorRepository{records: make(map[uuid.UUID]*model.FlavorAdmin)}
}

func (r *memoryFlavorRepository) Create(_ context.Context, flavor *model.FlavorAdmin) error {
	for _, existing := range r.records {
		if strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(flavor.Name)) {
			return repository.ErrNameConflict
		}
	}
	r.records[flavor.ID] = cloneFlavor(flavor)
	return nil
}

func (r *memoryFlavorRepository) FindByID(_ context.Context, id uuid.UUID) (*model.FlavorAdmin, error) {
	flavor, exists := r.records[id]
	if !exists {
		return nil, repository.ErrFlavorNotFound
	}
	return cloneFlavor(flavor), nil
}

func (r *memoryFlavorRepository) List(_ context.Context) ([]*model.FlavorAdmin, error) {
	items := make([]*model.FlavorAdmin, 0, len(r.records))
	for _, flavor := range r.records {
		items = append(items, cloneFlavor(flavor))
	}
	return items, nil
}

func (r *memoryFlavorRepository) Update(_ context.Context, previous, flavor *model.FlavorAdmin) error {
	if _, exists := r.records[flavor.ID]; !exists {
		return repository.ErrFlavorNotFound
	}
	if !reflect.DeepEqual(r.records[flavor.ID], previous) {
		return repository.ErrUpdateConflict
	}
	for id, existing := range r.records {
		if id != flavor.ID && strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(flavor.Name)) {
			return repository.ErrNameConflict
		}
	}
	r.records[flavor.ID] = cloneFlavor(flavor)
	return nil
}

func TestCatalogServiceCreateDefaultsAndPublicProjection(t *testing.T) {
	repo := newMemoryFlavorRepository()
	catalog := service.NewCatalogService(repo)
	name := "  Pistachio  "
	description := "Roasted pistachio gelato"
	price := model.Money{AmountMinor: 12000, Currency: "THB"}
	allergens := []model.Allergen{model.AllergenTreeNut, model.AllergenMilk}
	recipe := "milk; pistachio paste; sugar"

	created, err := catalog.Create(context.Background(), service.CreateFlavorInput{
		Name: &name, Description: &description, Price: &price, Allergens: &allergens, Recipe: &recipe,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Name != "Pistachio" || !created.Active || created.ID == uuid.Nil {
		t.Fatalf("unexpected created flavor: %+v", created)
	}
	if len(created.Allergens) != 2 || created.Allergens[0] != model.AllergenMilk {
		t.Fatalf("allergens were not normalized: %v", created.Allergens)
	}

	public, err := catalog.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	payload, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal public flavor: %v", err)
	}
	if strings.Contains(string(payload), "recipe") || strings.Contains(string(payload), "pistachio paste") {
		t.Fatalf("public flavor leaked recipe: %s", payload)
	}
}

func TestCatalogServiceValidatesCreateAndConflicts(t *testing.T) {
	repo := newMemoryFlavorRepository()
	catalog := service.NewCatalogService(repo)
	valid := validCreateInput("Vanilla")
	if _, err := catalog.Create(context.Background(), valid); err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	duplicate := validCreateInput(" vanilla ")
	if _, err := catalog.Create(context.Background(), duplicate); !errors.Is(err, service.ErrNameConflict) {
		t.Fatalf("expected name conflict, got %v", err)
	}

	invalidPrice := validCreateInput("Chocolate")
	invalidPrice.Price.AmountMinor = -1
	if _, err := catalog.Create(context.Background(), invalidPrice); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid price error, got %v", err)
	}

	missingRequired := service.CreateFlavorInput{}
	if _, err := catalog.Create(context.Background(), missingRequired); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected required-fields error, got %v", err)
	}

	duplicateAllergens := validCreateInput("Mango")
	values := []model.Allergen{model.AllergenMilk, model.AllergenMilk}
	duplicateAllergens.Allergens = &values
	if _, err := catalog.Create(context.Background(), duplicateAllergens); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected duplicate-allergen error, got %v", err)
	}
}

func TestCatalogServiceUpdateArchiveAndRecipe(t *testing.T) {
	repo := newMemoryFlavorRepository()
	catalog := service.NewCatalogService(repo)
	created, err := catalog.Create(context.Background(), validCreateInput("Strawberry"))
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updatedName := "Strawberry Cheesecake"
	updatedRecipe := "milk; strawberry; cream cheese"
	updated, err := catalog.Update(context.Background(), created.ID, service.UpdateFlavorInput{
		Name: &updatedName, Recipe: &updatedRecipe,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Name != updatedName || updated.Recipe != updatedRecipe || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("unexpected updated flavor: %+v", updated)
	}
	if _, err := catalog.Update(context.Background(), created.ID, service.UpdateFlavorInput{}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty update to fail, got %v", err)
	}

	recipe, err := catalog.GetRecipe(context.Background(), created.ID)
	if err != nil || recipe.Recipe != updatedRecipe {
		t.Fatalf("GetRecipe returned recipe=%+v err=%v", recipe, err)
	}
	if err := catalog.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive returned error: %v", err)
	}
	if err := catalog.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("idempotent Archive returned error: %v", err)
	}
	active := true
	items, err := catalog.List(context.Background(), &active)
	if err != nil || len(items) != 0 {
		t.Fatalf("active filter returned items=%+v err=%v", items, err)
	}
	inactive := false
	items, err = catalog.List(context.Background(), &inactive)
	if err != nil || len(items) != 1 {
		t.Fatalf("inactive filter returned items=%+v err=%v", items, err)
	}
}

func validCreateInput(nameValue string) service.CreateFlavorInput {
	description := "Small-batch gelato"
	price := model.Money{AmountMinor: 10000, Currency: "THB"}
	allergens := []model.Allergen{model.AllergenMilk}
	recipe := "milk; sugar; flavor base"
	return service.CreateFlavorInput{
		Name: &nameValue, Description: &description, Price: &price, Allergens: &allergens, Recipe: &recipe,
	}
}

func cloneFlavor(flavor *model.FlavorAdmin) *model.FlavorAdmin {
	copy := *flavor
	copy.Allergens = append([]model.Allergen{}, flavor.Allergens...)
	return &copy
}
