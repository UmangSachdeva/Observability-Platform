package interfaces

import (
	"context"
	"time"

	"observability-platform/internal/entity"
)

// TraceSummaryUpdate folds one batch of a trace's spans into its summary.
type TraceSummaryUpdate struct {
	AccountID  string
	ProjectID  string
	TraceID    string
	Start      time.Time
	End        time.Time
	SpanCount  int
	ErrorCount int
	Services   []string
	// Root is the batch's root span if it has one (RootIsTrue), otherwise its
	// earliest span, used only when the summary is created.
	Root       entity.Span
	RootIsTrue bool
}

// TraceQuery describes one page of the trace list, newest first.
type TraceQuery struct {
	AccountID  string
	ProjectID  string
	Services   []string
	Text       string
	ErrorsOnly bool
	From       *time.Time
	To         *time.Time
	After      *TraceCursor
	Limit      int
}

type TraceCursor struct {
	StartTime time.Time
	TraceID   string
}

type SpanRepository interface {
	// InsertSpans stores spans and returns the ones that were new; spans
	// already stored (an exporter retry) are skipped.
	InsertSpans(ctx context.Context, spans []entity.Span) ([]entity.Span, error)
	UpsertTraceSummary(ctx context.Context, update TraceSummaryUpdate) error
	SearchTraces(ctx context.Context, query TraceQuery) ([]entity.TraceSummary, error)
	// FindTrace returns up to limit spans of one trace, earliest first.
	FindTrace(ctx context.Context, accountID, projectID, traceID string, limit int) ([]entity.Span, error)
}
