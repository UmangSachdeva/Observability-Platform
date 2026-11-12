package validation

import (
	"strings"

	"observability-platform/internal/dtos"
	"observability-platform/internal/utils"
)

var ErrInvalidAPIKeyRequest = utils.NewError(400, "validation_error", "client_name is required and must be at most 100 characters")

func ValidateCreateAPIKey(request dtos.CreateAPIKeyRequest) error {
	name := strings.TrimSpace(request.ClientName)
	if name == "" || len(name) > 100 {
		return ErrInvalidAPIKeyRequest
	}
	return nil
}
