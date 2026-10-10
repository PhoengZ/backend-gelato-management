package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ProcessedEvent struct {
	EventID     string    `bson:"_id" json:"event_id"`
	EventType   string    `bson:"event_type" json:"event_type"`
	ProcessedAt time.Time `bson:"processed_at" json:"processed_at"`
}

type EventRepository interface {
	IsProcessed(ctx context.Context, eventID string) (bool, error)
	MarkProcessed(ctx context.Context, eventID, eventType string) error
}

type eventRepository struct {
	collection *mongo.Collection
}

func NewEventRepository(db *mongo.Database) EventRepository {
	coll := db.Collection("processed_events")

	// Ensure TTL index so event IDs are retained long enough to cover broker retry windows (e.g. 7 days)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		indexModel := mongo.IndexModel{
			Keys:    bson.M{"processed_at": 1},
			Options: options.Index().SetExpireAfterSeconds(7 * 24 * 3600),
		}
		_, _ = coll.Indexes().CreateOne(ctx, indexModel)
	}()

	return &eventRepository{
		collection: coll,
	}
}

func (r *eventRepository) IsProcessed(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	count, err := r.collection.CountDocuments(ctx, bson.M{"_id": eventID})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *eventRepository) MarkProcessed(ctx context.Context, eventID, eventType string) error {
	if eventID == "" {
		return nil
	}
	event := ProcessedEvent{
		EventID:     eventID,
		EventType:   eventType,
		ProcessedAt: time.Now().UTC(),
	}
	opts := options.Update().SetUpsert(true)
	_, err := r.collection.UpdateOne(ctx, bson.M{"_id": eventID}, bson.M{"$set": event}, opts)
	return err
}
