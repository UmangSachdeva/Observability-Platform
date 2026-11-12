package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

const inviteLifetime = 7 * 24 * time.Hour

type AccountService struct {
	accounts interfaces.AccountRepository
	invites  interfaces.InviteRepository
	tokens   *utils.SecretCipher
}

func NewAccountService(accounts interfaces.AccountRepository, invites interfaces.InviteRepository, tokens *utils.SecretCipher) *AccountService {
	return &AccountService{accounts: accounts, invites: invites, tokens: tokens}
}

// inviteTokenAAD binds an encrypted invite token to its invite, so the value
// cannot be copied into another invite or API-key record and still decrypt.
func inviteTokenAAD(inviteID string) []byte {
	return []byte("account_invite:" + inviteID)
}

// pendingInviteResponse maps an invite and decrypts its token for the invitee.
func (s *AccountService) pendingInviteResponse(invite entity.AccountInvite) (dtos.PendingInviteResponse, error) {
	response := mapper.InviteToPendingResponse(invite)
	if invite.EncryptedToken == "" {
		return response, nil
	}
	token, err := s.tokens.Decrypt(invite.EncryptedToken, inviteTokenAAD(invite.ID))
	if err != nil {
		return dtos.PendingInviteResponse{}, utils.WrapError(500, "internal_error", "Could not load invitation", err)
	}
	response.InviteToken = string(token)
	return response, nil
}

func (s *AccountService) CreateInvite(ctx context.Context, accountID, inviterID string, request dtos.CreateInviteRequest) (dtos.CreateInviteResponse, error) {
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.Role = strings.ToLower(strings.TrimSpace(request.Role))
	if request.Role == "" {
		request.Role = "member"
	}
	if err := validation.ValidateCreateInvite(request); err != nil {
		return dtos.CreateInviteResponse{}, err
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if errors.Is(err, interfaces.ErrAccountNotFound) {
		return dtos.CreateInviteResponse{}, utils.NewError(404, "account_not_found", "Account was not found")
	}
	if err != nil {
		return dtos.CreateInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not verify account membership", err)
	}
	admin := false
	for _, member := range account.Members {
		if member.UserID == inviterID && (member.Role == "admin" || member.Role == "owner") {
			admin = true
			break
		}
	}
	if !admin {
		return dtos.CreateInviteResponse{}, utils.NewError(403, "forbidden", "Only account admins can invite users")
	}
	inviteToken, err := randomToken("", 32)
	if err != nil {
		return dtos.CreateInviteResponse{}, utils.WrapError(500, "invite_creation_failed", "Could not create invitation", err)
	}
	id, err := newID()
	if err != nil {
		return dtos.CreateInviteResponse{}, utils.WrapError(500, "invite_creation_failed", "Could not create invitation", err)
	}
	now := time.Now().UTC()
	tokenHashBytes := sha256.Sum256([]byte(inviteToken))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])
	encryptedToken, err := s.tokens.Encrypt([]byte(inviteToken), inviteTokenAAD(id))
	if err != nil {
		return dtos.CreateInviteResponse{}, utils.WrapError(500, "invite_creation_failed", "Could not create invitation", err)
	}
	invite := mapper.CreateInviteRequestToEntity(request, id, accountID, inviterID, tokenHash, now, now.Add(inviteLifetime))
	invite.EncryptedToken = encryptedToken
	if err := s.invites.Create(ctx, invite); err != nil {
		return dtos.CreateInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not create invitation", err)
	}
	return dtos.CreateInviteResponse{
		ID: invite.ID, AccountID: invite.AccountID, Email: invite.Email, Role: invite.Role,
		Token: inviteToken, ExpiresAt: invite.ExpiresAt,
	}, nil
}

func (s *AccountService) GetAccount(ctx context.Context, accountID, userID string) (dtos.AccountResponse, error) {
	account, err := s.accounts.FindByID(ctx, accountID)
	if errors.Is(err, interfaces.ErrAccountNotFound) {
		return dtos.AccountResponse{}, utils.NewError(404, "account_not_found", "Account was not found")
	}
	if err != nil {
		return dtos.AccountResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load account", err)
	}
	member := false
	for _, accountMember := range account.Members {
		if accountMember.UserID == userID {
			member = true
			break
		}
	}
	if !member {
		return dtos.AccountResponse{}, utils.NewError(403, "forbidden", "You are not a member of this account")
	}
	return mapper.AccountToResponse(account), nil
}

