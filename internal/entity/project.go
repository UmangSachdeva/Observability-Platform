package entity

import "time"

type Project struct {
	ID          string    `bson:"id"`
	AccountID   string    `bson:"account_id"`
	Name        string    `bson:"name"`
	Description string    `bson:"description,omitempty"`
	CreatedBy   string    `bson:"created_by"`
	CreatedAt   time.Time `bson:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at"`
}
