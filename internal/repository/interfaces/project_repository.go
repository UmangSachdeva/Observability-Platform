package interfaces

import (
	"context"
	"errors"

	"observability-platform/internal/entity"
)

var (
	ErrProjectAlreadyExists = errors.New("project already exists in account")
	ErrProjectNotFound      = errors.New("project not found")
)

type ProjectRepository interface {
	Create(context.Context, entity.Project) error
	ListByAccount(context.Context, string, int) ([]entity.Project, error)
	// FindByID returns the project only when it belongs to accountID.
	FindByID(ctx context.Context, accountID, projectID string) (entity.Project, error)
}
