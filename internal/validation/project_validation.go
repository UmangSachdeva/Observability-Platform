package validation

import (
	"strings"

	"observability-platform/internal/dtos"
	"observability-platform/internal/utils"
)

func ValidateCreateProject(request dtos.CreateProjectRequest) error {
	if len(strings.TrimSpace(request.Name)) == 0 || len(strings.TrimSpace(request.Name)) > 80 {
		return utils.NewError(400, "validation_error", "project name is required and must be at most 80 characters")
	}
	if len(request.Description) > 1000 {
		return utils.NewError(400, "validation_error", "project description must be at most 1000 characters")
	}
	return nil
}
