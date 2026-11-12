package interfaces

import (
	"context"

	"observability-platform/internal/entity"
)

type ErrorRepository interface {
	Create(context.Context, entity.ErrorEvent) error
	// ListByProject returns the project's newest events first.
	ListByProject(ctx context.Context, projectID string, limit int) ([]entity.ErrorEvent, error)
}
