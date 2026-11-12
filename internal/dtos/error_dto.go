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
}

type CreateErrorResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type ErrorEventResponse struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"project_id"`
	Timestamp   time.Time         `json:"timestamp"`
	ReceivedAt  time.Time         `json:"received_at"`
	Service     string            `json:"service"`
	Environment string            `json:"environment,omitempty"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	StackTrace  string            `json:"stack_trace,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
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