func (s *AccountService) AcceptInvite(ctx context.Context, userID, userEmail string, request dtos.AcceptInviteRequest) (dtos.AcceptInviteResponse, error) {
	request.Token = strings.TrimSpace(request.Token)
	request.InviteID = strings.TrimSpace(request.InviteID)
	if err := validation.ValidateAcceptInvite(request); err != nil {
		return dtos.AcceptInviteResponse{}, err
	}
	var invite entity.AccountInvite
	var err error
	if request.InviteID != "" {
		// Accepting by id is safe: the invite must still match the caller's
		// verified email below, exactly as with a token.
		invite, err = s.invites.FindByID(ctx, request.InviteID)
	} else {
		tokenHashBytes := sha256.Sum256([]byte(request.Token))
		invite, err = s.invites.FindByTokenHash(ctx, hex.EncodeToString(tokenHashBytes[:]))
	}
	tokenHash := invite.TokenHash
	if errors.Is(err, interfaces.ErrInviteNotFound) {
		return dtos.AcceptInviteResponse{}, invalidInviteError()
	}
	if err != nil {
		return dtos.AcceptInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not verify invitation", err)
	}
	if !strings.EqualFold(invite.Email, userEmail) {
		return dtos.AcceptInviteResponse{}, utils.NewError(403, "invite_email_mismatch", "This invitation belongs to a different email address")
	}
	if invite.ExpiresAt.Before(time.Now().UTC()) {
		return dtos.AcceptInviteResponse{}, utils.NewError(410, "invite_expired", "This invitation has expired")
	}
	if invite.AcceptedAt != nil {
		isMember, err := s.accounts.IsMember(ctx, invite.AccountID, userID)
		if err != nil {
			return dtos.AcceptInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not verify account membership", err)
		}
		if isMember {
			return dtos.AcceptInviteResponse{AccountID: invite.AccountID, Status: "accepted"}, nil
		}
		return dtos.AcceptInviteResponse{}, invalidInviteError()
	}
	member := mapper.InviteToAccountMember(invite, userID, time.Now().UTC())
	if err := s.accounts.AddMember(ctx, invite.AccountID, member); err != nil {
		if errors.Is(err, interfaces.ErrAccountNotFound) {
			return dtos.AcceptInviteResponse{}, invalidInviteError()
		}
		return dtos.AcceptInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not add account member", err)
	}
	accepted, err := s.invites.MarkAccepted(ctx, tokenHash, time.Now().UTC())
	if err != nil {
		return dtos.AcceptInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not finalize invitation", err)
	}
	if !accepted {
		isMember, err := s.accounts.IsMember(ctx, invite.AccountID, userID)
		if err == nil && isMember {
			return dtos.AcceptInviteResponse{AccountID: invite.AccountID, Status: "accepted"}, nil
		}
		return dtos.AcceptInviteResponse{}, utils.NewError(409, "invite_already_used", "This invitation has already been accepted")
	}
	return dtos.AcceptInviteResponse{AccountID: invite.AccountID, Status: "accepted"}, nil
}

// LeaveAccount removes the user from the account. Members can always leave;
// admins can leave only while another admin remains in the account.
func (s *AccountService) LeaveAccount(ctx context.Context, accountID, userID string) (dtos.LeaveAccountResponse, error) {
	err := s.accounts.RemoveMember(ctx, accountID, userID)
	switch {
	case err == nil:
		return dtos.LeaveAccountResponse{AccountID: accountID, Status: "left"}, nil
	case errors.Is(err, interfaces.ErrAccountNotFound):
		return dtos.LeaveAccountResponse{}, utils.NewError(404, "account_not_found", "Account was not found")
	case errors.Is(err, interfaces.ErrNotAccountMember):
		return dtos.LeaveAccountResponse{}, utils.NewError(403, "forbidden", "You are not a member of this account")
	case errors.Is(err, interfaces.ErrLastAccountAdmin):
		return dtos.LeaveAccountResponse{}, utils.NewError(409, "last_admin", "Add another admin to this account before leaving it")
	default:
		return dtos.LeaveAccountResponse{}, utils.WrapError(503, "storage_unavailable", "Could not leave account", err)
	}
}

