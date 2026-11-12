package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type errorRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.ErrorRepository = (*errorRepository)(nil)

func NewErrorRepository(mongo interfaces.MongoLayer, collection string) interfaces.ErrorRepository {
	return &errorRepository{mongo: mongo, collection: collection}
}

func (r *errorRepository) Create(ctx context.Context, event entity.ErrorEvent) error {
	return r.mongo.InsertOne(ctx, r.collection, event)
}

func (r *errorRepository) ListByProject(ctx context.Context, projectID string, limit int) ([]entity.ErrorEvent, error) {
	events := make([]entity.ErrorEvent, 0, limit)
	err := r.mongo.Find(ctx, r.collection, bson.D{{Key: "project_id", Value: projectID}}, bson.D{{Key: "received_at", Value: -1}}, int64(limit), &events)
	if err != nil {
		return nil, err
	}
	return events, nil
}
