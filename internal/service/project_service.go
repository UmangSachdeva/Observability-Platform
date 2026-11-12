package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

type ProjectService struct {
	projects interfaces.ProjectRepository
	accounts interfaces.AccountRepository
}

func NewProjectService(projects interfaces.ProjectRepository, accounts interfaces.AccountRepository) *ProjectService {
	return &ProjectService{projects: projects, accounts: accounts}
}

func (s *ProjectService) ListByAccount(ctx context.Context, accountID, userID string, limit int) (dtos.ListProjectsResponse, error) {
	if limit < 1 || limit > 500 {
		return dtos.ListProjectsResponse{}, utils.NewError(400, "validation_error", "limit must be between 1 and 500")
	}
	if err := s.requireAccountMember(ctx, accountID, userID); err != nil {
		return dtos.ListProjectsResponse{}, err
	}
	projects, err := s.projects.ListByAccount(ctx, accountID, limit)
	if err != nil {
		return dtos.ListProjectsResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load projects", err)
	}
	response := dtos.ListProjectsResponse{Projects: make([]dtos.ProjectResponse, 0, len(projects))}
	for _, project := range projects {
		response.Projects = append(response.Projects, mapper.ProjectToResponse(project))
	}
	return response, nil
}

func (s *ProjectService) Create(ctx context.Context, accountID, userID string, request dtos.CreateProjectRequest) (dtos.ProjectResponse, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	if err := validation.ValidateCreateProject(request); err != nil {
		return dtos.ProjectResponse{}, err
	}
	if err := s.requireAccountMember(ctx, accountID, userID); err != nil {
		return dtos.ProjectResponse{}, err
	}
	id, err := newID()
	if err != nil {
		return dtos.ProjectResponse{}, utils.WrapError(500, "project_creation_failed", "Could not create project", err)
	}
	project := mapper.CreateProjectRequestToEntity(request, id, accountID, userID, time.Now().UTC())
	if err := s.projects.Create(ctx, project); err != nil {
		if errors.Is(err, interfaces.ErrProjectAlreadyExists) {
			return dtos.ProjectResponse{}, utils.NewError(409, "project_exists", "A project with this name already exists in the account")
		}
		return dtos.ProjectResponse{}, utils.WrapError(503, "storage_unavailable", "Could not create project", err)
	}
	return mapper.ProjectToResponse(project), nil
}

func (s *ProjectService) requireAccountMember(ctx context.Context, accountID, userID string) error {
	member, err := s.accounts.IsMember(ctx, accountID, userID)
	if err != nil {
		return utils.WrapError(503, "storage_unavailable", "Could not verify account membership", err)
	}
	if !member {
		return utils.NewError(403, "forbidden", "You are not a member of this account")
	}
	return nil
}
