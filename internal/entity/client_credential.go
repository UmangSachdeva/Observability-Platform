package entity

import "time"

// ClientCredential stores a client access key and an encrypted secret.
type ClientCredential struct {
	AccessKey       string    `bson:"access_key"`
	UserID          string    `bson:"user_id"`
	AccountID       string    `bson:"account_id,omitempty"`
	ProjectID       string    `bson:"project_id,omitempty"`
	ClientName      string    `bson:"client_name"`
	EncryptedSecret string    `bson:"encrypted_secret"`
	CreatedAt       time.Time `bson:"created_at"`
}
