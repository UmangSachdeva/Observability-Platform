package entity

import "time"

// User stores account identity and a one-way password hash.
type User struct {
	ID           string    `bson:"id"`
	Username     string    `bson:"username"`
	Email        string    `bson:"email"`
	PasswordHash string    `bson:"password_hash"`
	CreatedAt    time.Time `bson:"created_at"`
	UpdatedAt    time.Time `bson:"updated_at"`
}
