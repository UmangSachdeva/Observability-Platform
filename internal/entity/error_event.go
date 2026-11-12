package entity

import "time"

// ErrorEvent is the persisted application-error entity.
type ErrorEvent struct {
	ID          string            `bson:"id"`
	AccountID   string            `bson:"account_id"`
	ProjectID   string            `bson:"project_id"`
	Timestamp   time.Time         `bson:"timestamp"`
	ReceivedAt  time.Time         `bson:"received_at"`
	Service     string            `bson:"service"`
	Environment string            `bson:"environment,omitempty"`
	Level       string            `bson:"level"`
	Message     string            `bson:"message"`
	StackTrace  string            `bson:"stack_trace,omitempty"`
	Attributes  map[string]string `bson:"attributes,omitempty"`
}
