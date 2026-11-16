package dtos

import "time"

type CreateErrorRequest struct {
	Timestamp   time.Time         `json:"timestamp"`
	Service     string            `json:"service"`
	Environment string            `json:"environment"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	StackTrace  string            `json:"stack_trace"`
	Attributes  map[string]string `json:"attributes"`
	// TraceID and SpanID link the event to an OpenTelemetry trace (lowercase
	// hex, 32 and 16 characters). Both are optional.
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

type CreateErrorResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type ErrorEventResponse struct {
	ID string `json:"id"`
	// OccurrenceCount is how many times this error happened; FirstSeen is
	// when it first happened and Timestamp when it last happened (by the
	// occurrence timestamps the clients sent).
	OccurrenceCount int               `json:"occurrence_count"`
	FirstSeen       time.Time         `json:"first_seen"`
	ProjectID       string            `json:"project_id"`
	Timestamp       time.Time         `json:"timestamp"`
	ReceivedAt      time.Time         `json:"received_at"`
	Service         string            `json:"service"`
	Environment     string            `json:"environment,omitempty"`
	Level           string            `json:"level"`
	Message         string            `json:"message"`
	StackTrace      string            `json:"stack_trace,omitempty"`
	Attributes      map[string]string `json:"attributes,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
	SpanID          string            `json:"span_id,omitempty"`
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ListErrorsResponse struct {
	Errors []ErrorEventResponse `json:"errors"`
	// NextCursor is set when more results exist; pass it as ?cursor= with the
	// same filters and sort to get the next page.
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// SearchErrorsRequest holds the raw query parameters of an error search.
type SearchErrorsRequest struct {
	Query        string
	ProjectIDs   []string
	Services     []string
	Environments []string
	Levels       []string
	TraceID      string
	Attributes   map[string]string
	From         string
	To           string
	Sort         string
	Limit        string
	Cursor       string
}

type CreateAPIKeyRequest struct {
	ClientName string `json:"client_name"`
}

// APIKeyResponse describes a stored key. It never includes the secret.
type APIKeyResponse struct {
	AccessKey  string    `json:"access_key"`
	ClientName string    `json:"client_name"`
	AccountID  string    `json:"account_id,omitempty"`
	ProjectID  string    `json:"project_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type ListAPIKeysResponse struct {
	Keys []APIKeyResponse `json:"keys"`
}

type CreateAPIKeyResponse struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Algorithm string `json:"algorithm"`
	AccountID string `json:"account_id"`
	ProjectID string `json:"project_id"`
}

// OccurrenceResponse is one time an error happened.
type OccurrenceResponse struct {
	ID         string            `json:"id"`
	Timestamp  time.Time         `json:"timestamp"`
	ReceivedAt time.Time         `json:"received_at"`
	TraceID    string            `json:"trace_id,omitempty"`
	SpanID     string            `json:"span_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type ListOccurrencesResponse struct {
	Occurrences []OccurrenceResponse `json:"occurrences"`
	Total       int                  `json:"total"`
	FirstSeen   time.Time            `json:"first_seen"`
	LastSeen    time.Time            `json:"last_seen"`
	NextCursor  string               `json:"next_cursor,omitempty"`
	HasMore     bool                 `json:"has_more"`
}
