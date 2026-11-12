package interfaces

import (
	"context"
	"errors"

	"observability-platform/internal/entity"
)

var ErrAPIKeyNotFound = errors.New("API key not found")

type APIKeyRepository interface {
	Create(context.Context, entity.ClientCredential) error
	FindByAccessKey(context.Context, string) (entity.ClientCredential, error)
	// ListByUser returns the user's credentials, newest first.
	ListByUser(ctx context.Context, userID string, limit int) ([]entity.ClientCredential, error)
}