const maxPendingInvites = 100

// ListPendingInvites returns the invites waiting for the user's email address.
func (s *AccountService) ListPendingInvites(ctx context.Context, userEmail string, includeSnoozed bool) (dtos.ListPendingInvitesResponse, error) {
	email := strings.ToLower(strings.TrimSpace(userEmail))
	invites, err := s.invites.ListPendingByEmail(ctx, email, time.Now().UTC(), includeSnoozed, maxPendingInvites)
	if err != nil {
		return dtos.ListPendingInvitesResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load invitations", err)
	}
	response := dtos.ListPendingInvitesResponse{Invites: make([]dtos.PendingInviteResponse, 0, len(invites))}
	accountNames := map[string]string{}
	for _, invite := range invites {
		item, err := s.pendingInviteResponse(invite)
		if err != nil {
			return dtos.ListPendingInvitesResponse{}, err
		}
		name, seen := accountNames[invite.AccountID]
		if !seen {
			// The name is only for display; a missing or unreadable account
			// leaves it empty instead of failing the whole list.
			if account, err := s.accounts.FindByID(ctx, invite.AccountID); err == nil {
				name = account.Name
			}
			accountNames[invite.AccountID] = name
		}
		item.AccountName = name
		response.Invites = append(response.Invites, item)
	}
	return response, nil
}

// SnoozeInvite hides a pending invite from the pending list until the given
// time, or shows it again when the request clears the snooze.
func (s *AccountService) SnoozeInvite(ctx context.Context, inviteID, userEmail string, request dtos.SnoozeInviteRequest) (dtos.PendingInviteResponse, error) {
	until, err := validation.ParseSnoozeInvite(request)
	if err != nil {
		return dtos.PendingInviteResponse{}, err
	}
	invite, err := s.invites.FindByID(ctx, inviteID)
	if errors.Is(err, interfaces.ErrInviteNotFound) {
		return dtos.PendingInviteResponse{}, invalidInviteError()
	}
	if err != nil {
		return dtos.PendingInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load invitation", err)
	}
	// Another user's invite looks the same as a missing one.
	if !strings.EqualFold(invite.Email, strings.TrimSpace(userEmail)) {
		return dtos.PendingInviteResponse{}, invalidInviteError()
	}
	if invite.AcceptedAt != nil {
		return dtos.PendingInviteResponse{}, inviteAlreadyUsedError()
	}
	now := time.Now().UTC()
	if !invite.ExpiresAt.After(now) {
		return dtos.PendingInviteResponse{}, utils.NewError(410, "invite_expired", "This invitation has expired")
	}
	if until != nil && !until.After(now) {
		return dtos.PendingInviteResponse{}, utils.NewError(400, "validation_error", "snoozed_until must be in the future")
	}
	if until != nil && until.After(invite.ExpiresAt) {
		return dtos.PendingInviteResponse{}, utils.NewError(400, "validation_error", "snoozed_until must not be later than the invitation expiry")
	}
	changed, err := s.invites.SetSnooze(ctx, invite.ID, until)
	if err != nil {
		return dtos.PendingInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not snooze invitation", err)
	}
	if !changed {
		// Either the value was already set, or the invite was accepted meanwhile.
		current, err := s.invites.FindByID(ctx, invite.ID)
		if err != nil {
			return dtos.PendingInviteResponse{}, utils.WrapError(503, "storage_unavailable", "Could not snooze invitation", err)
		}
		if current.AcceptedAt != nil {
			return dtos.PendingInviteResponse{}, inviteAlreadyUsedError()
		}
	}
	invite.SnoozedUntil = until
	return s.pendingInviteResponse(invite)
}

func inviteAlreadyUsedError() error {
	return utils.NewError(409, "invite_already_used", "This invitation has already been accepted")
}

func invalidInviteError() error {
	return utils.NewError(404, "invite_not_found", "Invitation is invalid or unavailable")
}
