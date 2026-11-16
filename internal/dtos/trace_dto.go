package dtos

import "time"

// SearchTracesRequest holds the raw query parameters of a trace search.
type SearchTracesRequest struct {
	Query    string
	Services []string
	Status   string
	From     string
	To       string
	Limit    string
	Cursor   string
}

type TraceSummaryResponse struct {
	TraceID        string `json:"trace_id"`
	RootName       string `json:"root_name"`
	RootService    string `json:"root_service"`
	RootStatus     string `json:"root_status"`
	HTTPStatusCode string `json:"http_status_code,omitempty"`
	// Partial is true when this project has not received the trace's root
	// span (it may still be arriving, or belong to another project).
	Partial    bool      `json:"partial"`
	StartTime  time.Time `json:"start_time"`
	DurationMs float64   `json:"duration_ms"`
	SpanCount  int       `json:"span_count"`
	ErrorCount int       `json:"error_count"`
	Services   []string  `json:"services"`
}

type ListTracesResponse struct {
	Traces     []TraceSummaryResponse `json:"traces"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	HasMore    bool                   `json:"has_more"`
}

type SpanEventResponse struct {
	Name       string            `json:"name"`
	OffsetMs   float64           `json:"offset_ms"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// WaterfallSpanResponse is one row of the waterfall. Rows come in display
// order (each parent followed by its children); OffsetMs is the start
// relative to the trace start and Depth the indentation level.
type WaterfallSpanResponse struct {
	SpanID        string              `json:"span_id"`
	ParentSpanID  string              `json:"parent_span_id,omitempty"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	ServiceName   string              `json:"service_name"`
	StartTime     time.Time           `json:"start_time"`
	OffsetMs      float64             `json:"offset_ms"`
	DurationMs    float64             `json:"duration_ms"`
	Depth         int                 `json:"depth"`
	StatusCode    string              `json:"status_code"`
	StatusMessage string              `json:"status_message,omitempty"`
	Attributes    map[string]string   `json:"attributes,omitempty"`
	Resource      map[string]string   `json:"resource,omitempty"`
	Events        []SpanEventResponse `json:"events,omitempty"`
	// Orphan is true when the span names a parent this project never received.
	Orphan bool `json:"orphan,omitempty"`
}

type TraceResponse struct {
	TraceID    string                  `json:"trace_id"`
	StartTime  time.Time               `json:"start_time"`
	DurationMs float64                 `json:"duration_ms"`
	SpanCount  int                     `json:"span_count"`
	ErrorCount int                     `json:"error_count"`
	Services   []string                `json:"services"`
	Spans      []WaterfallSpanResponse `json:"spans"`
	// Errors are error events reported with this trace_id.
	Errors    []ErrorEventResponse `json:"errors"`
	Truncated bool                 `json:"truncated"`
}
