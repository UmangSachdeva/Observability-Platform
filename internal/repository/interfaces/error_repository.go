package interfaces

import (
	"context"
	"errors"
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
	TraceID      string
	// TraceErrorIDs are errors that had an occurrence in TraceID; they match
	// as well as errors whose latest occurrence carries that trace_id.
	TraceErrorIDs []string
	Attributes    map[string]string
	From          *time.Time
	To            *time.Time
	SortField     ErrorSortField
	SortDesc      bool
	After         *ErrorCursor
	Limit         int
}

// ErrorCursor is the sort value and id of the last event on the previous page.
// Value is a time.Time for time fields and a string otherwise.
type ErrorCursor struct {
	Value any
	ID    string
}

// OccurrenceCursor is the last occurrence of the previous page.
type OccurrenceCursor struct {
	ReceivedAt time.Time
	ID         string
}

type ErrorRepository interface {
	// RecordOccurrence stores one occurrence of event (grouped by event.ID):
	// it creates the error on its first occurrence and otherwise increments
	// its count and updates its latest-occurrence fields. It returns false,
	// and changes nothing, when the occurrence's key was already stored
	// (a retried or replayed delivery).
	RecordOccurrence(ctx context.Context, event entity.ErrorEvent, occurrence entity.ErrorOccurrence) (bool, error)
	// ListOccurrences returns an error's occurrences, newest first.
	ListOccurrences(ctx context.Context, accountID, projectID, errorID string, after *OccurrenceCursor, limit int) ([]entity.ErrorOccurrence, error)
	// ErrorIDsForTrace returns the errors that had an occurrence in traceID.
	ErrorIDsForTrace(ctx context.Context, accountID, projectID, traceID string, limit int) ([]string, error)
	// Search returns events matching query, ordered by its sort field and then
	// by id, starting after query.After.
	Search(ctx context.Context, query ErrorQuery) ([]entity.ErrorEvent, error)
	// FindByID returns one event, only if it belongs to the given account and
	// project; otherwise it returns ErrErrorEventNotFound.
	FindByID(ctx context.Context, accountID, projectID, id string) (entity.ErrorEvent, error)
}

var ErrErrorEventNotFound = errors.New("error event not found")
