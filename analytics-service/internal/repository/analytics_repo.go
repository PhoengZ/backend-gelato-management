package repository

import (
	"context"

	"analytics-service/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type AnalyticsRepository interface {
	FindByDate(ctx context.Context, date string) (*models.Analytics, error)
	Save(ctx context.Context, analytics *models.Analytics) error
	FindByDateRange(ctx context.Context, startDate, endDate string) ([]models.Analytics, error)
}

type analyticsRepository struct {
	collection *mongo.Collection
}

func NewAnalyticsRepository(db *mongo.Database) AnalyticsRepository {
	return &analyticsRepository{
		collection: db.Collection("analytics"),
	}
}

func (r *analyticsRepository) FindByDate(ctx context.Context, date string) (*models.Analytics, error) {
	var analytics models.Analytics
	err := r.collection.FindOne(ctx, bson.M{"date": date}).Decode(&analytics)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil // Return nil if not found
		}
		return nil, err
	}
	return &analytics, nil
}

func (r *analyticsRepository) Save(ctx context.Context, analytics *models.Analytics) error {
	filter := bson.M{"date": analytics.Date}
	// Exclude _id from $set to avoid modifying MongoDB's immutable field
	update := bson.M{"$set": bson.M{
		"date":         analytics.Date,
		"financials":   analytics.Financials,
		"operations":   analytics.Operations,
		"waste_stats":  analytics.WasteStats,
		"flavor_stats": analytics.FlavorStats,
	}}
	opts := options.Update().SetUpsert(true)

	_, err := r.collection.UpdateOne(ctx, filter, update, opts)
	return err
}

func (r *analyticsRepository) FindByDateRange(ctx context.Context, startDate, endDate string) ([]models.Analytics, error) {
	filter := bson.M{
		"date": bson.M{
			"$gte": startDate,
			"$lte": endDate,
		},
	}

	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []models.Analytics
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}
