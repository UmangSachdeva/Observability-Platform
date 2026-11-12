package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type apiKeyRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.APIKeyRepository = (*apiKeyRepository)(nil)

func NewAPIKeyRepository(mongo interfaces.MongoLayer, collection string) interfaces.APIKeyRepository {
	return &apiKeyRepository{mongo: mongo, collection: collection}
}

func (r *apiKeyRepository) Create(ctx context.Context, credential entity.ClientCredential) error {
	return r.mongo.InsertOne(ctx, r.collection, credential)
}

func (r *apiKeyRepository) FindByAccessKey(ctx context.Context, accessKey string) (entity.ClientCredential, error) {
	var credential entity.ClientCredential
	err := r.mongo.FindOne(ctx, r.collection, map[string]string{"access_key": accessKey}, &credential)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.ClientCredential{}, interfaces.ErrAPIKeyNotFound
	}
	if err != nil {
		return entity.ClientCredential{}, err
	}
	return credential, nil
}

func (r *apiKeyRepository) ListByUser(ctx context.Context, userID string, limit int) ([]entity.ClientCredential, error) {
	credentials := make([]entity.ClientCredential, 0, limit)
	err := r.mongo.Find(ctx, r.collection, bson.D{{Key: "user_id", Value: userID}}, bson.D{{Key: "created_at", Value: -1}}, int64(limit), &credentials)
	if err != nil {
		return nil, err
	}
	return credentials, nil
}
