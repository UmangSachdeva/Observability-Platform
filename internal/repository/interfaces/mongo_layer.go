package interfaces

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// MongoLayer exposes reusable collection-level MongoDB operations. Entity
// repositories build their persistence methods on top of this interface.
type MongoLayer interface {
	InsertOne(ctx context.Context, collection string, document any) error
	// InsertMany inserts documents without stopping at the first failure.
	InsertMany(ctx context.Context, collection string, documents []any) error
	// UpsertOne updates the document matching filter, creating it if needed.
	UpsertOne(ctx context.Context, collection string, filter any, update any) error
	FindOne(ctx context.Context, collection string, filter any, result any) error
	Find(ctx context.Context, collection string, filter any, sort bson.D, limit int64, result any) error
	UpdateOne(ctx context.Context, collection string, filter any, update any) (int64, error)
	DeleteOne(ctx context.Context, collection string, filter any) (int64, error)
	CreateIndex(ctx context.Context, collection string, keys bson.D, unique bool) error
	Close(ctx context.Context) error
}
