package interfaces

import (
	"context"
	"errors"
	"time"

	"observability-platform/internal/entity"
)

var ErrInviteNotFound = errors.New("invite not found")

type InviteRepository interface {
	Create(context.Context, entity.AccountInvite) error
	FindByTokenHash(context.Context, string) (entity.AccountInvite, error)
	MarkAccepted(context.Context, string, time.Time) (bool, error)
	FindByID(context.Context, string) (entity.AccountInvite, error)
	// ListPendingByEmail returns unaccepted, unexpired invites for email.
	// Snoozed invites are left out unless includeSnoozed is true.
	ListPendingByEmail(ctx context.Context, email string, now time.Time, includeSnoozed bool, limit int) ([]entity.AccountInvite, error)
	// SetSnooze sets snoozed_until on an unaccepted invite, or clears it when
	// snoozedUntil is nil. It reports whether the document changed.
	SetSnooze(ctx context.Context, id string, snoozedUntil *time.Time) (bool, error)
}
