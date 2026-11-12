package mapper

import (
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
)

func CreateInviteRequestToEntity(request dtos.CreateInviteRequest, id, accountID, inviterID, tokenHash string, now, expiresAt time.Time) entity.AccountInvite {
	return entity.AccountInvite{
		ID: id, AccountID: accountID, Email: request.Email, TokenHash: tokenHash,
		InvitedBy: inviterID, Role: request.Role, CreatedAt: now, ExpiresAt: expiresAt,
	}
}

func InviteToAccountMember(invite entity.AccountInvite, userID string, joinedAt time.Time) entity.AccountMember {
	return entity.AccountMember{UserID: userID, Role: invite.Role, JoinedAt: joinedAt}
}

func InviteToPendingResponse(invite entity.AccountInvite) dtos.PendingInviteResponse {
	return dtos.PendingInviteResponse{
		ID: invite.ID, AccountID: invite.AccountID, Email: invite.Email, Role: invite.Role,
		InvitedBy: invite.InvitedBy, CreatedAt: invite.CreatedAt, ExpiresAt: invite.ExpiresAt,
		SnoozedUntil: invite.SnoozedUntil,
	}
}
