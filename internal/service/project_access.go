package service

import (
	"context"
	"errors"

	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
)

// requireProjectAccess checks that userID is a member of accountID and that
// projectID belongs to that account. Services that read or write project data
// on behalf of a user call it first.
func requireProjectAccess(ctx context.Context, accounts interfaces.AccountRepository, projects interfaces.ProjectRepository, accountID, projectID, userID string) error {
	member, err := accounts.IsMember(ctx, accountID, userID)
	if err != nil {
		return utils.WrapError(503, "storage_unavailable", "Could not verify account membership", err)
	}
	if !member {
		return utils.NewError(403, "forbidden", "You are not a member of this account")
	}
	if _, err := projects.FindByID(ctx, accountID, projectID); err != nil {
		if errors.Is(err, interfaces.ErrProjectNotFound) {
			return utils.NewError(404, "project_not_found", "Project was not found in this account")
		}
		return utils.WrapError(503, "storage_unavailable", "Could not load project", err)
	}
	return nil
}
