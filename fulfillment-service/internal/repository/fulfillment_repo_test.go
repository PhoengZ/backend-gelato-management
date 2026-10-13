package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestActorIDContext(t *testing.T) {
	ctx := context.Background()

	// Initial context has no actor
	if actor := ActorIDFromContext(ctx); actor != nil {
		t.Fatalf("expected nil actor, got %v", actor)
	}

	// Nil UUID should also result in nil actor
	ctxNil := WithActorID(ctx, uuid.Nil)
	if actor := ActorIDFromContext(ctxNil); actor != nil {
		t.Fatalf("expected nil actor for uuid.Nil, got %v", actor)
	}

	// Valid UUID should be preserved and retrieved
	actorID := uuid.New()
	ctxWithActor := WithActorID(ctx, actorID)
	retrieved := ActorIDFromContext(ctxWithActor)
	if retrieved == nil || *retrieved != actorID {
		t.Fatalf("retrieved actor = %v, want %s", retrieved, actorID)
	}
}

func TestSlotDeterministicID(t *testing.T) {
	t1, _ := time.Parse(time.RFC3339, "2026-10-13T14:03:00Z")
	t2, _ := time.Parse(time.RFC3339, "2026-10-13T14:12:00Z")
	t3, _ := time.Parse(time.RFC3339, "2026-10-13T14:16:00Z")

	start1 := t1.UTC().Truncate(SlotLength)
	start2 := t2.UTC().Truncate(SlotLength)
	start3 := t3.UTC().Truncate(SlotLength)

	// t1 and t2 fall in the same 14:00 - 14:15 slot window
	if start1 != start2 {
		t.Fatalf("start1 (%v) != start2 (%v)", start1, start2)
	}

	slotID1 := uuid.NewSHA1(slotNamespace, []byte(start1.Format(time.RFC3339)))
	slotID2 := uuid.NewSHA1(slotNamespace, []byte(start2.Format(time.RFC3339)))
	if slotID1 != slotID2 {
		t.Fatalf("slotID1 (%s) != slotID2 (%s)", slotID1, slotID2)
	}

	// t3 falls in 14:15 - 14:30 slot window
	slotID3 := uuid.NewSHA1(slotNamespace, []byte(start3.Format(time.RFC3339)))
	if slotID1 == slotID3 {
		t.Fatalf("slotID1 (%s) should not equal slotID3 (%s)", slotID1, slotID3)
	}
}
