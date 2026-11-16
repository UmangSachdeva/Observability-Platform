package entity

import "time"

// Attribute is one key/value pair. Spans keep attributes as a list because
// OpenTelemetry keys contain dots, which MongoDB field names should avoid.
type Attribute struct {
	Key   string `bson:"k"`
	Value string `bson:"v"`
}

type SpanEvent struct {
	Name       string      `bson:"name"`
	Time       time.Time   `bson:"time"`
	Attributes []Attribute `bson:"attributes,omitempty"`
}

// Span is one OpenTelemetry span received through OTLP.
type Span struct {
	AccountID    string `bson:"account_id"`
	ProjectID    string `bson:"project_id"`
	TraceID      string `bson:"trace_id"`
	SpanID       string `bson:"span_id"`
	ParentSpanID string `bson:"parent_span_id"`
	Name         string `bson:"name"`
	// Kind is SERVER, CLIENT, INTERNAL, PRODUCER, CONSUMER or UNSPECIFIED.
	Kind        string    `bson:"kind"`
	ServiceName string    `bson:"service_name"`
	ScopeName   string    `bson:"scope_name,omitempty"`
	StartTime   time.Time `bson:"start_time"`
	EndTime     time.Time `bson:"end_time"`
	DurationNs  int64     `bson:"duration_ns"`
	// StatusCode is UNSET, OK or ERROR.
	StatusCode         string      `bson:"status_code"`
	StatusMessage      string      `bson:"status_message,omitempty"`
	Attributes         []Attribute `bson:"attributes,omitempty"`
	ResourceAttributes []Attribute `bson:"resource_attributes,omitempty"`
	Events             []SpanEvent `bson:"events,omitempty"`
	ReceivedAt         time.Time   `bson:"received_at"`
}

// TraceSummary is one row per trace and project, kept up to date as spans
// arrive, so traces can be listed without scanning every span.
type TraceSummary struct {
	AccountID  string    `bson:"account_id"`
	ProjectID  string    `bson:"project_id"`
	TraceID    string    `bson:"trace_id"`
	StartTime  time.Time `bson:"start_time"`
	EndTime    time.Time `bson:"end_time"`
	SpanCount  int       `bson:"span_count"`
	ErrorCount int       `bson:"error_count"`
	Services   []string  `bson:"services"`
	// Root fields describe the trace's root span. Until the root arrives (or
	// when it lives in another project) they describe the earliest span seen,
	// and HasRoot is false.
	RootName       string    `bson:"root_name"`
	RootService    string    `bson:"root_service"`
	RootStatus     string    `bson:"root_status"`
	RootHTTPStatus string    `bson:"root_http_status,omitempty"`
	HasRoot        bool      `bson:"has_root"`
	UpdatedAt      time.Time `bson:"updated_at"`
}
