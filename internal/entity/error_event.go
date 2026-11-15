package entity

import "time"

// ErrorEvent is one distinct error: events with the same service,
// environment, level, message and stack trace are stored once, and every
// time it happens is kept as an ErrorOccurrence. Timestamp, ReceivedAt,
// Attributes, TraceID and SpanID describe the latest occurrence.
type ErrorEvent struct {
	ID          string `bson:"id"`
	Fingerprint string `bson:"fingerprint,omitempty"`
	// OccurrenceCount and FirstSeen are empty on events stored before
	// grouping existed; those count as one occurrence.
	OccurrenceCount int               `bson:"occurrence_count,omitempty"`
	FirstSeen       time.Time         `bson:"first_seen,omitempty"`
	AccountID       string            `bson:"account_id"`
	ProjectID       string            `bson:"project_id"`
	Timestamp       time.Time         `bson:"timestamp"`
	ReceivedAt      time.Time         `bson:"received_at"`
	Service         string            `bson:"service"`
	Environment     string            `bson:"environment,omitempty"`
	Level           string            `bson:"level"`
	Message         string            `bson:"message"`
	StackTrace      string            `bson:"stack_trace,omitempty"`
	Attributes      map[string]string `bson:"attributes,omitempty"`
	TraceID         string            `bson:"trace_id,omitempty"`
	SpanID          string            `bson:"span_id,omitempty"`
}

// ErrorOccurrence records one time an error happened.
type ErrorOccurrence struct {
	ID        string `bson:"id"`
	ErrorID   string `bson:"error_id"`
	AccountID string `bson:"account_id"`
	ProjectID string `bson:"project_id"`
	// Key identifies a delivery (timestamp, trace, span and attributes); a
	// retried or replayed request has the same key and is not stored again.
	Key        string            `bson:"key"`
	Timestamp  time.Time         `bson:"timestamp"`
	ReceivedAt time.Time         `bson:"received_at"`
	TraceID    string            `bson:"trace_id,omitempty"`
	SpanID     string            `bson:"span_id,omitempty"`
	Attributes map[string]string `bson:"attributes,omitempty"`
}
