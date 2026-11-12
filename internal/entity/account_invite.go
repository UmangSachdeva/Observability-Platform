package entity

import "time"

type AccountInvite struct {
	ID        string `bson:"id"`
	AccountID string `bson:"account_id"`
	Email     string `bson:"email"`
	TokenHash string `bson:"token_hash"`
	// EncryptedToken lets the invitee read the token back from GET /v1/invites.
	EncryptedToken string     `bson:"encrypted_token,omitempty"`
	InvitedBy      string     `bson:"invited_by"`
	Role           string     `bson:"role"`
	CreatedAt      time.Time  `bson:"created_at"`
	ExpiresAt      time.Time  `bson:"expires_at"`
	AcceptedAt     *time.Time `bson:"accepted_at,omitempty"`
	// SnoozedUntil hides the invite from the pending list until this time.
	SnoozedUntil *time.Time `bson:"snoozed_until,omitempty"`
}
