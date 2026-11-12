package interfaces

import (
	"context"
	"errors"

	"observability-platform/internal/entity"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("username or email already exists")
)

type UserRepository interface {
	Create(context.Context, entity.User) error
	FindByLogin(context.Context, string) (entity.User, error)
}
