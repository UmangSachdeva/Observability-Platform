package validation

import (
	"net/mail"
	"regexp"
	"strings"

	"observability-platform/internal/dtos"
	"observability-platform/internal/utils"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{2,31}$`)

func ValidateSignup(request dtos.SignupRequest) error {
	if !usernamePattern.MatchString(request.Username) {
		return utils.NewError(400, "validation_error", "username must be 3 to 32 characters using letters, numbers, dots, dashes, or underscores")
	}
	if len(request.Email) > 254 {
		return utils.NewError(400, "validation_error", "email address is invalid")
	}
	parsed, err := mail.ParseAddress(request.Email)
	if err != nil || parsed.Address != request.Email || !strings.Contains(request.Email, "@") {
		return utils.NewError(400, "validation_error", "email address is invalid")
	}
	if len(request.Password) < 12 || len(request.Password) > 72 {
		return utils.NewError(400, "validation_error", "password must be between 12 and 72 bytes")
	}
	return nil
}

func ValidateLogin(request dtos.LoginRequest) error {
	if strings.TrimSpace(request.Login) == "" || len(request.Login) > 254 || request.Password == "" || len(request.Password) > 72 {
		return utils.NewError(400, "validation_error", "login and password are required")
	}
	return nil
}
