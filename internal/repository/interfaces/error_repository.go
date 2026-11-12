package interfaces

import (
	"context"
	"time"

	"observability-platform/internal/entity"
)

// ErrorSortField is a field error events can be sorted by. Every allowed field
// is always present on stored events, which keyset pagination relies on.
type ErrorSortField string

const (
	ErrorSortReceivedAt ErrorSortField = "received_at"
	ErrorSortTimestamp  ErrorSortField = "timestamp"
	ErrorSortLevel      ErrorSortField = "level"
	ErrorSortService    ErrorSortField = "service"
)

// ErrorQuery describes one page of an error search. AccountID is always
// applied, so a query can never read another account's events.
type ErrorQuery struct {
	AccountID    string
	ProjectIDs   []string
	Text         string
	Services     []string
	Environments []string
	Levels       []string
	Attributes   map[string]string
	From         *time.Time
	To           *time.Time
	SortField    ErrorSortField
	SortDesc     bool
	After        *ErrorCursor
	Limit        int
}

// ErrorCursor is the sort value and id of the last event on the previous page.
// Value is a time.Time for time fields and a string otherwise.
type ErrorCursor struct {
	Value any
	ID    string
}

type ErrorRepository interface {
	Create(context.Context, entity.ErrorEvent) error
	// Search returns events matching query, ordered by its sort field and then
	// by id, starting after query.After.
	Search(ctx context.Context, query ErrorQuery) ([]entity.ErrorEvent, error)
}
