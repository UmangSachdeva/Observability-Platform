package service

import (
	"context"
	"crypto/rand"
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

type ErrorService struct {
	repository interfaces.ErrorRepository
	accounts   interfaces.AccountRepository
	projects   interfaces.ProjectRepository
}

func NewErrorService(repository interfaces.ErrorRepository, accounts interfaces.AccountRepository, projects interfaces.ProjectRepository) *ErrorService {
	return &ErrorService{repository: repository, accounts: accounts, projects: projects}
}

// Create stores an event under the project of the API key that sent it.
func (s *ErrorService) Create(ctx context.Context, accountID, projectID string, request dtos.CreateErrorRequest) (entity.ErrorEvent, error) {
	if accountID == "" || projectID == "" {
		return entity.ErrorEvent{}, utils.WrapError(500, "internal_error", "Could not accept error event", errors.New("event has no project"))
	}
	if err := validation.ValidateCreateError(request); err != nil {
		return entity.ErrorEvent{}, err
	}
	request.Service = strings.TrimSpace(request.Service)
	request.Message = strings.TrimSpace(request.Message)
	request.Level = strings.ToLower(strings.TrimSpace(request.Level))
	if request.Level == "" {
		request.Level = "error"
	}
	if request.Timestamp.IsZero() {
		request.Timestamp = time.Now().UTC()
	} else {
		request.Timestamp = request.Timestamp.UTC()
	}
	request.Environment = strings.TrimSpace(request.Environment)
	id, err := newID()
	if err != nil {
		return entity.ErrorEvent{}, utils.WrapError(500, "internal_error", "Could not create error event", err)
	}
	event := mapper.CreateErrorRequestToEntity(request, id, accountID, projectID, time.Now().UTC())
	if err := s.repository.Create(ctx, event); err != nil {
		return entity.ErrorEvent{}, utils.WrapError(503, "storage_unavailable", "Could not accept error event", err)
	}
	return event, nil
}

// ListByProject returns a project's latest events to a member of its account.
func (s *ErrorService) ListByProject(ctx context.Context, accountID, projectID, userID string, limit int) ([]entity.ErrorEvent, error) {
	if limit < 1 || limit > 500 {
		return nil, utils.NewError(400, "validation_error", "limit must be between 1 and 500")
	}
	if err := requireProjectAccess(ctx, s.accounts, s.projects, accountID, projectID, userID); err != nil {
		return nil, err
	}
	events, err := s.repository.ListByProject(ctx, projectID, limit)
	if err != nil {
		return nil, utils.WrapError(503, "storage_unavailable", "Could not load error events", err)
	}
	return events, nil
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
