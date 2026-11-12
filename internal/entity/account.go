package entity

import "time"

type Account struct {
	ID        string          `bson:"id"`
	Name      string          `bson:"name"`
	Members   []AccountMember `bson:"members"`
	CreatedAt time.Time       `bson:"created_at"`
	UpdatedAt time.Time       `bson:"updated_at"`
}

type AccountMember struct {
	UserID   string    `bson:"user_id"`
	Role     string    `bson:"role"`
	JoinedAt time.Time `bson:"joined_at"`
}
