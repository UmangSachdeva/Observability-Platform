package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type projectRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.ProjectRepository = (*projectRepository)(nil)

func NewProjectRepository(mongo interfaces.MongoLayer, collection string) interfaces.ProjectRepository {
	return &projectRepository{mongo: mongo, collection: collection}
}

func (r *projectRepository) Create(ctx context.Context, project entity.Project) error {
	if err := r.mongo.InsertOne(ctx, r.collection, project); err != nil {
		if driver.IsDuplicateKeyError(err) {
			return interfaces.ErrProjectAlreadyExists
		}
		return err
	}
	return nil
}

func (r *projectRepository) ListByAccount(ctx context.Context, accountID string, limit int) ([]entity.Project, error) {
	projects := make([]entity.Project, 0, limit)
	err := r.mongo.Find(ctx, r.collection, bson.D{{Key: "account_id", Value: accountID}},
		bson.D{{Key: "created_at", Value: -1}}, int64(limit), &projects)
	if err != nil {
		return nil, err
	}
	return projects, nil
}

func (r *projectRepository) FindByID(ctx context.Context, accountID, projectID string) (entity.Project, error) {
	var project entity.Project
	err := r.mongo.FindOne(ctx, r.collection, bson.D{
		{Key: "id", Value: projectID},
		{Key: "account_id", Value: accountID},
	}, &project)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.Project{}, interfaces.ErrProjectNotFound
	}
	if err != nil {
		return entity.Project{}, err
	}
	return project, nil
}
