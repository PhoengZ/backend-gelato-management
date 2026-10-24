package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"catalog-service/internal/model"
	"github.com/google/uuid"
)

func TestAuditRequiresVerifiedManagerContext(t *testing.T) {
	flavor := &model.FlavorAdmin{Flavor: model.Flavor{ID: uuid.New(), Active: true}}
	for _, ctx := range []context.Context{
		context.Background(),
		WithActor(context.Background(), "spoofed-user", "MANAGER"),
		WithActor(context.Background(), uuid.Nil.String(), "MANAGER"),
		WithActor(context.Background(), uuid.NewString(), "CUSTOMER"),
		context.WithValue(context.Background(), "X-User-ID", uuid.NewString()),
	} {
		if _, err := NewRecord(ctx, Create, nil, flavor); !errors.Is(err, ErrActorRequired) {
			t.Fatalf("missing/untrusted actor was accepted: %v", err)
		}
	}
}

func TestAuditSnapshotsAllergensWithoutFreeformValues(t *testing.T) {
	id := uuid.New()
	before := &model.FlavorAdmin{Flavor: model.Flavor{ID: id, Name: "private-name@example.invalid", Allergens: []model.Allergen{model.AllergenMilk}, Active: true}, Recipe: "private-recipe"}
	after := &model.FlavorAdmin{Flavor: model.Flavor{ID: id, Name: "changed-name@example.invalid", Allergens: []model.Allergen{model.AllergenTreeNut}, Active: false}, Recipe: "changed-private-recipe"}
	actorID := uuid.New()
	ctx := WithActor(context.Background(), strings.ToUpper(actorID.String()), "MANAGER")
	record, err := NewRecord(ctx, Replace, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if record.ActorID != actorID || record.ID == uuid.Nil || record.OccurredAt.Location().String() != "UTC" {
		t.Fatalf("missing trusted/canonical identity or UTC time: %+v", record)
	}
	before.Allergens[0] = model.AllergenSoy
	after.Allergens[0] = model.AllergenSoy
	if record.Before.Allergens[0] != model.AllergenMilk || record.After.Allergens[0] != model.AllergenTreeNut {
		t.Fatal("audit snapshots alias mutable input")
	}
	body, _ := json.Marshal(record)
	for _, value := range []string{"example.invalid", "private-recipe"} {
		if strings.Contains(string(body), value) {
			t.Fatalf("freeform data entered audit record: %s", body)
		}
	}
	if strings.Join(record.ChangedFields, ",") != "name,allergens,recipe,active" {
		t.Fatal(record.ChangedFields)
	}
}
