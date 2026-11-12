package dtos

import (
	"encoding/json"
	"time"
)

type CreateInviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role,omitempty"`
}

type CreateInviteResponse struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Token     string    `json:"invite_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AcceptInviteRequest identifies the invitation either by the token shared by
// the inviter or by its id from GET /v1/invites. Exactly one must be set.
type AcceptInviteRequest struct {
	Token    string `json:"invite_token,omitempty"`
	InviteID string `json:"invite_id,omitempty"`
}

type AcceptInviteResponse struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
}

type LeaveAccountResponse struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
}

type PendingInviteResponse struct {
	ID           string     `json:"id"`
	AccountID    string     `json:"account_id"`
	AccountName  string     `json:"account_name,omitempty"`
	Email        string     `json:"email"`
	Role         string     `json:"role"`
	InvitedBy    string     `json:"invited_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	// InviteToken is sent to POST /v1/invites/accept. It is missing for
	// invites created before tokens were stored encrypted.
	InviteToken string `json:"invite_token,omitempty"`
}

type ListPendingInvitesResponse struct {
	Invites []PendingInviteResponse `json:"invites"`
}

// SnoozeInviteRequest keeps snoozed_until raw so a missing field can be told
// apart from an explicit null (which clears the snooze).
type SnoozeInviteRequest struct {
	SnoozedUntil json.RawMessage `json:"snoozed_until"`
}
