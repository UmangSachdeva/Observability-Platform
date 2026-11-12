package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type userRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.UserRepository = (*userRepository)(nil)

func NewUserRepository(mongo interfaces.MongoLayer, collection string) interfaces.UserRepository {
	return &userRepository{mongo: mongo, collection: collection}
}

func (r *userRepository) Create(ctx context.Context, user entity.User) error {
	if err := r.mongo.InsertOne(ctx, r.collection, user); err != nil {
		if driver.IsDuplicateKeyError(err) {
			return interfaces.ErrUserAlreadyExists
		}
		return err
	}
	return nil
}

func (r *userRepository) FindByLogin(ctx context.Context, login string) (entity.User, error) {
	filter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "email", Value: login}},
		bson.D{{Key: "username", Value: login}},
	}}}
	var user entity.User
	err := r.mongo.FindOne(ctx, r.collection, filter, &user)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.User{}, interfaces.ErrUserNotFound
	}
	if err != nil {
		return entity.User{}, err
	}
	return user, nil
}
