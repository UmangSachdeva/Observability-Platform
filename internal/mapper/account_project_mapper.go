package mapper

import (
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
)

func AccountToResponse(account entity.Account) dtos.AccountResponse {
	response := dtos.AccountResponse{
		ID: account.ID, Name: account.Name,
		Members:   make([]dtos.AccountMemberResponse, 0, len(account.Members)),
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}
	for _, member := range account.Members {
		role := member.Role
		if role == "owner" {
			role = "admin"
		}
		response.Members = append(response.Members, dtos.AccountMemberResponse{
			UserID: member.UserID, Role: role, JoinedAt: member.JoinedAt,
		})
	}
	return response
}

func CreateProjectRequestToEntity(request dtos.CreateProjectRequest, id, accountID, createdBy string, now time.Time) entity.Project {
	return entity.Project{
		ID: id, AccountID: accountID, Name: request.Name, Description: request.Description,
		CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
	}
}

func ProjectToResponse(project entity.Project) dtos.ProjectResponse {
	return dtos.ProjectResponse{
		ID: project.ID, AccountID: project.AccountID, Name: project.Name,
		Description: project.Description, CreatedBy: project.CreatedBy,
		CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
	}
}
