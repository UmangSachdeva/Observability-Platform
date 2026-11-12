package validation

import (
	"strings"

	"observability-platform/internal/dtos"
	"observability-platform/internal/utils"
)

var ErrInvalidErrorRequest = utils.NewError(400, "validation_error", "service and message are required")

func ValidateCreateError(request dtos.CreateErrorRequest) error {
	if strings.TrimSpace(request.Service) == "" || strings.TrimSpace(request.Message) == "" {
		return ErrInvalidErrorRequest
	}
	return nil
}
