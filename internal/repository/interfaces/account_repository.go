package interfaces

import (
	"context"
	"errors"

	"observability-platform/internal/entity"
)

var (
	ErrAccountNotFound  = errors.New("account not found")
	ErrNotAccountMember = errors.New("user is not a member of the account")
	ErrLastAccountAdmin = errors.New("user is the last admin of the account")
)

type AccountRepository interface {
	Create(context.Context, entity.Account) error
	DeleteByID(context.Context, string) error
	FindByID(context.Context, string) (entity.Account, error)
	FindDefaultForUser(context.Context, string) (entity.Account, error)
	IsMember(context.Context, string, string) (bool, error)
	AddMember(context.Context, string, entity.AccountMember) error
	RemoveMember(context.Context, string, string) error
}
