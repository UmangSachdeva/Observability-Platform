package validation

import (
	"bytes"
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/utils"
)

func ValidateCreateInvite(request dtos.CreateInviteRequest) error {
	if len(request.Email) > 254 {
		return utils.NewError(400, "validation_error", "email address is invalid")
	}
	parsed, err := mail.ParseAddress(request.Email)
	if err != nil || parsed.Address != request.Email || !strings.Contains(request.Email, "@") {
		return utils.NewError(400, "validation_error", "email address is invalid")
	}
	if request.Role != "" && request.Role != "admin" && request.Role != "member" {
		return utils.NewError(400, "validation_error", "role must be admin or member")
	}
	return nil
}

func ValidateAcceptInvite(request dtos.AcceptInviteRequest) error {
	hasToken := strings.TrimSpace(request.Token) != ""
	hasID := strings.TrimSpace(request.InviteID) != ""
	if hasToken == hasID {
		return utils.NewError(400, "validation_error", "send exactly one of invite_token or invite_id")
	}
	if hasToken && (len(strings.TrimSpace(request.Token)) < 32 || len(request.Token) > 256) {
		return utils.NewError(400, "validation_error", "invite_token is invalid")
	}
	if hasID && len(request.InviteID) > 128 {
		return utils.NewError(400, "validation_error", "invite_id is invalid")
	}
	return nil
}

// ParseSnoozeInvite returns the requested snooze end time in UTC, or nil when
// the client sent null to clear the snooze.
func ParseSnoozeInvite(request dtos.SnoozeInviteRequest) (*time.Time, error) {
	raw := bytes.TrimSpace(request.SnoozedUntil)
	if len(raw) == 0 {
		return nil, utils.NewError(400, "validation_error", "snoozed_until is required; send null to clear the snooze")
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var until time.Time
	if err := json.Unmarshal(raw, &until); err != nil {
		return nil, utils.NewError(400, "validation_error", "snoozed_until must be an RFC 3339 timestamp or null")
	}
	until = until.UTC().Truncate(time.Millisecond)
	return &until, nil
}
